package forecast

import (
	"math"
	"testing"
)

// ridgeRegress on a handcrafted system: y = 2*x1 + 3*x2 (+ tiny noise)
// β should recover ~[2, 3].
func TestRidgeRegressRecoversKnownCoefficients(t *testing.T) {
	X := make([][]float64, 50)
	y := make([]float64, 50)
	for i := 0; i < 50; i++ {
		x1 := float64(i) / 10
		x2 := float64(i%7) / 3
		X[i] = []float64{x1, x2}
		y[i] = 2*x1 + 3*x2
	}
	beta, ok := ridgeRegress(X, y, 0.01)
	if !ok {
		t.Fatal("ridge should succeed on well-conditioned input")
	}
	if math.Abs(beta[0]-2) > 0.1 {
		t.Errorf("β[0] = %.4f, want ≈ 2", beta[0])
	}
	if math.Abs(beta[1]-3) > 0.1 {
		t.Errorf("β[1] = %.4f, want ≈ 3", beta[1])
	}
}

// Singular input (two identical columns) should return !ok even with
// regularization too small to lift rank? — with λ=0.01 ridge will
// actually regularize a pure rank-1 pair, so use zero-rank input instead.
func TestRidgeRegressZeroDimensionReturnsNotOk(t *testing.T) {
	if _, ok := ridgeRegress(nil, nil, 0.01); ok {
		t.Error("nil inputs should fail")
	}
	if _, ok := ridgeRegress([][]float64{{}}, []float64{0}, 0.01); ok {
		t.Error("zero-feature input should fail")
	}
}

func TestRidgeRegularizationPreventsCollinearitySingularity(t *testing.T) {
	// Two perfectly collinear features. Plain OLS would fail;
	// ridge with λ=0.01 should still solve.
	X := [][]float64{
		{1, 1}, {2, 2}, {3, 3}, {4, 4}, {5, 5},
		{6, 6}, {7, 7}, {8, 8}, {9, 9}, {10, 10},
	}
	y := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if _, ok := ridgeRegress(X, y, 0.01); !ok {
		t.Error("ridge should handle perfectly collinear features via regularization")
	}
}

func TestDotBasic(t *testing.T) {
	if dot([]float64{1, 2, 3}, []float64{4, 5, 6}) != 32 {
		t.Error("dot = 32 expected")
	}
	if dot(nil, nil) != 0 {
		t.Error("nil inputs should yield 0")
	}
}

// End-to-end: a forecast with Options.RecencyWeighted disabled should
// still populate ensemble fields when the candidate pool is large
// enough. Uses the synthetic points from forecast_test.go.
func TestAnnotateHorizonEnsembleFillsFields(t *testing.T) {
	points := syntheticPoints(3200)
	fc, err := Build(points, testOpts())
	if err != nil {
		t.Fatal(err)
	}
	if len(fc.Horizons) == 0 {
		t.Fatal("expected at least one horizon")
	}
	// Synthetic data is deterministic enough that both fields should
	// populate (non-zero) on at least one horizon.
	sawFill := false
	for _, h := range fc.Horizons {
		if h.EnsembleLinearPct != 0 || h.EnsembleDisagreementPct != 0 {
			sawFill = true
		}
	}
	if !sawFill {
		t.Error("expected ensemble fields to populate on at least one horizon")
	}
}
