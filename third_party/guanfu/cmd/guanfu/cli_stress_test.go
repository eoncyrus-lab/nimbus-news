package main

import (
	"math"
	"testing"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

func TestFindSeriesMatchesTolerance(t *testing.T) {
	pts := []store.PricePoint{
		{Date: "2020-01-01", Close: 1.0},
		{Date: "2020-02-01", Close: 2.0},
		{Date: "2020-03-01", Close: 2.2},
		{Date: "2020-04-01", Close: 3.5},
	}
	got := findSeriesMatches(pts, 2.0, 0.3)
	if len(got) != 2 {
		t.Fatalf("want 2 matches (2.0 ± 0.3 covers 2.0 and 2.2), got %d", len(got))
	}
	for _, m := range got {
		if math.Abs(m.Value-2.0) > 0.3 {
			t.Errorf("match %v outside tolerance", m)
		}
	}
}

func TestFindSeriesMatchesEmpty(t *testing.T) {
	pts := []store.PricePoint{{Date: "2020-01-01", Close: 1.0}}
	if len(findSeriesMatches(pts, 10, 0.1)) != 0 {
		t.Error("distant target should produce no matches")
	}
}

func TestFindIndexOnOrAfter(t *testing.T) {
	pts := []store.PricePoint{
		{Date: "2020-01-01"},
		{Date: "2020-02-15"},
		{Date: "2020-03-01"},
	}
	if findIndexOnOrAfter(pts, "2020-02-10") != 1 {
		t.Error("first date ≥ 2020-02-10 should be idx 1")
	}
	if findIndexOnOrAfter(pts, "2020-01-01") != 0 {
		t.Error("exact match should hit idx 0")
	}
	if findIndexOnOrAfter(pts, "2025-01-01") != -1 {
		t.Error("future date should return -1")
	}
}

func TestSummarizeStressReturns(t *testing.T) {
	// Symmetric around 0.05
	sorted := []float64{-0.10, -0.05, 0.00, 0.05, 0.10, 0.15, 0.20, 0.25}
	s := summarizeStressReturns(sorted)
	if math.Abs(s.Mean-0.075) > 1e-9 {
		t.Errorf("mean = %v, want 0.075", s.Mean)
	}
	if s.Median <= 0 {
		t.Errorf("median should be > 0 for this sample: %v", s.Median)
	}
	if s.ProbUp < 0.5 || s.ProbUp > 0.8 {
		t.Errorf("prob_up = %v, want 0.5-0.8 given 5/8 positive", s.ProbUp)
	}
}

func TestSummarizeStressReturnsEmpty(t *testing.T) {
	s := summarizeStressReturns(nil)
	if s.Mean != 0 || s.Median != 0 {
		t.Errorf("empty input should yield zero values: %#v", s)
	}
}

func TestApplyAssetLookupSkipsInsufficientForward(t *testing.T) {
	asset := []store.PricePoint{
		{Date: "2020-01-01", Close: 100},
		{Date: "2020-01-02", Close: 105},
		// Only 2 points; horizon 30 impossible to satisfy.
	}
	matches := []seriesMatch{{Date: "2020-01-01", Value: 1.0}}
	got := applyAssetLookup(asset, matches, 30)
	if len(got) != 0 {
		t.Errorf("expected 0 returns when forward data missing, got %d", len(got))
	}
}

func TestApplyAssetLookupComputesForwardReturn(t *testing.T) {
	asset := make([]store.PricePoint, 40)
	for i := range asset {
		asset[i] = store.PricePoint{
			Date:  mustDateStr(i),
			Close: 100 + float64(i),
		}
	}
	// Match on day 0 → forward return over 30d = 130/100 - 1 = 0.30
	matches := []seriesMatch{{Date: asset[0].Date, Value: 1.0}}
	got := applyAssetLookup(asset, matches, 30)
	if len(got) != 1 {
		t.Fatalf("want 1 return, got %d", len(got))
	}
	if math.Abs(got[0]-0.30) > 1e-9 {
		t.Errorf("forward return = %v, want 0.30", got[0])
	}
}

// mustDateStr returns "2020-01-01" + offset days formatted YYYY-MM-DD.
// Used only by stress tests to build synthetic history.
func mustDateStr(offset int) string {
	base := [3]int{2020, 1, 1}
	day := base[2] + offset
	month := base[1]
	year := base[0]
	for day > 28 { // treat months as 28d for simplicity of test
		day -= 28
		month++
		if month > 12 {
			month = 1
			year++
		}
	}
	return fmtDate(year, month, day)
}

func fmtDate(y, m, d int) string {
	pad := func(x int) string {
		if x < 10 {
			return "0" + itoa(x)
		}
		return itoa(x)
	}
	return itoa(y) + "-" + pad(m) + "-" + pad(d)
}

func itoa(x int) string {
	if x == 0 {
		return "0"
	}
	buf := [12]byte{}
	i := len(buf)
	for x > 0 {
		i--
		buf[i] = byte('0' + x%10)
		x /= 10
	}
	return string(buf[i:])
}
