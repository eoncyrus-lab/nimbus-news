// dataplane.go — the data-access facade surface (:8800 dialect), served from
// this process (R6-b). Replaces the standalone Python facade + market-hub:
//
//   - /quote /kline            → market/hub handlers, in-process
//   - datagw-backed routes     → in-process dispatch onto the datagw mux
//     (path rewrite only, no HTTP hop)
//   - warehouse / py-worker / news → HTTP proxy (separate processes)
//   - /history                 → warehouse first, live /kline fallback
//     (semantics ported 1:1 from facade.py)
//
// Enabled only when DATAPLANE_ADDR is set (e.g. 127.0.0.1:8800), so the
// standalone datagw deployment is unchanged until the plist opts in.
package dataplaneapp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Ricaardo/nimbus-os/datasources/market/hub"
)

// dpDatagwRewrites: facade path → datagw-local path (query passes through).
// Mirrors facade.py ROUTES entries whose backend is DATAGW.
var dpDatagwRewrites = map[string]string{
	"/ratings":           "/ratings",
	"/profile":           "/profile",
	"/earnings_calendar": "/earnings-calendar",
	"/earnings":          "/earnings",
	"/insider":           "/insider",
	"/short":             "/short",
	"/filings":           "/filings",
	"/13f":               "/form-13f",
	"/macro":             "/macro-rows",
	"/company_news":      "/company-news",
	"/peers":             "/peers",
	"/basic_financials":  "/basic-financials",
	"/search":            "/search",
	"/market_status":     "/market-status",
	"/economic_calendar": "/economic-calendar",
	"/income_statement":  "/fmp/income-statement",
	"/dividends":         "/fmp/dividends",
	"/balance_sheet":     "/fmp/balance-sheet",
	"/cash_flow":         "/fmp/cash-flow",
	"/feargreed":         "/feargreed",
	"/funding":           "/funding-rate",
	"/open_interest":     "/open-interest",
	"/long_short":        "/long-short",
	"/dvol":              "/dvol",
	"/option":            "/option",
}

// dpProxyRoutes: facade path → full backend URL prefix (HTTP proxy).
func dpProxyRoutes() map[string]string {
	warehouse := envOr("WAREHOUSE_URL", "http://127.0.0.1:8814")
	pyWorker := envOr("PY_WORKER_URL", "http://127.0.0.1:8824")
	news := envOr("NEWS_URL", "http://127.0.0.1:8081")
	return map[string]string{
		"/fundamentals":  warehouse + "/fundamentals",
		"/technicals":    warehouse + "/technicals",
		"/universe":      warehouse + "/universe",
		"/screening":     warehouse + "/screening",
		"/cn_hsgt_flow":  pyWorker + "/cn_hsgt_flow",
		"/cn_margin":     pyWorker + "/cn_margin",
		"/cn_breadth":    pyWorker + "/cn_breadth",
		"/cn_ah_premium": pyWorker + "/cn_ah_premium",
		"/news":          news + "/api/news",
	}
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// dpFetch mirrors facade.py fetch(): 20s timeout, backend-unreachable turns
// into a 502 {"error": ..., "stale": true} envelope rather than an error page.
func dpFetch(url string, timeout time.Duration) (int, []byte) {
	c := &http.Client{Timeout: timeout}
	resp, err := c.Get(url)
	if err != nil {
		b, _ := json.Marshal(map[string]any{"error": fmt.Sprintf("backend unreachable: %v", err), "stale": true})
		return 502, b
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		b, _ := json.Marshal(map[string]any{"error": fmt.Sprintf("backend unreachable: %v", err), "stale": true})
		return 502, b
	}
	return resp.StatusCode, body
}

func dpSend(w http.ResponseWriter, code int, body []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write(body)
}

// memResponse is a minimal in-memory ResponseWriter for in-process dispatch.
type memResponse struct {
	header http.Header
	code   int
	buf    bytes.Buffer
}

func newMemResponse() *memResponse {
	return &memResponse{header: http.Header{}, code: 200}
}
func (m *memResponse) Header() http.Header         { return m.header }
func (m *memResponse) WriteHeader(code int)        { m.code = code }
func (m *memResponse) Write(b []byte) (int, error) { return m.buf.Write(b) }

// dpDispatch runs a handler in-process against a rewritten request path.
func dpDispatch(h http.Handler, r *http.Request, path string) (int, []byte) {
	r2 := r.Clone(r.Context())
	r2.URL.Path = path
	r2.URL.RawPath = ""
	m := newMemResponse()
	h.ServeHTTP(m, r2)
	return m.code, m.buf.Bytes()
}

// hasData is the legacy adapter's Python-truthiness compatibility check.
// Strict routes use explicit observation statuses instead.
func hasData(body []byte) bool {
	var parsed struct {
		Data any `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return false
	}
	switch v := parsed.Data.(type) {
	case nil:
		return false
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		return v != ""
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	default:
		return true
	}
}

// buildDataplaneMux assembles the facade dialect on its own mux.
// datagwMux is this process's own route surface (in-process dispatch target).
func buildDataplaneMux(datagwMux *http.ServeMux, marketHub *hub.Hub) *http.ServeMux {
	proxy := dpProxyRoutes()
	warehouse := envOr("WAREHOUSE_URL", "http://127.0.0.1:8814")
	mux := http.NewServeMux()

	// market (in-process handlers, same shapes as market-hub :8830)
	mux.HandleFunc("/quote", marketHub.HandleQuote)
	mux.HandleFunc("/kline", marketHub.HandleKline)

	// datagw-backed facade routes: in-process dispatch, path rewrite only
	for from, to := range dpDatagwRewrites {
		from, to := from, to
		mux.HandleFunc(from, func(w http.ResponseWriter, r *http.Request) {
			code, body := dpDispatch(datagwMux, r, to)
			if code == 404 && !json.Valid(body) { // typo'd rewrite target → Go text 404
				body = []byte(`{"error": "not found"}`)
			}
			dpSend(w, code, body)
		})
	}

	// plain HTTP proxies (warehouse / py-worker / news)
	for from, target := range proxy {
		target := target
		mux.HandleFunc(from, func(w http.ResponseWriter, r *http.Request) {
			url := target
			if q := r.URL.RawQuery; q != "" {
				url += "?" + q
			}
			code, body := dpFetch(url, 20*time.Second)
			dpSend(w, code, body)
		})
	}

	// /history: warehouse primary (stocks, 14yr); live kline fallback for
	// symbols the warehouse lacks (ETFs etc.) — legacy truthiness is retained
	// only on this unversioned adapter.
	mux.HandleFunc("/history", func(w http.ResponseWriter, r *http.Request) {
		url := warehouse + "/history"
		if q := r.URL.RawQuery; q != "" {
			url += "?" + q
		}
		code, body := dpFetch(url, 20*time.Second)
		if code == 200 && hasData(body) {
			dpSend(w, code, body)
			return
		}
		code2, body2 := dpDispatch(mux, r, "/kline")
		if code2 == 200 && hasData(body2) {
			dpSend(w, code2, body2)
			return
		}
		dpSend(w, code, body) // neither had data — keep warehouse shape
	})

	// Strict v1 routes are registered only for migrated producers. Their
	// response status comes from typed classification, never generic JSON
	// truthiness. Other facade routes intentionally remain legacy-only.
	mux.HandleFunc("/v1/quote", func(w http.ResponseWriter, r *http.Request) {
		symbols := r.URL.Query().Get("symbols")
		cleanSymbols := strings.Join(splitSymbols(symbols), ",")
		params := map[string]any{"symbols": cleanSymbols}
		if cleanSymbols == "" {
			writeStrictResult(w, failedResult("market-hub", "quote", params, http.StatusBadRequest,
				"invalid_request", "symbols required", "", false))
			return
		}
		code, body := dpDispatch(mux, r, "/quote")
		result := decodeFacadeResult(code, body, "market-hub", "quote", "market-hub", params)
		writeStrictResult(w, normalizeStrictQuoteResult(result))
	})
	mux.HandleFunc("/v1/history", func(w http.ResponseWriter, r *http.Request) {
		symbol := r.URL.Query().Get("symbol")
		params := map[string]any{
			"symbol":   symbol,
			"interval": r.URL.Query().Get("interval"),
			"limit":    atoiDefault(r.URL.Query().Get("limit"), 120),
		}
		if symbol == "" {
			writeStrictResult(w, failedResult("warehouse", "history", params, http.StatusBadRequest,
				"invalid_request", "symbol required", "", false))
			return
		}
		url := warehouse + "/history"
		if q := r.URL.RawQuery; q != "" {
			url += "?" + q
		}
		code, body := dpFetch(url, 20*time.Second)
		primary := decodeFacadeResult(code, body, "warehouse", "history", "warehouse", params)
		if !strictHistoryFallbackPolicy.allows(primary.Status) {
			writeStrictResult(w, primary)
			return
		}
		fallbackCode, fallbackBody := dpDispatch(mux, r, "/kline")
		fallback := decodeFacadeResult(fallbackCode, fallbackBody, "market-hub", "kline", "market-hub", params)
		writeStrictResult(w, applyFallback(primary, fallback))
	})

	// aggregate /health (same keys as facade.py; market-data and datagw are
	// in-process now, reported honestly as such)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		out := map[string]bool{"datagw": true}
		mdCode, _ := dpFetch(envOr("MARKET_FUTU_WORKER", "http://127.0.0.1:8810")+"/health", 4*time.Second)
		out["market-data"] = mdCode == 200 // 探真正的 futu worker，比旧 facade 探 market-hub 静态 ok 更诚实
		for name, base := range map[string]string{
			"warehouse": envOr("WAREHOUSE_URL", "http://127.0.0.1:8814"),
			"py-worker": envOr("PY_WORKER_URL", "http://127.0.0.1:8824"),
		} {
			code, _ := dpFetch(base+"/health", 4*time.Second)
			out[name] = code == 200
		}
		code, _ := dpFetch(envOr("NEWS_URL", "http://127.0.0.1:8081")+"/api/health", 4*time.Second)
		out["news"] = code == 200
		ok := true
		for _, v := range out {
			ok = ok && v
		}
		b, _ := json.Marshal(map[string]any{"ok": ok, "backends": out, "dataplane": true})
		dpSend(w, 200, b)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		dpSend(w, 404, []byte(`{"error": "not found"}`))
	})
	return mux
}

func newDataplaneServer(addr string, datagwMux *http.ServeMux) *http.Server {
	marketHub := hub.New()
	mux := buildDataplaneMux(datagwMux, marketHub)
	return &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
}

// NewDataplaneHandler assembles the facade dialect without owning a listener.
// dataHandler must be the handler returned by NewDataHandler so rewritten
// legacy routes dispatch through the same route table.
func NewDataplaneHandler(dataHandler *http.ServeMux) http.Handler {
	return buildDataplaneMux(dataHandler, hub.New())
}
