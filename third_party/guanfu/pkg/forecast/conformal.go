// Conformal prediction intervals (Track G1).
//
// Problem: forecast.HorizonForecast.P10ReturnPct / P90ReturnPct are
// empirical quantiles over the kNN analog forward-returns. With small
// analog samples (Gold horizon at n=51, or any user-facing --top-k=13
// query) the empirical 10th-percentile will NOT reliably cover the
// actual 10th-percentile of the forward distribution. A user quoting
// "p10 = -8%" from a 21-sample kNN is mis-using the number.
//
// Solution: split-conformal intervals. Under exchangeability of analog
// residuals, the coverage guarantee holds in finite samples:
//
//   Pr(y* ∈ [ŷ - q̂(1-α), ŷ + q̂(1-α)]) ≥ 1 - α
//
// where q̂ is the empirical (1-α)(1+1/n) quantile of |analog residuals|
// (a simple finite-sample correction: ceil((n+1)(1-α))/n).
//
// We produce ConformalLowPct / ConformalHighPct = median ± adjusted
// quantile of residuals. Coverage report: achievable 1-α given n.
//
// Trade-offs / scope:
//
//   - This is conformal-style but simplified. We use |median - analog|
//     as the nonconformity score (symmetric). True split-conformal
//     wants a held-out calibration set; here we reuse analogs since
//     BuildForecast doesn't have a separate calibration stream. The
//     result is approximately conservative for small n (the correction
//     term makes it so) and identical to quantile-based prediction
//     for large n. Good enough for user-facing guardrails.
//
//   - Coverage guarantee rests on analog exchangeability — our kNN
//     selection by distance ordering breaks exchangeability somewhat.
//     The correction + using the 1-α quantile of absolute residuals
//     is a standard pragmatic workaround (see Vovk 2005, and MAPIE's
//     EnbPI for the more rigorous version).
//
//   - We do NOT adjust empirical P10/P90 — those remain as-is so
//     downstream tools that rely on the old semantics don't break.
//     Conformal fields are additive.

package forecast

import (
	"math"
	"sort"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/assetprofile"
)

// defaultConformalAlpha is the 1-coverage target for conformal bounds.
// 0.20 → 80% interval, matching the p10/p90 display conventions elsewhere.
const defaultConformalAlpha = 0.20

// minConformalSamples is the floor below which we don't even try —
// fewer than ~10 residuals makes the finite-sample correction swallow
// the full distribution and emit a meaningless interval.
const minConformalSamples = 10

// computeConformalFromReturns derives a conformal interval given the
// horizon's median return and the analog forward-return array (all
// expressed as decimals, not percent).
//
// Returns low / high / achievedCoverage. Achievable coverage is
// ceil((n+1)(1-α)) / (n+1) in finite samples; we clamp to 1.0.
func computeConformalFromReturns(returns []float64, median float64, alpha float64) (low, high, coverage float64, ok bool) {
	n := len(returns)
	if n < minConformalSamples {
		return 0, 0, 0, false
	}
	if alpha <= 0 || alpha >= 1 {
		alpha = defaultConformalAlpha
	}

	// Nonconformity scores: absolute residual around the median.
	residuals := make([]float64, n)
	for i, r := range returns {
		residuals[i] = math.Abs(r - median)
	}
	sort.Float64s(residuals)

	// Finite-sample quantile index: ceil((n+1)(1-α)). When this exceeds
	// n, we cannot achieve 1-α coverage with the data we have; clamp
	// to the max and report the actual coverage we can hit.
	idx := int(math.Ceil(float64(n+1) * (1 - alpha)))
	if idx > n {
		idx = n
	}
	q := residuals[idx-1] // 1-indexed → 0-indexed

	// Symmetric interval around the median.
	low = median - q
	high = median + q

	// Achieved coverage is idx/(n+1) — the finite-sample bound.
	coverage = float64(idx) / float64(n+1)
	if coverage > 1 {
		coverage = 1
	}
	return low, high, coverage, true
}

// annotateHorizonConformalForAsset attaches conformal interval fields to one
// HorizonForecast, given the raw analog forward-returns for that
// horizon (decimals). No-op when sample too small — the fields
// stay at omitempty defaults.
func annotateHorizonConformalForAsset(h *HorizonForecast, returns []float64, currentPrice float64, asset string) {
	if h == nil {
		return
	}
	// returns here are decimals; median on the struct is in %.
	medianDec := h.MedianReturnPct / 100.0
	low, high, cov, ok := computeConformalFromReturns(returns, medianDec, defaultConformalAlpha)
	if !ok {
		return
	}
	scale := conformalCalibrationScale(asset, h.Days)
	if scale > 0 && scale != 1 {
		low = medianDec - (medianDec-low)*scale
		high = medianDec + (high-medianDec)*scale
		h.ConformalCalibrationScale = round4(scale)
	}
	h.ConformalLowPct = round2(low * 100)
	h.ConformalHighPct = round2(high * 100)
	h.ConformalAlpha = defaultConformalAlpha
	h.ConformalCoverage = round4(cov)
	if currentPrice > 0 {
		h.ConformalLowPrice = round2(currentPrice * (1 + low))
		h.ConformalHighPrice = round2(currentPrice * (1 + high))
	}
}

func conformalCalibrationScale(asset string, horizonDays int) float64 {
	return assetprofile.ConformalScale(asset, horizonDays)
}
