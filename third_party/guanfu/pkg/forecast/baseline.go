// Baseline comparison (H3/J14).
//
// Every forecast expectation gets contextualized: is +5% over 90d good?
// Compare vs:
//   - 3-month T-bill compounded to that horizon (risk-free anchor)
//   - A passive 60/40 rolled over the same horizon (the "do nothing" option)
//
// Risk-adjusted delta = median - max(tbill, passive). Positive means
// the forecast's central tendency beats "boring money"; negative means
// reading this forecast as actionable is probably wrong.
//
// We NEVER compute baselines inside Build() — that would force the
// forecast package to know about PriceStore / FRED. Instead, callers
// pass a BaselineFn that returns (tbill_pct, passive_pct, ok, note) for
// a given horizon-in-days.

package forecast

import (
	"fmt"
	"math"
)

// BaselineFn returns horizon-equivalent baseline returns in percent.
// ok=false → no baseline annotation will be written (the field stays
// zero-valued / omitempty). note may carry an as-of date.
type BaselineFn func(days int) (tbillPct, passivePct float64, ok bool, note string)

// AnnotateBaselines fills the baseline comparison fields on every horizon
// that returns ok=true from fn. Safe to call with a nil fc or nil fn
// (both become a no-op).
func AnnotateBaselines(fc *Forecast, fn BaselineFn) {
	if fc == nil || fn == nil {
		return
	}
	for i := range fc.Horizons {
		days := fc.Horizons[i].Days
		tb, passive, ok, note := fn(days)
		if !ok {
			continue
		}
		fc.Horizons[i].RiskFreeReturnPct = round2(tb)
		fc.Horizons[i].PassiveReturnPct = round2(passive)
		// Delta uses whichever baseline is higher (user's next-best alternative).
		benchmark := math.Max(tb, passive)
		fc.Horizons[i].RiskAdjustedDeltaPct = round2(fc.Horizons[i].MedianReturnPct - benchmark)
		fc.Horizons[i].BaselineNote = note
	}
}

// FlatRateBaseline is a cheap fallback baseline for callers that don't
// have a PriceStore handy: given a constant annualized risk-free rate
// (e.g. 4.5%) and an annualized passive return (e.g. 6.0% for a 60/40
// long-run assumption), returns horizon-compounded values. Useful for
// tests and for bootstrapping before FRED DGS3MO lands.
func FlatRateBaseline(annualRiskFree, annualPassive float64) BaselineFn {
	return func(days int) (float64, float64, bool, string) {
		if days <= 0 {
			return 0, 0, false, ""
		}
		yr := float64(days) / 365.0
		tb := (math.Pow(1+annualRiskFree/100, yr) - 1) * 100
		pa := (math.Pow(1+annualPassive/100, yr) - 1) * 100
		note := fmt.Sprintf("flat %.1f%%/%.1f%% annual", annualRiskFree, annualPassive)
		return tb, pa, true, note
	}
}
