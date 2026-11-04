package dca

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"
)

func syntheticDCAPoints(days int, trend float64) []Point {
	start := time.Date(2018, 1, 1, 0, 0, 0, 0, time.UTC)
	points := make([]Point, days)
	for i := 0; i < days; i++ {
		x := float64(i)
		price := 4000 * math.Exp(trend*x/365+0.2*math.Sin(x/60))
		points[i] = Point{
			Date:  start.AddDate(0, 0, i).Format("2006-01-02"),
			Close: price,
		}
	}
	return points
}

func TestRunDCA_Fixed(t *testing.T) {
	points := syntheticDCAPoints(1000, 0.8) // bullish trend
	r, err := Run(points, Params{Strategy: Fixed, MonthlyUSD: 1000})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.Strategy != "fixed" {
		t.Fatalf("expected fixed, got %s", r.Strategy)
	}
	if r.TotalInvested <= 0 {
		t.Fatal("expected positive invested amount")
	}
	if r.TotalBTC <= 0 {
		t.Fatal("expected positive BTC amount")
	}
	if r.CostBasis <= 0 {
		t.Fatal("expected positive cost basis")
	}
	t.Logf("Fixed: invested=$%.0f btc=%.4f value=$%.0f roi=%.1f%% cost=$%.0f dd=%.1f%%",
		r.TotalInvested, r.TotalBTC, r.CurrentValue, r.ROIPct, r.CostBasis, r.MaxDrawdown)
}

func TestRunDCA_AHR(t *testing.T) {
	points := syntheticDCAPoints(1000, 0.8)
	r, err := Run(points, Params{Strategy: AHR, MonthlyUSD: 1000})
	if err != nil {
		t.Fatalf("Run AHR: %v", err)
	}
	if r.Strategy != "ahr" {
		t.Fatalf("expected ahr, got %s", r.Strategy)
	}
	t.Logf("AHR: invested=$%.0f btc=%.4f value=$%.0f roi=%.1f%% cost=$%.0f",
		r.TotalInvested, r.TotalBTC, r.CurrentValue, r.ROIPct, r.CostBasis)
}

func TestRunDCA_Mayer(t *testing.T) {
	points := syntheticDCAPoints(1000, 0.8)
	r, err := Run(points, Params{Strategy: Mayer, MonthlyUSD: 1000})
	if err != nil {
		t.Fatalf("Run Mayer: %v", err)
	}
	t.Logf("Mayer: invested=$%.0f btc=%.4f value=$%.0f roi=%.1f%% cost=$%.0f",
		r.TotalInvested, r.TotalBTC, r.CurrentValue, r.ROIPct, r.CostBasis)
}

func TestRunComparison(t *testing.T) {
	points := syntheticDCAPoints(2000, 0.6)
	cr, err := RunComparison(points, 1000, 1460)
	if err != nil {
		t.Fatalf("RunComparison: %v", err)
	}
	if len(cr.Results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	if cr.Best == "" {
		t.Fatal("expected best strategy")
	}
	t.Logf("Best: %s", cr.Best)
	for _, r := range cr.Results {
		t.Logf("  %s: roi=%.1f%% dd=%.1f%% zone=%s", r.Strategy, r.ROIPct, r.MaxDrawdown, r.ValuationZone)
	}

	// JSON roundtrip
	b, _ := json.Marshal(cr)
	var decoded ComparisonResult
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("json: %v", err)
	}
}

func TestRunDCA_InsufficientHistory(t *testing.T) {
	points := syntheticDCAPoints(20, 0.5)
	_, err := Run(points, Params{Strategy: Fixed, MonthlyUSD: 1000})
	if err == nil {
		t.Fatal("expected error for short history")
	}
}

func TestRunDCA_DateRange(t *testing.T) {
	points := syntheticDCAPoints(1000, 0.5)
	r, err := Run(points, Params{
		Strategy:   Fixed,
		MonthlyUSD: 1000,
		StartDate:  "2019-01-01",
		EndDate:    "2020-01-01",
	})
	if err != nil {
		t.Fatalf("Run date range: %v", err)
	}
	if r.StartDate < "2019-01-01" {
		t.Fatalf("start date not respected: %s", r.StartDate)
	}
	if r.EndDate > "2020-01-01" {
		t.Fatalf("end date not respected: %s", r.EndDate)
	}
	t.Logf("Date range: %s → %s, roi=%.1f%%", r.StartDate, r.EndDate, r.ROIPct)
}

func TestStrategyWeights(t *testing.T) {
	points := syntheticDCAPoints(500, 0.3)

	// Fixed always = 1.0
	w := strategyWeight(Fixed, points, 400, Params{})
	if w != 1.0 {
		t.Fatalf("fixed weight should be 1.0, got %f", w)
	}

	// AHR and Mayer should return valid weights
	w = strategyWeight(AHR, points, 400, Params{HalfLifeDays: 1460})
	if w <= 0 || w > 3 {
		t.Fatalf("ahr weight out of range: %f", w)
	}

	w = strategyWeight(Mayer, points, 400, Params{})
	if w <= 0 || w > 3 {
		t.Fatalf("mayer weight out of range: %f", w)
	}
	t.Logf("weights: fixed=1.0 ahr=%.1f mayer=%.1f", strategyWeight(AHR, points, 400, Params{HalfLifeDays: 1460}), strategyWeight(Mayer, points, 400, Params{}))
}

func TestHarmonicMean(t *testing.T) {
	// Harmonic mean of equal prices = that price
	points := make([]Point, 200)
	for i := range points {
		points[i] = Point{Date: fmt.Sprintf("2020-01-%02d", (i%30)+1), Close: 40000}
	}
	hm := harmonicMean(points, 199, 200)
	if hm < 39000 || hm > 41000 {
		t.Fatalf("harmonic mean of 40000 should be ~40000, got %f", hm)
	}
}

func TestValuationZone(t *testing.T) {
	points := syntheticDCAPoints(500, 0.5)
	zone := currentValuationZone(points, 400)
	if zone == "" {
		t.Fatal("expected non-empty valuation zone")
	}
	t.Logf("zone: %s", zone)
}

func TestZoneReplayYearLen(t *testing.T) {
	// ~6.2 trading-years of points: enough for 252/yr windows, short of 365/yr.
	points := syntheticDCAPoints(252*5+300, 0.3)

	zr, err := ZoneReplayYearLen(points, 252)
	if err != nil {
		t.Fatalf("ZoneReplayYearLen(252): %v", err)
	}
	if len(zr.Periods) == 0 {
		t.Fatal("expected non-empty periods")
	}
	for _, p := range zr.Periods {
		if p.Years != 1 && p.Years != 3 && p.Years != 5 {
			t.Fatalf("unexpected period years: %d", p.Years)
		}
		if p.SampleCount <= 0 {
			t.Fatalf("expected positive samples for %dy", p.Years)
		}
	}

	// Same series under the 365/yr (BTC) assumption needs 365*5+200 points and
	// must report insufficient history — proving the parameter actually scopes
	// the window length.
	if _, err := ZoneReplay(points); err == nil {
		t.Fatal("expected ZoneReplay (365/yr) to reject a trading-day-length series")
	}
}
