package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast/backtest"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast/features"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

type putCallAblationResult struct {
	Asset              string  `json:"asset"`
	Variant            string  `json:"variant"`
	TestsRun           int     `json:"tests_run"`
	DirHit30d          float64 `json:"dir_hit_30d_pct"`
	DirHit90d          float64 `json:"dir_hit_90d_pct"`
	DirHit180d         float64 `json:"dir_hit_180d_pct"`
	PIT                float64 `json:"pit"`
	CRPS               float64 `json:"crps"`
	FeatureCoveragePct float64 `json:"feature_coverage_pct"`
	ConformalCov30d    float64 `json:"conformal_realized_coverage_30d_pct,omitempty"`
	ConformalCov90d    float64 `json:"conformal_realized_coverage_90d_pct,omitempty"`
	ConformalCov180d   float64 `json:"conformal_realized_coverage_180d_pct,omitempty"`
}

func runPutCallAblation(jsonOut, pretty, plain bool) {
	s := &store.PriceStore{}
	horizons := []int{30, 90, 180}
	var out []putCallAblationResult
	for _, asset := range []string{"qqq", "spy"} {
		pricePoints, err := s.Load(asset)
		if err != nil || len(pricePoints) < 500 {
			continue
		}
		points := make([]forecast.Point, len(pricePoints))
		for i, p := range pricePoints {
			points[i] = forecast.Point{Date: p.Date, Close: p.Close, Source: "price_store:" + asset}
		}
		for _, variant := range putCallAblationVariants() {
			extractors := features.EquityExtractorsWithPutCallMode(s, variant.mode)
			opts := forecast.Options{Horizons: horizons, TopK: 21, StepDays: 7, Extractors: extractors, MinFeatures: 6, Asset: asset}
			r, err := backtest.RunWithOptions(points, len(points)/3, 60, opts)
			if err != nil {
				continue
			}
			out = append(out, summarizePutCallAblation(asset, variant.name, r))
		}
	}
	if len(out) == 0 {
		fmt.Fprintln(os.Stderr, "put/call ablation: no QQQ/SPY data available")
		os.Exit(1)
	}
	if jsonOut || pretty {
		var b []byte
		if pretty {
			b, _ = json.MarshalIndent(out, "", "  ")
		} else {
			b, _ = json.Marshal(out)
		}
		fmt.Println(string(b))
		return
	}
	printPutCallAblation(out, plain)
}

type putCallAblationVariant struct {
	name string
	mode features.PutCallFeatureMode
}

func putCallAblationVariants() []putCallAblationVariant {
	return []putCallAblationVariant{
		{name: "none", mode: features.PutCallNone},
		{name: "ratio", mode: features.PutCallRatioOnly},
		{name: "ratio+change", mode: features.PutCallRatioAndChange},
		{name: "all", mode: features.PutCallAll},
	}
}

func summarizePutCallAblation(asset, variant string, r *backtest.Result) putCallAblationResult {
	res := putCallAblationResult{Asset: asset, Variant: variant, TestsRun: r.TotalTests}
	if hm := r.ByHorizon[30]; hm != nil {
		res.DirHit30d = roundPct(hm.DirectionHitRate())
		res.ConformalCov30d = roundPct(hm.ConformalHitRate())
	}
	if hm := r.ByHorizon[90]; hm != nil {
		res.DirHit90d = roundPct(hm.DirectionHitRate())
		res.ConformalCov90d = roundPct(hm.ConformalHitRate())
	}
	if hm := r.ByHorizon[180]; hm != nil {
		res.DirHit180d = roundPct(hm.DirectionHitRate())
		res.ConformalCov180d = roundPct(hm.ConformalHitRate())
	}
	pit, crps, cov, n := 0.0, 0.0, 0.0, 0.0
	for _, h := range []int{30, 90, 180} {
		hm := r.ByHorizon[h]
		if hm == nil || hm.SampleCount == 0 {
			continue
		}
		pit += hm.PITMean()
		crps += hm.CRPSScore()
		cov += hm.FeatureCoverageMean()
		n++
	}
	if n > 0 {
		res.PIT = math.Round(pit/n*1000) / 1000
		res.CRPS = math.Round(crps/n*10000) / 10000
		res.FeatureCoveragePct = math.Round(cov/n*10000) / 100
	}
	return res
}

func roundPct(v float64) float64 {
	return math.Round(v*10000) / 100
}

func printPutCallAblation(rows []putCallAblationResult, plain bool) {
	title := "观复 · QQQ/SPY Put/Call 特征消融"
	if plain {
		title = "Guanfu Put/Call Feature Ablation"
	}
	fmt.Println(strings.Repeat("=", 86))
	fmt.Printf("  %s\n", title)
	fmt.Println(strings.Repeat("=", 86))
	fmt.Printf("%-6s %-14s %6s %8s %8s %8s %7s %8s %8s %18s\n",
		"Asset", "Variant", "Tests", "Hit30d", "Hit90d", "Hit180d", "PIT", "CRPS", "FeatCov", "ConfCov 30/90/180")
	fmt.Println(strings.Repeat("-", 86))
	for _, r := range rows {
		fmt.Printf("%-6s %-14s %6d %7.1f%% %7.1f%% %7.1f%% %7.2f %8.4f %7.0f%% %5.0f%%/%3.0f%%/%3.0f%%\n",
			r.Asset, r.Variant, r.TestsRun, r.DirHit30d, r.DirHit90d, r.DirHit180d,
			r.PIT, r.CRPS, r.FeatureCoveragePct, r.ConformalCov30d, r.ConformalCov90d, r.ConformalCov180d)
	}
	fmt.Println(strings.Repeat("=", 86))
	fmt.Println("Use: guanfu backtest all --ablate-putcall [--json|--pretty]")
}
