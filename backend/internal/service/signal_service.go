package service

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/gunh0/midas-touch/internal/advisor"
	"github.com/gunh0/midas-touch/internal/marketdata"
	"github.com/gunh0/midas-touch/internal/mongodb"
)

type SignalService struct {
	db     *mongodb.Client
	market *marketdata.Client
}

func NewSignalService(db *mongodb.Client, market *marketdata.Client) *SignalService {
	return &SignalService{db: db, market: market}
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
	snapshot, err := s.market.FetchQuotes(contextSymbols)
	if err != nil {
		log.Printf("warn: fetch context quotes: %v", err)
		snapshot = make(map[string]marketdata.Quote)
	}

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
	reco.USDKRWRate = usdkrw

	return reco, nil
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
