// Unified data refresh framework.
//
// Each Source represents a PriceStore key (or related group) and knows how
// to do a full historical pull on first run, plus an incremental update on
// subsequent runs. RefreshAll runs sources in dependency order and reports
// which were skipped (already fresh), updated, or failed.
//
// The contract for a Source.Refresh:
//   1. Inspect PriceStore for last_date.
//   2. If fresh (≤1d ago), return Mode="skip".
//   3. If empty / very old, do full historical pull and Save.
//   4. If gap exists, fetch only the gap and Append.
//   5. Return RefreshResult with mode, count added, last_date, error.

package client

import (
	"context"
	"fmt"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

// Source describes a single dataset that can be refreshed.
type Source interface {
	Key() string
	DisplayName() string
	Refresh(ctx context.Context, s *store.PriceStore) (*RefreshResult, error)
}

// RefreshResult summarizes what happened for one source.
type RefreshResult struct {
	Key         string `json:"key"`
	DisplayName string `json:"display_name"`
	Mode        string `json:"mode"`                  // "skip" | "full" | "incremental" | "fail"
	SkipReason  string `json:"skip_reason,omitempty"` // fresh | config | no_new_data | not_applicable
	Stale       bool   `json:"stale,omitempty"`
	Action      string `json:"action,omitempty"` // ignore | configure | refresh | investigate
	Impact      string `json:"impact,omitempty"` // forecast | market_reading | both | optional
	Added       int    `json:"added"`
	Total       int    `json:"total"`
	LastDate    string `json:"last_date"`
	Duration    string `json:"duration"`
	Error       string `json:"error,omitempty"`
}

// RefreshAll runs every source sequentially and returns per-source results.
// Sequential is intentional — most sources hit external APIs with their own
// rate limits, and serial output is easier to debug than interleaved.
// A failing source does not stop the loop; its error is captured in the
// result and the next source runs.
func RefreshAll(ctx context.Context, s *store.PriceStore, sources []Source) []*RefreshResult {
	out := make([]*RefreshResult, 0, len(sources))
	for _, src := range sources {
		start := time.Now()
		r, err := src.Refresh(ctx, s)
		if r == nil {
			r = &RefreshResult{Key: src.Key(), DisplayName: src.DisplayName()}
		}
		if err != nil {
			r.Mode = "fail"
			r.Error = err.Error()
		}
		enrichRefreshResult(r)
		r.Duration = time.Since(start).Round(time.Millisecond).String()
		out = append(out, r)
	}
	return out
}

// staleThreshold returns true if the asset's last_date is older than 24h.
// "Fresh" means we skip the network call entirely.
func staleThreshold(s *store.PriceStore, key string) (stale bool, lastDate string) {
	last, err := s.LastDate(key)
	if err != nil || last == "" {
		return true, ""
	}
	t, err := time.Parse("2006-01-02", last)
	if err != nil {
		return true, last
	}
	return time.Since(t) > 24*time.Hour, last
}

// freshSkipResult builds a no-op result for a source whose data is recent.
func freshSkipResult(key, name, lastDate string, s *store.PriceStore) *RefreshResult {
	count, _ := s.Count(key)
	return &RefreshResult{
		Key:         key,
		DisplayName: name,
		Mode:        "skip",
		SkipReason:  "fresh",
		Action:      "ignore",
		Total:       count,
		LastDate:    lastDate,
	}
}

// FormatRefreshTable renders results as a human-readable status table.
func FormatRefreshTable(results []*RefreshResult) string {
	if len(results) == 0 {
		return "no refresh sources configured"
	}
	out := fmt.Sprintf("%-22s %-14s %-11s %-11s %8s %12s %10s\n", "DATASET", "MODE", "REASON", "ACTION", "ADDED", "TOTAL", "LAST_DATE")
	out += fmt.Sprintf("%-22s %-14s %-11s %-11s %8s %12s %10s\n", "-------", "----", "------", "------", "-----", "-----", "---------")
	for _, r := range results {
		mode := r.Mode
		if r.Error != "" {
			if r.Mode == "skip" {
				mode = "SKIP"
			} else {
				mode = "FAIL"
			}
		}
		reason := r.SkipReason
		if reason == "" && r.Error != "" {
			reason = truncate(r.Error, 11)
		}
		action := r.Action
		if action == "" {
			action = refreshAction(r)
		}
		last := r.LastDate
		if r.Stale && last != "" {
			last += "*"
		}
		out += fmt.Sprintf("%-22s %-14s %-11s %-11s %8d %12d %10s\n",
			truncate(r.Key, 22), truncate(mode, 14), truncate(reason, 11), truncate(action, 11), r.Added, r.Total, last)
		if r.Error != "" {
			out += fmt.Sprintf("  %-22s %s\n", "", truncate(r.Error, 96))
		}
	}
	out += "\n* last_date is stale for its source cadence\n"
	return out
}

func enrichRefreshResult(r *RefreshResult) {
	if r == nil {
		return
	}
	if r.Impact == "" {
		r.Impact = refreshImpact(r.Key)
	}
	if r.Action == "" {
		r.Action = refreshAction(r)
	}
}

func refreshAction(r *RefreshResult) string {
	if r == nil {
		return ""
	}
	switch {
	case r.Mode == "fail":
		return "investigate"
	case r.Mode == "skip" && r.SkipReason == "config":
		return "configure"
	case r.Mode == "skip" && r.SkipReason == "fresh":
		return "ignore"
	case r.Stale:
		return "refresh"
	case r.Mode == "full" || r.Mode == "incremental":
		return "ignore"
	default:
		return "investigate"
	}
}

func refreshImpact(key string) string {
	switch key {
	case "btc", "qqq", "spy", "gold", "spx_cape", "fred_dxy", "fred_dgs10",
		"fred_dfii10", "fred_yield_curve", "fred_breakeven", "fred_hy_spread",
		"fred_dgs3mo", "vixy", "uup", "tlt", "stooq_putcall":
		return "forecast"
	case "usd_cny", "fred_fed_funds", "fred_ecb_deposit_rate",
		"fred_boj_call_rate", "fred_pboc_interbank_rate", "cmc_market_context",
		"deribit_options", "deribit_dvol", "deribit_skew_25d_pct", "deribit_skew_expiry_days":
		return "market_reading"
	case "defillama_stablecoin_supply", "coinbase_btc", "stock_*":
		return "optional"
	default:
		return "optional"
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n < 4 {
		return s[:n]
	}
	return s[:n-1] + "…"
}
