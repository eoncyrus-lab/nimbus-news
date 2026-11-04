// DCA (Dollar Cost Averaging) backtest engine.
//
// Three strategies:
//   - Fixed: invest same amount every period
//   - AHR:   weight by AHR999 quantile (accelerate when cheap, decelerate when expensive)
//   - Mayer: weight by Mayer Multiple (same logic)
//
// Design principles:
//   - Never output "invest X times today"
//   - Output historical win rates by valuation zone
//   - Output cost anchor + floating P&L for context

package dca

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// Point is a daily price point (oldest-first).
type Point struct {
	Date  string
	Close float64
}

// Strategy identifies the DCA strategy.
type Strategy string

const (
	Fixed Strategy = "fixed"
	AHR   Strategy = "ahr"
	Mayer Strategy = "mayer"
)

// Params controls the DCA simulation.
type Params struct {
	Strategy     Strategy
	MonthlyUSD   float64 // fixed investment per month
	StartDate    string  // optional, empty = use full history
	EndDate      string  // optional, empty = today
	HalfLifeDays int     // AHR fitting half-life (default 1460 = 4 years)
}

// Result holds the output of a DCA simulation.
type Result struct {
	Strategy      string  `json:"strategy"`
	StartDate     string  `json:"start_date"`
	EndDate       string  `json:"end_date"`
	TotalInvested float64 `json:"total_invested"`
	TotalBTC      float64 `json:"total_btc"`
	CurrentValue  float64 `json:"current_value"`
	ROIPct        float64 `json:"roi_pct"`
	AnnualizedROI float64 `json:"annualized_roi_pct"`
	CostBasis     float64 `json:"cost_basis"`
	MaxDrawdown   float64 `json:"max_drawdown_pct"`

	// Zone analysis
	ValuationZone      string  `json:"valuation_zone"`
	ZoneHistoryWinRate float64 `json:"zone_history_win_rate"`
	ZoneMedianROI      float64 `json:"zone_median_roi_pct"`

	// Monthly breakdown (for detailed analysis)
	MonthlyInvested []float64 `json:"-"`
	MonthlyBTC      []float64 `json:"-"`
}

// ComparisonResult compares multiple strategies.
type ComparisonResult struct {
	Date    string   `json:"date"`
	Price   float64  `json:"price"`
	Results []Result `json:"results"`
	Best    string   `json:"best_strategy"`
}

// Run executes a DCA simulation.
func Run(points []Point, params Params) (*Result, error) {
	if len(points) < 30 {
		return nil, fmt.Errorf("need at least 30 days of history, got %d", len(points))
	}
	if params.MonthlyUSD <= 0 {
		params.MonthlyUSD = 1000
	}
	if params.HalfLifeDays <= 0 {
		params.HalfLifeDays = 1460
	}

	points = normalizeDCA(points)

	// Filter by date range
	startIdx := 0
	endIdx := len(points) - 1
	if params.StartDate != "" {
		for i, p := range points {
			if p.Date >= params.StartDate {
				startIdx = i
				break
			}
		}
	}
	if params.EndDate != "" {
		for i := len(points) - 1; i >= 0; i-- {
			if points[i].Date <= params.EndDate {
				endIdx = i
				break
			}
		}
	}

	// Monthly investment simulation
	totalInvested := 0.0
	totalBTC := 0.0
	peakValue := 0.0
	maxDD := 0.0
	investedAmounts := make([]float64, 0)
	btcAmounts := make([]float64, 0)

	// Walk forward month by month
	currentMonth := ""
	for i := startIdx; i <= endIdx; i++ {
		month := points[i].Date[:7] // "YYYY-MM"
		if month == currentMonth {
			continue
		}
		currentMonth = month

		price := points[i].Close
		weight := strategyWeight(params.Strategy, points, i, params)

		investUSD := params.MonthlyUSD * weight
		btc := investUSD / price

		totalInvested += investUSD
		totalBTC += btc
		investedAmounts = append(investedAmounts, totalInvested)
		btcAmounts = append(btcAmounts, totalBTC)

		// Track peak and drawdown
		currentValue := totalBTC * price
		if currentValue > peakValue {
			peakValue = currentValue
		}
		if peakValue > 0 {
			dd := (currentValue - peakValue) / peakValue * 100
			if dd < maxDD {
				maxDD = dd
			}
		}
	}

	if totalInvested <= 0 || totalBTC <= 0 {
		return nil, fmt.Errorf("no investments made in date range")
	}

	latestPrice := points[endIdx].Close
	currentValue := totalBTC * latestPrice
	roi := (currentValue - totalInvested) / totalInvested * 100
	costBasis := totalInvested / totalBTC

	// Annualized ROI
	startDate, _ := time.Parse("2006-01-02", points[startIdx].Date)
	endDate, _ := time.Parse("2006-01-02", points[endIdx].Date)
	years := endDate.Sub(startDate).Hours() / 24 / 365
	annROI := 0.0
	if years > 0.5 && totalInvested > 0 {
		annROI = (math.Pow(currentValue/totalInvested, 1/years) - 1) * 100
	}

	// Valuation zone
	zone := currentValuationZone(points, endIdx)

	r := &Result{
		Strategy:        string(params.Strategy),
		StartDate:       points[startIdx].Date,
		EndDate:         points[endIdx].Date,
		TotalInvested:   totalInvested,
		TotalBTC:        totalBTC,
		CurrentValue:    currentValue,
		ROIPct:          roi,
		AnnualizedROI:   annROI,
		CostBasis:       costBasis,
		MaxDrawdown:     maxDD,
		ValuationZone:   zone,
		MonthlyInvested: investedAmounts,
		MonthlyBTC:      btcAmounts,
	}

	// Historical win rate in similar valuation zones
	r.ZoneHistoryWinRate, r.ZoneMedianROI = computeZoneStats(points, zone)

	return r, nil
}

// RunComparison compares all three strategies.
func RunComparison(points []Point, monthlyUSD int, halfLife int) (*ComparisonResult, error) {
	if monthlyUSD <= 0 {
		monthlyUSD = 1000
	}

	strategies := []Strategy{Fixed, AHR, Mayer}
	var results []Result

	for _, s := range strategies {
		params := Params{
			Strategy:     s,
			MonthlyUSD:   float64(monthlyUSD),
			HalfLifeDays: halfLife,
		}
		r, err := Run(points, params)
		if err != nil {
			continue
		}
		results = append(results, *r)
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("no strategy produced valid results")
	}

	// Find best by ROI
	best := results[0]
	for _, r := range results[1:] {
		if r.ROIPct > best.ROIPct {
			best = r
		}
	}

	latestPrice := 0.0
	if len(points) > 0 {
		latestPrice = points[len(points)-1].Close
	}

	return &ComparisonResult{
		Date:    time.Now().UTC().Format("2006-01-02"),
		Price:   latestPrice,
		Results: results,
		Best:    best.Strategy,
	}, nil
}

// ─── Strategy weights ───────────────────────────────────

func strategyWeight(s Strategy, points []Point, i int, params Params) float64 {
	switch s {
	case Fixed:
		return 1.0
	case AHR:
		return ahrWeight(points, i, params.HalfLifeDays)
	case Mayer:
		return mayerWeight(points, i)
	default:
		return 1.0
	}
}

func ahrWeight(points []Point, i int, halfLife int) float64 {
	if i < 200 {
		return 1.0
	}

	// Calculate AHR999
	dcaCost := harmonicMean(points, i, 200)
	fairValue := fairValueAHR(points[i].Date)
	if dcaCost <= 0 || fairValue <= 0 {
		return 1.0
	}
	ahr := (points[i].Close / dcaCost) * (points[i].Close / fairValue)

	// Weighting: ahr < 0.8 → 2x, 0.8-1.2 → 1x, > 1.2 → 0.5x
	switch {
	case ahr < 0.8:
		return 2.0
	case ahr > 1.2:
		return 0.5
	default:
		return 1.0
	}
}

func mayerWeight(points []Point, i int) float64 {
	mayer := mayerMultiple(points, i)
	switch {
	case mayer < 0.8:
		return 2.0
	case mayer > 1.5:
		return 0.5
	default:
		return 1.0
	}
}

// ─── AHR999 Fair Value ──────────────────────────────────

const (
	ahrLogSlope     = 5.84
	ahrLogIntercept = -17.01
)

func fairValueAHR(dateStr string) float64 {
	t, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return 0
	}
	genesis := time.Date(2009, 1, 3, 0, 0, 0, 0, time.UTC)
	age := t.Sub(genesis).Hours() / 24
	if age <= 0 {
		return 0
	}
	return math.Pow(10, ahrLogSlope*math.Log10(age)+ahrLogIntercept)
}

func harmonicMean(points []Point, i, window int) float64 {
	if i-window+1 < 0 || window <= 0 {
		return 0
	}
	sum := 0.0
	count := 0
	for j := i - window + 1; j <= i; j++ {
		if points[j].Close > 0 {
			sum += 1 / points[j].Close
			count++
		}
	}
	if count == 0 || sum == 0 {
		return 0
	}
	return float64(count) / sum
}

func mayerMultiple(points []Point, i int) float64 {
	if i < 200 {
		return 1.0
	}
	sum := 0.0
	for j := i - 199; j <= i; j++ {
		sum += points[j].Close
	}
	sma := sum / 200
	if sma <= 0 {
		return 1.0
	}
	return points[i].Close / sma
}

// ─── Valuation zone ─────────────────────────────────────

func currentValuationZone(points []Point, i int) string {
	mayer := mayerMultiple(points, i)
	switch {
	case mayer < 0.8:
		return "低估区 (Mayer < 0.8)"
	case mayer < 1.2:
		return "中性区 (0.8 ≤ Mayer < 1.2)"
	case mayer < 1.5:
		return "偏高区 (1.2 ≤ Mayer < 1.5)"
	default:
		return "高估区 (Mayer ≥ 1.5)"
	}
}

// computeZoneStats estimates win rate and median ROI across the full history
// for DCA entries starting at similar valuation zones.
func computeZoneStats(points []Point, currentZone string) (winRate, medianROI float64) {
	// Simplified: sample 1y DCA windows across history
	winCount := 0
	totalTests := 0
	var rois []float64

	for start := 200; start+365 < len(points); start += 30 {
		startZone := currentValuationZone(points, start)
		if startZone != currentZone {
			continue
		}

		// Simulate 1y DCA
		invested := 0.0
		btc := 0.0
		currentMonth := ""
		for i := start; i < start+365 && i < len(points); i++ {
			month := points[i].Date[:7]
			if month == currentMonth {
				continue
			}
			currentMonth = month
			btc += 1000 / points[i].Close
			invested += 1000
		}

		if invested > 0 {
			endPrice := points[minInt(start+365, len(points)-1)].Close
			value := btc * endPrice
			roi := (value - invested) / invested * 100
			rois = append(rois, roi)
			if roi > 0 {
				winCount++
			}
			totalTests++
		}
	}

	if totalTests == 0 {
		return 0, 0
	}

	sort.Float64s(rois)
	winRate = float64(winCount) / float64(totalTests) * 100
	if len(rois) > 0 {
		medianROI = rois[len(rois)/2]
	}
	return
}

// ─── Helpers ────────────────────────────────────────────

func normalizeDCA(points []Point) []Point {
	byDate := make(map[string]Point, len(points))
	for _, p := range points {
		if p.Close <= 0 {
			continue
		}
		byDate[p.Date] = p
	}
	out := make([]Point, 0, len(byDate))
	for _, p := range byDate {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

// ZoneReplayResult holds DCA performance broken down by valuation zone and period.
type ZoneReplayResult struct {
	CurrentZone  string       `json:"current_zone"`
	CurrentPrice float64      `json:"current_price"`
	Periods      []ZonePeriod `json:"periods"`
}

// ZonePeriod holds DCA stats for a specific lookback period.
type ZonePeriod struct {
	Years        int     `json:"years"`
	WinRate      float64 `json:"win_rate_pct"`
	MedianROI    float64 `json:"median_roi_pct"`
	MaxDrawdown  float64 `json:"max_drawdown_pct"`
	SampleCount  int     `json:"sample_count"`
	MedianAnnual float64 `json:"median_annual_pct"`
}

// ZoneReplay runs historical DCA replay across 1y/3y/5y windows
// filtered to entries starting in the current valuation zone.
//
// Assumes calendar-daily series (365 points/year) — correct for BTC.
func ZoneReplay(points []Point) (*ZoneReplayResult, error) {
	return ZoneReplayYearLen(points, 365)
}

// ZoneReplayYearLen is ZoneReplay with an explicit points-per-year, so that
// trading-day series (equities/gold ≈ 252/yr) get correctly-labelled 1y/3y/5y
// windows instead of BTC's calendar-daily 365 assumption.
func ZoneReplayYearLen(points []Point, yearDays int) (*ZoneReplayResult, error) {
	if yearDays <= 0 {
		yearDays = 365
	}
	if len(points) < yearDays*5+200 {
		return nil, fmt.Errorf("need at least 5 years + 200 days of history")
	}
	points = normalizeDCA(points)
	currentIdx := len(points) - 1
	currentZone := currentValuationZone(points, currentIdx)

	result := &ZoneReplayResult{
		CurrentZone:  currentZone,
		CurrentPrice: points[currentIdx].Close,
	}

	periods := []int{1, 3, 5}
	for _, years := range periods {
		days := years * yearDays
		var rois []float64
		var maxDDs []float64

		for start := 200; start+days < len(points); start += 30 {
			startZone := currentValuationZone(points, start)
			if startZone != currentZone {
				continue
			}

			invested := 0.0
			btc := 0.0
			peakValue := 0.0
			worstDD := 0.0
			currentMonth := ""

			for i := start; i < start+days && i < len(points); i++ {
				month := points[i].Date[:7]
				if month == currentMonth {
					continue
				}
				currentMonth = month
				btc += 1000 / points[i].Close
				invested += 1000

				currentValue := btc * points[i].Close
				if currentValue > peakValue {
					peakValue = currentValue
				}
				if peakValue > 0 {
					dd := (currentValue - peakValue) / peakValue * 100
					if dd < worstDD {
						worstDD = dd
					}
				}
			}

			if invested > 0 {
				endPrice := points[minInt(start+days, len(points)-1)].Close
				value := btc * endPrice
				roi := (value - invested) / invested * 100
				rois = append(rois, roi)
				maxDDs = append(maxDDs, worstDD)
			}
		}

		if len(rois) == 0 {
			continue
		}

		sort.Float64s(rois)
		sort.Float64s(maxDDs)

		winCount := 0
		for _, r := range rois {
			if r > 0 {
				winCount++
			}
		}

		medianROI := rois[len(rois)/2]
		medianDD := maxDDs[len(maxDDs)/2]
		medianAnnual := 0.0
		if years > 0 && medianROI > -100 {
			medianAnnual = (math.Pow(1+medianROI/100, 1/float64(years)) - 1) * 100
		}

		result.Periods = append(result.Periods, ZonePeriod{
			Years:        years,
			WinRate:      float64(winCount) / float64(len(rois)) * 100,
			MedianROI:    medianROI,
			MaxDrawdown:  medianDD,
			SampleCount:  len(rois),
			MedianAnnual: medianAnnual,
		})
	}

	return result, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
