package advisor

import (
	"fmt"
	"math"
	"strconv"
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

type Recommendation struct {
	TargetSymbol string
	BuyPercent   float64
	SellPercent  float64
	HoldPercent  float64
	Action       string
	Reason       string
	ReasonKO     string
	USDKRWRate   float64
	RSI14        float64
	Snapshot     map[string]marketdata.Quote
	Horizons     []HorizonRecommendation
	Timestamp    time.Time
}

type HorizonRecommendation struct {
	Label         string
	LookbackDays  int
	BuyPercent    float64
	SellPercent   float64
	HoldPercent   float64
	Action        string
	MomentumPct   float64
	VsSMA20Pct    float64
	VsSMA50Pct    float64
	Score         float64
	PrimaryReason string
}

func RequiredSymbols() []string {
	return []string{SymbolNVDA, SymbolVIX, SymbolNQ, SymbolUSDKRW}
}

func Evaluate(snapshot map[string]marketdata.Quote, nvdaCloses []float64, now time.Time) (Recommendation, error) {
	nvda, ok := snapshot[SymbolNVDA]
	if !ok {
		return Recommendation{}, fmt.Errorf("missing quote for %s", SymbolNVDA)
	}
	vix, ok := snapshot[SymbolVIX]
	if !ok {
		return Recommendation{}, fmt.Errorf("missing quote for %s", SymbolVIX)
	}
	nq, ok := snapshot[SymbolNQ]
	if !ok {
		return Recommendation{}, fmt.Errorf("missing quote for %s", SymbolNQ)
	}
	usdkrw, ok := snapshot[SymbolUSDKRW]
	if !ok {
		return Recommendation{}, fmt.Errorf("missing quote for %s", SymbolUSDKRW)
	}
	if len(nvdaCloses) < 30 {
		return Recommendation{}, fmt.Errorf("insufficient NVDA history: need at least 30 closes")
	}

	macroBias, macroReasons := computeMacroBias(vix, nq, usdkrw, nvda)
	horizons := evaluateHorizons(nvdaCloses, macroBias)
	if len(horizons) == 0 {
		return Recommendation{}, fmt.Errorf("failed to compute horizon recommendations")
	}

	buy, sell, hold := weightedOverall(horizons)
	action := dominantAction(buy, sell, hold)
	combinedReasons := append([]string{}, macroReasons...)
	combinedReasons = append(combinedReasons, fmt.Sprintf("RSI14 is %.1f", rsi(nvdaCloses, 14)))
	reasonText := strings.Join(combinedReasons, "; ")

	return Recommendation{
		TargetSymbol: SymbolNVDA,
		BuyPercent:   buy,
		SellPercent:  sell,
		HoldPercent:  hold,
		Action:       action,
		Reason:       reasonText,
		ReasonKO:     translateReasonToKorean(reasonText),
		RSI14:        rsi(nvdaCloses, 14),
		Snapshot:     snapshot,
		Horizons:     horizons,
		Timestamp:    now,
	}, nil
}

func FormatMessage(r Recommendation) string {
	nvda := r.Snapshot[SymbolNVDA]
	vix := r.Snapshot[SymbolVIX]
	nq := r.Snapshot[SymbolNQ]
	usdkrw := r.Snapshot[SymbolUSDKRW]

	horizonLines := make([]string, 0, len(r.Horizons))
	for _, h := range r.Horizons {
		line := fmt.Sprintf(
			"- %s(%dd): Buy %.0f%% / Sell %.0f%% / Hold %.0f%% | Primary %s | Return %+.2f%% | vs SMA20 %+.2f%% | vs SMA50 %+.2f%%",
			h.Label,
			h.LookbackDays,
			h.BuyPercent,
			h.SellPercent,
			h.HoldPercent,
			h.Action,
			h.MomentumPct,
			h.VsSMA20Pct,
			h.VsSMA50Pct,
		)
		horizonLines = append(horizonLines, line)
	}

	return fmt.Sprintf(
		"Hourly Trading Decision\n"+
			"Time: %s\n\n"+
			"Scope\n"+
			"- Target Symbol: %s\n"+
			"- Recommendation Type: multi-timeframe position decision\n"+
			"- Suggested Move: %s\n\n"+
			"Overall Recommendation\n"+
			"- Buy: %.0f%%\n"+
			"- Sell: %.0f%%\n"+
			"- Hold: %.0f%%\n"+
			"- Primary: %s\n\n"+
			"Timeframe Outlook\n"+
			"%s\n\n"+
			"Key Indicators\n"+
			"- NVDA RSI14: %.1f\n"+
			"Market Snapshot\n"+
			"- USD/KRW Spot: 1 USD = KRW %.2f\n"+
			"- NVDA: %s (%+.2f%%)\n"+
			"- Volatility Proxy (VIXY): %s (%+.2f%%)\n"+
			"- Nasdaq Proxy (QQQ): %+.2f%%\n"+
			"- USD Strength Proxy (UUP): %s (%+.2f%%)\n\n"+
			"Reason (EN): %s\n"+
			"Reason (KO): %s",
		r.Timestamp.Format(time.RFC3339),
		r.TargetSymbol,
		actionGuidance(r.Action),
		r.BuyPercent,
		r.SellPercent,
		r.HoldPercent,
		r.Action,
		strings.Join(horizonLines, "\n"),
		r.RSI14,
		r.USDKRWRate,
		formatUSDWithKRW(nvda.Price, r.USDKRWRate),
		nvda.ChangePercent,
		formatUSDWithKRW(vix.Price, r.USDKRWRate),
		vix.ChangePercent,
		nq.ChangePercent,
		formatUSDWithKRW(usdkrw.Price, r.USDKRWRate),
		usdkrw.ChangePercent,
		r.Reason,
		r.ReasonKO,
	)
}

func computeMacroBias(vix, nq, usdkrw, nvda marketdata.Quote) (float64, []string) {
	bias := 0.0
	reasons := make([]string, 0, 6)

	if vix.ChangePercent >= 3.0 {
		bias -= 1.4
		reasons = append(reasons, "volatility proxy is spiking")
	} else if vix.ChangePercent >= 1.5 {
		bias -= 0.8
		reasons = append(reasons, "volatility proxy is rising")
	} else if vix.ChangePercent <= -1.5 {
		bias += 0.4
		reasons = append(reasons, "volatility proxy is cooling")
	}

	if nq.ChangePercent <= -1.0 {
		bias -= 1.0
		reasons = append(reasons, "Nasdaq proxy is weak")
	} else if nq.ChangePercent >= 1.0 {
		bias += 0.8
		reasons = append(reasons, "Nasdaq proxy is strong")
	}

	if usdkrw.ChangePercent >= 0.8 {
		bias -= 0.7
		reasons = append(reasons, "USD strength adds risk")
	} else if usdkrw.ChangePercent <= -0.5 {
		bias += 0.4
		reasons = append(reasons, "USD weakness supports risk assets")
	}

	if nvda.ChangePercent <= -1.5 {
		bias -= 0.6
		reasons = append(reasons, "NVDA daily momentum is weak")
	} else if nvda.ChangePercent >= 1.5 {
		bias += 0.6
		reasons = append(reasons, "NVDA daily momentum is positive")
	}

	if len(reasons) == 0 {
		reasons = append(reasons, "macro and momentum are mixed")
	}

	return bias, reasons
}

func evaluateHorizons(closes []float64, macroBias float64) []HorizonRecommendation {
	configs := []struct {
		label       string
		lookback    int
		macroWeight float64
	}{
		{label: "Daily", lookback: 5, macroWeight: 0.60},
		{label: "Weekly", lookback: 20, macroWeight: 0.50},
		{label: "Monthly", lookback: 60, macroWeight: 0.40},
		{label: "Quarterly", lookback: 120, macroWeight: 0.30},
		{label: "Yearly", lookback: 252, macroWeight: 0.20},
	}

	last := closes[len(closes)-1]
	sma20 := sma(closes, 20)
	sma50 := sma(closes, 50)

	result := make([]HorizonRecommendation, 0, len(configs))
	for _, cfg := range configs {
		if len(closes) <= cfg.lookback {
			continue
		}

		base := closes[len(closes)-1-cfg.lookback]
		momentumPct := pctChange(last, base)
		trendScore := clamp(momentumPct/4.0, -2.5, 2.5)

		maScore := 0.0
		vs20 := pctChange(last, sma20)
		vs50 := pctChange(last, sma50)
		if vs20 > 0 {
			maScore += 0.5
		} else {
			maScore -= 0.5
		}
		if vs50 > 0 {
			maScore += 0.5
		} else {
			maScore -= 0.5
		}

		score := trendScore + maScore + (macroBias * cfg.macroWeight)
		buy, sell, hold := toProbabilities(score)

		result = append(result, HorizonRecommendation{
			Label:         cfg.label,
			LookbackDays:  cfg.lookback,
			BuyPercent:    buy,
			SellPercent:   sell,
			HoldPercent:   hold,
			Action:        dominantAction(buy, sell, hold),
			MomentumPct:   momentumPct,
			VsSMA20Pct:    vs20,
			VsSMA50Pct:    vs50,
			Score:         score,
			PrimaryReason: horizonReason(momentumPct, vs20, vs50),
		})
	}

	return result
}

func weightedOverall(horizons []HorizonRecommendation) (float64, float64, float64) {
	weights := map[string]float64{
		"Daily":     0.30,
		"Weekly":    0.25,
		"Monthly":   0.20,
		"Quarterly": 0.15,
		"Yearly":    0.10,
	}

	totalWeight := 0.0
	buy := 0.0
	sell := 0.0
	hold := 0.0

	for _, h := range horizons {
		w := weights[h.Label]
		if w == 0 {
			continue
		}
		totalWeight += w
		buy += h.BuyPercent * w
		sell += h.SellPercent * w
		hold += h.HoldPercent * w
	}

	if totalWeight == 0 {
		return 33, 33, 34
	}

	return math.Round(buy / totalWeight), math.Round(sell / totalWeight), math.Round(hold / totalWeight)
}

func pctChange(current, base float64) float64 {
	if base == 0 {
		return 0
	}
	return ((current - base) / base) * 100
}

func sma(values []float64, period int) float64 {
	if len(values) == 0 {
		return 0
	}
	if period > len(values) {
		period = len(values)
	}
	start := len(values) - period
	sum := 0.0
	for i := start; i < len(values); i++ {
		sum += values[i]
	}
	return sum / float64(period)
}

func rsi(closes []float64, period int) float64 {
	if len(closes) < period+1 {
		return 50
	}

	gains := 0.0
	losses := 0.0
	start := len(closes) - (period + 1)
	for i := start + 1; i < len(closes); i++ {
		delta := closes[i] - closes[i-1]
		if delta > 0 {
			gains += delta
		} else {
			losses += -delta
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

func clamp(v, minV, maxV float64) float64 {
	if v < minV {
		return minV
	}
	if v > maxV {
		return maxV
	}
	return v
}

func horizonReason(momentum, vs20, vs50 float64) string {
	parts := make([]string, 0, 3)
	if momentum >= 2 {
		parts = append(parts, "strong positive momentum")
	} else if momentum <= -2 {
		parts = append(parts, "negative momentum")
	} else {
		parts = append(parts, "sideways momentum")
	}

	if vs20 >= 0 && vs50 >= 0 {
		parts = append(parts, "above SMA20 and SMA50")
	} else if vs20 < 0 && vs50 < 0 {
		parts = append(parts, "below SMA20 and SMA50")
	} else {
		parts = append(parts, "mixed around moving averages")
	}

	return strings.Join(parts, ", ")
}

func formatUSDWithKRW(usd, fx float64) string {
	if fx <= 0 {
		return fmt.Sprintf("$%.2f", usd)
	}
	return fmt.Sprintf("$%.2f (KRW %s)", usd, formatKRWWithCommas(usd*fx))
}

func formatKRWWithCommas(value float64) string {
	n := int64(math.Round(value))
	if n == 0 {
		return "0"
	}

	sign := ""
	if n < 0 {
		sign = "-"
		n = -n
	}

	s := strconv.FormatInt(n, 10)
	if len(s) <= 3 {
		return sign + s
	}

	parts := make([]string, 0, (len(s)+2)/3)
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)

	return sign + strings.Join(parts, ",")
}

func actionGuidance(action string) string {
	switch action {
	case "BUY":
		return "consider scaling in"
	case "SELL":
		return "consider reducing exposure"
	default:
		return "wait for clearer confirmation"
	}
}

func translateReasonToKorean(reason string) string {
	translated := reason
	replacements := []struct {
		en string
		ko string
	}{
		{"volatility proxy is spiking", "변동성 프록시가 급등하고 있습니다"},
		{"volatility proxy is rising", "변동성 프록시가 상승 중입니다"},
		{"volatility proxy is cooling", "변동성 프록시가 진정되고 있습니다"},
		{"Nasdaq proxy is weak", "나스닥 프록시 흐름이 약합니다"},
		{"Nasdaq proxy is strong", "나스닥 프록시 흐름이 강합니다"},
		{"USD strength adds risk", "달러 강세가 리스크를 키우고 있습니다"},
		{"USD weakness supports risk assets", "달러 약세가 위험자산에 우호적입니다"},
		{"NVDA daily momentum is weak", "NVDA 일간 모멘텀이 약합니다"},
		{"NVDA daily momentum is positive", "NVDA 일간 모멘텀이 긍정적입니다"},
		{"macro and momentum are mixed", "거시 환경과 모멘텀이 혼조입니다"},
		{"RSI14 is", "RSI14 값은"},
		{"balanced setup", "시장 신호가 혼조라 균형 구간입니다"},
	}

	for _, r := range replacements {
		translated = strings.ReplaceAll(translated, r.en, r.ko)
	}

	return translated
}

func toProbabilities(score float64) (buy, sell, hold float64) {
	xBuy := score / 1.25
	xSell := -score / 1.25
	xHold := 0.85 - math.Abs(score)/2.3

	eb := math.Exp(xBuy)
	es := math.Exp(xSell)
	eh := math.Exp(xHold)
	total := eb + es + eh

	buy = (eb / total) * 100
	sell = (es / total) * 100
	hold = (eh / total) * 100

	return math.Round(buy), math.Round(sell), math.Round(hold)
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
