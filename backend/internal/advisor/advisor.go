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
	TargetSymbol string
	BuyPercent   float64
	SellPercent  float64
	HoldPercent  float64
	Action       string
	Reason       string
	USDKRWRate   float64
	Snapshot     map[string]marketdata.Quote
	Indicators   Indicators
	Score        SignalScore
	Timestamp    time.Time
}

func RequiredSymbols() []string {
	return []string{SymbolNVDA, SymbolVIX, SymbolNQ, SymbolUSDKRW}
}

// Evaluate computes Buy/Hold/Sell probabilities purely from chart indicators.
// symbol: the ticker being analyzed (used for labeling only).
// closes: daily close prices oldest-first, at least 50 required.
func Evaluate(symbol string, snapshot map[string]marketdata.Quote, closes []float64, now time.Time) (Recommendation, error) {
	if len(closes) < 50 {
		return Recommendation{}, fmt.Errorf("need at least 50 closes, got %d", len(closes))
	}

	ind := computeIndicators(closes)
	score, reasons := scoreFromIndicators(ind, closes)
	buy, sell, hold := toProbabilities(score.Total)
	action := dominantAction(buy, sell, hold)

	return Recommendation{
		TargetSymbol: symbol,
		BuyPercent:   buy,
		SellPercent:  sell,
		HoldPercent:  hold,
		Action:       action,
		Reason:       strings.Join(reasons, "; "),
		Snapshot:     snapshot,
		Indicators:   ind,
		Score:        score,
		Timestamp:    now,
	}, nil
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
	stDir := "Bullish"
	if r.Indicators.SupertrendDir < 0 {
		stDir = "Bearish"
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

	return fmt.Sprintf(
		"Midas Touch Signal\n"+
			"Time: %s\n\n"+
			"[%s] %s\n"+
			"Buy: %.0f%% | Hold: %.0f%% | Sell: %.0f%%\n\n"+
			"Indicators\n"+
			"- RSI14: %.1f\n"+
			"- SMA20: %.2f | SMA50: %.2f\n"+
			"- BB: %.2f / %.2f / %.2f\n"+
			"- Supertrend: %s (%.2f)\n"+
			"- ATR14: %.2f\n\n"+
			"Market\n"+
			"- %s: $%.2f (%+.2f%%)\n"+
			"- VIX: %+.2f%% | NQ: %+.2f%%\n"+
			"- USD/KRW: %.2f\n\n"+
			"Reason: %s",
		r.Timestamp.Format("2006-01-02 15:04 KST"),
		r.TargetSymbol, r.Action,
		r.BuyPercent, r.HoldPercent, r.SellPercent,
		r.Indicators.RSI14,
		r.Indicators.SMA20, r.Indicators.SMA50,
		r.Indicators.BBUpper, r.Indicators.BBMid, r.Indicators.BBLower,
		stDir, r.Indicators.SupertrendLine,
		r.Indicators.ATR14,
		r.TargetSymbol, targetQuote.Price, targetQuote.ChangePercent,
		vixPct, nqPct,
		r.USDKRWRate,
		r.Reason,
	)
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
