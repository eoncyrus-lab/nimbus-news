// `guanfu refresh` — unified data import.
//
// Pulls full historical data on first run for every configured Source and
// does incremental updates on subsequent runs. Reports a per-source status
// table at the end so you can see which datasets were skipped (fresh),
// updated (incremental/full), or failed.
//
// Usage:
//   guanfu refresh                # all sources
//   guanfu refresh --only btc,fred_dxy
//   guanfu refresh --skip cape    # everything except cape
//   guanfu refresh --dry-run      # list what would run, don't fetch

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/client"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

// allRefreshSources returns the canonical list in dependency order.
// BTC is first because it tends to be the longest fetch; macros come
// after equities so a panel can render even if a macro source fails.
func allRefreshSources() []client.Source {
	srcs := []client.Source{
		client.BTCSource{},
		client.GoldSource{},
	}
	for _, y := range client.DefaultYahooETFSources() {
		srcs = append(srcs, y)
	}
	for _, f := range client.DefaultFREDSources() {
		srcs = append(srcs, f)
	}
	srcs = append(srcs, client.CAPESource{})
	srcs = append(srcs, client.DefillamaStablecoinSource{})
	srcs = append(srcs, client.CMCMarketContextSource{})
	srcs = append(srcs, client.DeribitOptionsSource{})
	srcs = append(srcs, client.CBOEPutCallSource{})
	srcs = append(srcs, client.CoinbaseBTCSource{})
	srcs = append(srcs, client.StockKeysSource{})
	return srcs
}

func runRefresh(only, skip string, dryRun, jsonOut, pretty bool, timeout time.Duration) {
	all := allRefreshSources()

	// Apply --only / --skip filters by Key().
	var sources []client.Source
	wantedSet := splitCSV(only)
	skipSet := splitCSV(skip)
	for _, s := range all {
		k := s.Key()
		if len(wantedSet) > 0 && !wantedSet[k] {
			continue
		}
		if skipSet[k] {
			continue
		}
		sources = append(sources, s)
	}

	if dryRun {
		fmt.Println("Would refresh:")
		for _, s := range sources {
			fmt.Printf("  %-22s  %s\n", s.Key(), s.DisplayName())
		}
		return
	}

	if len(sources) == 0 {
		fmt.Fprintln(os.Stderr, "no sources match filter")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	st := &store.PriceStore{}
	startedAt := time.Now()
	results := client.RefreshAll(ctx, st, sources)
	totalDuration := time.Since(startedAt).Round(time.Millisecond)

	if jsonOut || pretty {
		out := map[string]any{
			"started_at":     startedAt.UTC().Format(time.RFC3339),
			"total_duration": totalDuration.String(),
			"sources":        results,
		}
		var b []byte
		if pretty {
			b, _ = json.MarshalIndent(out, "", "  ")
		} else {
			b, _ = json.Marshal(out)
		}
		fmt.Println(string(b))
		return
	}

	// Human table
	fmt.Print(client.FormatRefreshTable(results))
	fmt.Println()
	fmt.Printf("done in %s — %d sources processed\n", totalDuration, len(results))

	// Surface failures explicitly so they're not buried in the table.
	var failed []string
	for _, r := range results {
		if r.Error != "" && r.Mode != "skip" {
			failed = append(failed, r.Key)
		}
	}
	if len(failed) > 0 {
		fmt.Fprintf(os.Stderr, "\nfailed: %s\n", strings.Join(failed, ", "))
		os.Exit(1)
	}
}

func splitCSV(s string) map[string]bool {
	out := map[string]bool{}
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out[p] = true
		}
	}
	return out
}
