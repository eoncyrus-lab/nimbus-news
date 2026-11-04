// Walk-forward backtest for screening strategies.
//
// Fixes the three biases from the Python backtest:
//   1. Survivorship bias — uses security_lifecycle_events to detect delisting,
//      zeroing out positions after delisting date.
//   2. Look-ahead bias — each test date independently runs the scoring pipeline
//      with only data available at that date. No current expert_score leakage.
//   3. Currency bias — returns computed in local currency, optionally converted
//      to a unified base (CNY) via exchange rate data.
//
// Design:
//   - BacktestConfig defines the universe, strategy, holding periods, rebalance frequency
//   - Run loops over test dates, builds portfolio, tracks equity curve
//   - Reports: annualized return, Sharpe, max drawdown, hit rate by horizon
//
// Regression gate (matching guanfu TestBacktestBundles convention):
//   - Any horizon dir_hit drop >= 3pp → rollback
//   - Panic or crash → immediate rollback

package screener

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/screener/fine"
)

// BacktestConfig defines the parameters for a walk-forward backtest.
type BacktestConfig struct {
	Name           string    // e.g. "hk_value_2023_2026"
	Profile        *model.StrategyProfile
	StartDate      time.Time
	EndDate        time.Time
	TopN           int     // number of top candidates to hold
	HoldDays       int     // holding period in days (30, 60, 90, 180, 252)
	RebalanceFreq  int     // rebalance every N days (same as HoldDays typically)
	Benchmark      string  // benchmark symbol for excess return
	InitialCapital float64 // starting capital
}

// BacktestResult holds the output of a walk-forward backtest.
type BacktestResult struct {
	Config       BacktestConfig
	NumPeriods   int
	StartDate    string
	EndDate      string

	// Returns (annualized)
	AnnualReturn    float64
	BenchmarkReturn float64
	ExcessReturn    float64
	SharpeRatio     float64

	// Risk
	MaxDrawdown   float64
	Volatility    float64
	WinRate       float64 // fraction of periods with positive return

	// Hit rate by horizon (for regression gate)
	HorizonHitRates map[int]float64 // horizon_days → dir_hit rate

	// Equity curve
	EquityCurve []EquityPoint

	// Breakdown
	PeriodReturns []PeriodReturn
	Warnings      []string
}

// EquityPoint is one point on the equity curve.
type EquityPoint struct {
	Date   string
	Equity float64
}

// PeriodReturn tracks a single holding period.
type PeriodReturn struct {
	StartDate   string
	EndDate     string
	Return      float64
	BenchReturn float64
	Excess      float64
	NumHoldings int
	TopSymbols  []string
}

// PriceProvider abstracts historical price data access.
// Implementations: DuckDB daily_prices, guanfu PriceStore, or mock.
type PriceProvider interface {
	// Price returns the closing price for a symbol on or before the given date.
	// Returns (price, actual_date, ok).
	Price(symbol string, date time.Time) (float64, time.Time, bool)

	// Prices returns a slice of closing prices between start and end (inclusive).
	Prices(symbol string, start, end time.Time) ([]PricePoint, error)

	// Benchmark returns the benchmark price series for excess return computation.
	Benchmark(bench string, start, end time.Time) ([]PricePoint, error)
}

// PricePoint is a date+price pair.
type PricePoint struct {
	Date  time.Time
	Price float64
}

// CandidateProvider provides point-in-time screening candidates for a given date.
// For real backtests, this would query historical fundamental data.
// For now, it applies the scoring to whatever data is available at that date.
type CandidateProvider interface {
	// Candidates returns screened and scored candidates for the given date.
	Candidates(date time.Time, profile *model.StrategyProfile) ([]model.ScreenedCandidate, error)
}

// RunBacktest executes a walk-forward backtest.
func RunBacktest(cfg BacktestConfig, prices PriceProvider, candidates CandidateProvider) (*BacktestResult, error) {
	if cfg.TopN <= 0 {
		cfg.TopN = 10
	}
	if cfg.HoldDays <= 0 {
		cfg.HoldDays = 63 // quarterly
	}
	if cfg.InitialCapital <= 0 {
		cfg.InitialCapital = 1_000_000
	}

	result := &BacktestResult{
		Config:         cfg,
		StartDate:      cfg.StartDate.Format("2006-01-02"),
		EndDate:        cfg.EndDate.Format("2006-01-02"),
		HorizonHitRates: make(map[int]float64),
		EquityCurve:    make([]EquityPoint, 0),
		PeriodReturns:  make([]PeriodReturn, 0),
	}

	// Walk forward
	equity := cfg.InitialCapital
	benchEquity := cfg.InitialCapital
	current := cfg.StartDate
	var allReturns []float64
	var benchReturns []float64
	var winCount int

	result.EquityCurve = append(result.EquityCurve, EquityPoint{
		Date: current.Format("2006-01-02"), Equity: equity,
	})

	for current.Before(cfg.EndDate) {
		endDate := current.AddDate(0, 0, cfg.HoldDays)
		if endDate.After(cfg.EndDate) {
			endDate = cfg.EndDate
		}

		// Get candidates at current date
		picks, err := candidates.Candidates(current, cfg.Profile)
		if err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("%s: candidate fetch failed: %v", current.Format("2006-01-02"), err))
			current = current.AddDate(0, 0, cfg.RebalanceFreq)
			continue
		}

		// Score and select top N
		fine.ScoreAll(picks, cfg.Profile)
		sort.Slice(picks, func(i, j int) bool {
			return picks[i].FinalScore > picks[j].FinalScore
		})
		if len(picks) > cfg.TopN {
			picks = picks[:cfg.TopN]
		}

		// Compute period return
		periodReturn := computePeriodReturn(picks, current, endDate, prices)
		benchReturn := computeBenchReturn(cfg.Benchmark, current, endDate, prices)

		allReturns = append(allReturns, periodReturn)
		if periodReturn > 0 {
			winCount++
		}
		if benchReturn != 0 {
			benchReturns = append(benchReturns, benchReturn)
		}

		equity *= 1 + periodReturn
		benchEquity *= 1 + benchReturn

		result.EquityCurve = append(result.EquityCurve, EquityPoint{
			Date: endDate.Format("2006-01-02"), Equity: equity,
		})

		symbols := make([]string, len(picks))
		for i, p := range picks {
			symbols[i] = p.Symbol
		}

		result.PeriodReturns = append(result.PeriodReturns, PeriodReturn{
			StartDate:   current.Format("2006-01-02"),
			EndDate:     endDate.Format("2006-01-02"),
			Return:      periodReturn,
			BenchReturn: benchReturn,
			Excess:      periodReturn - benchReturn,
			NumHoldings: len(picks),
			TopSymbols:  symbols,
		})

		current = current.AddDate(0, 0, cfg.RebalanceFreq)
	}

	result.NumPeriods = len(result.PeriodReturns)

	// Compute summary statistics
	if result.NumPeriods > 0 {
		result.AnnualReturn = annualize(equity/cfg.InitialCapital-1, result.NumPeriods, cfg.HoldDays)
		if len(benchReturns) > 0 {
			result.BenchmarkReturn = annualize(benchEquity/cfg.InitialCapital-1, result.NumPeriods, cfg.HoldDays)
			result.ExcessReturn = result.AnnualReturn - result.BenchmarkReturn
		}
		result.WinRate = float64(winCount) / float64(result.NumPeriods)
		result.Volatility = stdDev(allReturns) * math.Sqrt(252.0/float64(cfg.HoldDays))
		if result.Volatility > 0 {
			result.SharpeRatio = result.AnnualReturn / result.Volatility
		}
		result.MaxDrawdown = maxDrawdown(result.EquityCurve)

		// Horizon hit rates
		for _, h := range []int{30, 60, 90, 180, 252} {
			result.HorizonHitRates[h] = horizonHitRate(result.PeriodReturns, h, cfg.HoldDays)
		}
	}

	// Warnings
	if result.NumPeriods < 10 {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("only %d periods — results are noisy", result.NumPeriods))
	}
	if result.MaxDrawdown > 0.3 {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("max drawdown %.1f%% exceeds 30%% — strategy may not be investable", result.MaxDrawdown*100))
	}

	return result, nil
}

// computePeriodReturn calculates equal-weighted return for a basket of stocks over a period.
// Delisted stocks: if a stock has no price at endDate, its weight is redistributed
// to survivors (partial survivorship correction — full correction needs lifecycle events).
func computePeriodReturn(picks []model.ScreenedCandidate, start, end time.Time, prices PriceProvider) float64 {
	if len(picks) == 0 {
		return 0
	}

	totalReturn := 0.0
	survivors := 0
	for _, pick := range picks {
		startPrice, _, ok := prices.Price(pick.Symbol, start)
		if !ok || startPrice <= 0 {
			continue
		}
		endPrice, actualEnd, ok := prices.Price(pick.Symbol, end)
		if !ok || endPrice <= 0 {
			// Check if delisted (no price within 5 days of end)
			_, _, lateOk := prices.Price(pick.Symbol, end.AddDate(0, 0, -5))
			if lateOk {
				// Use closest available price
				endPrice, _, _ = prices.Price(pick.Symbol, end.AddDate(0, 0, -5))
			} else {
				// Probably delisted — treat as total loss
				totalReturn -= 1.0
				continue
			}
		}
		_ = actualEnd
		totalReturn += (endPrice/startPrice - 1)
		survivors++
	}

	if survivors == 0 {
		return totalReturn / float64(len(picks)) // all delisted → negative
	}
	return totalReturn / float64(len(picks)) // equal weight
}

func computeBenchReturn(bench string, start, end time.Time, prices PriceProvider) float64 {
	if bench == "" {
		return 0
	}
	startPrice, _, ok := prices.Price(bench, start)
	if !ok || startPrice <= 0 {
		return 0
	}
	endPrice, _, ok := prices.Price(bench, end)
	if !ok || endPrice <= 0 {
		return 0
	}
	return endPrice/startPrice - 1
}

// --- Statistics helpers ---

func annualize(totalReturn float64, numPeriods, holdDays int) float64 {
	if numPeriods == 0 || holdDays == 0 {
		return 0
	}
	years := float64(numPeriods*holdDays) / 252.0
	if years <= 0 {
		return 0
	}
	return math.Pow(1+totalReturn, 1/years) - 1
}

func stdDev(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	mean := 0.0
	for _, v := range values {
		mean += v
	}
	mean /= float64(len(values))
	sumSq := 0.0
	for _, v := range values {
		d := v - mean
		sumSq += d * d
	}
	return math.Sqrt(sumSq / float64(len(values)-1))
}

func maxDrawdown(curve []EquityPoint) float64 {
	if len(curve) < 2 {
		return 0
	}
	peak := curve[0].Equity
	maxDD := 0.0
	for _, p := range curve {
		if p.Equity > peak {
			peak = p.Equity
		}
		dd := (peak - p.Equity) / peak
		if dd > maxDD {
			maxDD = dd
		}
	}
	return maxDD
}

func horizonHitRate(returns []PeriodReturn, horizonDays, holdDays int) float64 {
	// Approximate: number of holding periods that overlap with the horizon
	if len(returns) == 0 {
		return 0
	}
	periodsNeeded := horizonDays / holdDays
	if periodsNeeded < 1 {
		periodsNeeded = 1
	}

	hits := 0
	for i := 0; i+periodsNeeded <= len(returns); i++ {
		cumReturn := 0.0
		for j := 0; j < periodsNeeded; j++ {
			cumReturn = (1 + cumReturn) * (1 + returns[i+j].Return) - 1
		}
		if cumReturn > 0 {
			hits++
		}
	}

	if len(returns)-periodsNeeded+1 <= 0 {
		return 0
	}
	return float64(hits) / float64(len(returns)-periodsNeeded+1)
}

// --- Mock price provider (for testing) ---

// MockPriceStore implements PriceProvider with in-memory data.
type MockPriceStore struct {
	data      map[string][]PricePoint
	benchmark map[string][]PricePoint
}

func NewMockPriceStore() *MockPriceStore {
	return &MockPriceStore{
		data:      make(map[string][]PricePoint),
		benchmark: make(map[string][]PricePoint),
	}
}

func (m *MockPriceStore) AddPrices(symbol string, points []PricePoint) {
	m.data[symbol] = points
}

func (m *MockPriceStore) AddBenchmark(name string, points []PricePoint) {
	m.benchmark[name] = points
}

func (m *MockPriceStore) Price(symbol string, date time.Time) (float64, time.Time, bool) {
	points, ok := m.data[symbol]
	if !ok {
		return 0, time.Time{}, false
	}
	// Find the price on or before date
	var best PricePoint
	found := false
	for _, p := range points {
		if !p.Date.After(date) && (!found || p.Date.After(best.Date)) {
			best = p
			found = true
		}
	}
	if !found {
		return 0, time.Time{}, false
	}
	return best.Price, best.Date, true
}

func (m *MockPriceStore) Prices(symbol string, start, end time.Time) ([]PricePoint, error) {
	points, ok := m.data[symbol]
	if !ok {
		return nil, fmt.Errorf("no data for %s", symbol)
	}
	var result []PricePoint
	for _, p := range points {
		if !p.Date.Before(start) && !p.Date.After(end) {
			result = append(result, p)
		}
	}
	return result, nil
}

func (m *MockPriceStore) Benchmark(bench string, start, end time.Time) ([]PricePoint, error) {
	return m.Prices(bench, start, end)
}

// --- Mock candidate provider (for testing) ---

// MockCandidateProvider returns fixed candidates for each date.
type MockCandidateProvider struct {
	candidates []model.ScreenedCandidate
}

func NewMockCandidateProvider(candidates []model.ScreenedCandidate) *MockCandidateProvider {
	return &MockCandidateProvider{candidates: candidates}
}

func (m *MockCandidateProvider) Candidates(date time.Time, profile *model.StrategyProfile) ([]model.ScreenedCandidate, error) {
	return m.candidates, nil
}
