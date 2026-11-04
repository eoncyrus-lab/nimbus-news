// Yahoo daily history → PriceStore (incremental, non-stock-namespaced).
//
// fetch_stock.go covers arbitrary tickers under "stock_<ticker>". This file
// covers ETFs / proxies / commodity futures stored under their core key
// (qqq, spy, gold, vixy, tlt, uup, gld). The fetch logic is shared
// (Yahoo /v8/finance/chart) but the storage convention is different:
//   - stocks: namespace-prefixed key, full-replace via Save
//   - core ETFs: bare key, Append (incremental) preferred over full-replace

package client

import (
	"context"
	"fmt"
	"strings"
	"time"

	dsyahoo "github.com/Ricaardo/nimbus-os/datasources/yahoo"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

// YahooETFSource refreshes one ETF / index from Yahoo into a bare PriceStore key.
type YahooETFSource struct {
	StoreKey string // e.g. "qqq" / "spy" / "vixy"
	Symbol   string // Yahoo symbol, e.g. "QQQ" / "VIXY" / "GC=F"
	FullDays int    // history window for the first full pull (~10y)
	Note     string // human description for status output
}

// DefaultYahooETFSources covers everything currently used in extractors
// or panels that hits Yahoo (QQQ/SPY price + cross-asset proxies).
//
// Gold is intentionally excluded — gold.go has DBnomics+Yahoo merge logic
// that handles 1968+ history; YahooETFSource would lose the DBnomics window.
func DefaultYahooETFSources() []*YahooETFSource {
	return []*YahooETFSource{
		{StoreKey: "qqq", Symbol: "QQQ", FullDays: 4000, Note: "Nasdaq-100 ETF"},
		{StoreKey: "spy", Symbol: "SPY", FullDays: 4000, Note: "S&P 500 ETF"},
		{StoreKey: "vixy", Symbol: "^VIX", FullDays: 4000, Note: "VIX index"},
		{StoreKey: "tlt", Symbol: "TLT", FullDays: 4000, Note: "20Y+ Treasury ETF"},
		{StoreKey: "uup", Symbol: "UUP", FullDays: 4000, Note: "USD bullish ETF"},
		{StoreKey: "usd_cny", Symbol: "CNY=X", FullDays: 9000, Note: "USD/CNY spot rate"},
	}
}

func (y *YahooETFSource) Key() string         { return y.StoreKey }
func (y *YahooETFSource) DisplayName() string { return y.StoreKey + " (" + y.Note + ")" }

// Refresh fetches the gap between PriceStore last_date and today.
// Strategy:
//   - empty / no data → full pull (FullDays)
//   - stale → fetch (daysSince + 5) buffer days, Append (Save dedups)
//   - fresh → skip
func (y *YahooETFSource) Refresh(ctx context.Context, s *store.PriceStore) (*RefreshResult, error) {
	stale, lastDate := staleThreshold(s, y.StoreKey)
	if !stale {
		return freshSkipResult(y.StoreKey, y.DisplayName(), lastDate, s), nil
	}

	// Decide window
	days := y.FullDays
	mode := "full"
	if lastDate != "" {
		t, err := time.Parse("2006-01-02", lastDate)
		if err == nil {
			gap := int(time.Since(t).Hours()/24) + 5 // +5d buffer
			if gap < y.FullDays {
				days = gap
				mode = "incremental"
			}
		}
	}

	points, err := fetchYahooSymbolDaily(ctx, y.Symbol, days)
	if err != nil {
		return nil, err
	}
	if len(points) == 0 {
		return nil, fmt.Errorf("yahoo %s: empty response", y.Symbol)
	}
	src := "yahoo:" + strings.ToUpper(y.Symbol)
	for i := range points {
		points[i].Source = src
	}

	if mode == "full" {
		if err := s.Save(y.StoreKey, points); err != nil {
			return nil, err
		}
	} else {
		if err := s.Append(y.StoreKey, points); err != nil {
			return nil, err
		}
	}

	count, _ := s.Count(y.StoreKey)
	last, _ := s.LastDate(y.StoreKey)
	return &RefreshResult{
		Key: y.StoreKey, DisplayName: y.DisplayName(),
		Mode: mode, Added: len(points), Total: count, LastDate: last,
	}, nil
}

// fetchYahooSymbolDaily fetches daily history via the shared datasources/yahoo
// package and emits store.PricePoint (oldest-first) ready to Save.
func fetchYahooSymbolDaily(ctx context.Context, symbol string, days int) ([]store.PricePoint, error) {
	if days <= 0 {
		days = 4000
	}
	bars, err := dsyahoo.GetHistoryDays(ctx, symbol, days)
	if err != nil {
		return nil, err
	}
	return yahooBarsToPricePoints(bars, "yahoo:"+strings.ToUpper(symbol)), nil
}

// yahooBarsToPricePoints maps datasources/yahoo bars (ascending) to PricePoints,
// skipping non-positive closes (matching the former chart-parse behaviour).
func yahooBarsToPricePoints(bars []dsyahoo.Bar, source string) []store.PricePoint {
	pts := make([]store.PricePoint, 0, len(bars))
	for _, b := range bars {
		if b.Close <= 0 {
			continue
		}
		pts = append(pts, store.PricePoint{Date: b.Date, Close: b.Close, Source: source})
	}
	return pts
}
