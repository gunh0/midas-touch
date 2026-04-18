package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gunh0/midas-touch/internal/advisor"
)

func TestEvaluateCached_UsesCacheWithinTTL(t *testing.T) {
	svc := &SignalService{
		signalCache:    make(map[string]signalCacheEntry),
		signalCacheTTL: 200 * time.Millisecond,
	}

	var calls int32
	svc.evaluateFn = func(_ context.Context, symbol, timingTF string) (advisor.Recommendation, error) {
		atomic.AddInt32(&calls, 1)
		return advisor.Recommendation{
			TargetSymbol: symbol,
			TimingTF:     timingTF,
			Action:       "BUY",
			Timestamp:    time.Unix(int64(atomic.LoadInt32(&calls)), 0),
		}, nil
	}

	ctx := context.Background()
	first, err := svc.EvaluateCached(ctx, "nvda", "120")
	if err != nil {
		t.Fatalf("first EvaluateCached failed: %v", err)
	}
	second, err := svc.EvaluateCached(ctx, "NVDA", "120")
	if err != nil {
		t.Fatalf("second EvaluateCached failed: %v", err)
	}

	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("expected evaluator to run once, got %d", calls)
	}
	if !first.Timestamp.Equal(second.Timestamp) {
		t.Fatalf("expected cached recommendation timestamp to match, got %v vs %v", first.Timestamp, second.Timestamp)
	}
}

func TestEvaluateCached_ExpiresAfterTTL(t *testing.T) {
	svc := &SignalService{
		signalCache:    make(map[string]signalCacheEntry),
		signalCacheTTL: 25 * time.Millisecond,
	}

	var calls int32
	svc.evaluateFn = func(_ context.Context, symbol, timingTF string) (advisor.Recommendation, error) {
		atomic.AddInt32(&calls, 1)
		return advisor.Recommendation{
			TargetSymbol: symbol,
			TimingTF:     timingTF,
			Action:       "HOLD",
			Timestamp:    time.Now(),
		}, nil
	}

	ctx := context.Background()
	if _, err := svc.EvaluateCached(ctx, "TSLA", "120"); err != nil {
		t.Fatalf("first EvaluateCached failed: %v", err)
	}
	time.Sleep(40 * time.Millisecond)
	if _, err := svc.EvaluateCached(ctx, "TSLA", "120"); err != nil {
		t.Fatalf("second EvaluateCached failed: %v", err)
	}

	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("expected evaluator to run twice after ttl expiry, got %d", calls)
	}
}
