package client

import (
	"testing"
	"time"
)

func TestDeribitExpiryDays(t *testing.T) {
	asOf := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	got, ok := deribitExpiryDays("2026-05-29", asOf)
	if !ok {
		t.Fatal("expected expiry days to parse")
	}
	if got != 18 {
		t.Fatalf("expiry days = %v, want 18", got)
	}
}

func TestDeribitExpiryDaysRejectsInvalid(t *testing.T) {
	if got, ok := deribitExpiryDays("bad-date", time.Now()); ok || got != 0 {
		t.Fatalf("expected invalid expiry to be rejected, got %v ok=%v", got, ok)
	}
}
