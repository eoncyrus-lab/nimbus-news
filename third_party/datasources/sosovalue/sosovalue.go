// Package sosovalue is the independent SoSoValue data source: spot crypto ETF
// daily net-inflow history. Fetch-only and stateless — the endpoint returns the
// full daily series each call, so callers derive their own windows (7d/30d/...).
//
// Free public API; an API key (x-soso-api-key) is optional and only raises rate
// limits.
package sosovalue

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/Ricaardo/nimbus-os/datasources/internal/httpx"
)

// BaseURL is the API host. It is a var (not const) so tests can point it at an
// httptest server.
var BaseURL = "https://api.sosovalue.xyz"

// ETFFlow is one day of aggregate spot-ETF flow for a fund type.
type ETFFlow struct {
	Date           time.Time
	NetInflowUSD   float64 // totalNetInflow
	TotalAssetsUSD float64 // totalNetAssets
	CumulativeUSD  float64 // cumNetInflow
}

type etfRow struct {
	Date           string  `json:"date"`
	TotalNetInflow float64 `json:"totalNetInflow"`
	TotalNetAssets float64 `json:"totalNetAssets"`
	CumNetInflow   float64 `json:"cumNetInflow"`
}

// HistoricalInflowChart returns the daily ETF net-inflow series for a fund type
// (e.g. "us-btc-spot"), oldest-first. apiKey may be empty. An upstream error
// code is returned as an error; an empty series → (nil, nil).
func HistoricalInflowChart(ctx context.Context, apiKey, fundType string) ([]ETFFlow, error) {
	var headers map[string]string
	if apiKey != "" {
		headers = map[string]string{"x-soso-api-key": apiKey}
	}
	reqBody := []byte(fmt.Sprintf(`{"type":%q}`, fundType))

	var parsed struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := httpx.Default.PostJSON(ctx, BaseURL+"/openapi/v2/etf/historicalInflowChart", headers, reqBody, &parsed); err != nil {
		return nil, err
	}
	if parsed.Code != 0 {
		return nil, fmt.Errorf("sosovalue err: code=%d msg=%s", parsed.Code, parsed.Msg)
	}
	rows, err := parseRows(parsed.Data)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}

	flows := make([]ETFFlow, 0, len(rows))
	for _, d := range rows {
		t, err := time.Parse("2006-01-02", d.Date)
		if err != nil {
			continue
		}
		flows = append(flows, ETFFlow{
			Date:           t,
			NetInflowUSD:   d.TotalNetInflow,
			TotalAssetsUSD: d.TotalNetAssets,
			CumulativeUSD:  d.CumNetInflow,
		})
	}
	if len(flows) == 0 {
		return nil, nil
	}
	sort.Slice(flows, func(i, j int) bool { return flows[i].Date.Before(flows[j].Date) })
	return flows, nil
}

// parseRows handles both data shapes SoSoValue has emitted: a bare array, or
// an object wrapping {"list": [...]}.
func parseRows(raw json.RawMessage) ([]etfRow, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var rows []etfRow
	if err := json.Unmarshal(raw, &rows); err == nil {
		return rows, nil
	}
	var wrapped struct {
		List []etfRow `json:"list"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, err
	}
	return wrapped.List, nil
}
