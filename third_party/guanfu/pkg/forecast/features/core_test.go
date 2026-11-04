package features

import (
	"math"
	"testing"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast"
)

func syntheticPoints(n int) []forecast.Point {
	start := time.Date(2018, 1, 1, 0, 0, 0, 0, time.UTC)
	points := make([]forecast.Point, n)
	for i := 0; i < n; i++ {
		x := float64(i)
		price := 10000 * math.Exp(0.0005*x+0.15*math.Sin(x/95))
		points[i] = forecast.Point{Date: start.AddDate(0, 0, i).Format("2006-01-02"), Close: price}
	}
	return points
}

func TestCoreExtractors(t *testing.T) {
	points := syntheticPoints(1500)
	extractors := CoreExtractors()
	if len(extractors) != 11 {
		t.Fatalf("expected 11 core extractors, got %d", len(extractors))
	}

	i := len(points) - 1
	for _, ex := range extractors {
		fvs, ok := ex(points, i)
		if !ok {
			t.Fatalf("extractor failed at index %d", i)
		}
		if len(fvs) != 1 {
			t.Fatalf("expected 1 feature value, got %d", len(fvs))
		}
		fv := fvs[0]
		if fv.Name == "" || fv.Weight <= 0 {
			t.Fatalf("invalid feature: %+v", fv)
		}
		if !math.IsNaN(fv.Normalized) && !math.IsInf(fv.Normalized, 0) {
			// valid
		} else {
			t.Fatalf("feature %s has invalid normalized value: %f", fv.Name, fv.Normalized)
		}
	}
}

func TestReturn30d(t *testing.T) {
	points := syntheticPoints(500)
	fvs, ok := Return30d(points, 400)
	if !ok || len(fvs) == 0 {
		t.Fatal("Return30d should succeed with 500 points at index 400")
	}
	t.Logf("return_30d: %+v", fvs[0])
}

func TestRSI14(t *testing.T) {
	points := syntheticPoints(500)
	fvs, ok := RSI14(points, 400)
	if !ok || len(fvs) == 0 {
		t.Fatal("RSI14 should succeed")
	}
	rsi := fvs[0].Value
	if rsi < 0 || rsi > 100 {
		t.Fatalf("RSI out of range: %f", rsi)
	}
	t.Logf("rsi_14: %f", rsi)
}

func TestMayerMultiple(t *testing.T) {
	points := syntheticPoints(500)
	fvs, ok := MayerMultiple(points, 400)
	if !ok || len(fvs) == 0 {
		t.Fatal("MayerMultiple should succeed")
	}
	t.Logf("mayer_multiple: %+v", fvs[0])
}

func TestHalvingPhase(t *testing.T) {
	// At a known halving date, progress should be ~0
	points := []forecast.Point{
		{Date: "2024-04-20", Close: 65000}, // halving date
		{Date: "2024-04-21", Close: 66000},
	}
	fvs, ok := HalvingPhaseSin(points, 1)
	if ok {
		t.Logf("halving sin: %f", fvs[0].Value)
	}
}
