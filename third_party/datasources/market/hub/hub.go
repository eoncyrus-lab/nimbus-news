// Package hub is the HTTP handler layer for the market service — the
// facade-shaped /quote and /kline surface. Extracted from cmd/market-hub so
// the same handlers can serve the standalone market-hub binary and be mounted
// in-process by the datagw dataplane listener (R6-b).
package hub

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Ricaardo/nimbus-os/datasources/market"
	types "github.com/Ricaardo/nimbus-os/datasources/market/types"
)

// Hub owns the market service and the Futu-worker delegation config.
type Hub struct {
	Svc        *market.MarketService
	futuWorker string
}

func New() *Hub {
	fw := os.Getenv("MARKET_FUTU_WORKER")
	if fw == "" {
		fw = "http://127.0.0.1:8810"
	}
	return &Hub{Svc: market.NewMarketService(), futuWorker: fw}
}

// Register mounts the hub surface on mux.
func (h *Hub) Register(mux *http.ServeMux) {
	mux.HandleFunc("/health", h.HandleHealth)
	mux.HandleFunc("/quote", h.HandleQuote)
	mux.HandleFunc("/kline", h.HandleKline)
}

// ToHubSymbol maps a data-access facade canonical symbol to the native ticker
// the market adapters expect. Bare native tickers (^VIX, CL=F, AAPL, BTC-USDT)
// pass through unchanged.
//
//	US:AAPL -> AAPL   HK:00700 -> 0700.HK   CN:600519 -> 600519
//	CRYPTO:BTC-USD -> BTC-USDT   IDX:VIX -> ^VIX   FUT:CL -> CL=F
func ToHubSymbol(canonical string) string {
	s := strings.TrimSpace(canonical)
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return s // native passthrough
	}
	mk, loc := strings.ToUpper(s[:i]), s[i+1:]
	switch mk {
	case "US":
		return loc
	case "CN":
		// Map to Yahoo-native exchange suffix for the internal adapter chain.
		if strings.HasPrefix(loc, "6") {
			return loc + ".SS"
		}
		return loc + ".SZ"
	case "HK":
		t := strings.TrimLeft(loc, "0")
		for len(t) < 4 {
			t = "0" + t
		}
		return t + ".HK"
	case "CRYPTO":
		u := strings.ToUpper(loc)
		if strings.HasSuffix(u, "-USD") {
			return u[:len(u)-4] + "-USDT"
		}
		return u
	case "IDX":
		if strings.HasPrefix(loc, "^") {
			return loc
		}
		return "^" + loc
	case "FUT":
		if strings.HasSuffix(loc, "=F") {
			return loc
		}
		return loc + "=F"
	default:
		return loc
	}
}

// isFutuEligible: US/HK stocks & ETFs go to the Futu worker; CN (A-shares),
// bare native tickers (^VIX, CL=F, BTC-USDT) and IDX/FUT/CRYPTO go to the
// internal adapter chain (market-data-svc → Yahoo fallback).
func isFutuEligible(canonical string) bool {
	i := strings.IndexByte(strings.TrimSpace(canonical), ':')
	if i < 0 {
		return false
	}
	switch strings.ToUpper(strings.TrimSpace(canonical)[:i]) {
	case "US", "HK":
		return true
	}
	return false
}

// proxyJSON GETs url and writes the raw body through (worker responses are already
// in the facade shape). Returns false on failure so the caller can fall back.
// Timeout is generous: the index/futures kline fallback goes worker -> Yahoo,
// which routinely takes ~10s and spikes higher under concurrent load; 15s was too
// tight and made the index-kline smoke flake (timeout -> empty data).
func proxyJSON(w http.ResponseWriter, url string) bool {
	c := &http.Client{Timeout: 30 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return false
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(200)
	_, _ = w.Write(body)
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// workerQuotes fetches US/HK/CN quotes from the persistent Futu worker; its
// response is already facade-shaped, so we return the data rows directly.
func (h *Hub) workerQuotes(canonicals []string) ([]map[string]any, error) {
	u := h.futuWorker + "/quote?symbols=" + url.QueryEscape(strings.Join(canonicals, ","))
	c := &http.Client{Timeout: 15 * time.Second}
	resp, err := c.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	return parsed.Data, nil
}

// quoteItem mirrors the facade /quote shape (Price -> last).
func quoteItem(canonical string, q *types.MarketQuote) map[string]any {
	return map[string]any{
		"symbol": canonical, "name": q.Name,
		"last": q.Price, "prev_close": q.PrevClose,
		"open": q.Open, "high": q.High, "low": q.Low,
		"volume": q.Volume, "change": q.Change, "change_pct": q.ChangePercent,
		"asset_type": q.AssetType, "provider": "market-hub",
		"ts": q.UpdateTime.UTC().Format(time.RFC3339),
	}
}

func (h *Hub) HandleQuote(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("symbols")
	if raw == "" {
		writeJSON(w, 400, map[string]any{"error": "symbols required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	errs := map[string]string{}
	// Preserve the caller's symbol order regardless of which backend serves each
	// symbol: internal adapters resolve inline, Futu-eligible symbols batch to the
	// worker, then we reassemble by the original request order.
	var order []string
	rowBySym := map[string]map[string]any{}
	var futuSyms []string
	for _, s := range strings.Split(raw, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		order = append(order, s)
		if isFutuEligible(s) {
			futuSyms = append(futuSyms, s)
			continue
		}
		q, err := h.Svc.GetMarketQuote(ctx, ToHubSymbol(s))
		if err != nil || q == nil {
			if err != nil {
				errs[s] = err.Error()
			}
			continue
		}
		rowBySym[s] = quoteItem(s, q)
	}
	if len(futuSyms) > 0 {
		rows, err := h.workerQuotes(futuSyms)
		if err != nil {
			errs["futu"] = err.Error()
		} else {
			for _, row := range rows {
				if sym, ok := row["symbol"].(string); ok {
					rowBySym[sym] = row
				}
			}
		}
	}
	data := make([]map[string]any, 0, len(order))
	for _, s := range order {
		if row, ok := rowBySym[s]; ok {
			data = append(data, row)
		}
	}
	writeJSON(w, 200, map[string]any{"data": data, "errors": errs, "provenance": map[string]any{"source": "market-hub"}})
}

func (h *Hub) HandleKline(w http.ResponseWriter, r *http.Request) {
	sym := strings.TrimSpace(r.URL.Query().Get("symbol"))
	if sym == "" {
		writeJSON(w, 400, map[string]any{"error": "symbol required"})
		return
	}
	interval := r.URL.Query().Get("interval")
	if interval == "" {
		interval = "1d"
	}
	limit := 120
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	// US/HK/CN stock & ETF history → persistent Futu worker (its /kline is
	// already facade-shaped; proxy straight through).
	if isFutuEligible(sym) {
		u := h.futuWorker + "/kline?symbol=" + url.QueryEscape(sym)
		if interval != "" {
			u += "&interval=" + url.QueryEscape(interval)
		}
		u += "&limit=" + strconv.Itoa(limit)
		if proxyJSON(w, u) {
			return
		}
	}
	mk, local := "US", sym
	if i := strings.IndexByte(sym, ':'); i >= 0 {
		mk, local = sym[:i], sym[i+1:]
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	bars, err := h.Svc.GetKLines(ctx, ToHubSymbol(sym), interval, limit)
	if err != nil || len(bars) == 0 {
		// internal adapters lack daily K for indices/futures — the Futu worker's
		// /kline covers those via its Yahoo (data-gateway) path. Fall back to it.
		u := h.futuWorker + "/kline?symbol=" + url.QueryEscape(sym) + "&limit=" + strconv.Itoa(limit)
		if proxyJSON(w, u) {
			return
		}
		writeJSON(w, 200, map[string]any{"data": []any{}, "provenance": map[string]any{"source": "market-hub", "error": "no kline"}})
		return
	}
	data := make([]map[string]any, 0, len(bars))
	for _, b := range bars {
		data = append(data, map[string]any{
			"market": strings.ToUpper(mk), "symbol": local,
			"trade_date": b.Time.Format("2006-01-02"),
			"open":       b.Open, "high": b.High, "low": b.Low, "close": b.Close,
			"volume": b.Volume, "amount": nil,
		})
	}
	writeJSON(w, 200, map[string]any{"data": data, "provenance": map[string]any{"source": "market-hub", "provider": "market-hub"}})
}

func (h *Hub) HandleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true, "service": "market-hub"})
}
