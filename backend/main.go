package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
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
			if err := db.AddToWatchlist(ctx, defaultSymbol, defaultIntervalHour); err != nil {
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

			reco, err := signalSvc.Evaluate(ctx, item.Symbol, timingTF)
			if err != nil {
				log.Printf("evaluate %s: %v", item.Symbol, err)
				continue
			}

			if err := signalSvc.AutoPrune(ctx, item.Symbol); err != nil {
				log.Printf("warn: auto prune %s: %v", item.Symbol, err)
			}

			normalDue := item.LastNotifiedAt == nil || now.Sub(*item.LastNotifiedAt) >= time.Duration(intervalHour)*time.Hour
			specialDue := reco.IsSpecial && (item.LastSpecialAt == nil || now.Sub(*item.LastSpecialAt) >= specialCooldownPeriod)
			if !normalDue && !specialDue {
				continue
			}

			msg := advisor.FormatMessage(reco)
			if specialDue && !normalDue {
				msg = "[SPECIAL SIGNAL] interval override triggered\n\n" + msg
			}

			if err := tgClient.SendMessage(msg); err != nil {
				log.Printf("telegram send %s: %v", item.Symbol, err)
				continue
			}

			signalSvc.SaveSignal(ctx, item.Symbol, reco, true)
			if err := db.MarkWatchlistNotified(ctx, item.Symbol, specialDue, now); err != nil {
				log.Printf("warn: mark notified %s: %v", item.Symbol, err)
			}
			log.Printf("notified symbol=%s action=%s interval=%dh special=%t", item.Symbol, reco.Action, intervalHour, specialDue)
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
