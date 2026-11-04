// Multi-asset backtest report + model improvement analysis.
//
// Usage:
//   guanfu backtest all        # run all available assets
//   guanfu backtest all --json # JSON output for further analysis

package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/assetprofile"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast/backtest"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

// BacktestReport holds results for multiple assets with analysis.
type BacktestReport struct {
	Assets      []AssetBacktestResult `json:"assets"`
	Comparison  ComparisonTable       `json:"comparison"`
	Findings    []string              `json:"findings"`
	Suggestions []string              `json:"suggestions"`
}

type AssetBacktestResult struct {
	Asset              string   `json:"asset"`
	Profile            string   `json:"profile,omitempty"`
	ProfileVersion     string   `json:"profile_version,omitempty"`
	AssetClass         string   `json:"asset_class,omitempty"`
	FeatureBundle      string   `json:"feature_bundle"`
	MissingFeatures    []string `json:"missing_features,omitempty"`
	DataDays           int      `json:"data_days"`
	TestsRun           int      `json:"tests_run"`
	DirHit30d          float64  `json:"dir_hit_30d_pct"`
	DirHit90d          float64  `json:"dir_hit_90d_pct"`
	DirHit180d         float64  `json:"dir_hit_180d_pct"`
	PIT30d             float64  `json:"pit_30d"`
	PIT90d             float64  `json:"pit_90d"`
	PIT180d            float64  `json:"pit_180d"`
	CRPS30d            float64  `json:"crps_30d"`
	CRPS90d            float64  `json:"crps_90d"`
	CRPS180d           float64  `json:"crps_180d"`
	FeatureCov30d      float64  `json:"feature_coverage_30d_pct"`
	FeatureCov90d      float64  `json:"feature_coverage_90d_pct"`
	FeatureCov180d     float64  `json:"feature_coverage_180d_pct"`
	ConformalCov30d    float64  `json:"conformal_realized_coverage_30d_pct,omitempty"`
	ConformalCov90d    float64  `json:"conformal_realized_coverage_90d_pct,omitempty"`
	ConformalCov180d   float64  `json:"conformal_realized_coverage_180d_pct,omitempty"`
	AvgHitRate         float64  `json:"avg_hit_rate_pct"`
	AvgPIT             float64  `json:"avg_pit"`
	AvgCRPS            float64  `json:"avg_crps"`
	AvgFeatureCoverage float64  `json:"avg_feature_coverage_pct"`
}

type ComparisonTable struct {
	BestHitRate     string  `json:"best_hit_rate_asset"`
	BestHitRateVal  float64 `json:"best_hit_rate_val"`
	BestCalibration string  `json:"best_calibration_asset"`
	BestCRPS        string  `json:"best_crps_asset"`
	Consistent30d   bool    `json:"all_above_random_30d"`
	Consistent90d   bool    `json:"all_above_random_90d"`
}

func runBacktestAll(jsonOut, pretty, plain bool) {
	s := &store.PriceStore{}

	// Assets to test (skip if no data)
	candidates := []string{"btc", "qqq", "spy", "gold"}
	available := []string{}
	unavailable := []string{}
	for _, a := range candidates {
		if count, _ := s.Count(a); count >= 500 {
			available = append(available, a)
		} else {
			unavailable = append(unavailable, a)
		}
	}

	if len(available) == 0 {
		fmt.Fprintf(os.Stderr, "backtest all: no assets with 500+ days of data\n")
		fmt.Fprintf(os.Stderr, "Missing: %s\n", strings.Join(unavailable, ", "))
		fmt.Fprintf(os.Stderr, "Run 'guanfu btc' first to import BTC history.\n")
		os.Exit(1)
	}

	if len(unavailable) > 0 {
		fmt.Fprintf(os.Stderr, "Note: skipping %s (insufficient data)\n\n", strings.Join(unavailable, ", "))
	}

	horizons := []int{30, 90, 180}
	report := BacktestReport{
		Findings:    []string{},
		Suggestions: []string{},
	}

	for _, asset := range available {
		pricePoints, _ := s.Load(asset)
		points := make([]forecast.Point, len(pricePoints))
		for i, p := range pricePoints {
			points[i] = forecast.Point{Date: p.Date, Close: p.Close}
		}

		extractors := backtestExtractorsForAsset(asset, s)
		diag := diagnoseFeatureBundle(asset, points, extractors)
		// BTC: recency weighting + regime gate to reduce bear-era analog pull.
		// QQQ/SPY: regime gate avoids cross-regime analog contamination.
		// Gold: no regime gate (gold transitions regimes frequently; gate hurts).
		// QQQ/SPY: 42-day step for more test points (28→~40).
		opts := forecast.Options{Horizons: horizons, TopK: 21, StepDays: 7, Extractors: extractors, MinFeatures: 6, Asset: asset}
		switch asset {
		case "btc":
			opts.RecencyWeighted = true
			opts.RegimeGate = true
		case "qqq", "spy":
			opts.RegimeGate = true
		}
		if asset == "qqq" || asset == "spy" {
			opts.StepDays = 42
		}
		r, err := backtest.RunWithOptions(points, len(points)/3, 60, opts)
		if err != nil {
			continue
		}

		abr := AssetBacktestResult{
			Asset:           asset,
			FeatureBundle:   backtestFeatureLabel(asset),
			MissingFeatures: diag.MissingFeatures,
			DataDays:        len(points),
			TestsRun:        r.TotalTests,
		}
		if p, ok := assetprofile.For(asset); ok {
			abr.Profile = p.Key
			abr.ProfileVersion = p.Version
			abr.AssetClass = string(p.Class)
			abr.FeatureBundle = p.FeatureBundle
		}
		if hm := r.ByHorizon[30]; hm != nil {
			abr.DirHit30d = math.Round(hm.DirectionHitRate()*10000) / 100
			abr.PIT30d = math.Round(hm.PITMean()*1000) / 1000
			abr.CRPS30d = math.Round(hm.CRPSScore()*10000) / 10000
			abr.FeatureCov30d = math.Round(hm.FeatureCoverageMean()*10000) / 100
			abr.ConformalCov30d = math.Round(hm.ConformalHitRate()*10000) / 100
		}
		if hm := r.ByHorizon[90]; hm != nil {
			abr.DirHit90d = math.Round(hm.DirectionHitRate()*10000) / 100
			abr.PIT90d = math.Round(hm.PITMean()*1000) / 1000
			abr.CRPS90d = math.Round(hm.CRPSScore()*10000) / 10000
			abr.FeatureCov90d = math.Round(hm.FeatureCoverageMean()*10000) / 100
			abr.ConformalCov90d = math.Round(hm.ConformalHitRate()*10000) / 100
		}
		if hm := r.ByHorizon[180]; hm != nil {
			abr.DirHit180d = math.Round(hm.DirectionHitRate()*10000) / 100
			abr.PIT180d = math.Round(hm.PITMean()*1000) / 1000
			abr.CRPS180d = math.Round(hm.CRPSScore()*10000) / 10000
			abr.FeatureCov180d = math.Round(hm.FeatureCoverageMean()*10000) / 100
			abr.ConformalCov180d = math.Round(hm.ConformalHitRate()*10000) / 100
		}
		abr.AvgHitRate = math.Round((abr.DirHit30d+abr.DirHit90d+abr.DirHit180d)/3*100) / 100
		abr.AvgPIT = math.Round((abr.PIT30d+abr.PIT90d+abr.PIT180d)/3*1000) / 1000
		abr.AvgCRPS = math.Round((abr.CRPS30d+abr.CRPS90d+abr.CRPS180d)/3*10000) / 10000
		abr.AvgFeatureCoverage = math.Round((abr.FeatureCov30d+abr.FeatureCov90d+abr.FeatureCov180d)/3*100) / 100

		report.Assets = append(report.Assets, abr)
	}

	// --- Analysis ---

	// Find best by hit rate
	bestHR := report.Assets[0]
	bestCal := report.Assets[0]
	bestCRPS := report.Assets[0]
	allAbove30d := true
	allAbove90d := true

	for _, a := range report.Assets {
		if a.AvgHitRate > bestHR.AvgHitRate {
			bestHR = a
		}
		if math.Abs(a.AvgPIT-0.5) < math.Abs(bestCal.AvgPIT-0.5) {
			bestCal = a
		}
		if a.AvgCRPS < bestCRPS.AvgCRPS {
			bestCRPS = a
		}
		if a.DirHit30d < 50 {
			allAbove30d = false
		}
		if a.DirHit90d < 50 {
			allAbove90d = false
		}
	}

	report.Comparison = ComparisonTable{
		BestHitRate:     bestHR.Asset,
		BestHitRateVal:  bestHR.AvgHitRate,
		BestCalibration: bestCal.Asset,
		BestCRPS:        bestCRPS.Asset,
		Consistent30d:   allAbove30d,
		Consistent90d:   allAbove90d,
	}

	// Generate findings
	report.Findings = analyzeFindings(report.Assets, report.Comparison)
	report.Suggestions = generateSuggestions(report.Assets, report.Comparison)

	if jsonOut || pretty {
		var b []byte
		if pretty {
			b, _ = json.MarshalIndent(report, "", "  ")
		} else {
			b, _ = json.Marshal(report)
		}
		fmt.Println(string(b))
		return
	}

	printBacktestReport(report, plain)
}

func analyzeFindings(assets []AssetBacktestResult, comp ComparisonTable) []string {
	f := []string{}

	// Hit rate vs random baseline
	above50 := 0
	for _, a := range assets {
		if a.DirHit90d > 50 {
			above50++
		}
	}
	f = append(f, fmt.Sprintf("%d/%d 资产 90d 方向命中率 > 50%%（优于随机）", above50, len(assets)))

	// Horizon decay pattern
	avg30 := 0.0
	avg90 := 0.0
	avg180 := 0.0
	for _, a := range assets {
		avg30 += a.DirHit30d
		avg90 += a.DirHit90d
		avg180 += a.DirHit180d
	}
	n := float64(len(assets))
	avg30 /= n
	avg90 /= n
	avg180 /= n
	f = append(f, fmt.Sprintf("平均方向命中: 30d=%.1f%%  90d=%.1f%%  180d=%.1f%%", avg30, avg90, avg180))

	if avg90 > avg30 {
		f = append(f, "中期(90d)命中率高于短期(30d): 趋势信号在中期更有效")
	} else {
		f = append(f, "短期(30d)命中率高于中期(90d): 动量信号短期衰减快")
	}

	// Calibration assessment
	wellCalibrated := 0
	for _, a := range assets {
		if a.AvgPIT >= 0.45 && a.AvgPIT <= 0.55 {
			wellCalibrated++
		}
	}
	f = append(f, fmt.Sprintf("%d/%d 资产 PIT 校准良好 (0.45-0.55)", wellCalibrated, len(assets)))

	// Best asset
	f = append(f, fmt.Sprintf("最优方向预测: %s (avg %.1f%%)", comp.BestHitRate, comp.BestHitRateVal))

	return f
}

func generateSuggestions(assets []AssetBacktestResult, comp ComparisonTable) []string {
	s := []string{}

	// Collect stats
	avg30 := 0.0
	avg180 := 0.0
	for _, a := range assets {
		avg30 += a.DirHit30d
		avg180 += a.DirHit180d
	}
	n := float64(len(assets))
	avg30 /= n
	avg180 /= n

	// 1. Horizon-specific weighting
	if avg180 > avg30 {
		s = append(s, "长周期(180d)命中率更高 → 已启用 horizon-specific analog re-rank；继续观察长周期权重是否改善校准")
	} else {
		s = append(s, "短周期(30d)命中率更高 → kNN 更适合短期形态匹配，考虑缩短匹配窗口")
	}

	// 2. Cross-asset features
	if !comp.Consistent90d {
		s = append(s, "并非所有资产 90d > 50% → 纯价格特征对部分资产不足，需引入资产特定特征（如 PE/PB 对权益，实际利率对黄金）")
	}

	// 3. Feature engineering
	s = append(s, "特征改进方向:")
	s = append(s, "  a) 增加波动率聚类特征（GARCH-style vol regime）")
	s = append(s, "  b) 增加尾部风险特征（skewness, max drawdown duration）")
	s = append(s, "  c) 对权益资产加入 PE/PB 分位（Phase 3 Futu 快照已支持）")
	s = append(s, "  d) 对黄金加入实际利率变化率（不仅水平值）")
	s = append(s, "  e) 对 BTC 加入链上特征（MVRV Z-score, NUPL, SOPR）")

	// 4. Ensemble approach
	s = append(s, "模型架构改进:")
	s = append(s, "  a) 多周期 ensemble: horizon-specific re-rank 已落地，下一步验证是否需要独立 TopK / window")
	s = append(s, "  b) 动态特征选择: 不同市场状态使用不同特征子集")
	s = append(s, "  c) 引入 regime 先验: bull/bear/fracture 状态下调整候选池权重")
	s = append(s, "  d) 时序交叉验证替代随机切分: 避免前视偏差")

	// 5. Data quality
	s = append(s, "数据质量:")
	s = append(s, "  a) 对齐跨资产数据日期（当前简化对齐可能引入偏差）")
	s = append(s, "  b) 对非美资产应先建立独立数据与验证链路，再纳入核心回测")

	return s
}

func printBacktestReport(report BacktestReport, plain bool) {
	title := "观复 · 多资产回测报告"
	if plain {
		title = "Guanfu Multi-Asset Backtest Report"
	}

	fmt.Println(strings.Repeat("=", 76))
	fmt.Printf("  %s\n", title)
	fmt.Println(strings.Repeat("=", 76))
	fmt.Println()

	// Asset results table
	fmt.Println("一、各资产回测结果")
	fmt.Println(strings.Repeat("-", 76))
	fmt.Printf("%-8s %6s %6s %8s %8s %8s %8s %8s %8s\n",
		"Asset", "Days", "Tests", "Hit30d", "Hit90d", "Hit180d", "PIT", "CRPS", "FeatCov")
	fmt.Println(strings.Repeat("-", 76))

	sort.Slice(report.Assets, func(i, j int) bool {
		return report.Assets[i].AvgHitRate > report.Assets[j].AvgHitRate
	})

	for _, a := range report.Assets {
		fmt.Printf("%-8s %5dd %5d  %6.1f%% %7.1f%% %7.1f%% %6.2f %7.4f %7.0f%%\n",
			a.Asset, a.DataDays, a.TestsRun,
			a.DirHit30d, a.DirHit90d, a.DirHit180d,
			a.AvgPIT, a.AvgCRPS, a.AvgFeatureCoverage)
		profile := a.Profile
		if profile == "" {
			profile = a.Asset
		}
		fmt.Printf("         profile: %s@%s (%s); bundle: %s; missing: %s; conformal realized: 30d %.0f%% / 90d %.0f%% / 180d %.0f%%\n",
			profile, a.ProfileVersion, a.AssetClass, a.FeatureBundle, missingFeatureText(a.MissingFeatures), a.ConformalCov30d, a.ConformalCov90d, a.ConformalCov180d)
	}
	fmt.Println(strings.Repeat("-", 76))
	fmt.Println()

	// Comparison
	fmt.Println("二、横向对比")
	fmt.Println(strings.Repeat("-", 76))
	fmt.Printf("  最佳方向预测: %s (avg %.1f%%)\n", report.Comparison.BestHitRate, report.Comparison.BestHitRateVal)
	fmt.Printf("  最佳校准:     %s\n", report.Comparison.BestCalibration)
	fmt.Printf("  最佳 CRPS:    %s\n", report.Comparison.BestCRPS)
	fmt.Printf("  30d 全优于随机: %v\n", report.Comparison.Consistent30d)
	fmt.Printf("  90d 全优于随机: %v\n", report.Comparison.Consistent90d)
	fmt.Println()

	// Findings
	fmt.Println("三、关键发现")
	fmt.Println(strings.Repeat("-", 76))
	for i, f := range report.Findings {
		fmt.Printf("  %d. %s\n", i+1, f)
	}
	fmt.Println()

	// Suggestions
	fmt.Println("四、模型改进建议")
	fmt.Println(strings.Repeat("-", 76))
	suggestionNo := 1
	for _, s := range report.Suggestions {
		if strings.HasPrefix(s, "  ") {
			fmt.Printf("    %s\n", strings.TrimSpace(s))
		} else {
			fmt.Printf("  %d. %s\n", suggestionNo, s)
			suggestionNo++
		}
	}
	fmt.Println()

	fmt.Println(strings.Repeat("=", 76))
	fmt.Println("回测基于历史数据的统计规律，未来可能不同。不是投资建议。")
	fmt.Println(strings.Repeat("=", 76))
}
