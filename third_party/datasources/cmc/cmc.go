// Package cmc is the independent CoinMarketCap data source: global metrics and
// cryptocurrency quotes from the pro API (requires an API key). Fetch-only —
// CMC's free tier returns only "now", so the daily-appended history lives in the
// caller's store; this package never accumulates.
package cmc

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Ricaardo/nimbus-os/datasources/internal/httpx"
)

// BaseURL is the API host. It is a var (not const) so tests can point it at an
// httptest server.
var BaseURL = "https://pro-api.coinmarketcap.com"

// GlobalMetrics is the latest global market snapshot (USD convert).
type GlobalMetrics struct {
	BTCDominance        float64
	ETHDominance        float64
	TotalMarketCapUSD   float64
	TotalVolume24hUSD   float64
	AltcoinMarketCapUSD float64
	AltcoinVolume24hUSD float64
	LastUpdated         string // RFC3339
}

// Quote is the latest quote for one cryptocurrency (USD convert).
type Quote struct {
	ID           int
	Symbol       string
	PriceUSD     float64
	Volume24hUSD float64
	MarketCapUSD float64
	LastUpdated  string // RFC3339
}

type status struct {
	ErrorCode    int    `json:"error_code"`
	ErrorMessage string `json:"error_message"`
}

func get(ctx context.Context, apiKey, pathQuery string, out any) error {
	headers := map[string]string{
		"Accept":            "application/json",
		"X-CMC_PRO_API_KEY": apiKey,
	}
	return httpx.Default.GetJSON(ctx, BaseURL+pathQuery, headers, out)
}

// GetGlobalMetrics returns the latest global-metrics quote in USD.
func GetGlobalMetrics(ctx context.Context, apiKey string) (*GlobalMetrics, error) {
	var resp struct {
		Status status `json:"status"`
		Data   struct {
			BTCDominance float64 `json:"btc_dominance"`
			ETHDominance float64 `json:"eth_dominance"`
			Quote        struct {
				USD struct {
					TotalMarketCap   float64 `json:"total_market_cap"`
					TotalVolume24h   float64 `json:"total_volume_24h"`
					AltcoinMarketCap float64 `json:"altcoin_market_cap"`
					AltcoinVolume24h float64 `json:"altcoin_volume_24h"`
					LastUpdated      string  `json:"last_updated"`
				} `json:"USD"`
			} `json:"quote"`
		} `json:"data"`
	}
	if err := get(ctx, apiKey, "/v1/global-metrics/quotes/latest?convert=USD", &resp); err != nil {
		return nil, err
	}
	if resp.Status.ErrorCode != 0 {
		return nil, fmt.Errorf("cmc global metrics: %s", resp.Status.ErrorMessage)
	}
	u := resp.Data.Quote.USD
	return &GlobalMetrics{
		BTCDominance:        resp.Data.BTCDominance,
		ETHDominance:        resp.Data.ETHDominance,
		TotalMarketCapUSD:   u.TotalMarketCap,
		TotalVolume24hUSD:   u.TotalVolume24h,
		AltcoinMarketCapUSD: u.AltcoinMarketCap,
		AltcoinVolume24hUSD: u.AltcoinVolume24h,
		LastUpdated:         u.LastUpdated,
	}, nil
}

// GetQuote returns the latest USD quote for a cryptocurrency by CMC id (BTC = 1).
func GetQuote(ctx context.Context, apiKey string, id int) (*Quote, error) {
	var resp struct {
		Status status `json:"status"`
		Data   map[string]struct {
			ID     int    `json:"id"`
			Symbol string `json:"symbol"`
			Quote  struct {
				USD struct {
					Price       float64 `json:"price"`
					Volume24h   float64 `json:"volume_24h"`
					MarketCap   float64 `json:"market_cap"`
					LastUpdated string  `json:"last_updated"`
				} `json:"USD"`
			} `json:"quote"`
		} `json:"data"`
	}
	idStr := strconv.Itoa(id)
	if err := get(ctx, apiKey, "/v2/cryptocurrency/quotes/latest?id="+idStr+"&convert=USD", &resp); err != nil {
		return nil, err
	}
	if resp.Status.ErrorCode != 0 {
		return nil, fmt.Errorf("cmc quote: %s", resp.Status.ErrorMessage)
	}
	c, ok := resp.Data[idStr]
	if !ok {
		return nil, fmt.Errorf("cmc quote: missing id=%d in response", id)
	}
	return &Quote{
		ID:           c.ID,
		Symbol:       c.Symbol,
		PriceUSD:     c.Quote.USD.Price,
		Volume24hUSD: c.Quote.USD.Volume24h,
		MarketCapUSD: c.Quote.USD.MarketCap,
		LastUpdated:  c.Quote.USD.LastUpdated,
	}, nil
}
