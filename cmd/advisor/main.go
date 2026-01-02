package main

import (
	"log"
	"os"
	"strconv"
	"time"
	_ "time/tzdata"

	"github.com/gunh0/midas-touch/internal/advisor"
	"github.com/gunh0/midas-touch/internal/marketdata"
	"github.com/gunh0/midas-touch/internal/telegram"
)

// scheduledHoursKST defines the hours (Asia/Seoul) at which the advisor fires.
var scheduledHoursKST = []int{0, 4, 8, 12, 16, 20}

func main() {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	chatID := os.Getenv("TELEGRAM_CHAT_ID")
	if token == "" || chatID == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID environment variables are required")
	}

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

		log.Printf("recommendation sent: action=%s buy=%.0f sell=%.0f hold=%.0f", reco.Action, reco.BuyPercent, reco.SellPercent, reco.HoldPercent)
	}

	send()
	if runOnce {
		return
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
		log.Printf("Asia/Seoul timezone unavailable, using UTC+9 fixed offset: %v", err)
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
