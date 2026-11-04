// Watch and digest subcommands (L4 / L6).
//
// Watch is a **one-shot** evaluator — given an asset + expression, fetch
// current panel, look up the metric, evaluate the condition, record an
// alert if it fires. Intended to be scheduled via cron/launchd; guanfu
// does not run a background daemon.
//
//	guanfu watch btc --when 'mayer_multiple < 0.8'
//	guanfu watch btc --when 'mayer < 0.8' --dispatch osascript
//	guanfu watch qqq --when 'rsi_14 > 75' --quiet
//
// Digest is a **passive** summary — no fetches; reads the claim ledger,
// alerts store, and PriceStore to print a 10-line daily brief.
//
//	guanfu digest
//	guanfu digest --since 2026-05-01

package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/alerts"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/calendar"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/claim"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/client"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/engine"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

const watchUsage = `usage: guanfu watch <asset> --when '<expr>' [--dispatch osascript|stdout] [--quiet]

  asset     btc / qqq / spy / gold / stock_<ticker>
  --when    e.g. 'mayer_multiple < 0.8' / 'rsi_14 > 75' / 'ahr999_compressed > 3.344'
  --dispatch  notification channel (default stdout; osascript requires macOS)
  --quiet   suppress stdout when condition NOT met (for cron use)
`

func runWatch(args []string) {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, watchUsage)
		os.Exit(2)
	}
	asset := strings.ToLower(args[0])
	flags := parseKV(args[1:])
	expr := flags["when"]
	channel := flags["dispatch"]
	quiet := flags["quiet"] == "true"
	if expr == "" {
		fmt.Fprint(os.Stderr, "watch: --when required\n"+watchUsage)
		os.Exit(2)
	}

	cond, err := alerts.Parse(expr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "watch: parse --when: %v\n", err)
		os.Exit(2)
	}

	observed, ok := fetchMetricValue(asset, cond.Metric)
	if !ok {
		fmt.Fprintf(os.Stderr, "watch: metric %q not available on %s panel\n", cond.Metric, asset)
		os.Exit(2)
	}

	if !cond.Evaluate(observed) {
		if !quiet {
			fmt.Printf("✓ %s: %s %s %g  (observed %g)  — not triggered\n",
				strings.ToUpper(asset), cond.Metric, cond.Operator, cond.Threshold, observed)
		}
		return
	}

	// Triggered — record + dispatch.
	store, err := alerts.Open("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "watch: open alerts store: %v\n", err)
		os.Exit(1)
	}
	a := alerts.Alert{
		Asset:         asset,
		Expression:    expr,
		Metric:        cond.Metric,
		Operator:      cond.Operator,
		Threshold:     cond.Threshold,
		ObservedValue: observed,
	}
	dispatchedCh, derr := alerts.Dispatch(a, channel)
	if derr != nil {
		fmt.Fprintf(os.Stderr, "watch: dispatch %q: %v\n", channel, derr)
	}
	if dispatchedCh != "" {
		a.Dispatched = []string{dispatchedCh}
	}
	path, rerr := store.Record(a)
	if rerr != nil {
		fmt.Fprintf(os.Stderr, "watch: record alert: %v\n", rerr)
		os.Exit(1)
	}
	fmt.Printf("✓ recorded: %s\n", path)
}

// fetchMetricValue pulls a single indicator value out of a fresh asset
// panel. Returns (value, true) on success; (0, false) when the metric
// isn't found. We rebuild the panel rather than caching — watch is
// meant for cron (minutes apart), so the rebuild cost is acceptable
// and guarantees fresh numbers.
func fetchMetricValue(asset, metric string) (float64, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var panel *model.IndicatorPanel
	switch asset {
	case "btc", "":
		cfg := defaultBTCConfig()
		panel = buildBTCPanelOrExit(ctx, cfg, "")
	case "qqq", "spy", "gold":
		a, err := engine.GetAsset(asset)
		if err != nil {
			return 0, false
		}
		snap, err := a.FetchSnapshot(ctx)
		if err != nil {
			return 0, false
		}
		panel, err = a.BuildPanel(snap)
		if err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return lookupMetric(panel, metric)
}

// lookupMetric searches every domain map on the panel for a case-
// insensitive key match. This lets --when take either the short
// "mayer_multiple" or any qualified form the user remembers.
func lookupMetric(panel *model.IndicatorPanel, metric string) (float64, bool) {
	if panel == nil {
		return 0, false
	}
	k := strings.ToLower(metric)
	domains := []map[string]model.Indicator{
		panel.Cycle, panel.Valuation, panel.Network, panel.Positioning,
		panel.Macro, panel.Flow, panel.Technical, panel.CrossAsset,
	}
	for _, dom := range domains {
		for name, ind := range dom {
			if strings.ToLower(name) == k {
				return ind.Value, true
			}
		}
	}
	return 0, false
}

// Small helpers for BTC watch path. We don't want to duplicate
// runBTCPanel's exit-on-error behavior; keep the shared construction
// in one place.
func defaultBTCConfig() *model.Config {
	return &model.Config{
		Weights: model.Weights{Trend: 0.30, Reversal: 0.25, Valuation: 0.25, Structure: 0.20},
		Thresholds: model.Thresholds{
			BTCMAFast: 120, BTCMASlow: 200, TopCoinCount: 50, AHRHalfLifeDays: 0,
		},
		API: model.APIConfig{Timeout: "10s", Retries: 3, Mock: false},
	}
}

func buildBTCPanelOrExit(ctx context.Context, cfg *model.Config, _ string) *model.IndicatorPanel {
	provider := client.NewRealClient()
	snap, err := provider.GetSnapshot(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "watch: fetch BTC snapshot: %v\n", err)
		os.Exit(1)
	}
	calc := engine.NewCalculator(cfg).WithPriceStore(&store.PriceStore{})
	return calc.BuildPanel(snap)
}

// -------------------- digest --------------------

const digestUsage = `usage: guanfu digest [--since YYYY-MM-DD]

Prints a summary from LOCAL data only (no network fetch):
  - alerts fired in the window
  - claims recorded in the window (forecasts the tool made)
  - latest-available price + 7d change per core asset

Intended as a morning glance, ~30 seconds of reading.
`

func runDigest(args []string) {
	flags := parseKV(args)
	since := time.Now().AddDate(0, 0, -7)
	if s := flags["since"]; s != "" {
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			fmt.Fprintf(os.Stderr, "digest: bad --since: %v\n", err)
			os.Exit(2)
		}
		since = t
	}

	fmt.Printf("guanfu digest  since %s\n\n", since.Format("2006-01-02"))

	// Alerts.
	if astore, err := alerts.Open(""); err == nil {
		list, _ := astore.List(since)
		if len(list) == 0 {
			fmt.Println("Alerts:  none fired in window")
		} else {
			fmt.Printf("Alerts:  %d fired\n", len(list))
			for _, a := range list {
				fmt.Printf("  %s  %s  %s %s %g  (observed %g)\n",
					a.Triggered.Format("2006-01-02 15:04"),
					strings.ToUpper(a.Asset), a.Metric, a.Operator, a.Threshold, a.ObservedValue)
			}
		}
	}
	fmt.Println()

	// Claims count.
	if ledger, err := claim.Open(""); err == nil {
		list, _ := ledger.ListClaims(func(c claim.Claim) bool {
			return !c.AsOf.Before(since)
		})
		if len(list) > 0 {
			byAsset := map[string]int{}
			for _, c := range list {
				byAsset[c.Asset]++
			}
			fmt.Printf("Claims:  %d forecasts recorded across %d assets\n", len(list), len(byAsset))
			for k, n := range byAsset {
				fmt.Printf("  %s  %d\n", strings.ToUpper(k), n)
			}
		} else {
			fmt.Println("Claims:  none recorded in window")
		}
	}
	fmt.Println()

	// Price diffs.
	fmt.Println("Prices (latest vs ~7d ago):")
	ps := &store.PriceStore{}
	for _, k := range []string{"btc", "qqq", "spy", "gold"} {
		p7, p0, ok := pricePair(ps, k, 7)
		if !ok {
			continue
		}
		change := (p0 - p7) / p7 * 100
		fmt.Printf("  %-6s  $%10.2f   (7d: %+.2f%%)\n", strings.ToUpper(k), p0, change)
	}
	fmt.Println()

	// Upcoming events (F7). 14-day look-ahead is enough for FOMC /
	// next-month CPI; longer confuses the "near-term heads-up" framing.
	events := calendar.Upcoming(time.Now().UTC(), 14)
	if len(events) == 0 {
		fmt.Println("Events:  none in next 14d")
		return
	}
	fmt.Println("Events (next 14d):")
	for _, e := range events {
		days := int(e.Date.Sub(time.Now().UTC()).Hours() / 24)
		fmt.Printf("  %s  (+%dd)  %s\n", e.Date.Format("2006-01-02"), days, e.Name)
	}
}

// pricePair returns (price_n_days_ago, latest_price, ok).
func pricePair(ps *store.PriceStore, asset string, daysAgo int) (float64, float64, bool) {
	points, err := ps.Load(asset)
	if err != nil || len(points) < daysAgo+1 {
		return 0, 0, false
	}
	return points[len(points)-1-daysAgo].Close, points[len(points)-1].Close, true
}
