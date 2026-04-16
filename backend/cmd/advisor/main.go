package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
	_ "time/tzdata"

	"github.com/gunh0/midas-touch/internal/advisor"
	"github.com/gunh0/midas-touch/internal/api"
	"github.com/gunh0/midas-touch/internal/marketdata"
	"github.com/gunh0/midas-touch/internal/mongodb"
	"github.com/gunh0/midas-touch/internal/telegram"
)

var scheduledHoursKST = []int{0, 4, 8, 12, 16, 20}

func main() {
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

	// Start REST API server
	port := os.Getenv("API_PORT")
	if port == "" {
		port = "8080"
	}
	handler := api.NewHandler(db, marketClient)
	go func() {
		log.Printf("API server listening on :%s", port)
		if err := http.ListenAndServe(":"+port, handler.Routes()); err != nil {
			log.Fatalf("api server: %v", err)
		}
	}()

	runOnce := parseBool(os.Getenv("ADVISOR_RUN_ONCE"))

	send := func() {
		quotes, err := marketClient.FetchQuotes(advisor.RequiredSymbols())
		if err != nil {
			log.Printf("fetch market data: %v", err)
			return
		}

		bars, err := marketClient.FetchDailyBars(advisor.SymbolNVDA, 300)
		if err != nil {
			log.Printf("fetch NVDA bars: %v", err)
			return
		}

		// Persist candles to MongoDB
		candles := make([]mongodb.CandleDoc, len(bars))
		for i, b := range bars {
			candles[i] = mongodb.CandleDoc{
				Symbol:    advisor.SymbolNVDA,
				Timestamp: b.Timestamp,
				Open:      b.Open,
				High:      b.High,
				Low:       b.Low,
				Close:     b.Close,
				Volume:    b.Volume,
			}
		}
		if err := db.UpsertCandles(ctx, candles); err != nil {
			log.Printf("warn: upsert candles: %v", err)
		}

		// Auto-prune if DB is getting full
		stats, err := db.GetDBStats(ctx)
		if err == nil && stats.OverLimit {
			deleted, err := db.PruneOldCandles(ctx, advisor.SymbolNVDA, 365)
			if err != nil {
				log.Printf("warn: prune candles: %v", err)
			} else {
				log.Printf("pruned %d old candles (DB over limit)", deleted)
			}
		}

		closes := make([]float64, len(bars))
		for i, b := range bars {
			closes[i] = b.Close
		}

		usdkrw, err := marketClient.FetchUSDKRWRate()
		if err != nil {
			log.Printf("warn: fetch usdkrw: %v", err)
		}

		reco, err := advisor.Evaluate(advisor.SymbolNVDA, quotes, closes, time.Now())
		if err != nil {
			log.Printf("evaluate: %v", err)
			return
		}
		reco.USDKRWRate = usdkrw

		// Save signal
		sig := mongodb.SignalDoc{
			Symbol:    advisor.SymbolNVDA,
			Timestamp: reco.Timestamp,
			Action:    reco.Action,
			BuyPct:    reco.BuyPercent,
			SellPct:   reco.SellPercent,
			HoldPct:   reco.HoldPercent,
			Reason:    reco.Reason,
		}
		if err := db.SaveSignal(ctx, sig); err != nil {
			log.Printf("warn: save signal: %v", err)
		}

		// Send telegram notification
		if err := tgClient.SendMessage(advisor.FormatMessage(reco)); err != nil {
			log.Printf("telegram send: %v", err)
			return
		}

		log.Printf("signal sent: action=%s buy=%.0f sell=%.0f hold=%.0f",
			reco.Action, reco.BuyPercent, reco.SellPercent, reco.HoldPercent)
	}

	send()
	if runOnce {
		select {} // keep API server alive
	}

	for {
		next := nextScheduledTime(time.Now(), scheduledHoursKST)
		waitDur := time.Until(next)
		log.Printf("next run at %s KST (in %s)",
			next.In(mustLoadKST()).Format("2006-01-02 15:04:05"),
			waitDur.Round(time.Second),
		)
		time.Sleep(waitDur)
		send()
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

func nextScheduledTime(now time.Time, hours []int) time.Time {
	loc := mustLoadKST()
	nowKST := now.In(loc)
	for _, h := range hours {
		candidate := time.Date(nowKST.Year(), nowKST.Month(), nowKST.Day(), h, 0, 0, 0, loc)
		if candidate.After(nowKST) {
			return candidate
		}
	}
	tomorrow := nowKST.AddDate(0, 0, 1)
	return time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), hours[0], 0, 0, 0, loc)
}

func mustLoadKST() *time.Location {
	loc, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		return time.FixedZone("KST", 9*60*60)
	}
	return loc
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
