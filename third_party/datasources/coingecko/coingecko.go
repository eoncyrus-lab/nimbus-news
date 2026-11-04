// Package coingecko is the independent CoinGecko data source: coins/markets,
// simple/price, and the global market snapshot. Free public API, no key.
// Fetch-only — callers own accumulation (e.g. archiving total market cap) and
// any derived metrics (altcoin season, dominance scaling).
package coingecko

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/Ricaardo/nimbus-os/datasources/internal/httpx"
)

const base = "https://api.coingecko.com/api/v3"

// Coin is one coins/markets row.
type Coin struct {
	ID           string  `json:"id"`
	Symbol       string  `json:"symbol"`
	Name         string  `json:"name"`
	CurrentPrice float64 `json:"current_price"`
	MarketCap    float64 `json:"market_cap"`
}

// MarketsParams parameterises coins/markets. Zero values use CoinGecko defaults
// (vs_currency=usd, order=market_cap_desc, per_page=100, page=1).
type MarketsParams struct {
	VsCurrency string
	IDs        []string // optional filter
	Order      string
	PerPage    int
	Page       int
}

// CoinsMarkets returns the markets list (descending by market cap by default).
func CoinsMarkets(ctx context.Context, p MarketsParams) ([]Coin, error) {
	q := url.Values{}
	q.Set("vs_currency", or(p.VsCurrency, "usd"))
	q.Set("order", or(p.Order, "market_cap_desc"))
	q.Set("per_page", strconv.Itoa(orInt(p.PerPage, 100)))
	q.Set("page", strconv.Itoa(orInt(p.Page, 1)))
	q.Set("sparkline", "false")
	if len(p.IDs) > 0 {
		q.Set("ids", strings.Join(p.IDs, ","))
	}
	var out []Coin
	if err := httpx.Default.GetJSON(ctx, base+"/coins/markets?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// SimplePrice returns ids -> vsCurrency -> price.
func SimplePrice(ctx context.Context, ids, vsCurrencies []string) (map[string]map[string]float64, error) {
	q := url.Values{}
	q.Set("ids", strings.Join(ids, ","))
	q.Set("vs_currencies", strings.Join(vsCurrencies, ","))
	var out map[string]map[string]float64
	if err := httpx.Default.GetJSON(ctx, base+"/simple/price?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Global is the global market snapshot subset.
type Global struct {
	TotalMarketCapUSD float64            // total_market_cap.usd
	MarketCapPct      map[string]float64 // market_cap_percentage (e.g. "btc": 52.1)
}

// GetGlobal returns the current total market cap and dominance percentages.
func GetGlobal(ctx context.Context) (*Global, error) {
	var resp struct {
		Data struct {
			TotalMarketCap map[string]float64 `json:"total_market_cap"`
			MarketCapPct   map[string]float64 `json:"market_cap_percentage"`
		} `json:"data"`
	}
	if err := httpx.Default.GetJSON(ctx, base+"/global", nil, &resp); err != nil {
		return nil, err
	}
	usd, ok := resp.Data.TotalMarketCap["usd"]
	if !ok {
		return nil, fmt.Errorf("coingecko global: missing total_market_cap.usd")
	}
	return &Global{TotalMarketCapUSD: usd, MarketCapPct: resp.Data.MarketCapPct}, nil
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func orInt(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}
