package main

import (
	"testing"
	"time"

	"github.com/gunh0/midas-touch/internal/advisor"
	"github.com/gunh0/midas-touch/internal/mongodb"
)

func makePolicy() AlertPolicy {
	return AlertPolicy{
		MinActionPct: 0,
		MinDeltaPct:  12,
		Cooldown:     30 * time.Minute,
	}
}

func TestShouldNotifyEvent_NoDuplicateWhenSpecialAndNoChange(t *testing.T) {
	policy := makePolicy()
	now := time.Now()
	lastSpecialAt := now.Add(-61 * time.Minute)

	item := mongodb.WatchlistItem{
		Symbol:        "NET",
		LastNotifiedAt: &now,
		LastSpecialAt:  &lastSpecialAt,
	}

	reco := advisor.Recommendation{
		Action:     "BUY",
		BuyPercent: 91,
		IsSpecial:  true,
	}

	last := &mongodb.SignalDoc{
		Action:    "BUY",
		BuyPct:    91,
		IsSpecial: true,
	}

	should, special, why := shouldNotifyEvent(reco, last, item, policy, now)
	if should {
		t.Fatalf("expected no notification when special is unchanged and confidence is same, got should=%t special=%t why=%s", should, special, why)
	}
}

func TestShouldNotifyEvent_SpecialTransition(t *testing.T) {
	policy := makePolicy()
	now := time.Now()

	item := mongodb.WatchlistItem{
		Symbol: "NET",
	}

	reco := advisor.Recommendation{
		Action:     "BUY",
		BuyPercent: 91,
		IsSpecial:  true,
	}

	// last was NOT special → now IS special = transition
	last := &mongodb.SignalDoc{
		Action:    "BUY",
		BuyPct:    91,
		IsSpecial: false,
	}

	should, special, why := shouldNotifyEvent(reco, last, item, policy, now)
	if !should {
		t.Fatalf("expected notification on special transition, got should=%t why=%s", should, why)
	}
	if !special {
		t.Fatalf("expected special=true on special transition")
	}
}

func TestShouldNotifyEvent_ConfidenceChange(t *testing.T) {
	policy := makePolicy()
	now := time.Now()
	lastNotifiedAt := now.Add(-31 * time.Minute)

	item := mongodb.WatchlistItem{
		Symbol:         "NET",
		LastNotifiedAt: &lastNotifiedAt,
	}

	reco := advisor.Recommendation{
		Action:     "BUY",
		BuyPercent: 76,
		IsSpecial:  true,
	}

	last := &mongodb.SignalDoc{
		Action:    "BUY",
		BuyPct:    91,
		IsSpecial: true,
	}

	should, special, why := shouldNotifyEvent(reco, last, item, policy, now)
	if !should {
		t.Fatalf("expected notification on confidence change, got should=%t why=%s", should, why)
	}
	if !special {
		t.Fatalf("expected special=true when IsSpecial is true and confidence changed")
	}
}

func TestShouldNotifyEvent_ActionChange(t *testing.T) {
	policy := makePolicy()
	now := time.Now()

	item := mongodb.WatchlistItem{
		Symbol: "NET",
	}

	reco := advisor.Recommendation{
		Action:     "SELL",
		SellPercent: 80,
		IsSpecial:  true,
	}

	last := &mongodb.SignalDoc{
		Action:    "BUY",
		BuyPct:    91,
		IsSpecial: true,
	}

	should, special, why := shouldNotifyEvent(reco, last, item, policy, now)
	if !should {
		t.Fatalf("expected notification on action change, got should=%t why=%s", should, why)
	}
	if !special {
		t.Fatalf("expected special=true when IsSpecial is true and action changed")
	}
}

func TestShouldNotifyEvent_HoldFiltered(t *testing.T) {
	policy := makePolicy()
	now := time.Now()

	item := mongodb.WatchlistItem{Symbol: "NET"}

	reco := advisor.Recommendation{
		Action:     "HOLD",
		HoldPercent: 80,
		IsSpecial:  true,
	}

	should, _, _ := shouldNotifyEvent(reco, nil, item, policy, now)
	if should {
		t.Fatalf("expected HOLD to be filtered")
	}
}

func TestShouldNotifyEvent_FirstSignal(t *testing.T) {
	policy := makePolicy()
	now := time.Now()

	item := mongodb.WatchlistItem{Symbol: "NET"}

	reco := advisor.Recommendation{
		Action:     "BUY",
		BuyPercent: 91,
		IsSpecial:  true,
	}

	should, special, _ := shouldNotifyEvent(reco, nil, item, policy, now)
	if !should {
		t.Fatalf("expected first signal to notify")
	}
	if !special {
		t.Fatalf("expected special=true on first signal when IsSpecial")
	}
}

func TestShouldNotifyInterval_NoDuplicateWhenSpecialAndNoChange(t *testing.T) {
	policy := makePolicy()
	now := time.Now()
	lastSpecialAt := now.Add(-61 * time.Minute)

	item := mongodb.WatchlistItem{
		Symbol:        "NET",
		LastSpecialAt: &lastSpecialAt,
	}

	reco := advisor.Recommendation{
		Action:     "BUY",
		BuyPercent: 91,
		IsSpecial:  true,
	}

	last := &mongodb.SignalDoc{
		Action:    "BUY",
		BuyPct:    91,
		IsSpecial: true,
	}

	should, special, why := shouldNotifyInterval(reco, last, item, policy, now)
	if should {
		t.Fatalf("expected no notification when special is unchanged and confidence is same, got should=%t special=%t why=%s", should, special, why)
	}
}

func TestShouldNotifyInterval_SpecialTransition(t *testing.T) {
	policy := makePolicy()
	now := time.Now()

	item := mongodb.WatchlistItem{Symbol: "NET"}

	reco := advisor.Recommendation{
		Action:     "BUY",
		BuyPercent: 91,
		IsSpecial:  true,
	}

	last := &mongodb.SignalDoc{
		Action:    "BUY",
		BuyPct:    91,
		IsSpecial: false,
	}

	should, special, why := shouldNotifyInterval(reco, last, item, policy, now)
	if !should {
		t.Fatalf("expected notification on special transition in interval mode, got should=%t why=%s", should, why)
	}
	if !special {
		t.Fatalf("expected special=true on special transition")
	}
}
