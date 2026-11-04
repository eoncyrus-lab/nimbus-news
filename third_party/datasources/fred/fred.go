// Package fred is the independent FRED (St. Louis Fed) data source: series
// observations from the keyed JSON API. Fetch-only and stateless — callers
// derive metrics or persist history themselves.
//
// Requires a FRED_API_KEY (passed in by the caller). FRED reports missing
// values as ".", which this package passes through verbatim; consumers decide
// how to treat them.
package fred

import (
	"context"
	"net/url"
	"strconv"

	"github.com/Ricaardo/nimbus-os/datasources/internal/httpx"
)

// BaseURL is the keyed JSON API host. It is a var (not const) so tests can
// point it at an httptest server.
var BaseURL = "https://api.stlouisfed.org"

// Observation is one raw FRED data point. Value is the upstream string; "."
// means missing.
type Observation struct {
	Date  string `json:"date"`
	Value string `json:"value"`
}

// Query selects observations for one series.
type Query struct {
	Series   string // FRED series_id, e.g. "DTWEXBGS"
	Limit    int    // max observations; 0 = FRED default (omit)
	SortDesc bool   // newest-first when true; else FRED's natural ascending order
	Start    string // observation_start YYYY-MM-DD; "" = omit
	End      string // observation_end YYYY-MM-DD; "" = omit
}

type observationsResp struct {
	Observations []Observation `json:"observations"`
}

// Observations fetches a series' observations per q. Empty result → (nil, nil).
func Observations(ctx context.Context, apiKey string, q Query) ([]Observation, error) {
	params := url.Values{}
	params.Set("series_id", q.Series)
	params.Set("api_key", apiKey)
	params.Set("file_type", "json")
	if q.Limit > 0 {
		params.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.SortDesc {
		params.Set("sort_order", "desc")
	}
	if q.Start != "" {
		params.Set("observation_start", q.Start)
	}
	if q.End != "" {
		params.Set("observation_end", q.End)
	}

	var resp observationsResp
	url := BaseURL + "/fred/series/observations?" + params.Encode()
	if err := httpx.Default.GetJSON(ctx, url, nil, &resp); err != nil {
		return nil, err
	}
	if len(resp.Observations) == 0 {
		return nil, nil
	}
	return resp.Observations, nil
}

// ReleaseDate is one entry in FRED's economic-release calendar.
type ReleaseDate struct {
	ReleaseID   int    `json:"release_id"`
	ReleaseName string `json:"release_name"`
	Date        string `json:"date"`
}

type releaseDatesResp struct {
	ReleaseDates []ReleaseDate `json:"release_dates"`
}

// ReleaseDates fetches the US economic data-release calendar between start and
// end (YYYY-MM-DD, realtime window), ascending. Schedule only — FRED carries no
// estimate/actual/impact. Empty result → (nil, nil).
func ReleaseDates(ctx context.Context, apiKey, start, end string) ([]ReleaseDate, error) {
	params := url.Values{}
	params.Set("api_key", apiKey)
	params.Set("file_type", "json")
	params.Set("realtime_start", start)
	params.Set("realtime_end", end)
	params.Set("include_release_dates_with_no_data", "true")
	params.Set("sort_order", "asc")
	params.Set("limit", "1000")

	var resp releaseDatesResp
	u := BaseURL + "/fred/releases/dates?" + params.Encode()
	if err := httpx.Default.GetJSON(ctx, u, nil, &resp); err != nil {
		return nil, err
	}
	if len(resp.ReleaseDates) == 0 {
		return nil, nil
	}
	return resp.ReleaseDates, nil
}
