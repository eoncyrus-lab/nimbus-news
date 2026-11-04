// Yahoo Finance auto-fetch for arbitrary US stock tickers.
//
// FetchAndCacheStock pulls daily closes from Yahoo's chart API and stores
// them in PriceStore under the namespaced key "stock_<ticker>" so they
// don't collide with core asset keys (btc/qqq/spy/gold/...) or
// feature data keys (fred_dxy/vixy/...).
//
// TTL: if cached data exists and the latest point is within 30h
// (covers weekend gaps), the call returns cached points without
// hitting Yahoo. Otherwise it does a full-window fetch and replaces
// the file. Yahoo gives ~max 10y daily for free, and storage is
// cheap, so full-replace is simpler than incremental and safe.

package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	dsyahoo "github.com/Ricaardo/nimbus-os/datasources/yahoo"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

// StockNamespacePrefix is prepended to lowercase ticker to form the
// PriceStore key. Exposed for tests and consumers that need to read
// stock data directly.
const StockNamespacePrefix = "stock_"

// stockTTL is the freshness window: if the most recent cached point
// is within this duration, FetchAndCacheStock skips the network call.
// 30h covers weekend gaps where Friday's close is the latest available
// until Monday afternoon UTC.
const stockTTL = 30 * time.Hour

// StockKey returns the PriceStore key for a given ticker.
func StockKey(ticker string) string {
	return StockNamespacePrefix + strings.ToLower(strings.TrimSpace(ticker))
}

// ValidateStockTicker rejects empty tickers and tickers that collide
// with any existing PriceStore key (case-insensitive). Existing keys
// include core asset prices (btc/qqq/...) and feature data
// (fred_dxy/vixy/...) — both must be protected from
// accidental overwrite via import-stock.
func ValidateStockTicker(s *store.PriceStore, ticker string) error {
	t := strings.ToLower(strings.TrimSpace(ticker))
	if t == "" {
		return errors.New("ticker is empty")
	}
	if strings.ContainsAny(t, "/\\ \t") {
		return fmt.Errorf("ticker %q contains invalid characters", ticker)
	}
	existing, _ := s.ListAssets()
	for _, k := range existing {
		if strings.EqualFold(k, t) {
			return fmt.Errorf("ticker %q conflicts with existing PriceStore key (core asset or feature data)", ticker)
		}
	}
	return nil
}

// FetchAndCacheStock returns daily closes for a ticker from PriceStore
// (if fresh) or from Yahoo (otherwise), persisting fetched data under
// the namespaced key "stock_<ticker>".
//
// days controls the requested history window when fetching. Yahoo
// caps free access at roughly 10y daily, so days >> 3650 falls back
// to whatever Yahoo returns.
func FetchAndCacheStock(ctx context.Context, s *store.PriceStore, ticker string, days int) ([]store.PricePoint, error) {
	if s == nil {
		return nil, errors.New("nil PriceStore")
	}
	if err := ValidateStockTicker(s, ticker); err != nil {
		return nil, err
	}
	if days <= 0 {
		days = 3650
	}

	key := StockKey(ticker)

	// TTL fast path: cached data exists and is fresh.
	if existing, err := s.Load(key); err == nil && len(existing) > 0 {
		if latest, ok := s.Latest(key); ok {
			if t, perr := time.Parse("2006-01-02", latest.Date); perr == nil {
				if time.Since(t) < stockTTL {
					return existing, nil
				}
			}
		}
	}

	points, err := fetchStockFromYahoo(ctx, ticker, days)
	if err != nil {
		return nil, fmt.Errorf("yahoo fetch %s: %w", ticker, err)
	}
	if len(points) == 0 {
		return nil, fmt.Errorf("yahoo: empty data for %s", ticker)
	}

	if err := s.Save(key, points); err != nil {
		return nil, fmt.Errorf("save %s: %w", key, err)
	}
	return points, nil
}

// fetchStockFromYahoo fetches daily history via the shared datasources/yahoo
// package into a store.PricePoint slice (oldest-first, dedup at PriceStore.Save
// time via NormalizePricePoints).
func fetchStockFromYahoo(ctx context.Context, ticker string, days int) ([]store.PricePoint, error) {
	bars, err := dsyahoo.GetHistoryDays(ctx, strings.ToUpper(ticker), days)
	if err != nil {
		return nil, err
	}
	return yahooBarsToPricePoints(bars, "yahoo:"+strings.ToUpper(ticker)), nil
}
