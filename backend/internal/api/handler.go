package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gunh0/midas-touch/internal/advisor"
	"github.com/gunh0/midas-touch/internal/marketdata"
	"github.com/gunh0/midas-touch/internal/mongodb"
	"github.com/gunh0/midas-touch/internal/telegram"
)

type Handler struct {
	db           *mongodb.Client
	marketClient *marketdata.Client
	tgClient     *telegram.Client
}

func NewHandler(db *mongodb.Client, mc *marketdata.Client) *Handler {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	chatID := os.Getenv("TELEGRAM_CHAT_ID")
	return &Handler{
		db:           db,
		marketClient: mc,
		tgClient:     telegram.NewClient(token, chatID),
	}
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", h.health)
	mux.HandleFunc("GET /api/candles", h.candles)
	mux.HandleFunc("GET /api/signal", h.signal)
	mux.HandleFunc("GET /api/signals", h.signals)
	mux.HandleFunc("POST /api/notify", h.notify)
	mux.HandleFunc("GET /api/watchlist", h.getWatchlist)
	mux.HandleFunc("POST /api/watchlist", h.addWatchlist)
	mux.HandleFunc("DELETE /api/watchlist", h.removeWatchlist)
	mux.HandleFunc("GET /api/db/stats", h.dbStats)
	mux.HandleFunc("POST /api/db/prune", h.dbPrune)
	return corsMiddleware(mux)
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "time": time.Now().Format(time.RFC3339)})
}

// GET /api/candles?symbol=NVDA&limit=300
func (h *Handler) candles(w http.ResponseWriter, r *http.Request) {
	symbol := normalizeSymbol(r.URL.Query().Get("symbol"))
	limit := 300
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	docs, err := h.db.GetCandles(ctx, symbol, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// If DB is empty, fetch from market and store
	if len(docs) == 0 {
		bars, err := h.marketClient.FetchDailyBars(symbol, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		candles := make([]mongodb.CandleDoc, len(bars))
		for i, b := range bars {
			candles[i] = mongodb.CandleDoc{
				Symbol:    symbol,
				Timestamp: b.Timestamp,
				Open:      b.Open,
				High:      b.High,
				Low:       b.Low,
				Close:     b.Close,
				Volume:    b.Volume,
			}
		}
		if err := h.db.UpsertCandles(ctx, candles); err != nil {
			log.Printf("warn: upsert candles: %v", err)
		}
		docs, _ = h.db.GetCandles(ctx, symbol, limit)
	}

	writeJSON(w, http.StatusOK, docs)
}

// GET /api/signal?symbol=NVDA
func (h *Handler) signal(w http.ResponseWriter, r *http.Request) {
	symbol := normalizeSymbol(r.URL.Query().Get("symbol"))

	reco, err := h.evaluate(r.Context(), symbol)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"symbol":     reco.TargetSymbol,
		"action":     reco.Action,
		"buy_pct":    reco.BuyPercent,
		"sell_pct":   reco.SellPercent,
		"hold_pct":   reco.HoldPercent,
		"reason":     reco.Reason,
		"indicators": reco.Indicators,
		"score":      reco.Score,
		"timestamp":  reco.Timestamp,
	})
}

// POST /api/notify?symbol=NVDA  — analyze and send to Telegram immediately
func (h *Handler) notify(w http.ResponseWriter, r *http.Request) {
	symbol := normalizeSymbol(r.URL.Query().Get("symbol"))

	reco, err := h.evaluate(r.Context(), symbol)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	msg := advisor.FormatMessage(reco)
	if err := h.tgClient.SendMessage(msg); err != nil {
		writeError(w, http.StatusInternalServerError, "telegram: "+err.Error())
		return
	}

	log.Printf("manual notify sent: symbol=%s action=%s", symbol, reco.Action)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"symbol": reco.TargetSymbol,
		"action": reco.Action,
	})
}

// GET /api/signals?symbol=NVDA&limit=20
func (h *Handler) signals(w http.ResponseWriter, r *http.Request) {
	symbol := normalizeSymbol(r.URL.Query().Get("symbol"))
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	docs, err := h.db.GetRecentSignals(ctx, symbol, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, docs)
}

// GET /api/db/stats
func (h *Handler) dbStats(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	stats, err := h.db.GetDBStats(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// POST /api/db/prune?symbol=NVDA&keep_days=365
func (h *Handler) dbPrune(w http.ResponseWriter, r *http.Request) {
	symbol := normalizeSymbol(r.URL.Query().Get("symbol"))
	keepDays := 365
	if k := r.URL.Query().Get("keep_days"); k != "" {
		if n, err := strconv.Atoi(k); err == nil && n > 0 {
			keepDays = n
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	deleted, err := h.db.PruneOldCandles(ctx, symbol, keepDays)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"deleted": deleted, "symbol": symbol, "keep_days": keepDays})
}

// evaluate is the shared logic for signal + notify.
func (h *Handler) evaluate(ctx context.Context, symbol string) (advisor.Recommendation, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Fetch market context (VIX, NQ, USDKRW) + the target symbol quote
	contextSymbols := []string{advisor.SymbolVIX, advisor.SymbolNQ, advisor.SymbolUSDKRW}
	snapshot, err := h.marketClient.FetchQuotes(contextSymbols)
	if err != nil {
		log.Printf("warn: fetch context quotes: %v", err)
		snapshot = make(map[string]marketdata.Quote)
	}

	// Also fetch a quote for the target symbol itself
	targetQuotes, err := h.marketClient.FetchQuotes([]string{symbol})
	if err == nil {
		for k, v := range targetQuotes {
			snapshot[k] = v
		}
	}

	closes, err := h.marketClient.FetchDailyCloses(symbol, 300)
	if err != nil {
		return advisor.Recommendation{}, fmt.Errorf("fetch closes for %s: %w", symbol, err)
	}

	usdkrw, err := h.marketClient.FetchUSDKRWRate()
	if err != nil {
		log.Printf("warn: fetch usdkrw: %v", err)
	}

	reco, err := advisor.Evaluate(symbol, snapshot, closes, time.Now())
	if err != nil {
		return advisor.Recommendation{}, fmt.Errorf("evaluate %s: %w", symbol, err)
	}
	reco.USDKRWRate = usdkrw

	// Persist signal
	sig := mongodb.SignalDoc{
		Symbol:    symbol,
		Timestamp: reco.Timestamp,
		Action:    reco.Action,
		BuyPct:    reco.BuyPercent,
		SellPct:   reco.SellPercent,
		HoldPct:   reco.HoldPercent,
		Reason:    reco.Reason,
	}
	if err := h.db.SaveSignal(ctx, sig); err != nil {
		log.Printf("warn: save signal: %v", err)
	}

	return reco, nil
}

// normalizeSymbol uppercases and trims the symbol; defaults to NVDA.
func normalizeSymbol(s string) string {
	s = strings.TrimSpace(strings.ToUpper(s))
	if s == "" {
		return advisor.SymbolNVDA
	}
	return s
}

// GET /api/watchlist
func (h *Handler) getWatchlist(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	items, err := h.db.GetWatchlist(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

// POST /api/watchlist?symbol=AAPL
func (h *Handler) addWatchlist(w http.ResponseWriter, r *http.Request) {
	symbol := normalizeSymbol(r.URL.Query().Get("symbol"))
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := h.db.AddToWatchlist(ctx, symbol); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"symbol": symbol})
}

// DELETE /api/watchlist?symbol=AAPL
func (h *Handler) removeWatchlist(w http.ResponseWriter, r *http.Request) {
	symbol := normalizeSymbol(r.URL.Query().Get("symbol"))
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := h.db.RemoveFromWatchlist(ctx, symbol); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"symbol": symbol})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
