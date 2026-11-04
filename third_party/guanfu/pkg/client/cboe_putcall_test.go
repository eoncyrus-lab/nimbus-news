package client

import (
	"testing"
	"time"
)

func TestCBOEPutCallSourceInterface(t *testing.T) {
	var s Source = CBOEPutCallSource{}
	if s.Key() != "stooq_putcall" {
		t.Errorf("Key = %q, want stooq_putcall", s.Key())
	}
	if s.DisplayName() == "" {
		t.Error("DisplayName empty")
	}
}

func TestCBOEDailyStartDateCapsOldGaps(t *testing.T) {
	now := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)
	got := cboeDailyStartDate("2020-01-03", now)
	want := time.Date(2025, 3, 17, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("start = %s, want %s", got.Format("2006-01-02"), want.Format("2006-01-02"))
	}
}
