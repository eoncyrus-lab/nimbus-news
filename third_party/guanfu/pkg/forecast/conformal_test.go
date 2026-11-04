package forecast

import (
	"math"
	"math/rand"
	"testing"
	"time"
)

func TestComputeConformalReturnsSymmetricAroundMedian(t *testing.T) {
	// Analog returns from [-0.1, +0.1] uniform; median is ~0.
	rng := rand.New(rand.NewSource(42))
	returns := make([]float64, 30)
	for i := range returns {
		returns[i] = rng.Float64()*0.2 - 0.1
	}
	low, high, cov, ok := computeConformalFromReturns(returns, 0, 0.20)
	if !ok {
		t.Fatal("30 samples should be enough")
	}
	if cov < 0.75 || cov > 1.0 {
		t.Errorf("coverage = %.3f, want ≥0.75 (target 0.80)", cov)
	}
	// Interval must be symmetric around median (= 0).
	if math.Abs(low+high) > 1e-9 {
		t.Errorf("expected symmetric around 0: low=%.4f high=%.4f", low, high)
	}
	// High must be positive, low negative.
	if high <= 0 || low >= 0 {
		t.Errorf("bounds should bracket median: low=%.4f high=%.4f", low, high)
	}
}

func TestComputeConformalRefusesSmallSample(t *testing.T) {
	_, _, _, ok := computeConformalFromReturns([]float64{0.01, 0.02, 0.03}, 0.01, 0.2)
	if ok {
		t.Error("n=3 should be below minConformalSamples")
	}
}

func TestComputeConformalCoverageMatchesTarget(t *testing.T) {
	// With enough samples, achieved coverage approaches 1-alpha.
	rng := rand.New(rand.NewSource(1))
	returns := make([]float64, 200)
	for i := range returns {
		// normal-ish returns around 0.05
		returns[i] = 0.05 + rng.NormFloat64()*0.08
	}
	_, _, cov, ok := computeConformalFromReturns(returns, 0.05, 0.10)
	if !ok {
		t.Fatal("200 samples must be ok")
	}
	// target 1 - 0.10 = 0.90; finite-sample bound ceil(201*0.9)/201 ≈ 0.9005
	if math.Abs(cov-0.90) > 0.02 {
		t.Errorf("coverage = %.4f, want ~0.90", cov)
	}
}

func TestComputeConformalWiderWhenResidualsLarger(t *testing.T) {
	tight := []float64{}
	wide := []float64{}
	for i := 0; i < 30; i++ {
		tight = append(tight, 0.02+float64(i)*0.001)
		wide = append(wide, 0.02+float64(i)*0.02)
	}
	// median here is arbitrary — use sample medians.
	medT := quantile(append([]float64(nil), tight...), 0.5)
	medW := quantile(append([]float64(nil), wide...), 0.5)
	sortTight := append([]float64(nil), tight...)
	sortWide := append([]float64(nil), wide...)
	_ = sortTight
	_ = sortWide
	lowT, highT, _, okT := computeConformalFromReturns(tight, medT, 0.2)
	lowW, highW, _, okW := computeConformalFromReturns(wide, medW, 0.2)
	if !okT || !okW {
		t.Fatal("both should be ok")
	}
	if (highT - lowT) > (highW - lowW) {
		t.Errorf("wide dispersion should yield wider interval: tight=%.4f wide=%.4f",
			highT-lowT, highW-lowW)
	}
}

func TestAnnotateHorizonConformalFillsFields(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	returns := make([]float64, 40)
	for i := range returns {
		returns[i] = 0.04 + rng.NormFloat64()*0.05
	}
	h := &HorizonForecast{Days: 90, MedianReturnPct: 4}
	annotateHorizonConformalForAsset(h, returns, 1000, "")
	if h.ConformalAlpha == 0 {
		t.Error("ConformalAlpha should be populated")
	}
	if h.ConformalLowPct >= h.ConformalHighPct {
		t.Errorf("low (%.2f) should be < high (%.2f)", h.ConformalLowPct, h.ConformalHighPct)
	}
	if h.ConformalCoverage < 0.75 {
		t.Errorf("coverage = %.3f, want ≥0.75", h.ConformalCoverage)
	}
	if h.ConformalLowPrice >= h.ConformalHighPrice {
		t.Errorf("low_price should be < high_price: %.2f vs %.2f",
			h.ConformalLowPrice, h.ConformalHighPrice)
	}
}

func TestAnnotateHorizonConformalAppliesAssetHorizonCalibration(t *testing.T) {
	returns := make([]float64, 40)
	for i := range returns {
		returns[i] = -0.10 + float64(i)*0.005
	}
	h := &HorizonForecast{Days: 90, MedianReturnPct: 0}
	annotateHorizonConformalForAsset(h, returns, 100, "qqq")
	if h.ConformalCalibrationScale != 1.8 {
		t.Fatalf("ConformalCalibrationScale = %.2f, want 1.8", h.ConformalCalibrationScale)
	}
	if h.ConformalLowPct >= -10 || h.ConformalHighPct <= 10 {
		t.Fatalf("expected calibrated interval to widen around median, got %.2f..%.2f", h.ConformalLowPct, h.ConformalHighPct)
	}
}

func TestAnnotateHorizonConformalSkipsSmallSamples(t *testing.T) {
	h := &HorizonForecast{Days: 30, MedianReturnPct: 1.0}
	annotateHorizonConformalForAsset(h, []float64{0.01, 0.02}, 100, "")
	if h.ConformalAlpha != 0 || h.ConformalLowPct != 0 || h.ConformalHighPct != 0 {
		t.Errorf("small-sample should skip: %#v", h)
	}
}

// G5 recency weighting.
func TestRecencyWeightedFavorsRecentAnalogs(t *testing.T) {
	points := syntheticPoints(3200)
	base := testOpts()
	rec := testOpts()
	rec.RecencyWeighted = true

	fcBase, err := Build(points, base)
	if err != nil {
		t.Fatal(err)
	}
	fcRec, err := Build(points, rec)
	if err != nil {
		t.Fatal(err)
	}
	// Verify analog mean date is more recent (closer to end of series)
	// under recency weighting. Skip if base happened to pick all recent
	// already (rare with synthetic data but possible).
	meanIdx := func(fc *Forecast) int {
		sum := 0
		for _, a := range fc.Analogs {
			t, _ := time.Parse("2006-01-02", a.Date)
			// rough "days from epoch" ordering
			sum += int(t.Unix() / 86400)
		}
		if len(fc.Analogs) == 0 {
			return 0
		}
		return sum / len(fc.Analogs)
	}
	if meanIdx(fcRec) < meanIdx(fcBase) {
		t.Errorf("recency weighting should shift analog mean LATER, got rec=%d vs base=%d",
			meanIdx(fcRec), meanIdx(fcBase))
	}
}

// G2 regime gating: regimeBucket maps a point index to {0,1,2}.
func TestRegimeBucketSmokeCases(t *testing.T) {
	pts := syntheticPoints(3200)
	// The earliest valid index is 200; beyond that we just expect no panic
	// and a valid bucket value.
	for _, i := range []int{250, 1000, 2000, 3000, len(pts) - 1} {
		b := regimeBucket(pts, i)
		if b < 0 || b > 2 {
			t.Errorf("regimeBucket(%d) = %d, want 0/1/2", i, b)
		}
	}
	// Too-short history falls back to bucket 0 (not -1 / panic).
	if b := regimeBucket(pts, 10); b != 0 {
		t.Errorf("short history should default to bucket 0, got %d", b)
	}
}

func TestRegimeGateProducesValidForecast(t *testing.T) {
	// Minimal smoke test: enabling RegimeGate doesn't break Build,
	// and still produces enough analogs (depends on synthetic shape).
	points := syntheticPoints(3200)
	opts := testOpts()
	opts.RegimeGate = true
	fc, err := Build(points, opts)
	if err != nil {
		t.Fatal(err)
	}
	if fc.Coverage.SelectedAnalogs < minSelectedAnalogs {
		t.Errorf("regime gate starved analogs: %d < %d",
			fc.Coverage.SelectedAnalogs, minSelectedAnalogs)
	}
}
