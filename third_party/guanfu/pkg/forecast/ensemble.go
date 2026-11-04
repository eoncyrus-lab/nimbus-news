// Linear-model ensemble cross-check (Track G4).
//
// Why this exists: kNN gives us a distribution of forward returns
// based on nearest historical analogs. A linear regression on the
// same (feature, forward-return) pairs answers "does the smooth
// trend across *all* candidates point the same way as the nearest
// few?". When the two disagree materially, the forecast is under
// regime stress — useful as an automatic caveat.
//
// This is NOT a replacement for kNN. The output is a single scalar
// `disagreement` (decimal return gap between kNN median and linear
// prediction) attached to each horizon; display layers can surface it.
//
// Implementation:
//
//   - Ridge regression with small L2 penalty (λ=0.01) for numerical
//     stability. With typical kNN candidate counts (200-1000) and
//     10-15 features, unregularized least squares would occasionally
//     produce ill-conditioned matrices on collinear feature bundles
//     (DXY + USD index proxies, etc.).
//   - Normal-equation solve: β = (XᵀX + λI)⁻¹ Xᵀy. For d≤20 features
//     the 20×20 matrix inverse is cheap; we implement a straight
//     Gauss-Jordan on a square matrix rather than pull in gonum.
//   - No BLAS / no third-party deps. Keeps the forecast package free
//     of heavy math libraries.
//
// This bundles in well with G1/G2/G5 as just another HorizonForecast
// enrichment field; does not change existing outputs.

package forecast

import (
	"math"
)

// annotateHorizonEnsemble fills HorizonForecast.EnsembleLinearPct
// and .EnsembleDisagreementPct by running ridge regression on the
// provided (candidate features, forward-return) pairs and comparing
// the linear-model prediction to the kNN median.
//
// No-op (fields stay zero) when the candidate pool is too small
// (<20) or the design matrix is singular despite regularization.
func annotateHorizonEnsemble(h *HorizonForecast, current featureSet, candidates []candidate, horizonDays int, points []Point) {
	if h == nil || len(candidates) < 20 {
		return
	}

	// Gather candidates that have a forward return at this horizon.
	// Skip anyone whose forward window falls off the end of the
	// price series — those contribute nothing to y.
	rows := make([][]float64, 0, len(candidates))
	ys := make([]float64, 0, len(candidates))
	featNames := make([]string, 0, len(current.values))
	for _, fv := range current.values {
		featNames = append(featNames, fv.Name)
	}

	for _, c := range candidates {
		if c.index+horizonDays >= len(points) {
			continue
		}
		x := make([]float64, len(featNames))
		ok := true
		for i, name := range featNames {
			fv, exists := c.features.byName[name]
			if !exists {
				ok = false
				break
			}
			x[i] = fv.Normalized
		}
		if !ok {
			continue
		}
		ret := points[c.index+horizonDays].Close/points[c.index].Close - 1
		rows = append(rows, x)
		ys = append(ys, ret)
	}
	if len(rows) < 20 {
		return
	}

	// Build current feature vector in the same order.
	xCurrent := make([]float64, len(featNames))
	for i, name := range featNames {
		fv, exists := current.byName[name]
		if !exists {
			return
		}
		xCurrent[i] = fv.Normalized
	}

	beta, ok := ridgeRegress(rows, ys, 0.01)
	if !ok {
		return
	}
	linearPred := dot(beta, xCurrent)
	if !usableFinite(linearPred) {
		return
	}

	// kNN median is in percent on the struct; linear prediction is a
	// decimal. Normalize to percent for comparability + display.
	linearPct := linearPred * 100
	disagreement := linearPct - h.MedianReturnPct

	h.EnsembleLinearPct = round2(linearPct)
	h.EnsembleDisagreementPct = round2(disagreement)
}

// ridgeRegress solves β = (XᵀX + λI)⁻¹ Xᵀy using Gauss-Jordan
// inversion on the d×d matrix. Returns (nil, false) if the matrix
// is singular even after regularization.
func ridgeRegress(X [][]float64, y []float64, lambda float64) ([]float64, bool) {
	n := len(X)
	if n == 0 {
		return nil, false
	}
	d := len(X[0])
	if d == 0 {
		return nil, false
	}
	// XᵀX + λI
	xtx := make([][]float64, d)
	for i := 0; i < d; i++ {
		xtx[i] = make([]float64, d)
		for j := 0; j < d; j++ {
			sum := 0.0
			for k := 0; k < n; k++ {
				sum += X[k][i] * X[k][j]
			}
			if i == j {
				sum += lambda
			}
			xtx[i][j] = sum
		}
	}
	// Xᵀy
	xty := make([]float64, d)
	for i := 0; i < d; i++ {
		sum := 0.0
		for k := 0; k < n; k++ {
			sum += X[k][i] * y[k]
		}
		xty[i] = sum
	}
	// Solve xtx · β = xty using Gauss-Jordan on an augmented matrix.
	aug := make([][]float64, d)
	for i := 0; i < d; i++ {
		aug[i] = make([]float64, d+1)
		copy(aug[i], xtx[i])
		aug[i][d] = xty[i]
	}
	// Forward elimination with partial pivoting.
	for i := 0; i < d; i++ {
		// Pivot: largest-magnitude row at or below i in column i.
		pivot := i
		for k := i + 1; k < d; k++ {
			if math.Abs(aug[k][i]) > math.Abs(aug[pivot][i]) {
				pivot = k
			}
		}
		if math.Abs(aug[pivot][i]) < 1e-12 {
			return nil, false
		}
		if pivot != i {
			aug[i], aug[pivot] = aug[pivot], aug[i]
		}
		// Normalize pivot row.
		inv := 1.0 / aug[i][i]
		for j := i; j <= d; j++ {
			aug[i][j] *= inv
		}
		// Eliminate column i from all other rows.
		for k := 0; k < d; k++ {
			if k == i {
				continue
			}
			f := aug[k][i]
			if f == 0 {
				continue
			}
			for j := i; j <= d; j++ {
				aug[k][j] -= f * aug[i][j]
			}
		}
	}
	beta := make([]float64, d)
	for i := 0; i < d; i++ {
		beta[i] = aug[i][d]
		if !usableFinite(beta[i]) {
			return nil, false
		}
	}
	return beta, true
}

// dot is a tiny vector dot product for the ensemble path; using
// gonum here would be overkill.
func dot(a, b []float64) float64 {
	s := 0.0
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}
