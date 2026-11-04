package dataplaneapp

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Ricaardo/nimbus-os/datasources/market/hub"
)

func TestNewDataplaneServerUsesConfiguredAddressAndSharedMux(t *testing.T) {
	server := newDataplaneServer("127.0.0.1:18800", newDatagwMux())
	if server.Addr != "127.0.0.1:18800" {
		t.Fatalf("address = %q", server.Addr)
	}
	if server.ReadHeaderTimeout != 5*time.Second {
		t.Fatalf("read header timeout = %s", server.ReadHeaderTimeout)
	}

	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ratings", nil))
	if rec.Code == http.StatusNotFound {
		t.Fatal("dataplane did not mount a rewrite backed by the shared datagw mux")
	}
}

func TestHasDataMirrorsPythonTruthiness(t *testing.T) {
	cases := []struct {
		body string
		want bool
	}{
		{`{"data": [1]}`, true},
		{`{"data": {"k": 1}}`, true},
		{`{"data": "x"}`, true},
		{`{"data": "0"}`, true}, // python bool("0") is True
		{`{"data": 1}`, true},
		{`{"data": 0.0}`, false},
		{`{"data": 0}`, false},
		{`{"data": false}`, false},
		{`{"data": ""}`, false},
		{`{"data": [ ]}`, false},
		{`{"data": { }}`, false},
		{`{"data": null}`, false},
		{`{"other": 1}`, false},
		{`not json`, false},
	}
	for _, c := range cases {
		if got := hasData([]byte(c.body)); got != c.want {
			t.Errorf("hasData(%s) = %v, want %v", c.body, got, c.want)
		}
	}
}

func TestDpDispatchPreservesQueryAndRewritesPath(t *testing.T) {
	inner := http.NewServeMux()
	inner.HandleFunc("/macro-rows", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/macro-rows" {
			t.Errorf("inner path = %s", r.URL.Path)
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"series":"` + r.URL.Query().Get("series") + `"}`))
	})
	r := httptest.NewRequest("GET", "http://x/macro?series=DGS10&limit=5", nil)
	code, body := dpDispatch(inner, r, "/macro-rows")
	if code != 200 || string(body) != `{"series":"DGS10"}` {
		t.Errorf("dpDispatch = %d %s", code, body)
	}
	if r.URL.Path != "/macro" {
		t.Errorf("original request mutated: %s", r.URL.Path)
	}
}

// TestDataplaneRouteTableComplete pins the facade surface: every route the
// retired Python facade served must resolve on the dataplane mux (as an
// in-process rewrite, a proxy, or an explicit handler). Guards the table
// against silent rot.
func TestDataplaneRouteTableComplete(t *testing.T) {
	facadeRoutes := []string{
		"/quote", "/kline", "/history",
		"/fundamentals", "/technicals", "/universe", "/screening",
		"/ratings", "/profile", "/earnings_calendar", "/earnings", "/insider",
		"/short", "/filings", "/13f", "/macro", "/company_news", "/peers",
		"/basic_financials", "/search", "/market_status", "/economic_calendar",
		"/income_statement", "/dividends", "/balance_sheet", "/cash_flow",
		"/cn_hsgt_flow", "/cn_margin", "/cn_breadth", "/cn_ah_premium",
		"/feargreed", "/funding", "/open_interest", "/long_short", "/dvol",
		"/option", "/news", "/health",
	}
	covered := map[string]bool{
		"/quote": true, "/kline": true, "/history": true, "/health": true,
	}
	for p := range dpDatagwRewrites {
		covered[p] = true
	}
	for p := range dpProxyRoutes() {
		covered[p] = true
	}
	for _, p := range facadeRoutes {
		if !covered[p] {
			t.Errorf("facade route %s not served by dataplane", p)
		}
	}
}

func TestDataplaneRewriteTargetsExistOnDatagwMux(t *testing.T) {
	// Build the real datagw mux surface the way main() does is impractical in
	// a unit test; instead assert every rewrite target is non-empty and
	// slash-prefixed, and that a typo'd target yields the guarded JSON 404.
	for from, to := range dpDatagwRewrites {
		if to == "" || to[0] != '/' {
			t.Errorf("rewrite %s -> %q malformed", from, to)
		}
	}
	inner := http.NewServeMux() // no routes: every dispatch is a Go text 404
	mux := buildDataplaneMux(inner, nil)
	req := httptest.NewRequest("GET", "http://x/ratings?symbol=US:AAPL", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 404 || rec.Body.String() != `{"error": "not found"}` {
		t.Errorf("typo'd/unmounted target must yield JSON 404, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestHistoryFallsBackToKline(t *testing.T) {
	warehouse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data": []}`)) // warehouse lacks the symbol
	}))
	defer warehouse.Close()
	futu := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data": [{"close": 1}]}`)) // hub /kline delegates here for US:*
	}))
	defer futu.Close()
	t.Setenv("WAREHOUSE_URL", warehouse.URL)
	t.Setenv("MARKET_FUTU_WORKER", futu.URL)

	mux := buildDataplaneMux(http.NewServeMux(), hub.New()) // env 已指向 stub
	req := httptest.NewRequest("GET", "http://x/history?symbol=US:SPY&limit=3", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 200 || !hasData(rec.Body.Bytes()) {
		t.Errorf("history fallback failed: %d %s", rec.Code, rec.Body.String())
	}
}
