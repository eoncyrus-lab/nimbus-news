// Path projection — fan chart from kNN analogue forward returns.
//
// Builds a P50 mainline + P25/P75 fan sectors showing the evolution
// of the forecast distribution over time (not just end-of-horizon snapshots).
//
// This is a forward-looking scenario tool, not a price prediction.

package forecast

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// PathProjection holds the forward path and fan sectors for visualization.
type PathProjection struct {
	Date         string      `json:"date"`
	CurrentPrice float64     `json:"current_price"`
	Horizon      int         `json:"horizon_days"` // max horizon
	Mainline     []PathPoint `json:"mainline"`     // P50
	UpperFan     []PathPoint `json:"upper_fan"`    // P75
	LowerFan     []PathPoint `json:"lower_fan"`    // P25
	P10Floor     []PathPoint `json:"p10_floor"`    // P10 worst case
	P90Ceiling   []PathPoint `json:"p90_ceiling"`  // P90 best case
}

// PathPoint is a single point on the projection path.
type PathPoint struct {
	DayOffset int     `json:"day"`
	Price     float64 `json:"price"`
	ReturnPct float64 `json:"return_pct"`
}

// BuildPathProjection constructs a forward path from analogue data.
// Uses the forward returns of selected analogues at each intermediate day
// to build a fan of possible price trajectories.
func BuildPathProjection(fc *Forecast, maxHorizon int) *PathProjection {
	if fc == nil || len(fc.Analogs) == 0 {
		return nil
	}
	if maxHorizon <= 0 {
		maxHorizon = 180
	}

	pp := &PathProjection{
		Date:         fc.Date,
		CurrentPrice: fc.CurrentPrice,
		Horizon:      maxHorizon,
	}

	// Build paths from each analogue's forward returns
	// Simplified: use the horizon-level distributions to interpolate paths
	for day := 7; day <= maxHorizon; day += 7 {
		// Collect all analogue returns at this approximate day offset
		returns := collectReturnsAtDay(fc.Analogs, day)
		if len(returns) < 3 {
			continue
		}
		sort.Float64s(returns)

		pp.Mainline = append(pp.Mainline, PathPoint{
			DayOffset: day,
			Price:     round2(fc.CurrentPrice * (1 + quantileFloat(returns, 0.50))),
			ReturnPct: round2(quantileFloat(returns, 0.50) * 100),
		})
		pp.UpperFan = append(pp.UpperFan, PathPoint{
			DayOffset: day,
			Price:     round2(fc.CurrentPrice * (1 + quantileFloat(returns, 0.75))),
			ReturnPct: round2(quantileFloat(returns, 0.75) * 100),
		})
		pp.LowerFan = append(pp.LowerFan, PathPoint{
			DayOffset: day,
			Price:     round2(fc.CurrentPrice * (1 + quantileFloat(returns, 0.25))),
			ReturnPct: round2(quantileFloat(returns, 0.25) * 100),
		})
		pp.P10Floor = append(pp.P10Floor, PathPoint{
			DayOffset: day,
			Price:     round2(fc.CurrentPrice * (1 + quantileFloat(returns, 0.10))),
			ReturnPct: round2(quantileFloat(returns, 0.10) * 100),
		})
		pp.P90Ceiling = append(pp.P90Ceiling, PathPoint{
			DayOffset: day,
			Price:     round2(fc.CurrentPrice * (1 + quantileFloat(returns, 0.90))),
			ReturnPct: round2(quantileFloat(returns, 0.90) * 100),
		})
	}

	return pp
}

func collectReturnsAtDay(analogs []Analog, targetDay int) []float64 {
	var returns []float64
	dayKey := fmt.Sprintf("%dd", targetDay)

	// First try exact match
	for _, a := range analogs {
		if ret, ok := a.ForwardReturnsPct[dayKey]; ok {
			returns = append(returns, ret/100)
		}
	}
	if len(returns) >= 3 {
		return returns
	}

	// Fall back to nearest available horizon
	for _, a := range analogs {
		for key, ret := range a.ForwardReturnsPct {
			var d int
			fmt.Sscanf(key, "%dd", &d)
			if d > 0 && abs(d-targetDay) <= 7 {
				returns = append(returns, ret/100)
			}
		}
	}
	return returns
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// quantileFloat returns the q-th quantile of sorted values.
func quantileFloat(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if q <= 0 {
		return sorted[0]
	}
	if q >= 1 {
		return sorted[len(sorted)-1]
	}
	pos := q * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	frac := pos - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

// ASCIIFan renders the projection as an ASCII fan chart.
// Width is the character width of the chart.
func (pp *PathProjection) ASCIIFan(width int) string {
	if pp == nil || len(pp.Mainline) == 0 {
		return "No path projection available."
	}
	if width < 40 {
		width = 60
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("BTC Path Projection  %s  (price: $%.0f)\n", pp.Date, pp.CurrentPrice))
	sb.WriteString(strings.Repeat("─", width) + "\n")
	sb.WriteString(fmt.Sprintf("%-8s %s\n", "Day", "Price Range (P10 ─ P25 ═ P50 ═ P75 ─ P90)"))
	sb.WriteString(strings.Repeat("─", width) + "\n")

	for i := range pp.Mainline {
		day := pp.Mainline[i].DayOffset
		p10 := pp.P10Floor[i].Price
		p25 := pp.LowerFan[i].Price
		p50 := pp.Mainline[i].Price
		p75 := pp.UpperFan[i].Price
		p90 := pp.P90Ceiling[i].Price

		// Determine chart scaling
		minPrice := p10
		maxPrice := p90
		priceRange := maxPrice - minPrice
		if priceRange <= 0 {
			priceRange = 1
		}

		chartWidth := width - 12

		p10Pos := int((p10 - minPrice) / priceRange * float64(chartWidth))
		p25Pos := int((p25 - minPrice) / priceRange * float64(chartWidth))
		p50Pos := int((p50 - minPrice) / priceRange * float64(chartWidth))
		p75Pos := int((p75 - minPrice) / priceRange * float64(chartWidth))
		p90Pos := int((p90 - minPrice) / priceRange * float64(chartWidth))

		// Build the bar (use runes for box-drawing glyphs)
		bar := make([]rune, chartWidth)
		for j := range bar {
			bar[j] = ' '
		}
		// Fill fan regions
		for j := p10Pos; j <= p90Pos; j++ {
			if j >= 0 && j < chartWidth {
				bar[j] = '·' // middle dot
			}
		}
		for j := p25Pos; j <= p75Pos; j++ {
			if j >= 0 && j < chartWidth {
				bar[j] = '▒' // medium shade
			}
		}
		// Mark quantile points
		setRune := func(pos int, r rune) {
			if pos >= 0 && pos < chartWidth {
				bar[pos] = r
			}
		}
		setRune(p10Pos, '├') // ├
		setRune(p25Pos, '┄') // ┄
		setRune(p50Pos, '╋') // ╋
		setRune(p75Pos, '┄') // ┄
		setRune(p90Pos, '┤') // ┤

		sb.WriteString(fmt.Sprintf("%4dd     %s  $%.0f\n", day, string(bar), p50))
	}

	sb.WriteString(strings.Repeat("─", width) + "\n")
	sb.WriteString("Legend: ├ P10  ┄ P25  ╋ P50  ┄ P75  ┤ P90    · background = range\n")
	sb.WriteString("This is a historical-analogue-based scenario fan, not a price prediction.\n")
	return sb.String()
}
