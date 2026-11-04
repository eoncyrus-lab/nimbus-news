package backtest

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
		price := 4000 * math.Exp(0.001*x+0.2*math.Sin(x/95))
		points[i] = forecast.Point{
			Date:  start.AddDate(0, 0, i).Format("2006-01-02"),
			Close: price,
		}
	}
	return points
}

func simpleExtractors() []forecast.FeatureExtractor {
	return []forecast.FeatureExtractor{
		func(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
			if i < 30 {
				return nil, false
			}
			r := points[i].Close/points[i-30].Close - 1
			return []forecast.FeatureValue{{Name: "ret30", Value: r * 100, Normalized: r / 0.30, Weight: 1.0}}, true
		},
		func(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
			if i < 90 {
				return nil, false
			}
			r := points[i].Close/points[i-90].Close - 1
			return []forecast.FeatureValue{{Name: "ret90", Value: r * 100, Normalized: r / 0.60, Weight: 1.0}}, true
		},
		func(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
			if i < 14 {
				return nil, false
			}
			g, l := 0.0, 0.0
			for j := i - 13; j <= i; j++ {
				d := points[j].Close - points[j-1].Close
				if d > 0 {
					g += d
				} else {
					l -= d
				}
			}
			if l == 0 {
				return []forecast.FeatureValue{{Name: "rsi", Value: 100, Normalized: 2.0, Weight: 0.8}}, true
			}
			rsi := 100 - 100/(1+(g/14)/(l/14))
			return []forecast.FeatureValue{{Name: "rsi", Value: rsi, Normalized: (rsi - 50) / 25, Weight: 0.8}}, true
		},
		func(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
			if i < 200 {
				return nil, false
			}
			sum := 0.0
			for j := i - 199; j <= i; j++ {
				sum += points[j].Close
			}
			mayer := points[i].Close / (sum / 200)
			return []forecast.FeatureValue{{Name: "mayer", Value: mayer, Normalized: mayer / 2.4, Weight: 1.0}}, true
		},
		func(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
			if i < 180 {
				return nil, false
			}
			r := points[i].Close/points[i-180].Close - 1
			return []forecast.FeatureValue{{Name: "ret180", Value: r * 100, Normalized: r / 1.0, Weight: 0.8}}, true
		},
		func(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
			if i < 200 {
				return nil, false
			}
			sum := 0.0
			for j := i - 199; j <= i; j++ {
				sum += points[j].Close
			}
			sma := sum / 200
			dev := points[i].Close/sma - 1
			return []forecast.FeatureValue{{Name: "sma200dev", Value: dev * 100, Normalized: dev / 0.50, Weight: 0.7}}, true
		},
	}
}

func TestBacktestRun(t *testing.T) {
	points := syntheticPoints(1500)
	extractors := simpleExtractors()
	horizons := []int{30, 90}

	opts := forecast.DefaultOptions()
	opts.Extractors = extractors
	opts.Horizons = horizons
	result, err := RunWithOptions(points, 500, 90, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.TotalTests == 0 {
		t.Fatal("expected at least 1 test")
	}

	t.Logf("Backtest: %d tests", result.TotalTests)
	for _, h := range horizons {
		hm := result.ByHorizon[h]
		if hm == nil {
			continue
		}
		t.Logf("  %3dd: samples=%d dir_hit=%.1f%% pit=%.2f crps=%.4f",
			h, hm.SampleCount, hm.DirectionHitRate()*100, hm.PITMean(), hm.CRPSScore())
	}
}

func TestBacktestInsufficientHistory(t *testing.T) {
	points := syntheticPoints(300)
	opts := forecast.DefaultOptions()
	opts.Extractors = simpleExtractors()
	opts.Horizons = []int{30}
	_, err := RunWithOptions(points, 200, 30, opts)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestBacktestNoExtractors(t *testing.T) {
	points := syntheticPoints(1000)
	opts := forecast.DefaultOptions()
	opts.Extractors = nil
	opts.Horizons = []int{30}
	_, err := RunWithOptions(points, 500, 30, opts)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPITCalculation(t *testing.T) {
	// Actual near median → PIT ~0.5
	pit := calcPIT(0.05, -0.10, -0.05, 0.05, 0.10, 0.15)
	if pit < 0.3 || pit > 0.7 {
		t.Fatalf("expected PIT near 0.5 for near-median, got %f", pit)
	}

	// Actual in left tail → PIT near 0
	pit = calcPIT(-0.15, -0.10, -0.05, 0.05, 0.10, 0.15)
	if pit > 0.2 {
		t.Fatalf("expected PIT near 0 for left tail, got %f", pit)
	}

	// Actual in right tail → PIT near 1
	pit = calcPIT(0.20, -0.10, -0.05, 0.05, 0.10, 0.15)
	if pit < 0.8 {
		t.Fatalf("expected PIT near 1 for right tail, got %f", pit)
	}
}

func TestCRPSCalculation(t *testing.T) {
	// Near median → lower CRPS
	crpsGood := calcCRPS(0.05, -0.10, -0.05, 0.05, 0.10, 0.15)
	// Far from median → higher CRPS
	crpsBad := calcCRPS(0.30, -0.10, -0.05, 0.05, 0.10, 0.15)
	if crpsGood >= crpsBad {
		t.Fatalf("expected CRPS(good)=%f < CRPS(bad)=%f", crpsGood, crpsBad)
	}
	t.Logf("CRPS: good=%.4f bad=%.4f", crpsGood, crpsBad)
}
