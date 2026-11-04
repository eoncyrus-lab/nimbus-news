// Package fmp is the independent Financial Modeling Prep data source
// (/stable API, free tier). Fetch-only and stateless.
//
// Free-tier semantics (hard-won, do not "fix"):
//   - Statement endpoints cap at 5 rows and 402 when a `limit` query param is
//     sent — so this package NEVER sends limit; callers slice client-side.
//   - /dividends serves the FULL history with no limit but 402s on limit>5 —
//     same rule: fetch all, slice client-side.
//   - Paid endpoints (key-metrics, etf holdings, company-screener) are
//     intentionally not wrapped.
//
// Requires an FMP_API_KEY (passed in by the caller; key lives in datagw's env).
package fmp

import (
	"context"
	"fmt"
	"net/url"

	"github.com/Ricaardo/nimbus-os/datasources/internal/httpx"
)

// BaseURL is a var so tests can point it at an httptest server.
var BaseURL = "https://financialmodelingprep.com/stable"

// Stable fetches a /stable list endpoint with the given params (apikey is
// attached here; `limit` is stripped defensively — see package doc).
func Stable(ctx context.Context, apiKey, endpoint string, params map[string]string) ([]map[string]any, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("fmp: missing api key")
	}
	q := url.Values{}
	for k, v := range params {
		if k == "limit" || v == "" {
			continue
		}
		q.Set(k, v)
	}
	q.Set("apikey", apiKey)
	var rows []map[string]any
	u := BaseURL + "/" + endpoint + "?" + q.Encode()
	if err := httpx.Default.GetJSON(ctx, u, nil, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// Statement fetches an income/balance/cash-flow statement for symbol+period and
// slices to limit client-side (free tier serves at most 5 rows, newest-first).
func Statement(ctx context.Context, apiKey, endpoint, symbol, period string, limit int) ([]map[string]any, error) {
	rows, err := Stable(ctx, apiKey, endpoint, map[string]string{"symbol": symbol, "period": period})
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

// Dividends fetches the full dividend history for symbol (newest-first) and
// slices to limit client-side.
func Dividends(ctx context.Context, apiKey, symbol string, limit int) ([]map[string]any, error) {
	rows, err := Stable(ctx, apiKey, "dividends", map[string]string{"symbol": symbol})
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}
