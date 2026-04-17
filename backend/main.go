package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gunh0/midas-touch/internal/advisor"
	"github.com/gunh0/midas-touch/internal/api"
	"github.com/gunh0/midas-touch/internal/marketdata"
	"github.com/gunh0/midas-touch/internal/mongodb"
	"github.com/gunh0/midas-touch/internal/service"
	"github.com/gunh0/midas-touch/internal/telegram"
)

// @title Midas Touch API
// @version 1.0
// @description Multi-timeframe signal API (daily direction + intraday timing)
// @BasePath /

const (
	defaultSymbol         = advisor.SymbolNVDA
	defaultIntervalHour   = 4
	monitorTickInterval   = 5 * time.Minute
	specialCooldownPeriod = 60 * time.Minute
	popularScanTF         = "120"
	dbFreeTierLimitMB     = 500.0
	dbAlertThresholdRatio = 0.80
	dbAlertCooldown       = 6 * time.Hour
)

func main() {
	gin.SetMode(gin.ReleaseMode)

	cleanupLogger := setupLogger()
	defer cleanupLogger()

	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	chatID := os.Getenv("TELEGRAM_CHAT_ID")
	if token == "" || chatID == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID are required")
	}

	ctx := context.Background()
	db, err := mongodb.NewClient(ctx)
	if err != nil {
		log.Fatalf("mongodb connect: %v", err)
	}
	log.Println("connected to mongodb")

	marketClient := marketdata.NewClient()
	tgClient := telegram.NewClient(token, chatID)
	signalSvc := service.NewSignalService(db, marketClient)

	port := os.Getenv("API_PORT")
	if port == "" {
		port = "8000"
	}
	handler := api.NewHandler(db, marketClient)
	router := api.NewRouter(handler)
	go func() {
		log.Printf("API server listening on :%s", port)
		if err := router.Run(":" + port); err != nil {
			log.Fatalf("api server: %v", err)
		}
	}()

	startupMsg := fmt.Sprintf("서버가 시작되었습니다.\nMidas Touch backend is running on :%s\n시간: %s", port, time.Now().Format("2006-01-02 15:04:05"))
	if err := tgClient.SendMessage(startupMsg); err != nil {
		log.Printf("warn: startup telegram send: %v", err)
	} else {
		log.Printf("startup telegram notification sent")
	}

	runOnce := parseBool(os.Getenv("ADVISOR_RUN_ONCE"))
	var lastDBUsageAlertAt time.Time
	alertCooldownMins := parseIntWithDefault(os.Getenv("ALERT_COOLDOWN_MINUTES"), 45)
	if alertCooldownMins < 0 {
		alertCooldownMins = 0
	}
	alertMinActionPct := parseFloatWithDefault(os.Getenv("ALERT_MIN_ACTION_PCT"), 60)
	if alertMinActionPct < 0 {
		alertMinActionPct = 0
	}
	if alertMinActionPct > 100 {
		alertMinActionPct = 100
	}
	alertMinDeltaPct := parseFloatWithDefault(os.Getenv("ALERT_MIN_DELTA_PCT"), 12)
	if alertMinDeltaPct < 0 {
		alertMinDeltaPct = 0
	}
	policy := AlertPolicy{
		MinActionPct: alertMinActionPct,
		MinDeltaPct:  alertMinDeltaPct,
		Cooldown:     time.Duration(alertCooldownMins) * time.Minute,
	}
	popularScanInterval := parseIntWithDefault(os.Getenv("POPULAR_SCAN_INTERVAL_HOURS"), 4)
	if popularScanInterval <= 0 {
		popularScanInterval = 4
	}
	popularBuyMinPct := parseFloatWithDefault(os.Getenv("POPULAR_BUY_MIN_PCT"), 55)
	if popularBuyMinPct < 0 {
		popularBuyMinPct = 0
	}
	if popularBuyMinPct > 100 {
		popularBuyMinPct = 100
	}
	var lastPopularScanAt time.Time

	monitorOnce := func() {
		now := time.Now()

		stats, err := db.GetDBStats(ctx)
		if err != nil {
			log.Printf("warn: db stats: %v", err)
		} else {
			thresholdMB := dbFreeTierLimitMB * dbAlertThresholdRatio
			needsAlert := stats.StorageSizeMB >= thresholdMB
			cooldownDone := lastDBUsageAlertAt.IsZero() || now.Sub(lastDBUsageAlertAt) >= dbAlertCooldown
			if needsAlert && cooldownDone {
				usedPct := (stats.StorageSizeMB / dbFreeTierLimitMB) * 100
				msg := fmt.Sprintf(
					"Midas Touch DB Alert (데이터베이스 경고)\n"+
						"MongoDB storage usage is high. / MongoDB 용량이 높습니다.\n"+
						"Used(사용량): %.1f MB / %.0f MB (%.1f%%)\n"+
						"Objects(문서수): %d\n"+
						"Recommendation(권장): run prune or check retention settings.",
					stats.StorageSizeMB,
					dbFreeTierLimitMB,
					usedPct,
					stats.Objects,
				)
				if err := tgClient.SendMessage(msg); err != nil {
					log.Printf("warn: db usage alert telegram send: %v", err)
				} else {
					lastDBUsageAlertAt = now
					log.Printf("db usage alert sent: %.1fMB (%.1f%%)", stats.StorageSizeMB, usedPct)
				}
			}
		}

		items, err := db.GetWatchlist(ctx)
		if err != nil {
			log.Printf("get watchlist: %v", err)
			return
		}
		if len(items) == 0 {
			if err := db.AddToWatchlist(ctx, defaultSymbol, defaultIntervalHour, "event"); err != nil {
				log.Printf("seed watchlist: %v", err)
				return
			}
			items, _ = db.GetWatchlist(ctx)
		}

		for _, item := range items {
			intervalHour := item.NotifyIntervalHour
			if intervalHour <= 0 {
				intervalHour = defaultIntervalHour
			}
			timingTF := timeframeFromIntervalHour(intervalHour)
			notifyMode := strings.ToLower(strings.TrimSpace(item.NotifyMode))
			if notifyMode == "" {
				notifyMode = "event"
			}

			reco, err := signalSvc.Evaluate(ctx, item.Symbol, timingTF)
			if err != nil {
				log.Printf("evaluate %s: %v", item.Symbol, err)
				continue
			}

			if err := signalSvc.AutoPrune(ctx, item.Symbol); err != nil {
				log.Printf("warn: auto prune %s: %v", item.Symbol, err)
			}

			shouldNotify := false
			specialDue := false
			why := ""

			if notifyMode == "interval" {
				normalDue := item.LastNotifiedAt == nil || now.Sub(*item.LastNotifiedAt) >= time.Duration(intervalHour)*time.Hour
				specialDue = reco.IsSpecial && (item.LastSpecialAt == nil || now.Sub(*item.LastSpecialAt) >= specialCooldownPeriod)
				shouldNotify = normalDue || specialDue
				if specialDue && !normalDue {
					why = fmt.Sprintf("interval mode + special override (%dh)", intervalHour)
				} else if normalDue {
					why = fmt.Sprintf("interval due (%dh)", intervalHour)
				} else {
					why = "interval wait"
				}
			} else {
				lastNotified, err := db.GetLatestNotifiedSignal(ctx, item.Symbol)
				if err != nil {
					log.Printf("warn: latest notified signal %s: %v", item.Symbol, err)
					continue
				}
				shouldNotify, specialDue, why = shouldNotifyEvent(reco, lastNotified, item, policy, now)
			}

			if !shouldNotify {
				log.Printf("skip notify symbol=%s action=%s mode=%s reason=%s", item.Symbol, reco.Action, notifyMode, why)
				continue
			}

			msg := advisor.FormatMessage(reco)
			if specialDue {
				msg = fmt.Sprintf("[SPECIAL SIGNAL][%s] %s\n\n%s", strings.ToUpper(notifyMode), why, msg)
			} else {
				msg = fmt.Sprintf("[%s] %s\n\n%s", strings.ToUpper(notifyMode), why, msg)
			}

			if err := tgClient.SendMessage(msg); err != nil {
				log.Printf("telegram send %s: %v", item.Symbol, err)
				continue
			}

			signalSvc.SaveSignal(ctx, item.Symbol, reco, true)
			if err := db.MarkWatchlistNotified(ctx, item.Symbol, specialDue, now); err != nil {
				log.Printf("warn: mark notified %s: %v", item.Symbol, err)
			}
			log.Printf("notified symbol=%s action=%s mode=%s special=%t reason=%s", item.Symbol, reco.Action, notifyMode, specialDue, why)
		}

		if lastPopularScanAt.IsZero() || now.Sub(lastPopularScanAt) >= time.Duration(popularScanInterval)*time.Hour {
			symbols := advisor.PopularLeaderSymbols()
			candidates := make([]advisor.Recommendation, 0, len(symbols))

			for _, symbol := range symbols {
				reco, err := signalSvc.Evaluate(ctx, symbol, popularScanTF)
				if err != nil {
					log.Printf("popular scan evaluate %s: %v", symbol, err)
					continue
				}

				signalSvc.SaveSignal(ctx, symbol, reco, false)
				if isBuyCandidate(reco, popularBuyMinPct) {
					candidates = append(candidates, reco)
				}
			}

			if len(candidates) > 0 {
				msg := formatPopularBuyDigest(candidates, popularBuyMinPct)
				if err := tgClient.SendMessage(msg); err != nil {
					log.Printf("popular scan telegram send: %v", err)
				} else {
					log.Printf("popular scan notified: %d candidates", len(candidates))
				}
			} else {
				log.Printf("popular scan: no BUY candidates (min_buy_pct=%.0f)", popularBuyMinPct)
			}

			lastPopularScanAt = now
		}
	}

	monitorOnce()
	if runOnce {
		select {}
	}

	ticker := time.NewTicker(monitorTickInterval)
	defer ticker.Stop()
	for range ticker.C {
		monitorOnce()
	}
}

func setupLogger() func() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	logPath := os.Getenv("ADVISOR_LOG_FILE")
	if logPath == "" {
		logPath = "advisor.log"
	}

	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("failed to open log file (%s): %v", logPath, err)
		return func() {}
	}

	log.SetOutput(io.MultiWriter(os.Stdout, file))
	return func() { file.Close() }
}

func parseBool(value string) bool {
	if value == "" {
		return false
	}
	b, err := strconv.ParseBool(value)
	if err != nil {
		return false
	}
	return b
}

func parseIntWithDefault(value string, fallback int) int {
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return n
}

func parseFloatWithDefault(value string, fallback float64) float64 {
	if value == "" {
		return fallback
	}
	n, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return n
}

func isBuyCandidate(reco advisor.Recommendation, minBuyPct float64) bool {
	if reco.Action != "BUY" {
		return false
	}
	if reco.BuyPercent < minBuyPct {
		return false
	}
	if reco.TrendAction == "SELL" || reco.TimingAction == "SELL" {
		return false
	}
	return true
}

func formatPopularBuyDigest(candidates []advisor.Recommendation, minBuyPct float64) string {
	b := strings.Builder{}
	b.WriteString("Popular Leaders Scan (인기 대장주 스캔)\n")
	b.WriteString(fmt.Sprintf("조건: BUY && Buy>=%.0f%%\n", minBuyPct))
	b.WriteString(fmt.Sprintf("시간: %s\n\n", time.Now().Format("2006-01-02 15:04 KST")))

	for _, reco := range candidates {
		b.WriteString(fmt.Sprintf(
			"[%s] %s %s\n- Buy %.0f%% | Hold %.0f%% | Sell %.0f%%\n- Direction: %s %s | Timing: %s %s\n\n",
			reco.TargetSymbol,
			actionSignalEmoji(reco.Action),
			actionWithKorean(reco.Action),
			reco.BuyPercent,
			reco.HoldPercent,
			reco.SellPercent,
			actionSignalEmoji(reco.TrendAction),
			actionWithKorean(reco.TrendAction),
			actionSignalEmoji(reco.TimingAction),
			actionWithKorean(reco.TimingAction),
		))
	}

	return strings.TrimSpace(b.String())
}

func actionWithKorean(action string) string {
	switch action {
	case "BUY":
		return "BUY(구매)"
	case "SELL":
		return "SELL(매도)"
	case "HOLD":
		return "HOLD(관망)"
	default:
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

type AlertPolicy struct {
	MinActionPct float64
	MinDeltaPct  float64
	Cooldown     time.Duration
}

func shouldNotifyEvent(reco advisor.Recommendation, last *mongodb.SignalDoc, item mongodb.WatchlistItem, policy AlertPolicy, now time.Time) (bool, bool, string) {
	specialDue := reco.IsSpecial && (item.LastSpecialAt == nil || now.Sub(*item.LastSpecialAt) >= specialCooldownPeriod)
	if specialDue {
		return true, true, "special signal"
	}

	if item.LastNotifiedAt != nil && now.Sub(*item.LastNotifiedAt) < policy.Cooldown {
		return false, false, "cooldown"
	}

	if reco.Action == "HOLD" {
		return false, false, "hold filtered"
	}

	currStrength := actionStrengthFromRecommendation(reco)
	if currStrength < policy.MinActionPct {
		return false, false, fmt.Sprintf("weak confidence %.0f<%.0f", currStrength, policy.MinActionPct)
	}

	if last == nil {
		return true, false, "first actionable signal"
	}

	if reco.Action != last.Action {
		return true, false, fmt.Sprintf("action changed %s->%s", last.Action, reco.Action)
	}

	lastStrength := actionStrengthFromSignal(*last)
	if math.Abs(currStrength-lastStrength) >= policy.MinDeltaPct {
		return true, false, fmt.Sprintf("confidence changed %.0f->%.0f", lastStrength, currStrength)
	}

	return false, false, "no meaningful change"
}

func actionStrengthFromRecommendation(reco advisor.Recommendation) float64 {
	switch reco.Action {
	case "BUY":
		return reco.BuyPercent
	case "SELL":
		return reco.SellPercent
	default:
		return reco.HoldPercent
	}
}

func actionStrengthFromSignal(sig mongodb.SignalDoc) float64 {
	switch sig.Action {
	case "BUY":
		return sig.BuyPct
	case "SELL":
		return sig.SellPct
	default:
		return sig.HoldPct
	}
}

func timeframeFromIntervalHour(hours int) string {
	if hours <= 1 {
		return "60"
	}
	if hours <= 2 {
		return "120"
	}
	if hours <= 4 {
		return "240"
	}
	return "240"
}
