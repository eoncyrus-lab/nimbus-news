package main

import (
	"math"
	"testing"
	"time"
)

func TestCalcOriginalAHRMatchesLegacyFormula(t *testing.T) {
	dates, closes := syntheticAHRSeries(220)
	v, ok := calcOriginalAHR(closes, dates, 199)
	if !ok {
		t.Fatal("calcOriginalAHR returned !ok at first 200d point")
	}

	price := closes[199]
	dca := arithmeticWindow(closes, 0, 199)
	expected := (price / dca) * (price / legacyFairValue(dates[199]))
	if math.Abs(v-expected) > 1e-12 {
		t.Fatalf("original AHR mismatch: got %.12f want %.12f", v, expected)
	}
}

func TestCalcModifiedAHRRequiresAdaptiveHistory(t *testing.T) {
	dates, closes := syntheticAHRSeries(ahrMinFitWindowDays + 10)

	if _, _, ok := calcModifiedAHR(closes, dates, ahrMinFitWindowDays-2); ok {
		t.Fatal("modified AHR should require at least ahrMinFitWindowDays of fit history")
	}

	raw, q, ok := calcModifiedAHR(closes, dates, len(closes)-1)
	if !ok {
		t.Fatal("modified AHR returned !ok after enough fit history")
	}
	if raw <= 0 || math.IsNaN(raw) || math.IsInf(raw, 0) {
		t.Fatalf("modified AHR raw should be positive finite, got %v", raw)
	}
	if q < 0 || q > 1 || math.IsNaN(q) {
		t.Fatalf("modified AHR q should be in [0,1], got %v", q)
	}
}

func TestReportRateFormattingKeepsRealZero(t *testing.T) {
	if got := rateN(0, 10); got != "0%" {
		t.Fatalf("rateN should render real zero as 0%%, got %q", got)
	}
	if got := rateN(0, 0); got != "n/a" {
		t.Fatalf("rateN should render missing samples as n/a, got %q", got)
	}
	if got := stanceHit("防守倾向", 0); got != "0%" {
		t.Fatalf("directional stance zero hit rate should render 0%%, got %q", got)
	}
	if got := stanceHit("等待", 0.5); got != "n/a" {
		t.Fatalf("neutral stance hit rate should render n/a, got %q", got)
	}
}

func TestClampClosedDailyEnd(t *testing.T) {
	now := time.Date(2026, 5, 3, 3, 0, 0, 0, time.UTC)

	closed, adjusted := clampClosedDailyEnd(time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC), now)
	if adjusted || closed.Format("2006-01-02") != "2026-05-02" {
		t.Fatalf("closed prior UTC day should not adjust, got %s adjusted=%v", closed.Format("2006-01-02"), adjusted)
	}

	closed, adjusted = clampClosedDailyEnd(time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC), now)
	if !adjusted || closed.Format("2006-01-02") != "2026-05-02" {
		t.Fatalf("current UTC day should adjust to prior closed day, got %s adjusted=%v", closed.Format("2006-01-02"), adjusted)
	}
}

func syntheticAHRSeries(n int) ([]time.Time, []float64) {
	dates := make([]time.Time, n)
	closes := make([]float64, n)
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		dates[i] = start.AddDate(0, 0, i)
		closes[i] = 10000 * math.Exp(0.0005*float64(i))
	}
	return dates, closes
}
