// Joint forecast subcommand (H5).
//
// Runs independent kNN forecasts on multiple assets and surfaces the
// cross-asset consensus: when several assets' dominant scenarios
// agree, that's a stronger signal than any single asset. When they
// diverge, that's itself information (regime uncertainty).
//
// We deliberately do NOT stitch features across assets into a joint
// vector — that collapses independent regime signals into mush and
// was rejected in v3 discussions. Independent runs preserve each
// asset's reliability metadata.
//
// Usage:
//
//	guanfu joint --assets btc,qqq,gold
//	guanfu joint --assets btc,qqq,spy --horizon 90
//	guanfu joint --assets btc,qqq --json

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/engine"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast"
)

const jointUsage = `usage: guanfu joint --assets X,Y[,Z...] [--horizon N] [--json]

  --assets   comma-separated asset keys (btc/qqq/spy/gold)
  --horizon  forward-return window in days (default 90)
  --json     structured JSON output
`

// JointRow summarizes one asset's contribution to the consensus.
type JointRow struct {
	Asset            string  `json:"asset"`
	DominantScenario string  `json:"dominant_scenario"` // "upside" / "range" / "downside"
	DominantLabel    string  `json:"dominant_label"`
	ProbUp           float64 `json:"prob_upside"`
	ProbRange        float64 `json:"prob_range"`
	ProbDown         float64 `json:"prob_downside"`
	MedianPct        float64 `json:"median_return_pct"`
	HardBlocked      bool    `json:"hard_blocked,omitempty"`
	ReliabilityNote  string  `json:"reliability_note,omitempty"`
	N                int     `json:"n_analogs"`
}

// JointResult is the consensus payload.
type JointResult struct {
	Horizon   int        `json:"horizon"`
	Rows      []JointRow `json:"rows"`
	Consensus string     `json:"consensus"` // "upside" / "range" / "downside" / "mixed"
	Agreement float64    `json:"agreement"` // 0-1, fraction of non-hard-blocked rows in the plurality scenario
	Note      string     `json:"note"`
}

func runJoint(args []string) {
	flags := parseKV(args)
	assetCSV := strings.TrimSpace(flags["assets"])
	if assetCSV == "" {
		fmt.Fprint(os.Stderr, jointUsage)
		os.Exit(2)
	}
	assets := strings.Split(strings.ToLower(assetCSV), ",")
	for i, a := range assets {
		assets[i] = strings.TrimSpace(a)
	}
	horizon := 90
	if v, ok := flags["horizon"]; ok {
		fmt.Sscanf(v, "%d", &horizon)
		if horizon <= 0 {
			horizon = 90
		}
	}
	jsonOut := flags["json"] == "true"

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	rows := make([]JointRow, 0, len(assets))
	for _, asset := range assets {
		row, err := buildJointRow(ctx, asset, horizon)
		if err != nil {
			fmt.Fprintf(os.Stderr, "joint: asset %q: %v\n", asset, err)
			continue
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		fmt.Fprintln(os.Stderr, "joint: no assets resolved")
		os.Exit(1)
	}

	result := summarizeConsensus(rows, horizon)
	if jsonOut {
		b, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(b))
		return
	}
	printJointHuman(result)
}

// buildJointRow runs per-asset BuildForecast at the requested horizon.
// For BTC the path differs (goes through the legacy snapshot API, not
// GetAsset); we inline that branch to avoid pulling the full CLI flow.
func buildJointRow(ctx context.Context, asset string, horizon int) (JointRow, error) {
	a, err := engine.GetAsset(asset)
	if err != nil {
		return JointRow{}, err
	}
	snap, err := a.FetchSnapshot(ctx)
	if err != nil {
		return JointRow{}, err
	}
	opts := forecast.DefaultOptions()
	opts.Horizons = []int{horizon}
	opts.TopK = 21
	fc, err := a.BuildForecast(snap, opts)
	if err != nil {
		return JointRow{}, err
	}
	if fc == nil || len(fc.Horizons) == 0 {
		return JointRow{}, fmt.Errorf("no horizon produced")
	}
	h := fc.Horizons[0]
	return JointRow{
		Asset:            asset,
		DominantScenario: h.DominantScenario,
		DominantLabel:    h.DominantLabel,
		ProbUp:           h.ProbabilityUpsideContinuation,
		ProbRange:        h.ProbabilityRange,
		ProbDown:         h.ProbabilityDownsidePressure,
		MedianPct:        h.MedianReturnPct,
		HardBlocked:      h.HardBlocked,
		ReliabilityNote:  h.ReliabilityNote,
		N:                h.SampleSize,
	}, nil
}

// summarizeConsensus counts agreement across non-hard-blocked rows.
// Hard-blocked rows (for example Gold 180d) are excluded from both
// numerator and denominator — we don't let noise vote.
func summarizeConsensus(rows []JointRow, horizon int) JointResult {
	eligible := 0
	votes := map[string]int{
		"upside_continuation": 0,
		"range":               0,
		"downside_pressure":   0,
	}
	for _, r := range rows {
		if r.HardBlocked {
			continue
		}
		eligible++
		votes[r.DominantScenario]++
	}

	note := ""
	consensus := "mixed"
	agreement := 0.0
	if eligible == 0 {
		note = "所有资产 hard-blocked;无法形成共识"
	} else {
		// Plurality winner
		best := ""
		max := 0
		for k, v := range votes {
			if v > max {
				best = k
				max = v
			}
		}
		agreement = float64(max) / float64(eligible)
		// Only label as a consensus when ≥ 2/3 agree (threshold picked so
		// 3 assets with 2 agree qualifies but a 50/50 split does not).
		if agreement >= 2.0/3.0 {
			consensus = best
			note = fmt.Sprintf("%d/%d 资产共同指向 %s",
				max, eligible, scenarioCN(best))
		} else {
			note = fmt.Sprintf("分歧: %d/%d 资产最强倾向为 %s (共识阈值 2/3 未达到)",
				max, eligible, scenarioCN(best))
		}
	}
	return JointResult{
		Horizon:   horizon,
		Rows:      rows,
		Consensus: consensus,
		Agreement: round4(agreement),
		Note:      note,
	}
}

func scenarioCN(k string) string {
	switch k {
	case "upside_continuation":
		return "上行延续"
	case "range":
		return "区间震荡"
	case "downside_pressure":
		return "下行压力"
	}
	return k
}

func printJointHuman(r JointResult) {
	fmt.Printf("Joint forecast  horizon=%dd  (%d assets)\n\n", r.Horizon, len(r.Rows))
	fmt.Printf("%-8s %-14s %7s %8s %8s %10s %s\n",
		"ASSET", "DOMINANT", "UP%", "RANGE%", "DOWN%", "MEDIAN%", "NOTE")
	fmt.Printf("%-8s %-14s %7s %8s %8s %10s %s\n",
		"-----", "--------", "---", "------", "-----", "-------", "----")
	for _, row := range r.Rows {
		note := ""
		if row.HardBlocked {
			note = "hard-block"
		} else if row.ReliabilityNote != "" {
			note = "⚠ " + strings.Split(row.ReliabilityNote, "(")[0]
		}
		fmt.Printf("%-8s %-14s %6.0f%% %7.0f%% %7.0f%% %9.2f%% %s\n",
			strings.ToUpper(row.Asset), row.DominantLabel,
			row.ProbUp*100, row.ProbRange*100, row.ProbDown*100,
			row.MedianPct, note)
	}
	fmt.Println()
	if r.Consensus != "mixed" {
		fmt.Printf("  → 共识: %s  (%.0f%% 一致率)\n", scenarioCN(r.Consensus), r.Agreement*100)
	} else {
		fmt.Printf("  → 无共识  (最强倾向一致率 %.0f%%,阈值 67%%)\n", r.Agreement*100)
	}
	fmt.Printf("  %s\n", r.Note)
	fmt.Println()
	fmt.Println("  ⚠ 共识不等于正确;分歧不等于错误。共识告诉你 regime 宽度,不替代单资产结论。")
}
