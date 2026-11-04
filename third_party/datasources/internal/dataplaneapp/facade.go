// facade.go — typed, shaped routes matching the retired Python reference-data
// service (8812) responses field-for-field, so the data-access facade can call
// datagw directly and Tier-2 consumers (which read only the `data` key) see
// identical rows. Shaping semantics ported 1:1 from the Python providers; do
// not "simplify" the quirks (×1e6 units, no-limit FMP, sort orders) — they are
// the contract.
//
// Routes (facade-facing):
//
//	GET /ratings?symbol=US:AAPL&limit=6
//	GET /profile?symbol=US:AAPL
//	GET /earnings-calendar?from=&to=&symbol=
//	GET /earnings?symbol=&limit=
//	GET /insider?symbol=&from=&to=
//	GET /company-news?symbol=&from=&to=&limit=
//	GET /peers?symbol=
//	GET /basic-financials?symbol=&metric=all
//	GET /search?q=
//	GET /market-status?exchange=US
//	GET /macro-rows?series=DGS10&limit=60        — [{date,<series>:value}] rows
//	GET /economic-calendar?from=&to=&limit=200   — FRED releases/dates
//	GET /fmp/income-statement|balance-sheet|cash-flow?symbol=&period=&limit=
//	GET /fmp/dividends?symbol=&limit=
//	GET /short?symbol=US:NVDA&days=8             — FINRA RegSHO aggregation
//	GET /filings?symbol=US:AAPL&limit=5          — EDGAR (canonical symbol in)
//	GET /funding-rate?symbol=BTC                 — shaped, binance→okx fallback
//	GET /sec-xbrl?path=/api/xbrl/companyfacts/CIK0000320193.json — passthrough
package dataplaneapp

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Ricaardo/nimbus-os/datasources/binance"
	"github.com/Ricaardo/nimbus-os/datasources/edgar"
	"github.com/Ricaardo/nimbus-os/datasources/envelope"
	"github.com/Ricaardo/nimbus-os/datasources/finnhub"
	"github.com/Ricaardo/nimbus-os/datasources/finra"
	"github.com/Ricaardo/nimbus-os/datasources/fmp"
	"github.com/Ricaardo/nimbus-os/datasources/fred"
	"github.com/Ricaardo/nimbus-os/datasources/internal/httpx"
	"github.com/Ricaardo/nimbus-os/datasources/okx"
	"github.com/Ricaardo/nimbus-os/datasources/symbols"
)

// ── TTL cache (reference data is slow-moving; mirrors 8812's 900s TTL) ──────

var facadeTTL = envDuration("DATAGW_FACADE_TTL_SEC", 900)

type cacheEntry struct {
	at  time.Time
	ttl time.Duration
	env envelope.Envelope
}

// maxCacheEntries bounds the cache (review finding: /sec-xbrl bodies can be
// multi-MB and a 3000-symbol enrich run would otherwise hold them all resident).
var maxCacheEntries = atoiDefault(os.Getenv("DATAGW_CACHE_MAX_ENTRIES"), 2048)

var (
	cacheMu sync.Mutex
	cacheM  = map[string]cacheEntry{}
)

func cacheGet(key string) (envelope.Envelope, bool) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	e, ok := cacheM[key]
	if !ok || time.Since(e.at) > e.ttl {
		delete(cacheM, key) // reclaim expired entries on read
		return envelope.Envelope{}, false
	}
	return e.env, true
}

func cachePut(key string, ttl time.Duration, env envelope.Envelope) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if len(cacheM) >= maxCacheEntries {
		evictLocked()
	}
	cacheM[key] = cacheEntry{at: time.Now(), ttl: ttl, env: env}
}

// evictLocked drops expired entries; if none were expired, drops the oldest
// ~10% so a put always finds room. Caller holds cacheMu.
func evictLocked() {
	before := len(cacheM)
	for k, e := range cacheM {
		if time.Since(e.at) > e.ttl {
			delete(cacheM, k)
		}
	}
	if len(cacheM) < before {
		return
	}
	drop := len(cacheM)/10 + 1
	type kv struct {
		k  string
		at time.Time
	}
	oldest := make([]kv, 0, len(cacheM))
	for k, e := range cacheM {
		oldest = append(oldest, kv{k, e.at})
	}
	sort.Slice(oldest, func(i, j int) bool { return oldest[i].at.Before(oldest[j].at) })
	for i := 0; i < drop && i < len(oldest); i++ {
		delete(cacheM, oldest[i].k)
	}
}

// serveCached runs fn once per TTL window and replays the envelope otherwise.
func serveCached(w http.ResponseWriter, key string, ttl time.Duration, fn func() (envelope.Envelope, error)) {
	if env, ok := cacheGet(key); ok {
		writeEnv(w, env)
		return
	}
	env, err := fn()
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	cachePut(key, ttl, env)
	writeEnv(w, env)
}

func envDuration(name string, defSec int) time.Duration {
	if v := os.Getenv(name); v != "" {
		if n := atoiDefault(v, defSec); n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return time.Duration(defSec) * time.Second
}

// ── finnhub shaped routes (ported from finnhub_provider.py) ─────────────────

func finnhubJSON(ctx context.Context, endpoint string, params map[string]string) (any, error) {
	apiKey := os.Getenv("FINNHUB_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("FINNHUB_API_KEY not configured")
	}
	raw, err := finnhub.Raw(ctx, apiKey, endpoint, params)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func handleRatings(w http.ResponseWriter, r *http.Request) {
	sym, limit := r.URL.Query().Get("symbol"), atoiDefault(r.URL.Query().Get("limit"), 6)
	serveCached(w, "r:"+sym+":"+itoa(limit), facadeTTL, func() (envelope.Envelope, error) {
		ticker, err := symbols.EdgarTicker(sym)
		if err != nil {
			return envelope.Envelope{}, err
		}
		c, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		raw, err := finnhubJSON(c, "/stock/recommendation", map[string]string{"symbol": ticker})
		if err != nil {
			return envelope.Envelope{}, err
		}
		rows := asList(raw)
		if len(rows) > limit {
			rows = rows[:limit]
		}
		return envelope.Wrap("finnhub", "stock.recommendation",
			map[string]any{"symbol": sym, "ticker": ticker, "limit": limit}, rows), nil
	})
}

func handleProfile(w http.ResponseWriter, r *http.Request) {
	sym := r.URL.Query().Get("symbol")
	serveCached(w, "p:"+sym, facadeTTL, func() (envelope.Envelope, error) {
		ticker, err := symbols.EdgarTicker(sym)
		if err != nil {
			return envelope.Envelope{}, err
		}
		c, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		raw, err := finnhubJSON(c, "/stock/profile2", map[string]string{"symbol": ticker})
		if err != nil {
			return envelope.Envelope{}, err
		}
		rows := []any{}
		if m, ok := raw.(map[string]any); ok && len(m) > 0 {
			// Finnhub reports marketCap/sharesOutstanding in MILLIONS; emit absolute.
			rows = append(rows, map[string]any{
				"symbol":            sym,
				"ticker":            ticker,
				"name":              m["name"],
				"sector":            m["finnhubIndustry"],
				"industry":          m["finnhubIndustry"],
				"exchange":          m["exchange"],
				"currency":          m["currency"],
				"country":           m["country"],
				"marketCap":         mul1e6(m["marketCapitalization"]),
				"sharesOutstanding": mul1e6(m["shareOutstanding"]),
				"ipo":               m["ipo"],
				"weburl":            m["weburl"],
			})
		}
		return envelope.Wrap("finnhub", "stock.profile2",
			map[string]any{"symbol": sym, "ticker": ticker}, rows), nil
	})
}

func handleEarningsCalendar(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, to, sym := q.Get("from"), q.Get("to"), q.Get("symbol")
	serveCached(w, "ec:"+from+":"+to+":"+sym, facadeTTL, func() (envelope.Envelope, error) {
		params := map[string]string{"from": from, "to": to}
		if sym != "" {
			ticker, err := symbols.EdgarTicker(sym)
			if err != nil {
				return envelope.Envelope{}, err
			}
			params["symbol"] = ticker
		}
		c, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		raw, err := finnhubJSON(c, "/calendar/earnings", params)
		if err != nil {
			return envelope.Envelope{}, err
		}
		rows := []any{}
		if m, ok := raw.(map[string]any); ok {
			rows = asList(m["earningsCalendar"])
		}
		sortByStr(rows, "date", false)
		return envelope.Wrap("finnhub", "calendar.earnings",
			map[string]any{"from": from, "to": to, "symbol": sym}, rows), nil
	})
}

func handleEarnings(w http.ResponseWriter, r *http.Request) {
	sym, limit := r.URL.Query().Get("symbol"), atoiDefault(r.URL.Query().Get("limit"), 8)
	serveCached(w, "e:"+sym+":"+itoa(limit), facadeTTL, func() (envelope.Envelope, error) {
		ticker, err := symbols.EdgarTicker(sym)
		if err != nil {
			return envelope.Envelope{}, err
		}
		c, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		raw, err := finnhubJSON(c, "/stock/earnings", map[string]string{"symbol": ticker})
		if err != nil {
			return envelope.Envelope{}, err
		}
		rows := asList(raw)
		if len(rows) > limit {
			rows = rows[:limit]
		}
		return envelope.Wrap("finnhub", "stock.earnings",
			map[string]any{"symbol": sym, "ticker": ticker, "limit": limit}, rows), nil
	})
}

func handleInsider(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sym, from, to := q.Get("symbol"), q.Get("from"), q.Get("to")
	serveCached(w, "ins:"+sym+":"+from+":"+to, facadeTTL, func() (envelope.Envelope, error) {
		ticker, err := symbols.EdgarTicker(sym)
		if err != nil {
			return envelope.Envelope{}, err
		}
		params := map[string]string{"symbol": ticker}
		if from != "" {
			params["from"] = from
		}
		if to != "" {
			params["to"] = to
		}
		c, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		raw, err := finnhubJSON(c, "/stock/insider-transactions", params)
		if err != nil {
			return envelope.Envelope{}, err
		}
		rows := []any{}
		if m, ok := raw.(map[string]any); ok {
			rows = asList(m["data"])
		}
		sortByStr(rows, "filingDate", true)
		return envelope.Wrap("finnhub", "stock.insider-transactions",
			map[string]any{"symbol": sym, "ticker": ticker, "from": from, "to": to}, rows), nil
	})
}

func handleCompanyNews(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sym, from, to := q.Get("symbol"), q.Get("from"), q.Get("to")
	limit := atoiDefault(q.Get("limit"), 20)
	serveCached(w, "cn:"+sym+":"+from+":"+to+":"+itoa(limit), facadeTTL, func() (envelope.Envelope, error) {
		ticker, err := symbols.EdgarTicker(sym)
		if err != nil {
			return envelope.Envelope{}, err
		}
		if to == "" {
			to = time.Now().UTC().Format("2006-01-02")
		}
		if from == "" {
			from = time.Now().UTC().Add(-7 * 24 * time.Hour).Format("2006-01-02")
		}
		c, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		raw, err := finnhubJSON(c, "/company-news", map[string]string{"symbol": ticker, "from": from, "to": to})
		if err != nil {
			return envelope.Envelope{}, err
		}
		rows := asList(raw)
		sortByNum(rows, "datetime", true)
		if len(rows) > limit {
			rows = rows[:limit]
		}
		return envelope.Wrap("finnhub", "company-news",
			map[string]any{"symbol": sym, "ticker": ticker, "from": from, "to": to, "limit": limit}, rows), nil
	})
}

func handlePeers(w http.ResponseWriter, r *http.Request) {
	sym := r.URL.Query().Get("symbol")
	serveCached(w, "pe:"+sym, facadeTTL, func() (envelope.Envelope, error) {
		ticker, err := symbols.EdgarTicker(sym)
		if err != nil {
			return envelope.Envelope{}, err
		}
		c, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		raw, err := finnhubJSON(c, "/stock/peers", map[string]string{"symbol": ticker})
		if err != nil {
			return envelope.Envelope{}, err
		}
		return envelope.Wrap("finnhub", "stock.peers",
			map[string]any{"symbol": sym, "ticker": ticker}, asList(raw)), nil
	})
}

func handleBasicFinancials(w http.ResponseWriter, r *http.Request) {
	sym, metric := r.URL.Query().Get("symbol"), r.URL.Query().Get("metric")
	if metric == "" {
		metric = "all"
	}
	serveCached(w, "bf:"+sym+":"+metric, facadeTTL, func() (envelope.Envelope, error) {
		ticker, err := symbols.EdgarTicker(sym)
		if err != nil {
			return envelope.Envelope{}, err
		}
		c, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		raw, err := finnhubJSON(c, "/stock/metric", map[string]string{"symbol": ticker, "metric": metric})
		if err != nil {
			return envelope.Envelope{}, err
		}
		data, _ := raw.(map[string]any)
		if data == nil {
			data = map[string]any{}
		}
		return envelope.Wrap("finnhub", "stock.metric",
			map[string]any{"symbol": sym, "ticker": ticker, "metric": metric}, data), nil
	})
}

func handleSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	serveCached(w, "se:"+query, facadeTTL, func() (envelope.Envelope, error) {
		c, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		raw, err := finnhubJSON(c, "/search", map[string]string{"q": query})
		if err != nil {
			return envelope.Envelope{}, err
		}
		rows := []any{}
		if m, ok := raw.(map[string]any); ok {
			rows = asList(m["result"])
		}
		return envelope.Wrap("finnhub", "search", map[string]any{"q": query}, rows), nil
	})
}

func handleMarketStatus(w http.ResponseWriter, r *http.Request) {
	exchange := r.URL.Query().Get("exchange")
	if exchange == "" {
		exchange = "US"
	}
	serveCached(w, "ms:"+exchange, facadeTTL, func() (envelope.Envelope, error) {
		c, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		raw, err := finnhubJSON(c, "/stock/market-status", map[string]string{"exchange": exchange})
		if err != nil {
			return envelope.Envelope{}, err
		}
		data, _ := raw.(map[string]any)
		if data == nil {
			data = map[string]any{}
		}
		return envelope.Wrap("finnhub", "stock.market-status", map[string]any{"exchange": exchange}, data), nil
	})
}

// ── FRED rows shape + economic calendar (ported from reference_data_svc) ────

func macroRowsEnv(ctx context.Context, series string, limit int) (envelope.Envelope, error) {
	apiKey := os.Getenv("FRED_API_KEY")
	if apiKey == "" {
		return envelope.Envelope{}, fmt.Errorf("FRED_API_KEY not configured")
	}
	obs, err := fred.Observations(ctx, apiKey, fred.Query{Series: series})
	if err != nil {
		return envelope.Envelope{}, err
	}
	if limit > 0 && len(obs) > limit {
		obs = obs[len(obs)-limit:]
	}
	rows := make([]any, 0, len(obs))
	for _, o := range obs {
		// Value column is named after the series (OpenBB-compatible shape).
		rows = append(rows, map[string]any{"date": o.Date, series: fredValue(o.Value)})
	}
	return envelope.Wrap("fred", "fredgraph.csv",
		map[string]any{"series": series, "limit": limit}, rows), nil
}

func handleMacroRows(w http.ResponseWriter, r *http.Request) {
	series := r.URL.Query().Get("series")
	if series == "" {
		writeErr(w, 400, "series required")
		return
	}
	limit := atoiDefault(r.URL.Query().Get("limit"), 60)
	serveCached(w, "mr:"+series+":"+itoa(limit), facadeTTL, func() (envelope.Envelope, error) {
		c, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		return macroRowsEnv(c, series, limit)
	})
}

func econCalendarEnv(ctx context.Context, from, to string, limit int) (envelope.Envelope, error) {
	apiKey := os.Getenv("FRED_API_KEY")
	if apiKey == "" {
		// Parity with the retired 8812: missing key degrades to empty, not error.
		return envelope.Wrap("fred", "releases.dates",
			map[string]any{"error": "missing FRED_API_KEY"}, []any{}), nil
	}
	fromD, toD := from, to
	if fromD == "" {
		fromD = time.Now().UTC().Format("2006-01-02")
	}
	if toD == "" {
		toD = time.Now().UTC().Add(14 * 24 * time.Hour).Format("2006-01-02")
	}
	rds, err := fred.ReleaseDates(ctx, apiKey, fromD, toD)
	if err != nil {
		return envelope.Envelope{}, err
	}
	seen := map[string]bool{}
	rows := []any{}
	for _, rd := range rds {
		key := rd.Date + "|" + rd.ReleaseName
		if rd.Date < fromD || rd.Date > toD || seen[key] {
			continue
		}
		seen[key] = true
		rows = append(rows, map[string]any{
			"date": rd.Date, "event": rd.ReleaseName,
			"country": "US", "currency": "USD", "release_id": rd.ReleaseID,
		})
	}
	sortByStr(rows, "date", false)
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return envelope.Wrap("fred", "releases.dates",
		map[string]any{"from": fromD, "to": toD, "limit": limit}, rows), nil
}

func handleEconomicCalendar(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, to := q.Get("from"), q.Get("to")
	limit := atoiDefault(q.Get("limit"), 200)
	// Key on the RAW params so the no-arg prewarm entry serves the common call.
	serveCached(w, "econ:"+from+":"+to+":"+itoa(limit), facadeTTL, func() (envelope.Envelope, error) {
		c, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		return econCalendarEnv(c, from, to, limit)
	})
}

// prewarmLoop keeps the hot FRED series + the default economic-calendar window
// warm (a cold FRED fetch can exceed the facade/SDK 20s timeout budget).
func prewarmLoop() {
	seriesList := strings.Split(getenvDefault("DATAGW_MACRO_PREWARM",
		"DFF,DGS10,DGS2,DGS3MO,DGS30,T10Y2Y,T10Y3M,T10YIE,VIXCLS,DTWEXBGS,"+
			"BAMLH0A0HYM2,WALCL,RRPONTSYD,WTREGEN,M2SL,SOFR,EFFR,CPIAUCSL,UNRATE,PAYEMS"), ",")
	macroLimit := atoiDefault(os.Getenv("DATAGW_MACRO_LIMIT"), 60)
	interval := envDuration("DATAGW_REWARM_SEC", 600)
	for {
		for _, s := range seriesList {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			c, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			if env, err := macroRowsEnv(c, s, macroLimit); err == nil {
				cachePut("mr:"+s+":"+itoa(macroLimit), interval+facadeTTL, env)
			}
			cancel()
		}
		c, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		if env, err := econCalendarEnv(c, "", "", 200); err == nil {
			cachePut("econ:::200", interval+facadeTTL, env)
		}
		cancel()
		time.Sleep(interval)
	}
}

// ── FMP statements/dividends (free-tier semantics live in the fmp package) ──

func handleFMPStatement(endpoint string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		sym, period := q.Get("symbol"), q.Get("period")
		if period == "" {
			period = "quarter"
		}
		limit := atoiDefault(q.Get("limit"), 8)
		serveCached(w, "fmp:"+endpoint+":"+sym+":"+period+":"+itoa(limit), facadeTTL, func() (envelope.Envelope, error) {
			apiKey := os.Getenv("FMP_API_KEY")
			if apiKey == "" {
				// Parity with the retired 8812: missing key degrades to empty, not error.
				return envelope.Wrap("fmp", "stable/"+endpoint,
					map[string]any{"symbol": sym, "error": "missing FMP_API_KEY"}, []any{}), nil
			}
			c, cancel := context.WithTimeout(r.Context(), 25*time.Second)
			defer cancel()
			rows, err := fmp.Statement(c, apiKey, endpoint, sym, period, limit)
			if err != nil {
				return envelope.Envelope{}, err
			}
			return envelope.Wrap("fmp", "stable/"+endpoint,
				map[string]any{"symbol": sym, "period": period, "limit": limit}, rows), nil
		})
	}
}

func handleFMPDividends(w http.ResponseWriter, r *http.Request) {
	sym := r.URL.Query().Get("symbol")
	limit := atoiDefault(r.URL.Query().Get("limit"), 40)
	serveCached(w, "fmp:div:"+sym+":"+itoa(limit), facadeTTL, func() (envelope.Envelope, error) {
		apiKey := os.Getenv("FMP_API_KEY")
		if apiKey == "" {
			// Parity with the retired 8812: missing key degrades to empty, not error.
			return envelope.Wrap("fmp", "stable/dividends",
				map[string]any{"symbol": sym, "error": "missing FMP_API_KEY"}, []any{}), nil
		}
		c, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		rows, err := fmp.Dividends(c, apiKey, sym, limit)
		if err != nil {
			return envelope.Envelope{}, err
		}
		return envelope.Wrap("fmp", "stable/dividends",
			map[string]any{"symbol": sym, "limit": limit}, rows), nil
	})
}

// ── FINRA short-volume aggregation (ported from finra_provider.short_volume) ─

func regshoDay(ctx context.Context, date string) (map[string]finra.ShortVol, error) {
	key := "finra:" + date
	if env, ok := cacheGet(key); ok {
		rows, _ := env.Data.(map[string]finra.ShortVol)
		return rows, nil
	}
	day, err := finra.RegSHODaily(ctx, date)
	if err != nil || day == nil {
		return nil, err // unpublished days are not cached (they may appear later)
	}
	cachePut(key, 24*time.Hour, envelope.Envelope{Data: day})
	return day, nil
}

func handleShort(w http.ResponseWriter, r *http.Request) {
	sym := r.URL.Query().Get("symbol")
	days := atoiDefault(r.URL.Query().Get("days"), 8)
	serveCached(w, "s:"+sym+":"+itoa(days), facadeTTL, func() (envelope.Envelope, error) {
		ticker, err := symbols.EdgarTicker(sym)
		if err != nil {
			return envelope.Envelope{}, err
		}
		c, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		type dayHit struct {
			date string
			rows map[string]finra.ShortVol
		}
		collected := []dayHit{}
		// Look back days+7 calendar days (mirrors _recent_dates(days+3) + 4 pad).
		for i := 0; i < days+7 && len(collected) < days; i++ {
			d := time.Now().AddDate(0, 0, -i).Format("20060102")
			rows, _ := regshoDay(c, d)
			if len(rows) > 0 {
				collected = append(collected, dayHit{d, rows})
			}
		}
		if len(collected) == 0 {
			return envelope.Envelope{}, fmt.Errorf("no FINRA daily short-volume file available in window")
		}
		var shortSum, totalSum float64
		used := []any{}
		for _, dh := range collected {
			if v, ok := dh.rows[ticker]; ok {
				shortSum += v.ShortVolume
				totalSum += v.TotalVolume
				used = append(used, map[string]any{"date": dh.date, "short_vol": v.ShortVolume, "total_vol": v.TotalVolume})
			}
		}
		var ratio any
		if totalSum > 0 {
			ratio = math.Round(shortSum/totalSum*1e4) / 1e4
		}
		return envelope.Wrap("finra", "regsho.daily_short_volume",
			map[string]any{"symbol": sym, "ticker": ticker, "days": days},
			map[string]any{"symbol": ticker, "short_ratio": ratio, "short_days": len(used),
				"short_vol": shortSum, "total_vol": totalSum, "daily": used}), nil
	})
}

// ── EDGAR filings by canonical symbol ────────────────────────────────────────

func handleFilings(w http.ResponseWriter, r *http.Request) {
	sym := r.URL.Query().Get("symbol")
	limit := atoiDefault(r.URL.Query().Get("limit"), 5)
	serveCached(w, "f:"+sym+":"+itoa(limit), facadeTTL, func() (envelope.Envelope, error) {
		ticker, err := symbols.EdgarTicker(sym)
		if err != nil {
			return envelope.Envelope{}, err
		}
		c, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		cik, err := edgar.CIKForTicker(c, ticker)
		if err != nil {
			return envelope.Envelope{}, err
		}
		filings, err := edgar.RecentFilings(c, cik, limit)
		if err != nil {
			return envelope.Envelope{}, err
		}
		return envelope.Wrap("edgar", "submissions.recent",
			map[string]any{"symbol": sym, "ticker": ticker, "cik": cik, "limit": limit}, filings), nil
	})
}

// ── shaped funding rate (binance → okx fallback; ported from 8812 funding) ──

func handleFundingRate(w http.ResponseWriter, r *http.Request) {
	base := strings.ToUpper(r.URL.Query().Get("symbol"))
	if base == "" {
		base = "BTC"
	}
	base = strings.NewReplacer("-USD", "", "USDT", "", "CRYPTO:", "").Replace(base)
	serveCached(w, "fund:"+base, facadeTTL, func() (envelope.Envelope, error) {
		c, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		asOf := time.Now().UTC().Format("2006-01-02")
		row := func(pct float64, source string) envelope.Envelope {
			return envelope.Wrap(source, "funding-rate", map[string]any{"symbol": base},
				[]any{map[string]any{"symbol": base,
					"funding_rate_pct": math.Round(pct*1e6) / 1e6, "source": source, "as_of": asOf}})
		}
		if f, err := binance.FundingRate(c, base+"USDT"); err == nil && f != nil {
			return row(f.Rate*100, "binance"), nil
		}
		if f, err := okx.GetFunding(c, base+"-USDT-SWAP"); err == nil && f != nil {
			return row(f.Rate*100, "okx"), nil
		}
		// Degrade like 8812 did: empty data, not an error.
		return envelope.Wrap("none", "funding-rate", map[string]any{"symbol": base}, []any{}), nil
	})
}

// ── SEC XBRL passthrough (data.sec.gov; host-locked, path-allowlisted) ──────

var secUA = getenvDefault("SEC_USER_AGENT", "nimbus-os datagw championdoggg@gmail.com")

// secXBRLCacheMax: bodies above this size (bytes) are served but NOT cached —
// companyfacts JSONs run multi-MB and would dominate the bounded cache.
var secXBRLCacheMax = atoiDefault(os.Getenv("DATAGW_SECXBRL_CACHE_MAX"), 262144)

// secXBRLPathOK validates the data.sec.gov passthrough path: prefix allowlist,
// no traversal, no scheme/host smuggling. Host is hardcoded by the caller.
func secXBRLPathOK(path string) bool {
	if !strings.HasPrefix(path, "/api/xbrl/") && !strings.HasPrefix(path, "/submissions/") {
		return false
	}
	if strings.Contains(path, "..") || strings.Contains(path, "//") ||
		strings.ContainsAny(path, "?#\\ ") {
		return false
	}
	return true
}

func handleSECXBRL(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if !secXBRLPathOK(path) {
		writeErr(w, 400, "path must start with /api/xbrl/ or /submissions/ (no traversal/query)")
		return
	}
	key := "xbrl:" + path
	if env, ok := cacheGet(key); ok {
		writeEnv(w, env)
		return
	}
	c, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	body, err := httpx.Default.GetBytes(c, "https://data.sec.gov"+path,
		map[string]string{"User-Agent": secUA, "Accept": "application/json"})
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	var out any
	if err := json.Unmarshal(body, &out); err != nil {
		writeErr(w, 502, "sec: non-JSON response")
		return
	}
	env := envelope.Wrap("sec", "data.sec.gov"+path, map[string]any{"path": path}, out)
	if len(body) <= secXBRLCacheMax {
		cachePut(key, facadeTTL, env)
	}
	writeEnv(w, env)
}

// ── small shared helpers ─────────────────────────────────────────────────────

func asList(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	return []any{}
}

func mul1e6(v any) any {
	if f, ok := v.(float64); ok {
		return f * 1e6
	}
	return nil
}

func strAt(row any, key string) string {
	if m, ok := row.(map[string]any); ok {
		if s, ok := m[key].(string); ok {
			return s
		}
	}
	return ""
}

func numAt(row any, key string) float64 {
	if m, ok := row.(map[string]any); ok {
		if f, ok := m[key].(float64); ok {
			return f
		}
	}
	return 0
}

func sortByStr(rows []any, key string, desc bool) {
	sort.SliceStable(rows, func(i, j int) bool {
		if desc {
			return strAt(rows[i], key) > strAt(rows[j], key)
		}
		return strAt(rows[i], key) < strAt(rows[j], key)
	})
}

func sortByNum(rows []any, key string, desc bool) {
	sort.SliceStable(rows, func(i, j int) bool {
		if desc {
			return numAt(rows[i], key) > numAt(rows[j], key)
		}
		return numAt(rows[i], key) < numAt(rows[j], key)
	})
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }

func getenvDefault(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// registerFacadeRoutes wires the shaped routes into the shared datagw mux.
func registerFacadeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/ratings", handleRatings)
	mux.HandleFunc("/profile", handleProfile)
	mux.HandleFunc("/earnings-calendar", handleEarningsCalendar)
	mux.HandleFunc("/earnings", handleEarnings)
	mux.HandleFunc("/insider", handleInsider)
	mux.HandleFunc("/company-news", handleCompanyNews)
	mux.HandleFunc("/peers", handlePeers)
	mux.HandleFunc("/basic-financials", handleBasicFinancials)
	mux.HandleFunc("/search", handleSearch)
	mux.HandleFunc("/market-status", handleMarketStatus)
	mux.HandleFunc("/macro-rows", handleMacroRows)
	mux.HandleFunc("/economic-calendar", handleEconomicCalendar)
	mux.HandleFunc("/fmp/income-statement", handleFMPStatement("income-statement"))
	mux.HandleFunc("/fmp/balance-sheet", handleFMPStatement("balance-sheet-statement"))
	mux.HandleFunc("/fmp/cash-flow", handleFMPStatement("cash-flow-statement"))
	mux.HandleFunc("/fmp/dividends", handleFMPDividends)
	mux.HandleFunc("/short", handleShort)
	mux.HandleFunc("/filings", handleFilings)
	mux.HandleFunc("/funding-rate", handleFundingRate)
	mux.HandleFunc("/sec-xbrl", handleSECXBRL)
}
