package calendar

import (
	"testing"
	"time"
)

func TestUpcomingSortedAndWithinWindow(t *testing.T) {
	// 2026-05-09 + 60d ought to pick up June FOMC, June/July CPI, July earnings.
	now := mustDate("2026-05-09")
	out := Upcoming(now, 60)
	if len(out) == 0 {
		t.Fatal("expected at least some events in 60d window")
	}
	end := now.AddDate(0, 0, 60)
	for i, e := range out {
		if e.Date.Before(now) || e.Date.After(end) {
			t.Errorf("event %d out of window: %v (now=%v end=%v)", i, e.Date, now, end)
		}
		if i > 0 && e.Date.Before(out[i-1].Date) {
			t.Errorf("events not sorted: %v before %v", e.Date, out[i-1].Date)
		}
	}
}

func TestUpcomingIncludesFOMCWhenInWindow(t *testing.T) {
	// A window that straddles 2026-06-17 FOMC
	now := mustDate("2026-06-01")
	out := Upcoming(now, 30)
	sawFOMC := false
	for _, e := range out {
		if e.Kind == KindFOMC && e.Date.Equal(mustDate("2026-06-17")) {
			sawFOMC = true
		}
	}
	if !sawFOMC {
		t.Error("expected 2026-06-17 FOMC to appear")
	}
}

func TestUpcomingEmptyOutsideAllRanges(t *testing.T) {
	// Far-future date past halving + past all scheduled FOMCs → CPI/earnings
	// rules still generate events; only truly empty when window is 0-size.
	now := mustDate("2030-01-01")
	out := Upcoming(now, 0) // 0 → defaulted to 30d
	// Should still return CPI + earnings rule-based entries.
	if len(out) == 0 {
		t.Error("CPI + earnings rules should still fire in 2030 window")
	}
}

func TestUpcomingCPISpacingMonthly(t *testing.T) {
	// Over 6 months we should see ~6 CPI entries, not more.
	now := mustDate("2026-01-01")
	out := Upcoming(now, 180)
	cpiCount := 0
	for _, e := range out {
		if e.Kind == KindCPI {
			cpiCount++
		}
	}
	if cpiCount < 5 || cpiCount > 7 {
		t.Errorf("expected 5-7 CPI events in 180d window, got %d", cpiCount)
	}
}

func TestHalvingEventFiresWhenDateInWindow(t *testing.T) {
	// Wide window straddling the 2028 halving.
	now := mustDate("2028-04-01")
	out := Upcoming(now, 30)
	sawHalving := false
	for _, e := range out {
		if e.Kind == KindHalving {
			sawHalving = true
		}
	}
	if !sawHalving {
		t.Error("expected halving event in window")
	}
}

func TestEventFieldsPopulated(t *testing.T) {
	now := mustDate("2026-06-01")
	out := Upcoming(now, 30)
	for _, e := range out {
		if e.Name == "" || e.Kind == "" || e.Date.IsZero() {
			t.Errorf("incomplete event: %#v", e)
		}
	}
}

// Sanity: mustDate panics on bad input — test the helper once so we
// notice if anyone changes it to silently return zero time.
func TestMustDatePanicsOnBadInput(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("mustDate should panic on malformed input")
		}
	}()
	_ = mustDate("not-a-date")
}

// Sanity-check the hardcoded fomcSchedule is in chronological order so
// a future editor who inserts a date doesn't silently break sort
// assumptions elsewhere.
func TestFOMCScheduleIsSortedAndFuture(t *testing.T) {
	for i := 1; i < len(fomcSchedule); i++ {
		if !fomcSchedule[i].After(fomcSchedule[i-1]) {
			t.Errorf("fomcSchedule out of order at %d: %v not after %v",
				i, fomcSchedule[i], fomcSchedule[i-1])
		}
	}
	// All dates should be after 2024-01-01 (anything older should have
	// been pruned by the annual refresh).
	cutoff := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, d := range fomcSchedule {
		if d.Before(cutoff) {
			t.Errorf("stale FOMC date: %v", d)
		}
	}
}
