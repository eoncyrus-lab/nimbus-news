// Stress subcommand (H4).
//
// Purpose: answer "what if <macro indicator> shifts by X?" using
// historical analogs — find past dates where that indicator was
// roughly at the hypothetical value, report how the asset actually
// behaved afterward.
//
// Why this is stress analysis, not just kNN re-scoring:
//   kNN rebuild with a shifted feature vector would force us to
//   reconstruct the full extractor chain + handle feature overrides;
//   too heavy and, more importantly, too coupled to whatever weights
//   the kNN bundle had when it was tuned. Instead we run a simple
//   retrospective: "In history, when real_yield was where you're
//   asking about, what happened next?" Fewer moving parts, easier
//   for the user to reason about.
//
// Usage:
//
//	guanfu stress --series fred_dfii10 --shift +1.5 --asset btc
//	guanfu stress --series fred_dxy --target-pct 102 --asset qqq --horizon 90
//	guanfu stress --series fred_hy_spread --shift +1.0 --asset spy --horizon 180
//
// --shift and --target-pct are mutually exclusive; --shift is
// additive (current + shift), --target-pct pins to an absolute value.

package main

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

const stressUsage = `usage: guanfu stress --series <key> [--shift X | --target-pct X] --asset <btc|qqq|spy|gold> [--horizon N] [--tolerance X]

  --series       PriceStore key of the macro input to perturb, e.g.
                 fred_dfii10 (real yield), fred_dxy, fred_hy_spread,
                 fred_tga, fred_rrp, etc.
  --shift        Hypothetical absolute shift from the current series value
                 (e.g. +1.5 for a 150bp increase in real yield).
  --target-pct   Hypothetical absolute value of the series (mutually
                 exclusive with --shift).
  --asset        Asset to look up historical outcomes for.
  --horizon      Forward-return window in days (default 90).
  --tolerance    Match tolerance around the hypothetical value
                 (default 0.20 for percent-like series).
`

func runStress(args []string) {
	flags := parseKV(args)
	series := strings.TrimSpace(flags["series"])
	asset := strings.ToLower(strings.TrimSpace(flags["asset"]))
	horizonStr := strings.TrimSpace(flags["horizon"])
	shiftStr := strings.TrimSpace(flags["shift"])
	targetStr := strings.TrimSpace(flags["target-pct"])
	tolStr := strings.TrimSpace(flags["tolerance"])

	if series == "" || asset == "" {
		fmt.Fprint(os.Stderr, stressUsage)
		os.Exit(2)
	}
	if shiftStr == "" && targetStr == "" {
		fmt.Fprintln(os.Stderr, "stress: need --shift or --target-pct")
		os.Exit(2)
	}
	if shiftStr != "" && targetStr != "" {
		fmt.Fprintln(os.Stderr, "stress: --shift and --target-pct are mutually exclusive")
		os.Exit(2)
	}

	horizon := 90
	if horizonStr != "" {
		if n, err := strconv.Atoi(horizonStr); err == nil && n > 0 {
			horizon = n
		}
	}
	tolerance := 0.20
	if tolStr != "" {
		if v, err := strconv.ParseFloat(tolStr, 64); err == nil && v > 0 {
			tolerance = v
		}
	}

	ps := &store.PriceStore{}
	seriesPts, err := ps.Load(series)
	if err != nil || len(seriesPts) < 100 {
		fmt.Fprintf(os.Stderr, "stress: need ≥100 points for series %q; run `guanfu refresh --only=%s` first\n",
			series, series)
		os.Exit(1)
	}
	current := seriesPts[len(seriesPts)-1].Close
	var target float64
	if shiftStr != "" {
		shift, err := strconv.ParseFloat(shiftStr, 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "stress: bad --shift %q: %v\n", shiftStr, err)
			os.Exit(2)
		}
		target = current + shift
	} else {
		v, err := strconv.ParseFloat(targetStr, 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "stress: bad --target-pct %q: %v\n", targetStr, err)
			os.Exit(2)
		}
		target = v
	}

	assetPts, err := ps.Load(asset)
	if err != nil || len(assetPts) < horizon+30 {
		fmt.Fprintf(os.Stderr, "stress: asset %q needs ≥%d price points; run `guanfu refresh` or `guanfu import-stock` first\n",
			asset, horizon+30)
		os.Exit(1)
	}

	matches := findSeriesMatches(seriesPts, target, tolerance)
	if len(matches) == 0 {
		fmt.Printf("stress: no historical dates matched %s ≈ %.2f (tolerance ±%.2f)\n",
			series, target, tolerance)
		fmt.Println("       try a wider --tolerance or a different target value")
		return
	}

	returns := applyAssetLookup(assetPts, matches, horizon)
	if len(returns) == 0 {
		fmt.Println("stress: matched dates exist but none had enough forward price data")
		return
	}

	fmt.Printf("Stress: IF %s ≈ %.2f  (current %.2f, shift %+.2f, tolerance ±%.2f)\n",
		series, target, current, target-current, tolerance)
	fmt.Printf("   → historical %s outcomes over +%dd from matching dates\n\n",
		strings.ToUpper(asset), horizon)

	sort.Float64s(returns)
	summary := summarizeStressReturns(returns)
	fmt.Printf("  matched dates    : %d\n", len(returns))
	fmt.Printf("  median return    : %+.2f%%\n", summary.Median*100)
	fmt.Printf("  mean             : %+.2f%%\n", summary.Mean*100)
	fmt.Printf("  p10 / p90        : %+.2f%% / %+.2f%%\n", summary.P10*100, summary.P90*100)
	fmt.Printf("  probability > 0  : %.0f%%\n", summary.ProbUp*100)
	fmt.Println()

	// Show up to 5 concrete matches so the user can sanity-check.
	n := len(matches)
	if n > 5 {
		n = 5
	}
	fmt.Println("  sample matches (date → series value → asset forward return):")
	for i := 0; i < n; i++ {
		m := matches[i]
		r, ok := forwardReturn(assetPts, m.Date, horizon)
		if !ok {
			continue
		}
		fmt.Printf("    %s   %6.2f   %+7.2f%%\n", m.Date, m.Value, r*100)
	}
	fmt.Println()
	fmt.Println("  ⚠ 历史 ≠ 未来。匹配样本的宏观上下文可能与今天显著不同")
	fmt.Println("    (如 regime / 政策框架 / 市场结构)。本输出是起点,不是结论。")
}

type seriesMatch struct {
	Date  string
	Value float64
}

func findSeriesMatches(pts []store.PricePoint, target, tolerance float64) []seriesMatch {
	out := make([]seriesMatch, 0, 16)
	for _, p := range pts {
		if math.Abs(p.Close-target) <= tolerance {
			out = append(out, seriesMatch{Date: p.Date, Value: p.Close})
		}
	}
	return out
}

// applyAssetLookup: for each matched date, look up asset's forward
// return over `horizon` days. Silently drops matches where the asset
// doesn't have enough forward data (recent matches at the tail).
func applyAssetLookup(assetPts []store.PricePoint, matches []seriesMatch, horizon int) []float64 {
	byDate := make(map[string]int, len(assetPts))
	for i, p := range assetPts {
		byDate[p.Date] = i
	}
	returns := make([]float64, 0, len(matches))
	for _, m := range matches {
		// asset doesn't trade every day (weekends, holidays) — find
		// nearest date on or after m.Date.
		idx := findIndexOnOrAfter(assetPts, m.Date)
		if idx < 0 || idx+horizon >= len(assetPts) {
			continue
		}
		start := assetPts[idx].Close
		end := assetPts[idx+horizon].Close
		if start <= 0 || end <= 0 {
			continue
		}
		returns = append(returns, end/start-1)
	}
	return returns
}

func forwardReturn(assetPts []store.PricePoint, date string, horizon int) (float64, bool) {
	idx := findIndexOnOrAfter(assetPts, date)
	if idx < 0 || idx+horizon >= len(assetPts) {
		return 0, false
	}
	start := assetPts[idx].Close
	end := assetPts[idx+horizon].Close
	if start <= 0 || end <= 0 {
		return 0, false
	}
	return end/start - 1, true
}

// findIndexOnOrAfter returns the index of the first PricePoint whose
// Date is >= target (string comparison works because dates are
// YYYY-MM-DD). Returns -1 if none.
func findIndexOnOrAfter(pts []store.PricePoint, target string) int {
	// PricePoints are stored oldest-first after Load. Linear scan is
	// fine: typical lengths are a few thousand and we call this once
	// per match; a binary search micro-optimization isn't worth it.
	for i, p := range pts {
		if p.Date >= target {
			return i
		}
	}
	return -1
}

type stressSummary struct {
	Mean, Median, P10, P90, ProbUp float64
}

func summarizeStressReturns(sorted []float64) stressSummary {
	n := len(sorted)
	if n == 0 {
		return stressSummary{}
	}
	sum := 0.0
	up := 0
	for _, r := range sorted {
		sum += r
		if r > 0 {
			up++
		}
	}
	q := func(p float64) float64 {
		pos := p * float64(n-1)
		lo := int(math.Floor(pos))
		hi := int(math.Ceil(pos))
		if lo == hi {
			return sorted[lo]
		}
		frac := pos - float64(lo)
		return sorted[lo]*(1-frac) + sorted[hi]*frac
	}
	return stressSummary{
		Mean:   sum / float64(n),
		Median: q(0.50),
		P10:    q(0.10),
		P90:    q(0.90),
		ProbUp: float64(up) / float64(n),
	}
}

// Avoid unused-import warnings when the surrounding file's build flags
// change; this is a cheap guard that future refactoring doesn't accidentally
// regress the time import.
var _ = time.Now
