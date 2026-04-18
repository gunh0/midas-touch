package main

import (
	"testing"
	"time"
)

func TestIsAlignedScanSlot_MinuteIntervals(t *testing.T) {
	base := time.Date(2026, 4, 18, 10, 0, 0, 0, time.UTC)

	if !isAlignedScanSlot(base.Add(3*time.Minute), 3) {
		t.Fatalf("expected 10:03 to align for 3-minute interval")
	}
	if isAlignedScanSlot(base, 3) {
		t.Fatalf("expected 10:00 to not align for 3-minute interval")
	}
	if !isAlignedScanSlot(base.Add(5*time.Minute), 5) {
		t.Fatalf("expected 10:05 to align for 5-minute interval")
	}
	if isAlignedScanSlot(base.Add(4*time.Minute), 5) {
		t.Fatalf("expected 10:04 to not align for 5-minute interval")
	}
}

func TestIsAlignedScanSlot_HourIntervals(t *testing.T) {
	if !isAlignedScanSlot(time.Date(2026, 4, 18, 11, 0, 0, 0, time.UTC), 60) {
		t.Fatalf("expected top of hour to align for 60-minute interval")
	}
	if isAlignedScanSlot(time.Date(2026, 4, 18, 11, 30, 0, 0, time.UTC), 60) {
		t.Fatalf("expected non-top of hour to not align for 60-minute interval")
	}

	if !isAlignedScanSlot(time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC), 240) {
		t.Fatalf("expected 12:00 to align for 4-hour interval")
	}
	if isAlignedScanSlot(time.Date(2026, 4, 18, 10, 0, 0, 0, time.UTC), 240) {
		t.Fatalf("expected 10:00 to not align for 4-hour interval")
	}
}
