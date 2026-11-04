// Rolling-window backtest engine for kNN forecast validation.
//
// Walk-forward protocol:
//   - For each test date (stepping by stepDays):
//     * Slice price history up to that date
//     * Build a forecast using the same extractors
//     * Record actual forward returns at each horizon
//   - Aggregate: direction hit rate, PIT calibration, CRPS
//
// This validates whether the kNN method generalizes out-of-sample.

package backtest

import (
	"fmt"
	"math"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast"
)

func RunWithOptions(points []forecast.Point, startIdx, stepDays int, opts forecast.Options) (*Result, error) {
	if len(points) < startIdx+200 {
		return nil, fmt.Errorf("backtest: need more history (have %d, need %d+200)", len(points), startIdx)
	}
	if len(opts.Extractors) == 0 {
		return nil, fmt.Errorf("backtest: no feature extractors provided")
	}
	if stepDays <= 0 {
		stepDays = 30
	}
	horizons := opts.Horizons
	if len(horizons) == 0 {
		horizons = []int{30, 90, 180}
		opts.Horizons = horizons
	}

	maxHorizon := 0
	for _, h := range horizons {
		if h > maxHorizon {
			maxHorizon = h
		}
	}

	r := &Result{
		Horizons:   horizons,
		TotalTests: 0,
		ByHorizon:  make(map[int]*HorizonMetrics),
		ByYear:     make(map[int]*YearMetrics),
	}

	for _, h := range horizons {
		r.ByHorizon[h] = &HorizonMetrics{Days: h}
	}

	for idx := startIdx; idx+maxHorizon < len(points); idx += stepDays {
		// Slice history up to idx
		history := points[:idx+1]

		// Build forecast
		fc, err := forecast.Build(history, opts)
		if err != nil {
			continue // skip dates where forecast can't be built
		}

		// Compare forecast to actual outcomes
		for _, h := range fc.Horizons {
			if idx+h.Days >= len(points) {
				continue
			}
			actualReturn := points[idx+h.Days].Close/points[idx].Close - 1

			hm := r.ByHorizon[h.Days]
			hm.SampleCount++
			hm.FeatureCoverageSum += fc.Coverage.FeatureCoverage
			hm.FeatureCoverageCount++

			// Direction hit
			predictedUp := h.MedianReturnPct > 0
			actualUp := actualReturn > 0
			hit := predictedUp == actualUp
			if hit {
				hm.DirectionHits++
			}

			// PIT: where does actual fall in forecast distribution
			pit := calcPIT(actualReturn, h.P10ReturnPct/100, h.P25ReturnPct/100,
				h.P75ReturnPct/100, h.P90ReturnPct/100, h.MedianReturnPct/100)
			hm.PITSum += pit
			hm.PITCount++

			// CRPS
			crps := calcCRPS(actualReturn, h.P10ReturnPct/100, h.P25ReturnPct/100,
				h.MedianReturnPct/100, h.P75ReturnPct/100, h.P90ReturnPct/100)
			hm.CRPSSum += crps

			if h.ConformalAlpha > 0 {
				hm.ConformalCount++
				if actualReturn >= h.ConformalLowPct/100 && actualReturn <= h.ConformalHighPct/100 {
					hm.ConformalHits++
				}
			}

			// Year + per-horizon breakdown (walk-forward view).
			year := extractYear(points, idx)
			ym := r.ByYear[year]
			if ym == nil {
				ym = &YearMetrics{Year: year, ByHorizon: make(map[int]*HorizonHitTally)}
				r.ByYear[year] = ym
			}
			if ym.ByHorizon == nil {
				ym.ByHorizon = make(map[int]*HorizonHitTally)
			}
			ym.TotalTests++
			tally := ym.ByHorizon[h.Days]
			if tally == nil {
				tally = &HorizonHitTally{}
				ym.ByHorizon[h.Days] = tally
			}
			tally.Total++
			if hit {
				tally.Hits++
			}
		}
		r.TotalTests++
	}

	return r, nil
}

// extractYear parses the year from a point's date string "2006-01-02".
func extractYear(points []forecast.Point, idx int) int {
	if idx < 0 || idx >= len(points) {
		return 0
	}
	// Fast parse without time library
	if len(points[idx].Date) >= 4 {
		y := 0
		for i := 0; i < 4; i++ {
			if i < len(points[idx].Date) && points[idx].Date[i] >= '0' && points[idx].Date[i] <= '9' {
				y = y*10 + int(points[idx].Date[i]-'0')
			}
		}
		if y >= 1900 && y <= 2100 {
			return y
		}
	}
	return 2020
}

// calcPIT returns a simplified PIT (Probability Integral Transform) value.
// Returns ~0.5 if actual is near median, <0.1 or >0.9 if in tails.
func calcPIT(actual, p10, p25, p75, p90, median float64) float64 {
	switch {
	case actual <= p10:
		return 0.05
	case actual <= p25:
		return 0.175
	case actual <= median:
		return 0.375
	case actual <= p75:
		return 0.625
	case actual <= p90:
		return 0.825
	default:
		return 0.95
	}
}

// calcCRPS computes a simplified Continuous Ranked Probability Score.
// Lower CRPS = better calibrated forecast.
func calcCRPS(actual, p10, p25, median, p75, p90 float64) float64 {
	// Simplified CRPS: squared error weighted by where actual falls in distribution
	absErr := math.Abs(actual - median)
	if actual >= p25 && actual <= p75 {
		return absErr * 0.5 // in the interquartile range → lower penalty
	}
	if actual >= p10 && actual <= p90 {
		return absErr * 1.0 // in the 80% range
	}
	return absErr * 2.0 // in the tails → higher penalty
}

// Result holds aggregated backtest results.
type Result struct {
	Horizons   []int                   `json:"horizons"`
	TotalTests int                     `json:"total_tests"`
	ByHorizon  map[int]*HorizonMetrics `json:"by_horizon"`
	ByYear     map[int]*YearMetrics    `json:"by_year"`
}

// HorizonMetrics holds metrics for a single forecast horizon.
type HorizonMetrics struct {
	Days                 int     `json:"days"`
	SampleCount          int     `json:"sample_count"`
	DirectionHits        int     `json:"direction_hits"`
	PITSum               float64 `json:"-"`
	PITCount             int     `json:"-"`
	CRPSSum              float64 `json:"-"`
	ConformalHits        int     `json:"conformal_hits,omitempty"`
	ConformalCount       int     `json:"conformal_count,omitempty"`
	FeatureCoverageSum   float64 `json:"-"`
	FeatureCoverageCount int     `json:"-"`
}

// DirectionHitRate returns direction accuracy as a fraction.
func (m *HorizonMetrics) DirectionHitRate() float64 {
	if m.SampleCount == 0 {
		return 0
	}
	return float64(m.DirectionHits) / float64(m.SampleCount)
}

// PITMean returns the mean PIT value (should be ~0.5 for well-calibrated).
func (m *HorizonMetrics) PITMean() float64 {
	if m.PITCount == 0 {
		return 0
	}
	return m.PITSum / float64(m.PITCount)
}

// CRPSScore returns mean CRPS (lower is better).
func (m *HorizonMetrics) CRPSScore() float64 {
	if m.SampleCount == 0 {
		return 0
	}
	return m.CRPSSum / float64(m.SampleCount)
}

// ConformalHitRate returns realized coverage of the conformal interval.
func (m *HorizonMetrics) ConformalHitRate() float64 {
	if m.ConformalCount == 0 {
		return 0
	}
	return float64(m.ConformalHits) / float64(m.ConformalCount)
}

// FeatureCoverageMean returns the average forecast feature coverage seen
// during this horizon's walk-forward test cells.
func (m *HorizonMetrics) FeatureCoverageMean() float64 {
	if m.FeatureCoverageCount == 0 {
		return 0
	}
	return m.FeatureCoverageSum / float64(m.FeatureCoverageCount)
}

// YearMetrics holds yearly breakdown.
//
// ByHorizon lets us see whether a low overall dir_hit (e.g. Gold 49% on
// n=51) is uniform across years or driven by a few bad regimes — the
// classic walk-forward sanity check.
type YearMetrics struct {
	Year       int                      `json:"year"`
	TotalTests int                      `json:"total_tests"`
	ByHorizon  map[int]*HorizonHitTally `json:"by_horizon,omitempty"`
}

// HorizonHitTally counts directional hits/total per (year, horizon) cell.
type HorizonHitTally struct {
	Hits  int `json:"hits"`
	Total int `json:"total"`
}
