// Package dataplaneapp assembles the Go data gateway and its optional facade
// dialect listener. Both command entrypoints use this package so they expose one
// route table and one lifecycle implementation.
//
// Routes (proving slice):
//
//	GET /quote?symbol=AAPL                         — yahoo quote
//	GET /history?symbol=AAPL&limit=120             — yahoo daily history (ascending)
//	GET /crypto-quote?venue=binance&symbol=BTCUSDT — binance/okx ticker
//	GET /funding?venue=binance&symbol=BTCUSDT      — perp funding rate
//	GET /open-interest?venue=binance&symbol=BTCUSDT
//	GET /long-short?venue=binance&symbol=BTCUSDT&period=1h   (okx: ccy=BTC&period=1D)
//	GET /depth?venue=binance&symbol=BTCUSDT&limit=100        (okx: symbol=BTC-USDT)
//	GET /finnhub?endpoint=/stock/profile2&symbol=AAPL        (key-attaching passthrough)
//	GET /cboe-quote?symbol=_VIX                              (CBOE delayed quote)
//	GET /dvol?currency=BTC                          — deribit implied-vol index
//	GET /option?instrument=BTC-25SEP26-66000-P      — deribit option IV+greeks
//	GET /feargreed?limit=1                          — alternative.me crypto Fear & Greed
//	GET /macro?series=DGS10&limit=60                — FRED keyless series (full history; limit=tail)
//	GET /short-file?date=20260625                    — FINRA RegSHO daily short-volume file
//	GET /edgar-filings?ticker=AAPL&limit=5           — SEC EDGAR recent filings
//	GET /form-13f?cik=0001067983&periods=2&limit=0   — SEC 13F-HR holdings
//	GET /health
package dataplaneapp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/Ricaardo/nimbus-os/datasources/alternativeme"
	"github.com/Ricaardo/nimbus-os/datasources/binance"
	"github.com/Ricaardo/nimbus-os/datasources/cboe"
	"github.com/Ricaardo/nimbus-os/datasources/deribit"
	"github.com/Ricaardo/nimbus-os/datasources/edgar"
	"github.com/Ricaardo/nimbus-os/datasources/envelope"
	"github.com/Ricaardo/nimbus-os/datasources/finnhub"
	"github.com/Ricaardo/nimbus-os/datasources/finra"
	"github.com/Ricaardo/nimbus-os/datasources/fred"
	"github.com/Ricaardo/nimbus-os/datasources/okx"
)

type runtimeConfig struct {
	dataAddr      string
	dataplaneAddr string
	serviceLabel  string
}

func configFromEnv(serviceLabel string) runtimeConfig {
	port := os.Getenv("DATAGW_PORT")
	if port == "" {
		port = "8821"
	}
	return runtimeConfig{
		dataAddr:      "127.0.0.1:" + port,
		dataplaneAddr: os.Getenv("DATAPLANE_ADDR"),
		serviceLabel:  serviceLabel,
	}
}

// Run starts the compatibility data core. serviceLabel only affects the
// listener banner; the HTTP contract continues to identify itself as datagw.
func Run(serviceLabel string) error {
	cfg := configFromEnv(serviceLabel)
	mux := newDatagwMux()
	go prewarmLoop()

	dataServer := &http.Server{Addr: cfg.dataAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	fmt.Printf("%s listening on http://%s\n", cfg.serviceLabel, cfg.dataAddr)
	if cfg.dataplaneAddr == "" {
		return dataServer.ListenAndServe()
	}

	dataplaneServer := newDataplaneServer(cfg.dataplaneAddr, mux)
	fmt.Printf("dataplane (facade dialect) listening on http://%s\n", cfg.dataplaneAddr)
	errs := make(chan error, 2)
	go func() { errs <- dataServer.ListenAndServe() }()
	go func() { errs <- fmt.Errorf("dataplane: %w", dataplaneServer.ListenAndServe()) }()
	return <-errs
}

func newDatagwMux() *http.ServeMux {
	return newDatagwMuxWithProducers(defaultCoreProducers())
}

// NewDataHandler assembles the shared legacy and migrated strict data routes.
// It does not start background work or bind a listener; the owning application
// controls that lifecycle.
func NewDataHandler() *http.ServeMux {
	return newDatagwMux()
}

func newDatagwMuxWithProducers(producers coreProducers) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "service": "datagw", "sources": []string{"yahoo", "binance", "okx", "deribit", "alternative.me", "fred", "finra", "edgar", "finnhub", "cboe", "fmp", "sec-xbrl"}})
	})
	mux.HandleFunc("/quote", func(w http.ResponseWriter, r *http.Request) {
		handleLegacyQuote(producers, w, r)
	})
	mux.HandleFunc("/history", func(w http.ResponseWriter, r *http.Request) {
		handleLegacyHistory(producers, w, r)
	})
	// Strict v1 is intentionally limited to producers that have migrated to an
	// explicit outcome. Unmigrated legacy routes are not exposed under /v1.
	mux.HandleFunc("/v1/quote", func(w http.ResponseWriter, r *http.Request) {
		handleStrictQuote(producers, w, r)
	})
	mux.HandleFunc("/v1/history", func(w http.ResponseWriter, r *http.Request) {
		handleStrictHistory(producers, w, r)
	})
	mux.HandleFunc("/crypto-quote", handleCryptoQuote)
	mux.HandleFunc("/funding", handleFunding)
	mux.HandleFunc("/open-interest", handleOpenInterest)
	mux.HandleFunc("/long-short", handleLongShort)
	mux.HandleFunc("/depth", handleDepth)
	mux.HandleFunc("/dvol", handleDVOL)
	mux.HandleFunc("/option", handleOption)
	mux.HandleFunc("/feargreed", handleFearGreed)
	mux.HandleFunc("/macro", handleMacro)
	mux.HandleFunc("/short-file", handleShortFile)
	mux.HandleFunc("/finnhub", handleFinnhub)
	mux.HandleFunc("/cboe-quote", handleCBOEQuote)
	mux.HandleFunc("/edgar-filings", handleEdgarFilings)
	mux.HandleFunc("/form-13f", handleForm13F)
	registerFacadeRoutes(mux) // shaped routes replacing the retired 8812/8820 (facade-facing)
	return mux
}

func ctx(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 25*time.Second)
}

func handleCryptoQuote(w http.ResponseWriter, r *http.Request) {
	venue, sym := r.URL.Query().Get("venue"), r.URL.Query().Get("symbol")
	c, cancel := ctx(r)
	defer cancel()
	switch venue {
	case "okx":
		t, err := okx.GetTicker(c, sym)
		respond(w, "okx", "market.ticker", map[string]any{"symbol": sym}, t, err)
	default:
		t, err := binance.Ticker24h(c, sym)
		respond(w, "binance", "ticker.24hr", map[string]any{"symbol": sym}, t, err)
	}
}

func handleFunding(w http.ResponseWriter, r *http.Request) {
	venue, sym := r.URL.Query().Get("venue"), r.URL.Query().Get("symbol")
	c, cancel := ctx(r)
	defer cancel()
	if venue == "okx" {
		f, err := okx.GetFunding(c, sym)
		respond(w, "okx", "public.funding-rate", map[string]any{"symbol": sym}, f, err)
		return
	}
	f, err := binance.FundingRate(c, sym)
	respond(w, "binance", "fapi.premiumIndex", map[string]any{"symbol": sym}, f, err)
}

func handleOpenInterest(w http.ResponseWriter, r *http.Request) {
	venue, sym := r.URL.Query().Get("venue"), r.URL.Query().Get("symbol")
	c, cancel := ctx(r)
	defer cancel()
	if venue == "okx" {
		oi, err := okx.GetOpenInterest(c, "SWAP", sym)
		respond(w, "okx", "public.open-interest", map[string]any{"symbol": sym}, oi, err)
		return
	}
	oi, err := binance.OpenInterestNow(c, sym)
	respond(w, "binance", "fapi.openInterest", map[string]any{"symbol": sym}, oi, err)
}

func handleLongShort(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	venue, period := q.Get("venue"), q.Get("period")
	c, cancel := ctx(r)
	defer cancel()
	if venue == "okx" {
		if period == "" {
			period = "1D"
		}
		ls, err := okx.LongShortRatio(c, q.Get("ccy"), period)
		respond(w, "okx", "rubik.long-short-account-ratio", map[string]any{"ccy": q.Get("ccy"), "period": period}, ls, err)
		return
	}
	if period == "" {
		period = "1h"
	}
	sym := q.Get("symbol")
	ls, err := binance.LongShortRatio(c, sym, period, atoiDefault(q.Get("limit"), 30))
	respond(w, "binance", "futures.globalLongShortAccountRatio", map[string]any{"symbol": sym, "period": period}, ls, err)
}

func handleDepth(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	venue, sym := q.Get("venue"), q.Get("symbol")
	c, cancel := ctx(r)
	defer cancel()
	if venue == "okx" {
		ob, err := okx.GetOrderBook(c, sym, atoiDefault(q.Get("limit"), 100))
		respond(w, "okx", "market.books", map[string]any{"symbol": sym}, ob, err)
		return
	}
	ob, err := binance.Depth(c, sym, atoiDefault(q.Get("limit"), 100))
	respond(w, "binance", "depth", map[string]any{"symbol": sym}, ob, err)
}

func handleDVOL(w http.ResponseWriter, r *http.Request) {
	cur := r.URL.Query().Get("currency")
	c, cancel := ctx(r)
	defer cancel()
	d, err := deribit.GetDVOL(c, cur, time.Now().Unix())
	respond(w, "deribit", "get_volatility_index_data", map[string]any{"currency": cur}, d, err)
}

func handleOption(w http.ResponseWriter, r *http.Request) {
	inst := r.URL.Query().Get("instrument")
	c, cancel := ctx(r)
	defer cancel()
	o, err := deribit.GetOptionTicker(c, inst)
	respond(w, "deribit", "ticker", map[string]any{"instrument": inst}, o, err)
}

func handleFearGreed(w http.ResponseWriter, r *http.Request) {
	limit := atoiDefault(r.URL.Query().Get("limit"), 1)
	c, cancel := ctx(r)
	defer cancel()
	fg, err := alternativeme.FearGreedIndex(c, limit)
	respond(w, "alternative.me", "fng", map[string]any{"limit": limit}, fg, err)
}

// handleMacro serves a FRED series via the keyed observations API (full history
// ascending; limit keeps only the most recent N). Values are emitted as JSON
// numbers with FRED's "." missing-marker as null — matching the legacy Python
// fred provider's {series, observations:[{date,value}]} shape. Requires
// FRED_API_KEY in datagw's environment (keys governed in one place).
func handleMacro(w http.ResponseWriter, r *http.Request) {
	series := r.URL.Query().Get("series")
	if series == "" {
		writeErr(w, 400, "series required")
		return
	}
	apiKey := os.Getenv("FRED_API_KEY")
	if apiKey == "" {
		writeErr(w, 503, "FRED_API_KEY not configured")
		return
	}
	limit := atoiDefault(r.URL.Query().Get("limit"), 0)
	c, cancel := ctx(r)
	defer cancel()
	// SortDesc=false → FRED natural ascending (oldest-first), full history.
	obs, err := fred.Observations(c, apiKey, fred.Query{Series: series})
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	if limit > 0 && len(obs) > limit {
		obs = obs[len(obs)-limit:]
	}
	rows := make([]map[string]any, 0, len(obs))
	for _, o := range obs {
		rows = append(rows, map[string]any{"date": o.Date, "value": fredValue(o.Value)})
	}
	writeEnv(w, envelope.Wrap("fred", "fredgraph.csv",
		map[string]any{"series": series, "limit": limit},
		map[string]any{"series": series, "observations": rows}))
}

// handleFinnhub is a thin passthrough to any Finnhub free-tier endpoint:
// GET /finnhub?endpoint=/stock/profile2&symbol=AAPL. datagw holds the
// FINNHUB_API_KEY (single key home) and attaches it upstream, returning the
// JSON verbatim inside the envelope's data so callers keep their own
// per-endpoint shaping (the data-gateway Python provider). NOTE: this route
// does NOT authenticate the caller — access control is the 127.0.0.1 bind in
// Run(); do not expose datagw beyond loopback without adding auth.
func handleFinnhub(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	endpoint := q.Get("endpoint")
	if endpoint == "" {
		writeErr(w, 400, "endpoint required (e.g. /stock/profile2)")
		return
	}
	apiKey := os.Getenv("FINNHUB_API_KEY")
	if apiKey == "" {
		writeErr(w, 503, "FINNHUB_API_KEY not configured")
		return
	}
	params := map[string]string{}
	echo := map[string]any{"endpoint": endpoint}
	for k, vs := range q {
		if k == "endpoint" || len(vs) == 0 {
			continue
		}
		params[k] = vs[0]
		echo[k] = vs[0]
	}
	c, cancel := ctx(r)
	defer cancel()
	raw, err := finnhub.Raw(c, apiKey, endpoint, params)
	respond(w, "finnhub", endpoint, echo, raw, err)
}

// handleCBOEQuote serves a CBOE delayed quote (current_price + IV30/bid/ask/
// OHLC) for an index symbol, e.g. /cboe-quote?symbol=_VIX. Keyless.
func handleCBOEQuote(w http.ResponseWriter, r *http.Request) {
	sym := r.URL.Query().Get("symbol")
	if sym == "" {
		writeErr(w, 400, "symbol required (e.g. _VIX)")
		return
	}
	c, cancel := ctx(r)
	defer cancel()
	q, err := cboe.GetQuote(c, sym)
	respond(w, "cboe", "delayed_quotes", map[string]any{"symbol": sym}, q, err)
}

// handleShortFile serves one trading day's FINRA RegSHO consolidated
// short-volume file. rows are {symbol: [short_vol, total_vol]} (matching the
// legacy Python finra provider); published=false when the file isn't out yet.
func handleShortFile(w http.ResponseWriter, r *http.Request) {
	date := r.URL.Query().Get("date")
	if date == "" {
		writeErr(w, 400, "date required (YYYYMMDD)")
		return
	}
	c, cancel := ctx(r)
	defer cancel()
	day, err := finra.RegSHODaily(c, date)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	rows := make(map[string][]float64, len(day))
	for sym, v := range day {
		rows[sym] = []float64{v.ShortVolume, v.TotalVolume}
	}
	writeEnv(w, envelope.Wrap("finra", "regsho.daily_file",
		map[string]any{"date": date},
		map[string]any{"date": date, "published": day != nil, "rows": rows}))
}

// handleEdgarFilings serves a ticker's recent SEC filings. ticker must already
// be normalized by the caller; the resolved CIK is echoed in params.
func handleEdgarFilings(w http.ResponseWriter, r *http.Request) {
	ticker := r.URL.Query().Get("ticker")
	if ticker == "" {
		writeErr(w, 400, "ticker required")
		return
	}
	limit := atoiDefault(r.URL.Query().Get("limit"), 5)
	c, cancel := ctx(r)
	defer cancel()
	cik, err := edgar.CIKForTicker(c, ticker)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	filings, err := edgar.RecentFilings(c, cik, limit)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeEnv(w, envelope.Wrap("edgar", "submissions.recent",
		map[string]any{"ticker": ticker, "cik": cik, "limit": limit}, filings))
}

// handleForm13F serves a CIK's latest 13F-HR holdings.
func handleForm13F(w http.ResponseWriter, r *http.Request) {
	cik := r.URL.Query().Get("cik")
	if cik == "" {
		writeErr(w, 400, "cik required")
		return
	}
	periods := atoiDefault(r.URL.Query().Get("periods"), 2)
	limit := atoiDefault(r.URL.Query().Get("limit"), 0)
	c, cancel := ctx(r)
	defer cancel()
	out, err := edgar.Holdings13F(c, cik, periods, limit)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeEnv(w, envelope.Wrap("edgar", "form_13f.holdings",
		map[string]any{"cik": cik, "periods": periods, "limit": limit},
		map[string]any{"periods": out}))
}

// fredValue converts a FRED observation value to a JSON number, or null for the
// "." missing-marker.
func fredValue(s string) any {
	if s == "." || s == "" || s == "NA" {
		return nil
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		return v
	}
	return nil
}

func respond(w http.ResponseWriter, provider, endpoint string, params map[string]any, data any, err error) {
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeEnv(w, envelope.Wrap(provider, endpoint, params, data))
}

func writeEnv(w http.ResponseWriter, e envelope.Envelope) { writeJSON(w, 200, e) }

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func atoiDefault(s string, d int) int {
	if s == "" {
		return d
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return d
}
