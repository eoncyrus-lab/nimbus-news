// Calibrate subcommand (K4).
//
// Reads the claim ledger, picks claims whose horizon has matured, looks
// up actual price at the target date via PriceStore, and computes:
//
//   - direction hit rate: sign(actual_return) == sign(expected_return)
//   - interval coverage: fraction of actuals inside [IntervalLow, IntervalHigh]
//     or conformal [low, high] when present
//   - median absolute error
//   - Brier-like score for ProbabilityUp (0=perfect, 1=terrible)
//
// Output is a per-(asset, horizon) table plus totals. Read-only — does
// not mutate the ledger, does not fetch network data.
//
// Usage:
//   guanfu calibrate
//   guanfu calibrate --asset btc --since 2026-01-01
//   guanfu calibrate --json

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/claim"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/client"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

const calibrateUsage = `usage: guanfu calibrate [--asset X] [--since YYYY-MM-DD] [--json]

  Reads claim ledger, scores matured forecasts against PriceStore.
  --asset  filter by asset key (default all)
  --since  earliest claim AsOf date to include
  --json   emit JSON instead of human table
`

// calibrationRow aggregates matured claims for one (asset, horizon) cell.
type calibrationRow struct {
	Asset        string  `json:"asset"`
	Horizon      int     `json:"horizon"`
	N            int     `json:"n"`
	DirHit       float64 `json:"dir_hit"`
	IntervalCov  float64 `json:"interval_coverage"`
	ConformalCov float64 `json:"conformal_coverage,omitempty"` // only when any claim had conformal bounds
	MedianAbsErr float64 `json:"median_abs_error_pct"`
	BrierUp      float64 `json:"brier_up"`
	TargetCov    float64 `json:"target_coverage"` // declared, for comparison
}

func runCalibrate(args []string) {
	flags := parseKV(args)
	assetFilter := strings.ToLower(flags["asset"])
	jsonOut := flags["json"] == "true"
	var sinceT time.Time
	if s := flags["since"]; s != "" {
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			fmt.Fprintf(os.Stderr, "calibrate: bad --since: %v\n", err)
			os.Exit(2)
		}
		sinceT = t
	}

	ledger, err := claim.Open("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "calibrate: open ledger: %v\n", err)
		os.Exit(1)
	}
	now := time.Now().UTC()
	claims, err := ledger.ListClaims(func(c claim.Claim) bool {
		if assetFilter != "" && c.Asset != assetFilter {
			return false
		}
		if !sinceT.IsZero() && c.AsOf.Before(sinceT) {
			return false
		}
		// Matured = AsOf + Horizon days <= today
		return !c.AsOf.AddDate(0, 0, c.Horizon).After(now)
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "calibrate: list: %v\n", err)
		os.Exit(1)
	}
	if len(claims) == 0 {
		fmt.Println("calibrate: no matured claims to score")
		return
	}

	ps := &store.PriceStore{}
	rows := scoreClaims(claims, ps)
	if len(rows) == 0 {
		fmt.Println("calibrate: matured claims exist but none resolvable (missing target-date prices)")
		return
	}
	if jsonOut {
		out, _ := json.MarshalIndent(rows, "", "  ")
		fmt.Println(string(out))
		return
	}
	printCalibrationTable(rows)
}

// scoreClaims groups matured claims by (asset, horizon) and computes
// the calibration metrics. Claims with no resolvable actual price
// are silently dropped.
func scoreClaims(claims []claim.Claim, ps *store.PriceStore) []calibrationRow {
	// Bucket by (asset, horizon) → list of (claim, actual_return_pct).
	type resolved struct {
		c      claim.Claim
		actual float64 // actual return as percent
	}
	buckets := map[string][]resolved{}
	order := []string{}

	for _, c := range claims {
		actual, ok := resolveActualReturn(c, ps)
		if !ok {
			continue
		}
		key := fmt.Sprintf("%s|%d", c.Asset, c.Horizon)
		if _, seen := buckets[key]; !seen {
			order = append(order, key)
		}
		buckets[key] = append(buckets[key], resolved{c: c, actual: actual})
	}

	rows := make([]calibrationRow, 0, len(buckets))
	for _, k := range order {
		parts := strings.SplitN(k, "|", 2)
		asset := parts[0]
		var horizon int
		fmt.Sscanf(parts[1], "%d", &horizon)
		b := buckets[k]
		n := len(b)
		if n == 0 {
			continue
		}

		var dirHit, intervalIn, conformalIn int
		var conformalN int
		errs := make([]float64, 0, n)
		var brier float64
		for _, r := range b {
			expectedDir := 1.0
			if r.c.ExpectedReturn < 0 {
				expectedDir = -1.0
			}
			if r.c.ExpectedReturn == 0 {
				expectedDir = 0
			}
			actualDir := 1.0
			if r.actual < 0 {
				actualDir = -1.0
			}
			if r.actual == 0 {
				actualDir = 0
			}
			if expectedDir == actualDir {
				dirHit++
			}
			// Interval coverage: actual in [low, high]? IntervalLow/High are decimals
			// per Claim schema; actual is percent — compare in same units.
			actualDec := r.actual / 100.0
			if actualDec >= r.c.IntervalLow && actualDec <= r.c.IntervalHigh {
				intervalIn++
			}
			// Conformal (if recorded on original claim — we currently don't
			// persist conformal bounds on the Claim struct; skip when absent).
			// Placeholder for future: add r.c.ConformalLow/High fields if we want
			// to measure conformal separately from empirical.
			if false {
				conformalN++
				conformalIn++
			}
			errs = append(errs, abs(actualDec-r.c.ExpectedReturn)*100)
			// Brier for probability_up against realized up/down.
			if r.c.ProbabilityUp > 0 {
				var y float64
				if r.actual > 0 {
					y = 1.0
				}
				diff := r.c.ProbabilityUp - y
				brier += diff * diff
			}
		}
		row := calibrationRow{
			Asset:        asset,
			Horizon:      horizon,
			N:            n,
			DirHit:       round4(float64(dirHit) / float64(n)),
			IntervalCov:  round4(float64(intervalIn) / float64(n)),
			MedianAbsErr: round2(medianOf(errs)),
			BrierUp:      round4(brier / float64(n)),
			TargetCov:    0.80, // empirical p10/p90 default target
		}
		if conformalN > 0 {
			row.ConformalCov = round4(float64(conformalIn) / float64(conformalN))
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Asset == rows[j].Asset {
			return rows[i].Horizon < rows[j].Horizon
		}
		return rows[i].Asset < rows[j].Asset
	})
	return rows
}

// resolveActualReturn looks up the price at the claim's target date and
// returns the actual return as percent. Returns !ok when the price
// store doesn't have data at or near the target.
func resolveActualReturn(c claim.Claim, ps *store.PriceStore) (float64, bool) {
	if c.PriceAtClaim <= 0 {
		return 0, false
	}
	target := c.AsOf.AddDate(0, 0, c.Horizon)
	key := c.Asset
	if !isCoreAsset(key) {
		// Assume stock_* namespace; pkg/client/StockKey applied at record time
		// gives the exact key on disk, so this is already correct.
	}
	points, err := ps.Load(key)
	if err != nil || len(points) == 0 {
		return 0, false
	}
	// Find closest point on or after target date. Many assets have
	// weekend gaps; tolerate up to +5 days.
	var price float64
	found := false
	targetStr := target.Format("2006-01-02")
	for _, p := range points {
		if p.Date >= targetStr {
			actualDate, err := time.Parse("2006-01-02", p.Date)
			if err != nil {
				continue
			}
			if actualDate.Sub(target) > 5*24*time.Hour {
				return 0, false
			}
			price = p.Close
			found = true
			break
		}
	}
	if !found {
		// Target date is in the future relative to store — claim not yet
		// matured from the price-data point of view.
		return 0, false
	}
	if price <= 0 {
		return 0, false
	}
	return (price - c.PriceAtClaim) / c.PriceAtClaim * 100, true
}

func isCoreAsset(k string) bool {
	return map[string]bool{"btc": true, "qqq": true, "spy": true, "gold": true}[k]
}

func printCalibrationTable(rows []calibrationRow) {
	fmt.Println("guanfu calibrate  (matured claims vs. PriceStore actuals)")
	fmt.Println()
	fmt.Printf("%-14s %6s %4s %8s %12s %10s %10s\n",
		"ASSET", "HRZN", "N", "DIR_HIT", "INTERVAL%", "MED_ABS_E", "BRIER_UP")
	fmt.Printf("%-14s %6s %4s %8s %12s %10s %10s\n",
		"-----", "----", "-", "-------", "---------", "---------", "--------")
	for _, r := range rows {
		intervalStr := fmt.Sprintf("%.0f%% (t=%.0f%%)", r.IntervalCov*100, r.TargetCov*100)
		fmt.Printf("%-14s %6d %4d %7.1f%% %12s %9.2f%% %10.4f\n",
			strings.ToUpper(r.Asset), r.Horizon, r.N,
			r.DirHit*100, intervalStr, r.MedianAbsErr, r.BrierUp)
	}
	fmt.Println()
	fmt.Println("  DIR_HIT     方向命中率 (sign(expected) == sign(actual))")
	fmt.Println("  INTERVAL%   实际落入声明 [p10,p90] 区间的比例 (target 80%)")
	fmt.Println("  MED_ABS_E   median(|expected - actual|) 百分点")
	fmt.Println("  BRIER_UP    P(up) vs 实际上行的 Brier score (越低越好,0.25 ≈ 随机)")
}

// medianOf returns the median of a float slice. Handles empty → 0.
func medianOf(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sorted := append([]float64(nil), xs...)
	sort.Float64s(sorted)
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}

func round4(v float64) float64 {
	return float64(int(v*10000+0.5)) / 10000
}

// ensure imports keep usage
var _ = client.StockKey
