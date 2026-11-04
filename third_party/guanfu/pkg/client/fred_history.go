// FRED daily series → PriceStore archive (incremental).
//
// FetchMacroData (in fred.go) returns a snapshot struct used by panels but
// does not persist series history. This file adds historical archive support
// for the daily series guanfu kNN extractors consume:
//
//   fred_dxy          DTWEXBGS    Trade-Weighted USD (daily)
//   fred_fed_funds    DFF         Effective federal funds rate (daily)
//   fred_dgs10        DGS10       10Y Treasury (daily)
//   fred_dfii10       DFII10      10Y TIPS / real yield (daily)
//   fred_yield_curve  T10Y2Y      10Y-2Y spread (daily)
//   fred_breakeven    T10YIE      10Y breakeven inflation (daily)
//   fred_hy_spread    BAMLH0A0HYM2  ICE BofA US HY OAS (daily)
//
// Requires FRED_API_KEY. Without it, FRED sources skip with a clear note
// rather than failing — keeps the unified refresh pipeline running.

package client

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
	"github.com/Ricaardo/nimbus-os/datasources/fred"
)

// FREDSource maps one FRED series ID → one PriceStore key.
type FREDSource struct {
	StoreKey    string // e.g. "fred_dxy"
	Series      string // e.g. "DTWEXBGS"
	Description string
	StartDate   string // YYYY-MM-DD floor for full pull (FRED returns from this date)
}

// DefaultFREDSources lists every fred_* series referenced by current
// extractors. Adding a new series here automatically wires it into refresh.
func DefaultFREDSources() []*FREDSource {
	return []*FREDSource{
		{StoreKey: "fred_dxy", Series: "DTWEXBGS", Description: "Trade-weighted USD (Broad)", StartDate: "2006-01-01"},
		{StoreKey: "fred_fed_funds", Series: "DFF", Description: "Effective federal funds rate", StartDate: "1981-01-01"},
		{StoreKey: "fred_dgs10", Series: "DGS10", Description: "10Y Treasury yield", StartDate: "1990-01-01"},
		{StoreKey: "fred_dgs3mo", Series: "DGS3MO", Description: "3M Treasury yield (risk-free baseline, F4)", StartDate: "1981-09-01"},
		{StoreKey: "fred_dfii10", Series: "DFII10", Description: "10Y TIPS / real yield", StartDate: "2003-01-01"},
		{StoreKey: "fred_yield_curve", Series: "T10Y2Y", Description: "10Y-2Y spread", StartDate: "1976-06-01"},
		{StoreKey: "fred_breakeven", Series: "T10YIE", Description: "10Y breakeven inflation", StartDate: "2003-01-01"},
		{StoreKey: "fred_hy_spread", Series: "BAMLH0A0HYM2", Description: "BofA US HY OAS", StartDate: "1996-12-31"},
		{StoreKey: "fred_tga", Series: "WTREGEN", Description: "Treasury General Account (F1)", StartDate: "2005-01-05"},
		{StoreKey: "fred_rrp", Series: "RRPONTSYD", Description: "Overnight Reverse Repo (F1)", StartDate: "2013-02-04"},
		{StoreKey: "fred_ecb_deposit_rate", Series: "ECBDFR", Description: "ECB deposit facility rate", StartDate: "1999-01-01"},
		{StoreKey: "fred_boj_call_rate", Series: "IRSTCI01JPM156N", Description: "Japan overnight call/interbank rate", StartDate: "1985-07-01"},
		{StoreKey: "fred_pboc_interbank_rate", Series: "IRSTCI01CNM156N", Description: "China overnight call/interbank rate", StartDate: "1990-01-01"},
	}
}

func (f *FREDSource) Key() string         { return f.StoreKey }
func (f *FREDSource) DisplayName() string { return f.StoreKey + " (" + f.Description + ")" }

// Refresh fetches the gap between PriceStore last_date and today from FRED.
// On full import, walks back to f.StartDate.
func (f *FREDSource) Refresh(ctx context.Context, s *store.PriceStore) (*RefreshResult, error) {
	stale, lastDate := staleThreshold(s, f.StoreKey)
	apiKey := strings.TrimSpace(os.Getenv("FRED_API_KEY"))
	if apiKey == "" {
		count, _ := s.Count(f.StoreKey)
		return &RefreshResult{
			Key: f.StoreKey, DisplayName: f.DisplayName(),
			Mode:       "skip",
			SkipReason: "config",
			Stale:      stale,
			Action:     "configure",
			Total:      count,
			LastDate:   lastDate,
			Error:      "FRED_API_KEY not set",
		}, nil
	}

	if !stale {
		return freshSkipResult(f.StoreKey, f.DisplayName(), lastDate, s), nil
	}

	// Decide window: lastDate+1 → today for incremental, StartDate → today for full.
	from := f.StartDate
	mode := "full"
	if lastDate != "" {
		t, err := time.Parse("2006-01-02", lastDate)
		if err == nil {
			from = t.AddDate(0, 0, 1).Format("2006-01-02")
			mode = "incremental"
		}
	}
	to := time.Now().UTC().Format("2006-01-02")

	points, err := fetchFREDSeriesRange(ctx, apiKey, f.Series, from, to)
	if err != nil {
		return nil, err
	}
	if len(points) == 0 {
		// no new observations between from-to (e.g. weekend/holiday on incremental)
		count, _ := s.Count(f.StoreKey)
		return &RefreshResult{
			Key: f.StoreKey, DisplayName: f.DisplayName(),
			Mode: mode, SkipReason: "no_new_data", Added: 0, Total: count, LastDate: lastDate,
		}, nil
	}

	// Tag source for traceability — preserved by Save/Append.
	src := "fred:" + f.Series
	for i := range points {
		points[i].Source = src
	}

	if mode == "full" {
		if err := s.Save(f.StoreKey, points); err != nil {
			return nil, err
		}
	} else {
		if err := s.Append(f.StoreKey, points); err != nil {
			return nil, err
		}
	}

	count, _ := s.Count(f.StoreKey)
	last, _ := s.LastDate(f.StoreKey)
	return &RefreshResult{
		Key: f.StoreKey, DisplayName: f.DisplayName(),
		Mode: mode, Added: len(points), Total: count, LastDate: last,
	}, nil
}

// fetchFREDSeriesRange pulls observations from from..to inclusive.
// FRED returns "." for missing values; those are filtered out.
func fetchFREDSeriesRange(ctx context.Context, apiKey, series, from, to string) ([]store.PricePoint, error) {
	obs, err := fred.Observations(ctx, apiKey, fred.Query{
		Series: series, Start: from, End: to, Limit: 100000,
	})
	if err != nil {
		return nil, err
	}
	out := make([]store.PricePoint, 0, len(obs))
	for _, o := range obs {
		if o.Value == "." || o.Value == "" {
			continue
		}
		v, err := strconv.ParseFloat(o.Value, 64)
		if err != nil {
			continue
		}
		out = append(out, store.PricePoint{
			Date:  o.Date,
			Close: v,
		})
	}
	return out, nil
}
