package api

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	"github.com/gunh0/midas-touch/internal/advisor"
	"github.com/gunh0/midas-touch/internal/marketdata"
	"github.com/gunh0/midas-touch/internal/mongodb"
	"github.com/gunh0/midas-touch/internal/service"
	"github.com/gunh0/midas-touch/internal/telegram"
)

type Handler struct {
	db           *mongodb.Client
	marketClient *marketdata.Client
	tgClient     *telegram.Client
	signalSvc    *service.SignalService
}

func NewHandler(db *mongodb.Client, mc *marketdata.Client) *Handler {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	chatID := os.Getenv("TELEGRAM_CHAT_ID")
	return &Handler{
		db:           db,
		marketClient: mc,
		tgClient:     telegram.NewClient(token, chatID),
		signalSvc:    service.NewSignalService(db, mc),
	}
}

func NewRouter(h *Handler) *gin.Engine {
	r := gin.New()
	_ = r.SetTrustedProxies(nil)
	r.Use(gin.Logger(), gin.Recovery(), corsMiddleware())

	r.GET("/swagger.json", h.swaggerDoc)
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler, ginSwagger.URL("/swagger.json")))

	api := r.Group("/api")
	{
		api.GET("/health", h.health)
		api.GET("/universe", h.getUniverse)
		api.POST("/universe", h.addUniverse)
		api.DELETE("/universe", h.removeUniverse)
		api.GET("/view-history", h.getViewHistory)
		api.POST("/view-history", h.touchViewHistory)
		api.DELETE("/view-history", h.removeViewHistory)
		api.GET("/sources/status", h.sourcesStatus)
		api.GET("/candles", h.candles)
		api.GET("/signal", h.signal)
		api.POST("/signals/batch", h.signalsBatch)
		api.GET("/signals", h.signals)
		api.GET("/symbols/search", h.searchSymbols)
		api.GET("/valuation", h.valuation)
		api.POST("/notify", h.notify)
		api.GET("/watchlist", h.getWatchlist)
		api.POST("/watchlist", h.addWatchlist)
		api.POST("/watchlist/pin", h.pinWatchlist)
		api.POST("/watchlist/favorites/analyze-notify", h.analyzeNotifyFavorites)
		api.POST("/watchlist/reorder", h.reorderWatchlist)
		api.DELETE("/watchlist", h.removeWatchlist)
		api.GET("/db/stats", h.dbStats)
		api.POST("/db/prune", h.dbPrune)
	}

	return r
}

type valuationModelResult struct {
	Name      string  `json:"name"`
	Value     float64 `json:"value"`
	Available bool    `json:"available"`
	Note      string  `json:"note,omitempty"`
}

func clampFloat(v, minV, maxV float64) float64 {
	if v < minV {
		return minV
	}
	if v > maxV {
		return maxV
	}
	return v
}

func modelDCF(in marketdata.ValuationInputs) valuationModelResult {
	if in.FreeCashflow <= 0 || in.SharesOutstanding <= 0 {
		return valuationModelResult{Name: "DCF", Available: false, Note: "insufficient free cash flow data"}
	}

	fcfPerShare := in.FreeCashflow / in.SharesOutstanding
	g := in.EarningsGrowth
	if g == 0 {
		g = 0.06
	}
	g = clampFloat(g, -0.02, 0.18)
	r := 0.10
	tg := 0.03

	pv := 0.0
	for y := 1; y <= 5; y++ {
		fcf := fcfPerShare * math.Pow(1+g, float64(y))
		pv += fcf / math.Pow(1+r, float64(y))
	}
	terminalCF := fcfPerShare * math.Pow(1+g, 5) * (1 + tg)
	terminal := terminalCF / (r - tg)
	pv += terminal / math.Pow(1+r, 5)

	return valuationModelResult{Name: "DCF", Value: pv, Available: true}
}

func modelComparables(in marketdata.ValuationInputs) valuationModelResult {
	values := []float64{}
	eps := in.TrailingEPS
	if eps <= 0 {
		eps = in.ForwardEPS
	}
	if eps > 0 {
		values = append(values, eps*18.0)
	}
	if in.BookValue > 0 {
		values = append(values, in.BookValue*3.0)
	}
	if in.TargetMeanPrice > 0 {
		values = append(values, in.TargetMeanPrice)
	}
	if len(values) == 0 {
		return valuationModelResult{Name: "Industry Multiples", Available: false, Note: "missing EPS/book value inputs"}
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return valuationModelResult{Name: "Industry Multiples", Value: sum / float64(len(values)), Available: true}
}

func modelDDM(in marketdata.ValuationInputs) valuationModelResult {
	if in.DividendRate <= 0 {
		return valuationModelResult{Name: "Dividend Discount", Available: false, Note: "no regular dividend"}
	}
	r := 0.09
	g := 0.03
	d1 := in.DividendRate * (1 + g)
	value := d1 / (r - g)
	return valuationModelResult{Name: "Dividend Discount", Value: value, Available: true}
}

func blendedValuation(models []valuationModelResult) valuationModelResult {
	available := make([]float64, 0, len(models))
	for _, m := range models {
		if m.Available && m.Value > 0 {
			available = append(available, m.Value)
		}
	}
	if len(available) == 0 {
		return valuationModelResult{Name: "Blended Fair Value", Available: false, Note: "no available valuation model"}
	}
	sum := 0.0
	for _, v := range available {
		sum += v
	}
	return valuationModelResult{Name: "Blended Fair Value", Value: sum / float64(len(available)), Available: true}
}

func (h *Handler) valuation(c *gin.Context) {
	symbol := service.NormalizeSymbol(c.Query("symbol"))
	inputs, err := h.marketClient.FetchValuationInputs(symbol)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	dcf := modelDCF(inputs)
	comps := modelComparables(inputs)
	ddm := modelDDM(inputs)
	blend := blendedValuation([]valuationModelResult{dcf, comps, ddm})

	upsidePct := 0.0
	if blend.Available && inputs.CurrentPrice > 0 {
		upsidePct = ((blend.Value - inputs.CurrentPrice) / inputs.CurrentPrice) * 100
	}

	c.JSON(http.StatusOK, gin.H{
		"symbol": symbol,
		"currency": inputs.Currency,
		"current_price": inputs.CurrentPrice,
		"upside_pct": upsidePct,
		"models": []valuationModelResult{dcf, comps, ddm, blend},
		"inputs": gin.H{
			"target_mean_price": inputs.TargetMeanPrice,
			"target_low_price": inputs.TargetLowPrice,
			"target_high_price": inputs.TargetHighPrice,
			"free_cashflow": inputs.FreeCashflow,
			"earnings_growth": inputs.EarningsGrowth,
			"shares_outstanding": inputs.SharesOutstanding,
			"trailing_eps": inputs.TrailingEPS,
			"forward_eps": inputs.ForwardEPS,
			"book_value": inputs.BookValue,
			"dividend_rate": inputs.DividendRate,
			"dividend_yield": inputs.DividendYield,
		},
		"assumptions": gin.H{
			"discount_rate": 0.10,
			"terminal_growth": 0.03,
			"pe_proxy": 18.0,
			"pb_proxy": 3.0,
			"note": "Model outputs are heuristic estimates, not investment advice.",
		},
	})
}

type SourceStatus struct {
	Source    string  `json:"source"`
	OK        bool    `json:"ok"`
	LatencyMS int64   `json:"latency_ms"`
	Error     string  `json:"error,omitempty"`
	Detail    string  `json:"detail,omitempty"`
	CheckedAt string  `json:"checked_at"`
	Score     float64 `json:"score"`
}

func (h *Handler) swaggerDoc(c *gin.Context) {
	b, err := os.ReadFile("docs/swagger.json")
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "swagger doc not found; run: make swagger"})
		return
	}
	c.Data(http.StatusOK, "application/json", b)
}

// health godoc
// @Summary Health check
// @Tags system
// @Produce json
// @Success 200 {object} map[string]string
// @Router /api/health [get]
func (h *Handler) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "time": time.Now().Format(time.RFC3339)})
}

// sourcesStatus godoc
// @Summary External data source status
// @Tags system
// @Produce json
// @Success 200 {array} SourceStatus
// @Router /api/sources/status [get]
func (h *Handler) sourcesStatus(c *gin.Context) {
	type probe struct {
		source string
		detail string
		check  func(context.Context) error
	}

	probes := []probe{
		{
			source: "finnhub",
			detail: "quote API",
			check: func(ctx context.Context) error {
				_, err := h.marketClient.FetchQuotes([]string{advisor.SymbolNVDA})
				return err
			},
		},
		{
			source: "yahoo",
			detail: "chart API",
			check: func(ctx context.Context) error {
				_, err := h.marketClient.FetchDailyBars(advisor.SymbolNVDA, 10)
				return err
			},
		},
		{
			source: "frankfurter",
			detail: "USD/KRW FX",
			check: func(ctx context.Context) error {
				_, err := h.marketClient.FetchUSDKRWRate()
				return err
			},
		},
	}

	results := make([]SourceStatus, len(probes))
	var wg sync.WaitGroup
	for i, p := range probes {
		wg.Add(1)
		go func(idx int, pr probe) {
			defer wg.Done()
			started := time.Now()
			ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
			defer cancel()
			err := pr.check(ctx)
			latency := time.Since(started).Milliseconds()
			status := SourceStatus{
				Source:    pr.source,
				LatencyMS: latency,
				Detail:    pr.detail,
				CheckedAt: time.Now().Format(time.RFC3339),
				OK:        err == nil,
			}
			if err != nil {
				status.Error = err.Error()
			}
			if status.OK {
				status.Score = 1.0
			} else if strings.Contains(strings.ToLower(status.Error), "403") {
				status.Score = 0.5
			} else {
				status.Score = 0.0
			}
			results[idx] = status
		}(i, p)
	}
	wg.Wait()

	c.JSON(http.StatusOK, results)
}

// candles godoc
// @Summary Get candles
// @Tags market
// @Produce json
// @Param symbol query string false "Ticker symbol" default(NVDA)
// @Param timeframe query string false "1d,5,15,30,60,120,240" default(1d)
// @Param limit query int false "rows" default(300)
// @Success 200 {array} mongodb.CandleDoc
// @Failure 500 {object} map[string]string
// @Router /api/candles [get]
func (h *Handler) candles(c *gin.Context) {
	symbol := service.NormalizeSymbol(c.Query("symbol"))
	timeframe := service.NormalizeTimeframe(c.Query("timeframe"))
	limit := 300
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	docs, err := h.db.GetCandles(ctx, symbol, timeframe, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if len(docs) == 0 {
		var bars []marketdata.OHLCVBar
		source := "yahoo"
		if timeframe == "1d" {
			bars, err = h.marketClient.FetchDailyBars(symbol, limit)
		} else {
			source = "finnhub|yahoo"
			bars, err = h.marketClient.FetchIntradayBars(symbol, timeframe, limit)
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		candles := make([]mongodb.CandleDoc, len(bars))
		for i, b := range bars {
			candles[i] = mongodb.CandleDoc{
				Symbol:    symbol,
				Timeframe: timeframe,
				Source:    source,
				Timestamp: b.Timestamp,
				Open:      b.Open,
				High:      b.High,
				Low:       b.Low,
				Close:     b.Close,
				Volume:    b.Volume,
			}
		}
		if err := h.db.UpsertCandles(ctx, candles); err == nil {
			docs, _ = h.db.GetCandles(ctx, symbol, timeframe, limit)
		}
	}

	c.JSON(http.StatusOK, docs)
}

// signal godoc
// @Summary Evaluate a signal
// @Tags signal
// @Produce json
// @Param symbol query string false "Ticker symbol" default(NVDA)
// @Param timing_tf query string false "60,120,240" default(120)
// @Success 200 {object} map[string]interface{}
// @Failure 500 {object} map[string]string
// @Router /api/signal [get]
func (h *Handler) signal(c *gin.Context) {
	symbol := service.NormalizeSymbol(c.Query("symbol"))
	timingTF := service.NormalizeTimeframe(c.Query("timing_tf"))
	if timingTF == "1d" {
		timingTF = "120"
	}

	reco, err := h.signalSvc.EvaluateCached(c.Request.Context(), symbol, timingTF)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	h.signalSvc.SaveSignal(c.Request.Context(), symbol, reco, false)
	c.JSON(http.StatusOK, h.signalPayload(reco))
}

func (h *Handler) signalPayload(reco advisor.Recommendation) gin.H {
	return gin.H{
		"symbol":            reco.TargetSymbol,
		"action":            reco.Action,
		"trend_action":      reco.TrendAction,
		"timing_action":     reco.TimingAction,
		"weekly_action":     reco.WeeklyAction,
		"is_special_signal": reco.IsSpecial,
		"data_quality_note": reco.DataQualityNote,
		"buy_pct":           reco.BuyPercent,
		"sell_pct":          reco.SellPercent,
		"hold_pct":          reco.HoldPercent,
		"reason":            reco.Reason,
		"timeframe_bias":    reco.TimeframeBias,
		"indicators":        reco.Indicators,
		"timing_indicators": reco.Timing,
		"score":             reco.Score,
		"timing_score":      reco.TimingScore,
		"timestamp":         reco.Timestamp,
	}
}

type signalBatchRequest struct {
	Symbols []string `json:"symbols"`
}

func (h *Handler) signalsBatch(c *gin.Context) {
	var req signalBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	timingTF := service.NormalizeTimeframe(c.Query("timing_tf"))
	if timingTF == "1d" {
		timingTF = "120"
	}

	normalized := make([]string, 0, len(req.Symbols))
	seen := map[string]struct{}{}
	for _, sym := range req.Symbols {
		n := service.NormalizeSymbol(sym)
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		normalized = append(normalized, n)
	}

	if len(normalized) == 0 {
		c.JSON(http.StatusOK, []gin.H{})
		return
	}

	workerCount := 4
	if workerCount > len(normalized) {
		workerCount = len(normalized)
	}

	results := make([]gin.H, len(normalized))
	indexBySymbol := make(map[string]int, len(normalized))
	for i, sym := range normalized {
		indexBySymbol[sym] = i
	}

	jobs := make(chan string, len(normalized))
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for sym := range jobs {
				reco, err := h.signalSvc.EvaluateCached(c.Request.Context(), sym, timingTF)
				idx := indexBySymbol[sym]
				if err != nil {
					results[idx] = gin.H{"symbol": sym, "error": err.Error()}
					continue
				}
				h.signalSvc.SaveSignal(c.Request.Context(), sym, reco, false)
				results[idx] = h.signalPayload(reco)
			}
		}()
	}

	for _, sym := range normalized {
		jobs <- sym
	}
	close(jobs)
	wg.Wait()

	out := make([]gin.H, 0, len(results))
	for _, row := range results {
		if row != nil {
			out = append(out, row)
		}
	}

	c.JSON(http.StatusOK, out)
}

// notify godoc
// @Summary Evaluate and send Telegram signal
// @Tags signal
// @Produce json
// @Param symbol query string false "Ticker symbol" default(NVDA)
// @Param timing_tf query string false "60,120,240" default(120)
// @Success 200 {object} map[string]interface{}
// @Failure 500 {object} map[string]string
// @Router /api/notify [post]
func (h *Handler) notify(c *gin.Context) {
	symbol := service.NormalizeSymbol(c.Query("symbol"))
	timingTF := service.NormalizeTimeframe(c.Query("timing_tf"))
	if timingTF == "1d" {
		timingTF = "120"
	}

	reco, err := h.signalSvc.Evaluate(c.Request.Context(), symbol, timingTF)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	manualHeader := fmt.Sprintf("✅ 웹 전송 시그널 [MANUAL] symbol=%s timing=%s", reco.TargetSymbol, strings.ToUpper(timingTF))
	msg := manualHeader + "\n\n" + advisor.FormatMessage(reco)
	if err := h.tgClient.SendMessage(msg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "telegram: " + err.Error()})
		return
	}
	h.signalSvc.SaveSignal(c.Request.Context(), symbol, reco, true)

	c.JSON(http.StatusOK, gin.H{"ok": true, "symbol": reco.TargetSymbol, "action": reco.Action, "is_special": reco.IsSpecial})
}

func (h *Handler) analyzeNotifyFavorites(c *gin.Context) {
	timingTF := service.NormalizeTimeframe(c.Query("timing_tf"))
	if timingTF == "1d" {
		timingTF = "120"
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()

	items, err := h.db.GetWatchlist(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	favorites := make([]mongodb.WatchlistItem, 0, len(items))
	for _, item := range items {
		if item.Pinned {
			favorites = append(favorites, item)
		}
	}

	if len(favorites) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"ok":           true,
			"target_count": 0,
			"sent_count":   0,
			"buy_count":    0,
			"hold_count":   0,
			"sell_count":   0,
			"failed":       []gin.H{},
		})
		return
	}

	sentCount := 0
	buyCount := 0
	holdCount := 0
	sellCount := 0
	failed := make([]gin.H, 0)

	for i, item := range favorites {
		reco, evalErr := h.signalSvc.Evaluate(ctx, item.Symbol, timingTF)
		if evalErr != nil {
			failed = append(failed, gin.H{"symbol": item.Symbol, "stage": "evaluate", "error": evalErr.Error()})
			continue
		}

		switch reco.Action {
		case "BUY":
			buyCount++
		case "SELL":
			sellCount++
		default:
			holdCount++
		}

		header := fmt.Sprintf("⭐ 즐겨찾기 일괄 분석 [FAVORITES] %d/%d symbol=%s timing=%s", i+1, len(favorites), reco.TargetSymbol, strings.ToUpper(timingTF))
		msg := header + "\n\n" + advisor.FormatMessage(reco)
		if sendErr := h.tgClient.SendMessage(msg); sendErr != nil {
			failed = append(failed, gin.H{"symbol": item.Symbol, "stage": "notify", "error": sendErr.Error()})
			continue
		}

		h.signalSvc.SaveSignal(ctx, item.Symbol, reco, true)
		sentCount++
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":           true,
		"target_count": len(favorites),
		"sent_count":   sentCount,
		"buy_count":    buyCount,
		"hold_count":   holdCount,
		"sell_count":   sellCount,
		"failed":       failed,
	})
}

func (h *Handler) signals(c *gin.Context) {
	symbol := service.NormalizeSymbol(c.Query("symbol"))
	limit := 20
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	docs, err := h.db.GetRecentSignals(ctx, symbol, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, docs)
}

func (h *Handler) searchSymbols(c *gin.Context) {
	query := strings.TrimSpace(c.Query("q"))
	limit := 10
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	results, err := h.marketClient.SearchSymbols(query, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, results)
}

func (h *Handler) dbStats(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	stats, err := h.db.GetDBStats(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, stats)
}

func (h *Handler) dbPrune(c *gin.Context) {
	symbol := service.NormalizeSymbol(c.Query("symbol"))
	timeframe := service.NormalizeTimeframe(c.Query("timeframe"))
	keepDays := 365
	if k := c.Query("keep_days"); k != "" {
		if n, err := strconv.Atoi(k); err == nil && n > 0 {
			keepDays = n
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	deleted, err := h.db.PruneOldCandles(ctx, symbol, timeframe, keepDays)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": deleted, "symbol": symbol, "timeframe": timeframe, "keep_days": keepDays})
}

func (h *Handler) getWatchlist(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	items, err := h.db.GetWatchlist(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, items)
}

func (h *Handler) addWatchlist(c *gin.Context) {
	symbol := service.NormalizeSymbol(c.Query("symbol"))
	notifyMinutes := 3
	notifyMode := c.DefaultQuery("notify_mode", "event")
	if n := c.Query("notify_interval_minutes"); n != "" {
		if parsed, err := strconv.Atoi(n); err == nil && parsed > 0 {
			notifyMinutes = parsed
		}
	}
	if n := c.Query("notify_interval_hours"); n != "" {
		if parsed, err := strconv.Atoi(n); err == nil && parsed > 0 {
			notifyMinutes = parsed * 60
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	if err := h.db.AddToWatchlist(ctx, symbol, notifyMinutes, notifyMode); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"symbol": symbol, "notify_interval_minutes": notifyMinutes, "notify_mode": notifyMode})
}

func (h *Handler) removeWatchlist(c *gin.Context) {
	symbol := service.NormalizeSymbol(c.Query("symbol"))
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	if err := h.db.RemoveFromWatchlist(ctx, symbol); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"symbol": symbol})
}

func (h *Handler) getUniverse(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	docs, err := h.db.GetUniverseSymbols(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if len(docs) == 0 {
		if err := h.db.EnsureBaseUniverse(ctx, advisor.PopularLeaderSymbols()); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		docs, err = h.db.GetUniverseSymbols(ctx)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	c.JSON(http.StatusOK, docs)
}

func (h *Handler) addUniverse(c *gin.Context) {
	symbol := service.NormalizeSymbol(c.Query("symbol"))
	kind := strings.TrimSpace(c.DefaultQuery("kind", "custom"))
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	if err := h.db.AddUniverseSymbol(ctx, symbol, kind); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"symbol": symbol, "kind": kind})
}

func (h *Handler) removeUniverse(c *gin.Context) {
	symbol := service.NormalizeSymbol(c.Query("symbol"))
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	if err := h.db.RemoveUniverseSymbol(ctx, symbol); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	_ = h.db.RemoveFromWatchlist(ctx, symbol)
	c.JSON(http.StatusOK, gin.H{"symbol": symbol})
}

func (h *Handler) getViewHistory(c *gin.Context) {
	limit := 20
	if q := c.Query("limit"); q != "" {
		if parsed, err := strconv.Atoi(q); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	history, err := h.db.GetViewHistory(ctx, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, history)
}

func (h *Handler) touchViewHistory(c *gin.Context) {
	symbol := service.NormalizeSymbol(c.Query("symbol"))
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	if err := h.db.TouchViewHistory(ctx, symbol, 20); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"symbol": symbol})
}

func (h *Handler) removeViewHistory(c *gin.Context) {
	symbol := service.NormalizeSymbol(c.Query("symbol"))
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	if err := h.db.RemoveViewHistorySymbol(ctx, symbol); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"symbol": symbol})
}

func (h *Handler) pinWatchlist(c *gin.Context) {
	symbol := service.NormalizeSymbol(c.Query("symbol"))
	pinned := true
	if p := c.Query("pinned"); p != "" {
		if parsed, err := strconv.ParseBool(p); err == nil {
			pinned = parsed
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	if err := h.db.SetWatchlistPinned(ctx, symbol, pinned); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"symbol": symbol, "pinned": pinned})
}

type watchlistReorderRequest struct {
	Symbols []string `json:"symbols"`
}

func (h *Handler) reorderWatchlist(c *gin.Context) {
	var req watchlistReorderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	if err := h.db.ReorderWatchlist(ctx, req.Symbols); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"count": len(req.Symbols)})
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
