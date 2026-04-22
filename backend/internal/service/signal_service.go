package service

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/gunh0/midas-touch/internal/advisor"
	"github.com/gunh0/midas-touch/internal/marketdata"
	"github.com/gunh0/midas-touch/internal/mongodb"
)

type SignalService struct {
	db     *mongodb.Client
	market *marketdata.Client

	evaluateFn func(context.Context, string, string) (advisor.Recommendation, error)

	mu                   sync.RWMutex
	contextQuoteCache    map[string]marketdata.Quote
	contextQuoteCachedAt time.Time
	contextQuoteCacheTTL time.Duration
	signalCache          map[string]signalCacheEntry
	signalCacheTTL       time.Duration
}

type signalCacheEntry struct {
	reco      advisor.Recommendation
	expiresAt time.Time
}

func NewSignalService(db *mongodb.Client, market *marketdata.Client) *SignalService {
	return &SignalService{
		db:                   db,
		market:               market,
		evaluateFn:           nil,
		contextQuoteCacheTTL: 8 * time.Second,
		signalCache:          make(map[string]signalCacheEntry),
		signalCacheTTL:       15 * time.Second,
	}
}

func signalCacheKey(symbol, timingTF string) string {
	return NormalizeSymbol(symbol) + "|" + NormalizeTimeframe(timingTF)
}

func (s *SignalService) EvaluateCached(ctx context.Context, symbol, timingTF string) (advisor.Recommendation, error) {
	key := signalCacheKey(symbol, timingTF)
	now := time.Now()

	s.mu.RLock()
	if entry, ok := s.signalCache[key]; ok && now.Before(entry.expiresAt) {
		s.mu.RUnlock()
		return entry.reco, nil
	}
	s.mu.RUnlock()

	evaluator := s.evaluateFn
	if evaluator == nil {
		evaluator = s.Evaluate
	}

	reco, err := evaluator(ctx, symbol, timingTF)
	if err != nil {
		return advisor.Recommendation{}, err
	}

	s.mu.Lock()
	s.signalCache[key] = signalCacheEntry{reco: reco, expiresAt: now.Add(s.signalCacheTTL)}
	if len(s.signalCache) > 512 {
		for k, v := range s.signalCache {
			if now.After(v.expiresAt) {
				delete(s.signalCache, k)
			}
		}
	}
	s.mu.Unlock()

	return reco, nil
}

func NormalizeSymbol(s string) string {
	s = strings.TrimSpace(strings.ToUpper(s))
	if s == "" {
		return advisor.SymbolNVDA
	}
	return s
}

func NormalizeTimeframe(tf string) string {
	tf = strings.TrimSpace(strings.ToLower(tf))
	switch tf {
	case "", "1d", "d", "daily":
		return "1d"
	case "5", "5m", "m5":
		return "5"
	case "15", "15m", "m15":
		return "15"
	case "30", "30m", "m30":
		return "30"
	case "60", "1h", "h1":
		return "60"
	case "120", "2h", "h2":
		return "120"
	case "240", "4h", "h4":
		return "240"
	default:
		return "1d"
	}
}

func (s *SignalService) Evaluate(ctx context.Context, symbol, timingTF string) (advisor.Recommendation, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	symbol = NormalizeSymbol(symbol)
	timingTF = NormalizeTimeframe(timingTF)
	if timingTF == "1d" {
		timingTF = "120"
	}

	contextSymbols := []string{advisor.SymbolVIX, advisor.SymbolNQ, advisor.SymbolUSDKRW}
	snapshot := s.getContextQuotes(contextSymbols)

	targetQuotes, err := s.market.FetchQuotes([]string{symbol})
	if err == nil {
		for k, v := range targetQuotes {
			snapshot[k] = v
		}
	}

	dailyBars, err := s.market.FetchDailyBars(symbol, 300)
	if err != nil {
		return advisor.Recommendation{}, fmt.Errorf("fetch daily bars for %s: %w", symbol, err)
	}
	intradayBars, err := s.market.FetchIntradayBars(symbol, timingTF, 300)
	if err != nil {
		return advisor.Recommendation{}, fmt.Errorf("fetch intraday bars for %s (%s): %w", symbol, timingTF, err)
	}

	docs := make([]mongodb.CandleDoc, 0, len(dailyBars)+len(intradayBars))
	for _, b := range dailyBars {
		docs = append(docs, mongodb.CandleDoc{
			Symbol:    symbol,
			Timeframe: "1d",
			Source:    "yahoo",
			Timestamp: b.Timestamp,
			Open:      b.Open,
			High:      b.High,
			Low:       b.Low,
			Close:     b.Close,
			Volume:    b.Volume,
		})
	}
	for _, b := range intradayBars {
		docs = append(docs, mongodb.CandleDoc{
			Symbol:    symbol,
			Timeframe: timingTF,
			Source:    "finnhub|yahoo",
			Timestamp: b.Timestamp,
			Open:      b.Open,
			High:      b.High,
			Low:       b.Low,
			Close:     b.Close,
			Volume:    b.Volume,
		})
	}
	if err := s.db.UpsertCandles(ctx, docs); err != nil {
		log.Printf("warn: upsert mtf candles: %v", err)
	}

	dailyCloses := make([]float64, len(dailyBars))
	for i, b := range dailyBars {
		dailyCloses[i] = b.Close
	}
	intradayCloses := make([]float64, len(intradayBars))
	for i, b := range intradayBars {
		intradayCloses[i] = b.Close
	}

	usdkrw, err := s.market.FetchUSDKRWRate()
	if err != nil {
		log.Printf("warn: fetch usdkrw: %v", err)
	}

	reco, err := advisor.EvaluateMultiTimeframe(symbol, snapshot, dailyCloses, intradayCloses, time.Now())
	if err != nil {
		return advisor.Recommendation{}, fmt.Errorf("evaluate %s: %w", symbol, err)
	}
	reco.TimingTF = timingTF
	reco.USDKRWRate = usdkrw
	reco.FullName = advisor.SymbolFullName(symbol)
	if reco.FullName == "" {
		if rows, err := s.market.SearchSymbols(symbol, 5); err == nil {
			for _, row := range rows {
				if strings.EqualFold(strings.TrimSpace(row.Symbol), symbol) && strings.TrimSpace(row.Name) != "" {
					reco.FullName = strings.TrimSpace(row.Name)
					break
				}
			}
		}
	}
	reco.TimeframeBias = s.buildTimeframeBias(symbol, dailyCloses)

	return reco, nil
}

func (s *SignalService) getContextQuotes(symbols []string) map[string]marketdata.Quote {
	now := time.Now()

	s.mu.RLock()
	if len(s.contextQuoteCache) > 0 && now.Sub(s.contextQuoteCachedAt) <= s.contextQuoteCacheTTL {
		cached := make(map[string]marketdata.Quote, len(s.contextQuoteCache))
		for k, v := range s.contextQuoteCache {
			cached[k] = v
		}
		s.mu.RUnlock()
		return cached
	}
	s.mu.RUnlock()

	fresh, err := s.market.FetchQuotes(symbols)
	if err != nil {
		log.Printf("warn: fetch context quotes: %v", err)
		s.mu.RLock()
		defer s.mu.RUnlock()
		fallback := make(map[string]marketdata.Quote, len(s.contextQuoteCache))
		for k, v := range s.contextQuoteCache {
			fallback[k] = v
		}
		return fallback
	}

	s.mu.Lock()
	s.contextQuoteCache = make(map[string]marketdata.Quote, len(fresh))
	for k, v := range fresh {
		s.contextQuoteCache[k] = v
	}
	s.contextQuoteCachedAt = now
	s.mu.Unlock()

	return fresh
}

func (s *SignalService) SaveSignal(ctx context.Context, symbol string, reco advisor.Recommendation, notified bool) {
	sig := mongodb.SignalDoc{
		Symbol:    NormalizeSymbol(symbol),
		Timestamp: reco.Timestamp,
		Action:    reco.Action,
		BuyPct:    reco.BuyPercent,
		SellPct:   reco.SellPercent,
		HoldPct:   reco.HoldPercent,
		Reason:    reco.Reason,
		Notified:  notified,
		IsSpecial: reco.IsSpecial,
	}
	if err := s.db.SaveSignal(ctx, sig); err != nil {
		log.Printf("warn: save signal: %v", err)
	}
}

func (s *SignalService) AutoPrune(ctx context.Context, symbol string) error {
	stats, err := s.db.GetDBStats(ctx)
	if err != nil || !stats.OverLimit {
		return err
	}
	symbol = NormalizeSymbol(symbol)
	if _, err := s.db.PruneOldCandles(ctx, symbol, "1d", 365); err != nil {
		return err
	}
	if _, err := s.db.PruneOldCandles(ctx, symbol, "240", 120); err != nil {
		return err
	}
	if _, err := s.db.PruneOldCandles(ctx, symbol, "120", 90); err != nil {
		return err
	}
	return nil
}

func (s *SignalService) buildTimeframeBias(symbol string, dailyCloses []float64) map[string]string {
	bias := map[string]string{}

	if len(dailyCloses) > 0 {
		bias["1d"] = directionalActionFromCloses(dailyCloses)
		start := len(dailyCloses) - 22
		if start < 0 {
			start = 0
		}
		bias["1mo"] = directionalActionFromCloses(dailyCloses[start:])
	}

	type tfPair struct {
		key string
		tf  string
	}
	tfs := []tfPair{{key: "30m", tf: "30"}, {key: "1h", tf: "60"}, {key: "4h", tf: "240"}}

	var wg sync.WaitGroup
	var biasMu sync.Mutex
	for _, item := range tfs {
		item := item
		wg.Add(1)
		go func() {
			defer wg.Done()
			bars, err := s.market.FetchIntradayBars(symbol, item.tf, 120)
			if err != nil {
				return
			}
			action := directionalActionFromCloses(closesFromBars(bars))
			biasMu.Lock()
			bias[item.key] = action
			biasMu.Unlock()
		}()
	}
	wg.Wait()

	for _, key := range []string{"30m", "1h", "4h", "1d", "1mo"} {
		if _, ok := bias[key]; !ok {
			bias[key] = "HOLD"
		}
	}

	return bias
}

func closesFromBars(bars []marketdata.OHLCVBar) []float64 {
	closes := make([]float64, 0, len(bars))
	for _, b := range bars {
		if b.Close > 0 {
			closes = append(closes, b.Close)
		}
	}
	return closes
}

func directionalActionFromCloses(closes []float64) string {
	if len(closes) < 5 {
		return "HOLD"
	}
	last := closes[len(closes)-1]
	short := simpleAverage(closes, 5)
	long := simpleAverage(closes, 20)
	if long <= 0 {
		long = simpleAverage(closes, len(closes))
	}

	baseIdx := len(closes) - 11
	if baseIdx < 0 {
		baseIdx = 0
	}
	base := closes[baseIdx]
	momentum := 0.0
	if base > 0 {
		momentum = ((last - base) / base) * 100
	}

	if last > long && short >= long && momentum > 0.3 {
		return "BUY"
	}
	if last < long && short <= long && momentum < -0.3 {
		return "SELL"
	}
	return "HOLD"
}

func simpleAverage(values []float64, period int) float64 {
	if len(values) == 0 {
		return 0
	}
	if period <= 0 || period > len(values) {
		period = len(values)
	}
	sum := 0.0
	for _, v := range values[len(values)-period:] {
		sum += v
	}
	return sum / float64(period)
}
