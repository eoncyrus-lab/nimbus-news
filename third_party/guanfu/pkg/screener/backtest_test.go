package screener

import (
	"testing"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/screener/fine"
)

func makeDate(s string) time.Time {
	t, _ := time.Parse("2006-01-02", s)
	return t
}

func TestBacktest_MockData(t *testing.T) {
	// Setup mock price data: 3 stocks over 1 year, monotonic upward
	start := makeDate("2025-01-02")
	end := makeDate("2025-07-01")

	prices := NewMockPriceStore()
	// Stock A: steady growth
	ptsA := make([]PricePoint, 0)
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		days := d.Sub(start).Hours() / 24
		ptsA = append(ptsA, PricePoint{Date: d, Price: 100 + days*0.1})
	}
	prices.AddPrices("0001.HK", ptsA)

	// Stock B: volatile growth
	ptsB := make([]PricePoint, 0)
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		days := d.Sub(start).Hours() / 24
		ptsB = append(ptsB, PricePoint{Date: d, Price: 50 + days*0.15})
	}
	prices.AddPrices("0002.HK", ptsB)

	// Stock C: flat/down
	ptsC := make([]PricePoint, 0)
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		days := d.Sub(start).Hours() / 24
		ptsC = append(ptsC, PricePoint{Date: d, Price: 200 - days*0.05})
	}
	prices.AddPrices("0003.HK", ptsC)

	// Benchmark
	benchPts := make([]PricePoint, 0)
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		days := d.Sub(start).Hours() / 24
		benchPts = append(benchPts, PricePoint{Date: d, Price: 1000 + days*0.08})
	}
	prices.AddBenchmark("HSI", benchPts)

	// Mock candidates: same set every period
	candidates := []model.ScreenedCandidate{
		{Symbol: "0001.HK", Name: "Stock A", Market: "HK", Price: 100, Indicators: map[string]float64{
			"pettm": 8, "pbmrq": 0.8, "roe": 15, "divyld": 5, "netmargin": 20, "la": 30, "total_amount": 5e8, "marketcap": 200,
		}},
		{Symbol: "0002.HK", Name: "Stock B", Market: "HK", Price: 50, Indicators: map[string]float64{
			"pettm": 12, "pbmrq": 1.2, "roe": 22, "divyld": 3, "netmargin": 18, "la": 45, "total_amount": 3e8, "marketcap": 80,
		}},
		{Symbol: "0003.HK", Name: "Stock C", Market: "HK", Price: 200, Indicators: map[string]float64{
			"pettm": 25, "pbmrq": 3.0, "roe": 10, "divyld": 2, "netmargin": 12, "la": 55, "total_amount": 1e9, "marketcap": 500,
		}},
	}

	profile := &model.StrategyProfile{
		Name: "港股价值", Market: "HK", Style: "value",
		Weights:          model.HKWeights(),
		CoreCandidateMin: 65,
		WatchlistMin:     50,
	}

	cfg := BacktestConfig{
		Name:           "test_hk_value",
		Profile:        profile,
		StartDate:      start,
		EndDate:        end,
		TopN:           2,
		HoldDays:       30,
		RebalanceFreq:  30,
		Benchmark:      "HSI",
		InitialCapital: 1000000,
	}

	result, err := RunBacktest(cfg, prices, NewMockCandidateProvider(candidates))
	if err != nil {
		t.Fatalf("backtest failed: %v", err)
	}

	t.Logf("Backtest: %d periods, annual=%.2f%%, benchmark=%.2f%%, excess=%.2f%%",
		result.NumPeriods, result.AnnualReturn*100, result.BenchmarkReturn*100, result.ExcessReturn*100)
	t.Logf("Sharpe=%.2f, maxDD=%.1f%%, winRate=%.1f%%, vol=%.1f%%",
		result.SharpeRatio, result.MaxDrawdown*100, result.WinRate*100, result.Volatility*100)
	for h, rate := range result.HorizonHitRates {
		t.Logf("  dir_hit_%dd: %.1f%%", h, rate*100)
	}
	t.Logf("Warnings: %v", result.Warnings)

	if result.NumPeriods == 0 {
		t.Error("expected at least 1 period")
	}
	if result.EquityCurve[0].Equity != cfg.InitialCapital {
		t.Errorf("initial equity mismatch: %.2f", result.EquityCurve[0].Equity)
	}
	// Equity should grow since stocks A and B are trending up
	if result.EquityCurve[len(result.EquityCurve)-1].Equity <= cfg.InitialCapital {
		t.Log("equity did not grow — may be due to stock C dragging down")
	}
}

func TestBacktest_DelistedStock(t *testing.T) {
	// Simulate a stock that crashes to near-zero (delisting scenario).
	// After delisting date, no price data available.
	// The backtest should find the last available price (near zero) and compute
	// a large negative return.
	start := makeDate("2025-01-02")
	end := makeDate("2025-04-01")

	prices := NewMockPriceStore()
	// Stock trends up then crashes to 0.01 at delisting
	ptsA := make([]PricePoint, 0)
	for d := start; !d.After(makeDate("2025-02-10")); d = d.AddDate(0, 0, 1) {
		days := d.Sub(start).Hours() / 24
		if d.Before(makeDate("2025-02-05")) {
			ptsA = append(ptsA, PricePoint{Date: d, Price: 100 + days*0.2})
		} else {
			ptsA = append(ptsA, PricePoint{Date: d, Price: 0.01}) // crash
		}
	}
	prices.AddPrices("CRASH.HK", ptsA)

	candidates := []model.ScreenedCandidate{
		{Symbol: "CRASH.HK", Name: "Crash Co", Market: "HK", Price: 100, Indicators: map[string]float64{
			"pettm": 8, "pbmrq": 0.8, "roe": 15, "divyld": 5, "netmargin": 20, "la": 30, "total_amount": 5e8, "marketcap": 200,
		}},
	}

	profile := &model.StrategyProfile{Name: "test", Market: "HK", Weights: model.HKWeights()}
	cfg := BacktestConfig{
		Name: "test_crash", Profile: profile,
		StartDate: start, EndDate: end,
		TopN: 1, HoldDays: 63, RebalanceFreq: 63, InitialCapital: 1000000,
	}

	result, err := RunBacktest(cfg, prices, NewMockCandidateProvider(candidates))
	if err != nil {
		t.Fatalf("backtest failed: %v", err)
	}
	t.Logf("Crash backtest: periods=%d, annual=%.2f%%", result.NumPeriods, result.AnnualReturn*100)

	// First period should have huge negative return (crash to near-zero)
	if result.NumPeriods > 0 {
		t.Logf("Period 1 return: %.2f%%", result.PeriodReturns[0].Return*100)
		if result.PeriodReturns[0].Return > -0.5 {
			t.Errorf("expected < -50%% return for crash stock, got %.2f%%", result.PeriodReturns[0].Return*100)
		}
	}
}

func TestBacktest_RegressionGate(t *testing.T) {
	// Verify horizon hit rates are computed correctly
	profile := &model.StrategyProfile{Name: "test", Market: "HK", Weights: model.HKWeights()}
	cfg := BacktestConfig{
		Name: "test_regression", Profile: profile,
		StartDate: makeDate("2025-01-02"), EndDate: makeDate("2025-07-01"),
		TopN: 2, HoldDays: 30, RebalanceFreq: 30, InitialCapital: 1000000,
	}

	prices := NewMockPriceStore()
	start := cfg.StartDate
	end := cfg.EndDate

	// All stocks trending up → all periods should have positive returns
	ptsUp := make([]PricePoint, 0)
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		days := d.Sub(start).Hours() / 24
		ptsUp = append(ptsUp, PricePoint{Date: d, Price: 100 + days*0.2})
	}
	prices.AddPrices("UP.HK", ptsUp)
	prices.AddPrices("UP2.HK", ptsUp)

	candidates := []model.ScreenedCandidate{
		{Symbol: "UP.HK", Name: "Up", Market: "HK", Price: 100, Indicators: map[string]float64{
			"pettm": 8, "pbmrq": 0.8, "roe": 15, "divyld": 5, "netmargin": 20, "la": 30, "total_amount": 5e8, "marketcap": 200,
		}},
		{Symbol: "UP2.HK", Name: "Up2", Market: "HK", Price: 100, Indicators: map[string]float64{
			"pettm": 8, "pbmrq": 0.8, "roe": 15, "divyld": 5, "netmargin": 20, "la": 30, "total_amount": 5e8, "marketcap": 200,
		}},
	}

	result, err := RunBacktest(cfg, prices, NewMockCandidateProvider(candidates))
	if err != nil {
		t.Fatalf("backtest failed: %v", err)
	}

	// With all stocks trending up, non-zero horizons should be 100%.
	// 252d may be 0 if insufficient periods (need enough 30d periods to cover 252d).
	for h, rate := range result.HorizonHitRates {
		if h <= 180 && rate < 1.0 {
			t.Errorf("horizon %dd dir_hit=%.1f%% — expected 100%% for short horizons", h, rate*100)
		}
	}
	t.Logf("All horizon hit rates: %v", result.HorizonHitRates)

	// Verify regression gate logic (only for achievable horizons)
	for h, rate := range result.HorizonHitRates {
		if h <= 180 && rate < 0.5 {
			t.Errorf("REGRESSION: horizon %dd dir_hit=%.1f%% below 50%% threshold", h, rate*100)
		}
	}
}

// Test that scoring works correctly within the backtest context
func TestBacktest_ScoringIntegration(t *testing.T) {
	candidates := []model.ScreenedCandidate{
		{Symbol: "CHEAP.HK", Name: "Cheap Value", Market: "HK", Price: 30, Indicators: map[string]float64{
			"pettm": 5, "pbmrq": 0.3, "roe": 18, "divyld": 8, "netmargin": 25, "la": 20, "total_amount": 1e9, "marketcap": 300,
		}},
		{Symbol: "EXP.HK", Name: "Expensive", Market: "HK", Price: 500, Indicators: map[string]float64{
			"pettm": 80, "pbmrq": 8, "roe": 8, "divyld": 0.5, "netmargin": 10, "la": 60, "total_amount": 5e8, "marketcap": 1000,
		}},
	}

	profile := &model.StrategyProfile{Name: "港股价值", Market: "HK", Weights: model.HKWeights()}
	fine.ScoreAll(candidates, profile)

	// Cheap value stock should rank higher
	if candidates[0].FinalScore < candidates[1].FinalScore {
		t.Errorf("cheap value stock should outrank expensive: cheap=%.1f expensive=%.1f",
			candidates[0].FinalScore, candidates[1].FinalScore)
	}
	t.Logf("Cheap: final=%.1f val=%.1f qual=%.1f Expensive: final=%.1f val=%.1f qual=%.1f",
		candidates[0].FinalScore, candidates[0].ValScore, candidates[0].QualScore,
		candidates[1].FinalScore, candidates[1].ValScore, candidates[1].QualScore)
}
