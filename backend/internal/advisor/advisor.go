package advisor

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/gunh0/midas-touch/internal/marketdata"
)

const (
	SymbolNVDA   = "NVDA"
	SymbolVIX    = "^VIX"
	SymbolNQ     = "NQ=F"
	SymbolUSDKRW = "USDKRW=X"
)

var popularLeaderSymbols = []string{
	"TQQQ", "QQQ", "SPY", "SOXL", "SOXX",
	"NVDA", "TSLA", "AAPL", "MSFT", "AMZN", "META", "GOOGL", "AMD", "PLTR", "SMCI",
}

const (
	minPreferredCloses = 50
	minRelaxedCloses   = 20
)

// Indicators holds all computed technical indicator values.
type Indicators struct {
	RSI14 float64

	SMA20 float64
	SMA50 float64

	BBUpper float64
	BBMid   float64
	BBLower float64

	// Supertrend: direction 1=bullish, -1=bearish
	SupertrendDir  int
	SupertrendLine float64

	ATR14 float64
}

// SignalScore is the raw score breakdown from each indicator group.
type SignalScore struct {
	RSIScore        float64 // -1 to +1
	MAScore         float64 // -1 to +1
	BBScore         float64 // -1 to +1
	SupertrendScore float64 // -1 or +1
	Total           float64 // weighted sum
}

// Recommendation is the final output.
type Recommendation struct {
	TargetSymbol  string
	FullName      string
	BuyPercent    float64
	SellPercent   float64
	HoldPercent   float64
	Action        string
	TrendAction   string
	TimingAction  string
	TimingTF      string
	WeeklyAction  string
	WeeklyChange  float64
	IsSpecial     bool
	Reason        string
	DataQualityNote string
	USDKRWRate    float64
	Snapshot      map[string]marketdata.Quote
	Indicators    Indicators
	Timing        Indicators
	Score         SignalScore
	TimingScore   SignalScore
	TimeframeBias map[string]string
	Timestamp     time.Time
}

func RequiredSymbols() []string {
	return []string{SymbolNVDA, SymbolVIX, SymbolNQ, SymbolUSDKRW}
}

func PopularLeaderSymbols() []string {
	cloned := make([]string, len(popularLeaderSymbols))
	copy(cloned, popularLeaderSymbols)
	return cloned
}

func SymbolFullName(symbol string) string {
	switch strings.ToUpper(strings.TrimSpace(symbol)) {
	case "TQQQ":
		return "ProShares UltraPro QQQ"
	case "QQQ":
		return "Invesco QQQ Trust"
	case "SPY":
		return "SPDR S&P 500 ETF Trust"
	case "SOXL":
		return "Direxion Daily Semiconductor Bull 3X"
	case "SOXX":
		return "iShares Semiconductor ETF"
	case "NVDA":
		return "NVIDIA Corporation"
	case "TSLA":
		return "Tesla, Inc."
	case "AAPL":
		return "Apple Inc."
	case "MSFT":
		return "Microsoft Corporation"
	case "AMZN":
		return "Amazon.com, Inc."
	case "META":
		return "Meta Platforms, Inc."
	case "GOOGL":
		return "Alphabet Inc. Class A"
	case "AMD":
		return "Advanced Micro Devices, Inc."
	case "PLTR":
		return "Palantir Technologies Inc."
	case "SMCI":
		return "Super Micro Computer, Inc."
	default:
		return ""
	}
}

// Evaluate computes Buy/Hold/Sell probabilities purely from chart indicators.
// symbol: the ticker being analyzed (used for labeling only).
// closes: daily close prices oldest-first, at least 20 required.
func Evaluate(symbol string, snapshot map[string]marketdata.Quote, closes []float64, now time.Time) (Recommendation, error) {
	if len(closes) < minRelaxedCloses {
		return Recommendation{}, fmt.Errorf("need at least %d closes, got %d", minRelaxedCloses, len(closes))
	}

	dataNote := ""
	if len(closes) < minPreferredCloses {
		dataNote = fmt.Sprintf("Limited history: %d daily bars (<%d). Provisional signal.", len(closes), minPreferredCloses)
	}

	ind := computeIndicators(closes)
	score, reasons := scoreFromIndicators(ind, closes)
	finalScore := dampenScoreForLimitedSamples(score.Total, len(closes), len(closes))
	buy, sell, hold := toProbabilities(finalScore)
	action := dominantAction(buy, sell, hold)
	weeklyAction, weeklyChange := weeklyBiasFromCloses(closes)
	reason := strings.Join(reasons, "; ")
	if dataNote != "" {
		reason = fmt.Sprintf("[DATA WARNING] %s\n%s", dataNote, reason)
	}

	return Recommendation{
		TargetSymbol:  symbol,
		FullName:      SymbolFullName(symbol),
		BuyPercent:    buy,
		SellPercent:   sell,
		HoldPercent:   hold,
		Action:        action,
		TrendAction:   action,
		TimingAction:  action,
		TimingTF:      "1d",
		WeeklyAction:  weeklyAction,
		WeeklyChange:  weeklyChange,
		Reason:        reason,
		DataQualityNote: dataNote,
		Snapshot:      snapshot,
		Indicators:    ind,
		Timing:        ind,
		Score:         score,
		TimingScore:   score,
		TimeframeBias: map[string]string{"1d": action, "1mo": weeklyAction},
		Timestamp:     now,
	}, nil
}

// EvaluateMultiTimeframe computes final probabilities from:
// - daily closes: direction
// - intraday closes: timing
func EvaluateMultiTimeframe(symbol string, snapshot map[string]marketdata.Quote, dailyCloses, intradayCloses []float64, now time.Time) (Recommendation, error) {
	if len(dailyCloses) < minRelaxedCloses {
		return Recommendation{}, fmt.Errorf("need at least %d daily closes, got %d", minRelaxedCloses, len(dailyCloses))
	}
	if len(intradayCloses) < minRelaxedCloses {
		return Recommendation{}, fmt.Errorf("need at least %d intraday closes, got %d", minRelaxedCloses, len(intradayCloses))
	}

	dataNote := ""
	if len(dailyCloses) < minPreferredCloses || len(intradayCloses) < minPreferredCloses {
		dataNote = fmt.Sprintf("Limited history: daily %d / intraday %d bars (<%d). Provisional signal.", len(dailyCloses), len(intradayCloses), minPreferredCloses)
	}

	dailyInd := computeIndicators(dailyCloses)
	dailyScore, dailyReasons := scoreFromIndicators(dailyInd, dailyCloses)
	dBuy, dSell, dHold := toProbabilities(dailyScore.Total)
	trendAction := dominantAction(dBuy, dSell, dHold)

	timingInd := computeIndicators(intradayCloses)
	timingScore, timingReasons := scoreFromIndicators(timingInd, intradayCloses)
	tBuy, tSell, tHold := toProbabilities(timingScore.Total)
	timingAction := dominantAction(tBuy, tSell, tHold)

	finalScore := dailyScore.Total*0.65 + timingScore.Total*0.35
	finalScore = dampenScoreForLimitedSamples(finalScore, len(dailyCloses), len(intradayCloses))
	buy, sell, hold := toProbabilities(finalScore)
	action := dominantAction(buy, sell, hold)
	weeklyAction, weeklyChange := weeklyBiasFromCloses(dailyCloses)

	isSpecial := false
	if (dailyScore.Total >= 0.55 && timingScore.Total >= 0.65) ||
		(dailyScore.Total <= -0.55 && timingScore.Total <= -0.65) {
		isSpecial = true
	}

	reason := fmt.Sprintf(
		"- Direction(D): %s %s (score %.2f)\n"+
			"- Timing(H): %s %s (score %.2f)\n"+
			"- Daily details: %s\n"+
			"- Intraday details: %s",
		actionSignalEmoji(trendAction), actionWithKorean(trendAction), dailyScore.Total,
		actionSignalEmoji(timingAction), actionWithKorean(timingAction), timingScore.Total,
		strings.Join(dailyReasons, " | "),
		strings.Join(timingReasons, " | "),
	)
	if dataNote != "" {
		reason = fmt.Sprintf("[DATA WARNING] %s\n%s", dataNote, reason)
	}

	return Recommendation{
		TargetSymbol:  symbol,
		FullName:      SymbolFullName(symbol),
		BuyPercent:    buy,
		SellPercent:   sell,
		HoldPercent:   hold,
		Action:        action,
		TrendAction:   trendAction,
		TimingAction:  timingAction,
		WeeklyAction:  weeklyAction,
		WeeklyChange:  weeklyChange,
		IsSpecial:     isSpecial,
		Reason:        reason,
		DataQualityNote: dataNote,
		Snapshot:      snapshot,
		Indicators:    dailyInd,
		Timing:        timingInd,
		Score:         dailyScore,
		TimingScore:   timingScore,
		TimeframeBias: map[string]string{"1d": trendAction, "1mo": weeklyAction},
		Timestamp:     now,
	}, nil
}

func dampenScoreForLimitedSamples(score float64, dailyCount, intradayCount int) float64 {
	minCount := dailyCount
	if intradayCount < minCount {
		minCount = intradayCount
	}
	if minCount >= minPreferredCloses {
		return score
	}
	ratio := float64(minCount) / float64(minPreferredCloses)
	if ratio < 0 {
		ratio = 0
	}
	factor := 0.5 + (0.5 * ratio)
	return score * factor
}

// computeIndicators calculates all technical indicators from close prices.
func computeIndicators(closes []float64) Indicators {
	upper, mid, lower := bollingerBands(closes, 20, 2.0)
	stDir, stLine := supertrend(closes, 14, 3.0)

	return Indicators{
		RSI14:          rsi(closes, 14),
		SMA20:          sma(closes, 20),
		SMA50:          sma(closes, 50),
		BBUpper:        upper,
		BBMid:          mid,
		BBLower:        lower,
		SupertrendDir:  stDir,
		SupertrendLine: stLine,
		ATR14:          atr(closes, 14),
	}
}

// scoreFromIndicators converts indicator values into a single [-1, +1] score.
// Positive = bullish (buy), negative = bearish (sell), near 0 = hold.
//
// Weights:
//   - Supertrend direction: 35%
//   - SMA cross (price vs SMA20, SMA50): 30%
//   - Bollinger Band position: 20%
//   - RSI: 15%
func scoreFromIndicators(ind Indicators, closes []float64) (SignalScore, []string) {
	last := closes[len(closes)-1]
	var reasons []string
	var s SignalScore

	// ── Supertrend (35%) ──────────────────────────────────────────────────
	s.SupertrendScore = float64(ind.SupertrendDir) // +1 or -1
	if ind.SupertrendDir > 0 {
		reasons = append(reasons, "Supertrend bullish")
	} else {
		reasons = append(reasons, "Supertrend bearish")
	}

	// ── Moving averages (30%) ─────────────────────────────────────────────
	aboveSMA20 := last > ind.SMA20
	aboveSMA50 := last > ind.SMA50
	switch {
	case aboveSMA20 && aboveSMA50:
		s.MAScore = 1.0
		reasons = append(reasons, "price above SMA20 and SMA50")
	case !aboveSMA20 && !aboveSMA50:
		s.MAScore = -1.0
		reasons = append(reasons, "price below SMA20 and SMA50")
	case aboveSMA50 && !aboveSMA20:
		s.MAScore = 0.3
		reasons = append(reasons, "price above SMA50 but below SMA20")
	default:
		s.MAScore = -0.3
		reasons = append(reasons, "price above SMA20 but below SMA50")
	}

	// ── Bollinger Bands (20%) ─────────────────────────────────────────────
	bbRange := ind.BBUpper - ind.BBLower
	if bbRange > 0 {
		// position within band: 0 = lower, 1 = upper
		pos := (last - ind.BBLower) / bbRange
		// map [0,1] -> [-1, +1], but clamp extremes as mean-reversion signals
		switch {
		case pos > 0.95: // near/above upper band -> overbought
			s.BBScore = -0.8
			reasons = append(reasons, "near Bollinger upper band (overbought)")
		case pos < 0.05: // near/below lower band -> oversold
			s.BBScore = 0.8
			reasons = append(reasons, "near Bollinger lower band (oversold)")
		case pos > 0.6:
			s.BBScore = 0.4
			reasons = append(reasons, "price in upper Bollinger zone")
		case pos < 0.4:
			s.BBScore = -0.4
			reasons = append(reasons, "price in lower Bollinger zone")
		default:
			s.BBScore = 0.0
			reasons = append(reasons, "price at Bollinger midline")
		}
	}

	// ── RSI (15%) ─────────────────────────────────────────────────────────
	switch {
	case ind.RSI14 >= 70:
		s.RSIScore = -1.0
		reasons = append(reasons, fmt.Sprintf("RSI14 %.1f overbought", ind.RSI14))
	case ind.RSI14 <= 30:
		s.RSIScore = 1.0
		reasons = append(reasons, fmt.Sprintf("RSI14 %.1f oversold", ind.RSI14))
	case ind.RSI14 >= 55:
		s.RSIScore = 0.4
		reasons = append(reasons, fmt.Sprintf("RSI14 %.1f bullish zone", ind.RSI14))
	case ind.RSI14 <= 45:
		s.RSIScore = -0.4
		reasons = append(reasons, fmt.Sprintf("RSI14 %.1f bearish zone", ind.RSI14))
	default:
		s.RSIScore = 0.0
		reasons = append(reasons, fmt.Sprintf("RSI14 %.1f neutral", ind.RSI14))
	}

	// ── Weighted total ────────────────────────────────────────────────────
	s.Total = s.SupertrendScore*0.35 +
		s.MAScore*0.30 +
		s.BBScore*0.20 +
		s.RSIScore*0.15

	return s, reasons
}

// toProbabilities converts a score in [-1, +1] to Buy/Sell/Hold percentages.
// score > 0 favors buy, score < 0 favors sell, near 0 favors hold.
func toProbabilities(score float64) (buy, sell, hold float64) {
	// Softmax over three logits
	xBuy := score * 2.5
	xSell := -score * 2.5
	xHold := -math.Abs(score) * 1.2 // hold is penalized when signal is strong

	eb := math.Exp(xBuy)
	es := math.Exp(xSell)
	eh := math.Exp(xHold)
	total := eb + es + eh

	buy = math.Round((eb / total) * 100)
	sell = math.Round((es / total) * 100)
	hold = math.Round((eh / total) * 100)

	// Ensure they sum to 100
	diff := 100 - (buy + sell + hold)
	hold += diff
	return
}

func dominantAction(buy, sell, hold float64) string {
	if buy >= sell && buy >= hold {
		return "BUY"
	}
	if sell >= buy && sell >= hold {
		return "SELL"
	}
	return "HOLD"
}

// FormatMessage formats a Recommendation for Telegram.
func FormatMessage(r Recommendation) string {
	stDir := "Bullish(상승)"
	if r.Indicators.SupertrendDir < 0 {
		stDir = "Bearish(하락)"
	}
	confidencePct := confidenceByAction(r)
	dataQualityLine := ""
	if strings.TrimSpace(r.DataQualityNote) != "" {
		dataQualityLine = fmt.Sprintf("Data Quality(데이터 품질): ⚠ %s\n\n", r.DataQualityNote)
	}
	fullName := r.FullName
	if strings.TrimSpace(fullName) == "" {
		fullName = SymbolFullName(r.TargetSymbol)
	}
	title := r.TargetSymbol
	if fullName != "" {
		title = fmt.Sprintf("%s | %s", r.TargetSymbol, fullName)
	}

	// Best-effort market context from snapshot
	vixPct, nqPct := 0.0, 0.0
	if v, ok := r.Snapshot[SymbolVIX]; ok {
		vixPct = v.ChangePercent
	}
	if n, ok := r.Snapshot[SymbolNQ]; ok {
		nqPct = n.ChangePercent
	}
	targetQuote := r.Snapshot[r.TargetSymbol]
	currentPrice := targetQuote.Price
	if currentPrice <= 0 {
		currentPrice = r.Indicators.SMA20
	}
	intradayLabel := timingTFLabel(r.TimingTF)
	tf30 := actionWithKorean(directionAt(r.TimeframeBias, "30m", r.TimingAction))
	tf1h := actionWithKorean(directionAt(r.TimeframeBias, "1h", r.TimingAction))
	tf4h := actionWithKorean(directionAt(r.TimeframeBias, "4h", r.TimingAction))
	tf1d := actionWithKorean(directionAt(r.TimeframeBias, "1d", r.TrendAction))
	tf1mo := actionWithKorean(directionAt(r.TimeframeBias, "1mo", r.WeeklyAction))
	weeklyAction := r.WeeklyAction
	if weeklyAction == "" {
		weeklyAction = r.TrendAction
	}
	entry, stop, target1, target2 := buildTradePlan(r, currentPrice)
	entryChangePct := priceChangePct(currentPrice, entry)
	stopChangePct := priceChangePct(currentPrice, stop)
	target1ChangePct := priceChangePct(currentPrice, target1)
	target2ChangePct := priceChangePct(currentPrice, target2)
	entryLine := fmt.Sprintf("- Entry(진입가): $%.2f (%+.2f%%)", entry, entryChangePct)
	stopLabel := "Stop Loss(손절가)"
	target1Label := "Target 1(목표가1)"
	target2Label := "Target 2(목표가2)"
	planNote := ""
	if strings.EqualFold(r.Action, "SELL") {
		entryLine = "- Entry(신규매수): 보류"
		stopLabel = "보유자 방어선(이탈 시 비중축소)"
		target1Label = "재진입 관심가 1"
		target2Label = "재진입 관심가 2"
		planNote = "- Note(참고): 일반 투자자(롱 전용) 기준으로 SELL은 신규 숏 진입이 아니라 보수적 대응 신호입니다.\n"
	}

	return fmt.Sprintf(
		"Midas Touch Signal (시그널)\n"+
			"Time(시간): %s\n\n"+
			"%s"+
			"[%s] %s %s\n"+
			"Confidence(신뢰도): %.0f%%\n"+
			"Direction(방향 D): %s %s | Timing(타이밍 H): %s %s\n"+
			"Buy(상승): %.0f%% | Hold(횡보): %.0f%% | Sell(하락): %.0f%%\n\n"+
			"Multi-timeframe Direction (다중 타임프레임 방향성)\n"+
			"- 30m: %s\n"+
			"- 1h: %s\n"+
			"- 4h: %s\n"+
			"- 1d: %s\n"+
			"- 1mo: %s\n\n"+
			"Execution Guide(실행 가이드)\n"+
			"- Current Price(현재가): $%.2f\n"+
			"- 1D Bias(방향): %s %s\n"+
			"- %s Bias(진입): %s %s\n"+
			"- 7D Bias(스윙): %s %s (7D %+.2f%%)\n"+
			"%s"+
			"%s\n"+
			"- %s: $%.2f (%+.2f%%)\n"+
			"- %s: $%.2f (%+.2f%%)\n"+
			"- %s: $%.2f (%+.2f%%)\n\n"+
			"Indicators(지표, 한글 설명 포함)\n"+
			"- RSI14(상대강도지수): %.1f (과열/침체 강도 확인)\n"+
			"- SMA20/50(단순이동평균): %.2f / %.2f (단기/중기 추세선)\n"+
			"- BB(볼린저밴드 상/중/하): %.2f / %.2f / %.2f (변동성 범위)\n"+
			"- Supertrend(추세전환선): %s (%.2f)\n"+
			"- ATR14(평균진폭): %.2f (손절/목표 거리 기준)\n\n"+
			"Market(시장)\n"+
			"- %s: $%.2f (%+.2f%%)\n"+
			"- VIX: %+.2f%% | NQ: %+.2f%%\n"+
			"- USD/KRW: %.2f",
		r.Timestamp.Format("2006-01-02 15:04 KST"),
		dataQualityLine,
		title, actionSignalEmoji(r.Action), actionWithKorean(r.Action),
		confidencePct,
		actionSignalEmoji(r.TrendAction), actionWithKorean(r.TrendAction), actionSignalEmoji(r.TimingAction), actionWithKorean(r.TimingAction),
		r.BuyPercent, r.HoldPercent, r.SellPercent,
		tf30,
		tf1h,
		tf4h,
		tf1d,
		tf1mo,
		currentPrice,
		actionSignalEmoji(r.TrendAction), actionWithKorean(r.TrendAction),
		intradayLabel, actionSignalEmoji(r.TimingAction), actionWithKorean(r.TimingAction),
		actionSignalEmoji(weeklyAction), actionWithKorean(weeklyAction), r.WeeklyChange,
		planNote,
		entryLine,
		stopLabel, stop, stopChangePct,
		target1Label, target1, target1ChangePct,
		target2Label, target2, target2ChangePct,
		r.Indicators.RSI14,
		r.Indicators.SMA20, r.Indicators.SMA50,
		r.Indicators.BBUpper, r.Indicators.BBMid, r.Indicators.BBLower,
		stDir, r.Indicators.SupertrendLine,
		r.Indicators.ATR14,
		r.TargetSymbol, targetQuote.Price, targetQuote.ChangePercent,
		vixPct, nqPct,
		r.USDKRWRate,
	)
}

func confidenceByAction(r Recommendation) float64 {
	switch strings.ToUpper(strings.TrimSpace(r.Action)) {
	case "BUY":
		return r.BuyPercent
	case "SELL":
		return r.SellPercent
	default:
		return r.HoldPercent
	}
}

func priceChangePct(base, target float64) float64 {
	if base == 0 {
		return 0
	}
	return ((target - base) / base) * 100
}

func directionAt(tf map[string]string, key, fallback string) string {
	if tf == nil {
		return fallback
	}
	v := strings.TrimSpace(tf[key])
	if v == "" {
		return fallback
	}
	return v
}

func weeklyBiasFromCloses(closes []float64) (string, float64) {
	if len(closes) < 8 {
		return "HOLD", 0
	}
	last := closes[len(closes)-1]
	prev := closes[len(closes)-8]
	if prev == 0 {
		return "HOLD", 0
	}
	change := ((last - prev) / prev) * 100
	switch {
	case change >= 2.0:
		return "BUY", change
	case change <= -2.0:
		return "SELL", change
	default:
		return "HOLD", change
	}
}

func timingTFLabel(tf string) string {
	switch strings.TrimSpace(strings.ToLower(tf)) {
	case "60", "1h", "h1":
		return "1H"
	case "120", "2h", "h2":
		return "2H"
	case "240", "4h", "h4":
		return "4H"
	default:
		if tf == "" {
			return "2H"
		}
		return strings.ToUpper(tf)
	}
}

func buildTradePlan(r Recommendation, currentPrice float64) (entry, stop, target1, target2 float64) {
	entry = currentPrice
	if entry <= 0 {
		entry = r.Indicators.SMA20
	}
	if entry <= 0 {
		entry = 1
	}

	atrBase := r.Timing.ATR14
	if atrBase <= 0 {
		atrBase = r.Indicators.ATR14
	}
	if atrBase <= 0 {
		atrBase = entry * 0.015
	}

	if strings.ToUpper(r.Action) == "SELL" {
		stop = entry + (1.2 * atrBase)
		if r.Timing.SupertrendLine > entry {
			stop = math.Min(stop, r.Timing.SupertrendLine)
		}
		target1 = entry - (1.8 * atrBase)
		target2 = entry - (3.0 * atrBase)
		if target2 < 0.01 {
			target2 = 0.01
		}
		if target1 < 0.01 {
			target1 = 0.01
		}
		return
	}

	stop = entry - (1.2 * atrBase)
	if r.Timing.SupertrendLine > 0 && r.Timing.SupertrendLine < entry {
		stop = math.Max(stop, r.Timing.SupertrendLine)
	}
	target1 = entry + (1.8 * atrBase)
	target2 = entry + (3.0 * atrBase)
	if stop < 0.01 {
		stop = 0.01
	}
	return
}

func actionWithKorean(action string) string {
	switch strings.ToUpper(strings.TrimSpace(action)) {
	case "BUY":
		return "BUY(구매)"
	case "SELL":
		return "SELL(매도)"
	case "HOLD":
		return "HOLD(관망)"
	default:
		if action == "" {
			return "HOLD(관망)"
		}
		return action
	}
}

func actionSignalEmoji(action string) string {
	switch strings.ToUpper(strings.TrimSpace(action)) {
	case "BUY":
		return "🟢"
	case "SELL":
		return "🔴"
	case "HOLD":
		return "🟡"
	default:
		return "⚪"
	}
}

// ── Technical indicator calculations ──────────────────────────────────────

func sma(values []float64, period int) float64 {
	if len(values) == 0 {
		return 0
	}
	if period > len(values) {
		period = len(values)
	}
	sum := 0.0
	for _, v := range values[len(values)-period:] {
		sum += v
	}
	return sum / float64(period)
}

func rsi(closes []float64, period int) float64 {
	if len(closes) < period+1 {
		return 50
	}
	gains, losses := 0.0, 0.0
	start := len(closes) - (period + 1)
	for i := start + 1; i < len(closes); i++ {
		d := closes[i] - closes[i-1]
		if d > 0 {
			gains += d
		} else {
			losses -= d
		}
	}
	avgGain := gains / float64(period)
	avgLoss := losses / float64(period)
	if avgLoss == 0 {
		return 100
	}
	rs := avgGain / avgLoss
	return 100 - (100 / (1 + rs))
}

func bollingerBands(closes []float64, period int, mult float64) (upper, mid, lower float64) {
	if period > len(closes) {
		period = len(closes)
	}
	slice := closes[len(closes)-period:]
	mid = sma(slice, period)
	variance := 0.0
	for _, v := range slice {
		d := v - mid
		variance += d * d
	}
	stddev := math.Sqrt(variance / float64(period))
	upper = mid + mult*stddev
	lower = mid - mult*stddev
	return
}

func atr(closes []float64, period int) float64 {
	if len(closes) < period+1 {
		return 0
	}
	sum := 0.0
	for i := len(closes) - period; i < len(closes); i++ {
		sum += math.Abs(closes[i] - closes[i-1])
	}
	return sum / float64(period)
}

// supertrend uses close-to-close ATR approximation (no OHLC available).
// Returns direction (+1 bullish / -1 bearish) and the trend line value.
func supertrend(closes []float64, period int, mult float64) (direction int, line float64) {
	if len(closes) < period+1 {
		return 1, closes[len(closes)-1]
	}

	last := closes[len(closes)-1]
	a := atr(closes, period)
	mid := sma(closes, period)

	upperBand := mid + mult*a
	lowerBand := mid - mult*a

	if last > upperBand {
		return 1, lowerBand
	}
	if last < lowerBand {
		return -1, upperBand
	}
	// In the band: use momentum to decide
	if last >= closes[len(closes)-2] {
		return 1, lowerBand
	}
	return -1, upperBand
}
