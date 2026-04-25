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
	"sync"
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
	defaultIntervalMinute = 3
	monitorTickInterval   = 20 * time.Second
	specialCooldownPeriod = 60 * time.Minute
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
	monitorMaxWorkers := parseIntWithDefault(os.Getenv("MONITOR_MAX_WORKERS"), 4)
	if monitorMaxWorkers <= 0 {
		monitorMaxWorkers = 1
	}
	lastScannedSlotBySymbol := map[string]int64{}

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
			if err := db.AddToWatchlist(ctx, defaultSymbol, defaultIntervalMinute, "event"); err != nil {
				log.Printf("seed watchlist: %v", err)
				return
			}
			items, _ = db.GetWatchlist(ctx)
		}

		type monitorJob struct {
			item         mongodb.WatchlistItem
			timingTF     string
			notifyMode   string
			intervalMins int
		}
		jobsToRun := make([]monitorJob, 0, len(items))

		for _, item := range items {
			intervalMinute := item.NotifyIntervalMinute
			if intervalMinute <= 0 {
				intervalMinute = item.NotifyIntervalHour * 60
			}
			if intervalMinute <= 0 {
				intervalMinute = defaultIntervalMinute
			}

			if !isAlignedScanSlot(now, intervalMinute) {
				continue
			}
			slot := now.Unix() / 60
			normalizedSymbol := strings.ToUpper(strings.TrimSpace(item.Symbol))
			if lastSlot, exists := lastScannedSlotBySymbol[normalizedSymbol]; exists && lastSlot == slot {
				continue
			}
			lastScannedSlotBySymbol[normalizedSymbol] = slot

			timingTF := timeframeFromIntervalMinute(intervalMinute)
			notifyMode := strings.ToLower(strings.TrimSpace(item.NotifyMode))
			if notifyMode == "" {
				notifyMode = "event"
			}

			jobsToRun = append(jobsToRun, monitorJob{
				item:         item,
				timingTF:     timingTF,
				notifyMode:   notifyMode,
				intervalMins: intervalMinute,
			})
		}

		if len(jobsToRun) > 0 {
			workers := monitorMaxWorkers
			if workers > len(jobsToRun) {
				workers = len(jobsToRun)
			}

			jobCh := make(chan monitorJob, len(jobsToRun))
			var wg sync.WaitGroup
			for i := 0; i < workers; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for job := range jobCh {
						reco, err := signalSvc.EvaluateCached(ctx, job.item.Symbol, job.timingTF)
						if err != nil {
							log.Printf("evaluate %s: %v", job.item.Symbol, err)
							continue
						}

						if err := signalSvc.AutoPrune(ctx, job.item.Symbol); err != nil {
							log.Printf("warn: auto prune %s: %v", job.item.Symbol, err)
						}

						shouldNotify := false
						specialDue := false
						why := ""
						lastNotified, err := db.GetLatestNotifiedSignal(ctx, job.item.Symbol)
						if err != nil {
							log.Printf("warn: latest notified signal %s: %v", job.item.Symbol, err)
							continue
						}

						if job.notifyMode == "interval" {
							shouldNotify, specialDue, why = shouldNotifyInterval(reco, lastNotified, job.item, policy, now)
						} else {
							shouldNotify, specialDue, why = shouldNotifyEvent(reco, lastNotified, job.item, policy, now)
						}

						if !shouldNotify {
							log.Printf("skip notify symbol=%s action=%s mode=%s reason=%s", job.item.Symbol, reco.Action, job.notifyMode, why)
							continue
						}

						msg := advisor.FormatMessage(reco)
						msg = formatSignalHeader(job.notifyMode, specialDue, why) + "\n\n" + msg

						if err := tgClient.SendMessage(msg); err != nil {
							log.Printf("telegram send %s: %v", job.item.Symbol, err)
							continue
						}

						signalSvc.SaveSignal(ctx, job.item.Symbol, reco, true)
						if err := db.MarkWatchlistNotified(ctx, job.item.Symbol, specialDue, now); err != nil {
							log.Printf("warn: mark notified %s: %v", job.item.Symbol, err)
						}
						log.Printf("notified symbol=%s action=%s mode=%s special=%t reason=%s", job.item.Symbol, reco.Action, job.notifyMode, specialDue, why)
					}
				}()
			}

			for _, job := range jobsToRun {
				jobCh <- job
			}
			close(jobCh)
			wg.Wait()
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

func formatSignalHeader(notifyMode string, specialDue bool, reason string) string {
	mode := strings.ToLower(strings.TrimSpace(notifyMode))

	if specialDue {
		return fmt.Sprintf("🚨 강제 시그널 [FORCED] %s", reason)
	}

	if mode == "interval" {
		return fmt.Sprintf("⏰ 인터벌 시그널 [INTERVAL] %s", reason)
	}

	return fmt.Sprintf("🚨 이벤트 시그널 [EVENT] %s", reason)
}

type AlertPolicy struct {
	MinActionPct float64
	MinDeltaPct  float64
	Cooldown     time.Duration
}

func shouldNotifyEvent(reco advisor.Recommendation, last *mongodb.SignalDoc, item mongodb.WatchlistItem, policy AlertPolicy, now time.Time) (bool, bool, string) {
	if reco.Action == "HOLD" {
		return false, false, "hold filtered"
	}

	if last == nil {
		special := reco.IsSpecial
		return true, special, "first actionable signal"
	}

	if reco.Action != last.Action {
		special := reco.IsSpecial
		return true, special, fmt.Sprintf("action changed %s->%s", last.Action, reco.Action)
	}

	// IsSpecial transition: not special → special (only when it first becomes special)
	if reco.IsSpecial && !last.IsSpecial {
		specialCooldownOk := item.LastSpecialAt == nil || now.Sub(*item.LastSpecialAt) >= specialCooldownPeriod
		if specialCooldownOk {
			return true, true, "special signal activated"
		}
	}

	if item.LastNotifiedAt != nil && now.Sub(*item.LastNotifiedAt) < policy.Cooldown {
		return false, false, "cooldown (same action)"
	}

	currStrength := actionStrengthFromRecommendation(reco)
	if currStrength < policy.MinActionPct {
		return false, false, fmt.Sprintf("weak confidence %.0f<%.0f", currStrength, policy.MinActionPct)
	}

	lastStrength := actionStrengthFromSignal(*last)
	if math.Abs(currStrength-lastStrength) >= policy.MinDeltaPct {
		special := reco.IsSpecial
		return true, special, fmt.Sprintf("confidence changed %.0f->%.0f", lastStrength, currStrength)
	}

	return false, false, "no meaningful change"
}

func shouldNotifyInterval(reco advisor.Recommendation, last *mongodb.SignalDoc, item mongodb.WatchlistItem, policy AlertPolicy, now time.Time) (bool, bool, string) {
	if reco.Action == "HOLD" {
		return false, false, "interval slot but hold filtered"
	}

	currStrength := actionStrengthFromRecommendation(reco)
	if currStrength < policy.MinActionPct {
		return false, false, fmt.Sprintf("interval slot but weak confidence %.0f<%.0f", currStrength, policy.MinActionPct)
	}

	if last == nil {
		special := reco.IsSpecial
		return true, special, "interval first actionable signal"
	}

	// IsSpecial transition: not special → special (only when it first becomes special)
	if reco.IsSpecial && !last.IsSpecial {
		specialCooldownOk := item.LastSpecialAt == nil || now.Sub(*item.LastSpecialAt) >= specialCooldownPeriod
		if specialCooldownOk {
			return true, true, "interval + special signal activated"
		}
	}

	if reco.Action != last.Action {
		special := reco.IsSpecial
		return true, special, fmt.Sprintf("interval action changed %s->%s", last.Action, reco.Action)
	}

	lastStrength := actionStrengthFromSignal(*last)
	if math.Abs(currStrength-lastStrength) >= policy.MinDeltaPct {
		special := reco.IsSpecial
		return true, special, fmt.Sprintf("interval confidence changed %.0f->%.0f", lastStrength, currStrength)
	}

	return false, false, "interval slot but no meaningful change"
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

func timeframeFromIntervalMinute(minutes int) string {
	if minutes <= 15 {
		return "30"
	}
	if minutes <= 60 {
		return "60"
	}
	if minutes <= 180 {
		return "120"
	}
	return "240"
}

func isAlignedScanSlot(now time.Time, intervalMinute int) bool {
	if intervalMinute <= 0 {
		intervalMinute = defaultIntervalMinute
	}
	slot := now.Unix() / 60
	if intervalMinute < 60 && now.Minute() == 0 {
		return false
	}
	return slot%int64(intervalMinute) == 0
}

