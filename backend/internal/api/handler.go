package api

import (
	"context"
	"fmt"
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
		api.GET("/sources/status", h.sourcesStatus)
		api.GET("/candles", h.candles)
		api.GET("/signal", h.signal)
		api.GET("/signals", h.signals)
		api.GET("/symbols/search", h.searchSymbols)
		api.POST("/notify", h.notify)
		api.GET("/watchlist", h.getWatchlist)
		api.POST("/watchlist", h.addWatchlist)
		api.POST("/watchlist/pin", h.pinWatchlist)
		api.POST("/watchlist/reorder", h.reorderWatchlist)
		api.DELETE("/watchlist", h.removeWatchlist)
		api.GET("/db/stats", h.dbStats)
		api.POST("/db/prune", h.dbPrune)
	}

	return r
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

	reco, err := h.signalSvc.Evaluate(c.Request.Context(), symbol, timingTF)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	h.signalSvc.SaveSignal(c.Request.Context(), symbol, reco, false)

	c.JSON(http.StatusOK, gin.H{
		"symbol":            reco.TargetSymbol,
		"action":            reco.Action,
		"trend_action":      reco.TrendAction,
		"timing_action":     reco.TimingAction,
		"is_special_signal": reco.IsSpecial,
		"buy_pct":           reco.BuyPercent,
		"sell_pct":          reco.SellPercent,
		"hold_pct":          reco.HoldPercent,
		"reason":            reco.Reason,
		"indicators":        reco.Indicators,
		"timing_indicators": reco.Timing,
		"score":             reco.Score,
		"timing_score":      reco.TimingScore,
		"timestamp":         reco.Timestamp,
	})
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
	notifyHours := 4
	notifyMode := c.DefaultQuery("notify_mode", "event")
	if n := c.Query("notify_interval_hours"); n != "" {
		if parsed, err := strconv.Atoi(n); err == nil && parsed > 0 {
			notifyHours = parsed
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	if err := h.db.AddToWatchlist(ctx, symbol, notifyHours, notifyMode); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"symbol": symbol, "notify_interval_hours": notifyHours, "notify_mode": notifyMode})
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
