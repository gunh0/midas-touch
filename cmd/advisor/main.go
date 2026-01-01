package main

import (
	"log"
	"os"
	"strconv"
	"time"

	"github.com/gunh0/midas-touch/internal/advisor"
	"github.com/gunh0/midas-touch/internal/marketdata"
	"github.com/gunh0/midas-touch/internal/telegram"
)

func main() {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	chatID := os.Getenv("TELEGRAM_CHAT_ID")
	if token == "" || chatID == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID environment variables are required")
	}

	interval := parseInterval(os.Getenv("ADVISOR_INTERVAL"))
	runOnce := parseBool(os.Getenv("ADVISOR_RUN_ONCE"))

	tgClient := telegram.NewClient(token, chatID)
	marketClient := marketdata.NewClient()

	send := func() {
		quotes, err := marketClient.FetchQuotes(advisor.RequiredSymbols())
		if err != nil {
			log.Printf("failed to fetch market data: %v", err)
			return
		}

		nvdaCloses, err := marketClient.FetchDailyCloses(advisor.SymbolNVDA, 300)
		if err != nil {
			log.Printf("failed to fetch NVDA history: %v", err)
			return
		}

		usdkrwRate, err := marketClient.FetchUSDKRWRate()
		if err != nil {
			log.Printf("failed to fetch usdkrw rate: %v", err)
			return
		}

		reco, err := advisor.Evaluate(quotes, nvdaCloses, time.Now().UTC())
		if err != nil {
			log.Printf("failed to evaluate recommendation: %v", err)
			return
		}
		reco.USDKRWRate = usdkrwRate

		if err := tgClient.SendMessage(advisor.FormatMessage(reco)); err != nil {
			log.Printf("failed to send telegram message: %v", err)
			return
		}

		log.Printf("hourly recommendation sent: action=%s buy=%.0f sell=%.0f hold=%.0f", reco.Action, reco.BuyPercent, reco.SellPercent, reco.HoldPercent)
	}

	send()
	if runOnce {
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		send()
	}
}

func parseInterval(value string) time.Duration {
	if value == "" {
		return time.Hour
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		log.Printf("invalid ADVISOR_INTERVAL %q, fallback to 1h", value)
		return time.Hour
	}
	if d < time.Minute {
		log.Printf("ADVISOR_INTERVAL too small (%s), fallback to 1h", d)
		return time.Hour
	}
	return d
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
