package dataplaneapp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ricaardo/nimbus-os/datasources/finnhub"
)

func TestConfigFromEnvPreservesDatagwDefaultsAndOverrides(t *testing.T) {
	t.Setenv("DATAGW_PORT", "")
	t.Setenv("DATAPLANE_ADDR", "")
	cfg := configFromEnv("datagw")
	if cfg.dataAddr != "127.0.0.1:8821" || cfg.dataplaneAddr != "" || cfg.serviceLabel != "datagw" {
		t.Fatalf("default config = %#v", cfg)
	}

	t.Setenv("DATAGW_PORT", "19821")
	t.Setenv("DATAPLANE_ADDR", "127.0.0.1:18800")
	cfg = configFromEnv("nimbusd (phase-0 compatibility data core)")
	if cfg.dataAddr != "127.0.0.1:19821" || cfg.dataplaneAddr != "127.0.0.1:18800" {
		t.Fatalf("override config = %#v", cfg)
	}
}

func TestSharedDatagwMuxContainsCompatibilityRoutes(t *testing.T) {
	mux := newDatagwMux()
	routes := []string{
		"/health", "/quote", "/history", "/crypto-quote", "/funding",
		"/open-interest", "/long-short", "/depth", "/dvol", "/option",
		"/feargreed", "/macro", "/short-file", "/finnhub", "/cboe-quote",
		"/edgar-filings", "/form-13f", "/ratings", "/profile",
		"/earnings-calendar", "/earnings", "/insider", "/company-news",
		"/peers", "/basic-financials", "/search", "/market-status",
		"/macro-rows", "/economic-calendar", "/fmp/income-statement",
		"/fmp/balance-sheet", "/fmp/cash-flow", "/fmp/dividends", "/short",
		"/filings", "/funding-rate", "/sec-xbrl",
	}
	for _, route := range routes {
		req := httptest.NewRequest(http.MethodGet, route, nil)
		_, pattern := mux.Handler(req)
		if pattern != route {
			t.Errorf("route %s resolved as %q", route, pattern)
		}
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	var health struct {
		OK      bool     `json:"ok"`
		Service string   `json:"service"`
		Sources []string `json:"sources"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || !health.OK || health.Service != "datagw" || len(health.Sources) != 12 {
		t.Fatalf("health compatibility changed: code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleFinnhubEndpointRequired(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/finnhub", nil)
	handleFinnhub(rec, req)
	if rec.Code != 400 {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
}

func TestHandleFinnhubMissingKey(t *testing.T) {
	t.Setenv("FINNHUB_API_KEY", "")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/finnhub?endpoint=/quote&symbol=AAPL", nil)
	handleFinnhub(rec, req)
	if rec.Code != 503 {
		t.Fatalf("code = %d, want 503", rec.Code)
	}
}

func TestHandleFinnhubForwardsAndWraps(t *testing.T) {
	old := finnhub.BaseURL
	defer func() { finnhub.BaseURL = old }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stock/profile2" {
			t.Fatalf("path %s", r.URL.Path)
		}
		// datagw must attach the key and forward the caller's params
		if r.URL.Query().Get("token") != "SECRET" || r.URL.Query().Get("symbol") != "AAPL" {
			t.Fatalf("query %s", r.URL.RawQuery)
		}
		w.Write([]byte(`{"name":"Apple Inc"}`))
	}))
	defer srv.Close()
	finnhub.BaseURL = srv.URL
	t.Setenv("FINNHUB_API_KEY", "SECRET")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/finnhub?endpoint=/stock/profile2&symbol=AAPL", nil)
	handleFinnhub(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code = %d body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Provider string          `json:"provider"`
		Params   map[string]any  `json:"params"`
		Data     json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Provider != "finnhub" {
		t.Fatalf("provider = %q", env.Provider)
	}
	// the upstream JSON is passed through verbatim in data
	if !strings.Contains(string(env.Data), "Apple Inc") {
		t.Fatalf("data = %s", string(env.Data))
	}
	// the secret key must NOT be echoed back in params
	if strings.Contains(rec.Body.String(), "SECRET") {
		t.Fatalf("response leaked the api key: %s", rec.Body.String())
	}
}
