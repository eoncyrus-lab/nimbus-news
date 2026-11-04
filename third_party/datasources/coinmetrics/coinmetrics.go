// Package coinmetrics is the independent CoinMetrics data source: the v4
// timeseries asset-metrics endpoint (community or paid host, paginated). It is
// fetch-only — callers own any accumulation, caching, or derived math (MVRV
// z-scores, realized cap, NUPL quantiles, etc.).
package coinmetrics

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Ricaardo/nimbus-os/datasources/internal/httpx"
)

const (
	communityBase = "https://community-api.coinmetrics.io/v4/timeseries/asset-metrics"
	paidBase      = "https://api.coinmetrics.io/v4/timeseries/asset-metrics"
)

// Point is one asset-metrics observation. Metrics maps each requested metric
// name to its raw string value (CoinMetrics serves numbers as strings).
type Point struct {
	Asset   string
	Time    time.Time
	Metrics map[string]string
}

// Query parameterises an asset-metrics request. Zero Start/End omit that bound;
// empty APIKey uses the free community host.
type Query struct {
	Asset     string
	Metrics   []string
	Frequency string // default "1d"
	Start     time.Time
	End       time.Time
	Limit     int // limit_per_asset + page_size; default 10000
	APIKey    string
}

type rawResp struct {
	Data        []map[string]string `json:"data"`
	NextPageURL string              `json:"next_page_url"`
}

// AssetMetrics fetches the timeseries, following next_page_url pagination, and
// returns points in the order CoinMetrics serves them (ascending by time).
func AssetMetrics(ctx context.Context, q Query) ([]Point, error) {
	base := communityBase
	if q.APIKey != "" {
		base = paidBase
	}
	freq := q.Frequency
	if freq == "" {
		freq = "1d"
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 10000
	}

	params := url.Values{}
	params.Set("assets", q.Asset)
	params.Set("metrics", strings.Join(q.Metrics, ","))
	params.Set("frequency", freq)
	if !q.Start.IsZero() {
		params.Set("start_time", q.Start.UTC().Format("2006-01-02"))
	}
	if !q.End.IsZero() {
		params.Set("end_time", q.End.UTC().Format("2006-01-02"))
	}
	params.Set("limit_per_asset", strconv.Itoa(limit))
	params.Set("page_size", strconv.Itoa(limit))
	if q.APIKey != "" {
		params.Set("api_key", q.APIKey)
	}

	next := base + "?" + params.Encode()
	var out []Point
	for next != "" {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var resp rawResp
		if err := httpx.Default.GetJSON(ctx, next, map[string]string{"Accept": "application/json"}, &resp); err != nil {
			return nil, err
		}
		for _, row := range resp.Data {
			t, err := time.Parse(time.RFC3339Nano, row["time"])
			if err != nil {
				continue
			}
			m := make(map[string]string, len(row))
			for k, v := range row {
				if k == "time" || k == "asset" {
					continue
				}
				m[k] = v
			}
			out = append(out, Point{Asset: row["asset"], Time: t, Metrics: m})
		}
		next = resp.NextPageURL
	}
	// Fetch-only: an empty range is returned as (nil, nil), not an error — the
	// caller decides whether empty is a failure for its use case.
	return out, nil
}
