// Package finnhub is the independent Finnhub.io data source: market & company
// news plus the free-tier equity surface (profile, analyst recommendation,
// earnings, insider transactions, key metrics, peers, search, market status).
// Fetch-only and stateless.
//
// Finnhub auth is a `token` query param (free tier, 60 req/min). News has typed
// helpers; the rest is exposed through Raw so callers that do their own shaping
// (the data-gateway Python provider) receive untouched JSON.
package finnhub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/Ricaardo/nimbus-os/datasources/internal/httpx"
)

// BaseURL is the Finnhub REST root. Var (not const) so tests can point it at an
// httptest server.
var BaseURL = "https://finnhub.io/api/v1"

// News is one market/company news item (/news and /company-news share shape).
type News struct {
	Category string `json:"category"`
	Datetime int64  `json:"datetime"`
	Headline string `json:"headline"`
	ID       int64  `json:"id"`
	Image    string `json:"image"`
	Related  string `json:"related"`
	Source   string `json:"source"`
	Summary  string `json:"summary"`
	URL      string `json:"url"`
}

// Raw GETs a Finnhub endpoint and returns the raw JSON body. path is the
// endpoint path (e.g. "/stock/profile2"); params must NOT include the token —
// it is appended here.
func Raw(ctx context.Context, apiKey, path string, params map[string]string) (json.RawMessage, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("finnhub: missing api key")
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	q := url.Values{}
	for k, v := range params {
		q.Set(k, v)
	}
	q.Set("token", apiKey)
	body, err := httpx.Default.GetBytes(ctx, BaseURL+path+"?"+q.Encode(),
		map[string]string{"Accept": "application/json"})
	if err != nil {
		return nil, err
	}
	return json.RawMessage(body), nil
}

// MarketNews returns general market news for a category ("general","forex",
// "crypto","merger"), in Finnhub's order (newest-first).
func MarketNews(ctx context.Context, apiKey, category string) ([]News, error) {
	if category == "" {
		category = "general"
	}
	raw, err := Raw(ctx, apiKey, "/news", map[string]string{"category": category})
	if err != nil {
		return nil, err
	}
	var out []News
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("finnhub market news: %w", err)
	}
	return out, nil
}

// CompanyNews returns per-symbol news in [from,to] (YYYY-MM-DD), newest-first.
func CompanyNews(ctx context.Context, apiKey, symbol, from, to string) ([]News, error) {
	raw, err := Raw(ctx, apiKey, "/company-news",
		map[string]string{"symbol": symbol, "from": from, "to": to})
	if err != nil {
		return nil, err
	}
	var out []News
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("finnhub company news: %w", err)
	}
	return out, nil
}
