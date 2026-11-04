// services_market.go — cross-asset prices + equity valuation from the resident
// nimbus services (market-data-svc :8810, warehouse-svc :8814), replacing the
// hand-rolled Futu OpenD client. One Futu execution point now lives in
// market-data-svc; guanfu is a thin HTTP client.
//
//	history series  ← market-data-svc /kline  (Futu daily bars, realtime tail)
//	realtime price  ← market-data-svc /quote  (persistent Futu context)
//	PE/PB           ← market-data-svc /quote  (null for ETFs → 0 → 待接入 placeholder)
//
// History uses /kline rather than warehouse-svc /history because the latter's
// tail lags ~1 month and is shallower here; /kline is the same Futu daily-bar
// source the legacy code used, now behind the single 8810 execution point, with
// the same Futu→Yahoo resilience (caller falls back to Yahoo on error).
//
// Env: MARKET_DATA_URL (default http://127.0.0.1:8810).
package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// CrossAssetFutuPrices holds cross-asset prices. Histories are newest-first
// (history[0] = most recent), matching the Yahoo and London-gold paths.
type CrossAssetFutuPrices struct {
	QQQPrice       float64
	QQQHistory     []float64
	QQQPriceAsOf   string
	SPYPrice       float64
	SPYHistory     []float64
	SPYPriceAsOf   string
	GLDPrice       float64 // 实物黄金 ETF
	GLDHistory     []float64
	GLDPriceAsOf   string
	UUPPrice       float64 // 做多美元 ETF (DXY proxy)
	UUPHistory     []float64
	UUPPriceAsOf   string
	TLTPrice       float64 // 20Y+ 美债 ETF
	TLTHistory     []float64
	TLTPriceAsOf   string
	VIXYPrice      float64 // VIX 波动率 ETF
	WTIPrice       float64 // USO oil ETF proxy
	WTIHistory     []float64
	WTIPriceAsOf   string
	OilPriceSource string
	VIXYHistory    []float64
	VIXYPriceAsOf  string
	Warnings       []string
}

// FutuEquityValuation holds PE/PB for QQQ and SPY.
type FutuEquityValuation struct {
	QQQPE float64
	QQQPB float64
	SPYPE float64
	SPYPB float64
}

func marketDataURL() string {
	if v := os.Getenv("MARKET_DATA_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://127.0.0.1:8810"
}

// mdQuoteRow mirrors a market-data-svc /quote row. PE/PB are pointers because
// the service emits null for instruments Futu has no PE/PB for (e.g. ETFs).
type mdQuoteRow struct {
	Symbol string   `json:"symbol"`
	Last   float64  `json:"last"`
	PE     *float64 `json:"pe"`
	PETTM  *float64 `json:"pe_ttm"`
	PB     *float64 `json:"pb"`
	Ts     string   `json:"ts"`
}

type mdQuoteResp struct {
	Data []mdQuoteRow `json:"data"`
}

type klineRow struct {
	TradeDate string  `json:"trade_date"`
	Close     float64 `json:"close"`
}

type klineResp struct {
	Data []klineRow `json:"data"`
}

// fetchQuotes batches a market-data-svc /quote call for the given canonical
// symbols (e.g. "US:QQQ"), returning a map keyed by canonical symbol.
func fetchQuotes(canonicals []string) (map[string]mdQuoteRow, error) {
	hc := &http.Client{Timeout: 12 * time.Second}
	reqURL := marketDataURL() + "/quote?symbols=" + url.QueryEscape(strings.Join(canonicals, ","))
	resp, err := hc.Get(reqURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("market-data /quote http %d: %s", resp.StatusCode, body)
	}
	var parsed mdQuoteResp
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	out := make(map[string]mdQuoteRow, len(parsed.Data))
	for _, r := range parsed.Data {
		out[r.Symbol] = r
	}
	return out, nil
}

// fetchKlineHistory returns newest-first closes for one canonical symbol.
// market-data-svc /kline returns daily bars ascending (oldest-first); we reverse
// to newest-first to match the legacy convention.
func fetchKlineHistory(canonical string, days int) ([]float64, string, error) {
	hc := &http.Client{Timeout: 15 * time.Second}
	reqURL := fmt.Sprintf("%s/kline?symbol=%s&limit=%d", marketDataURL(), url.QueryEscape(canonical), days)
	resp, err := hc.Get(reqURL)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, "", fmt.Errorf("market-data /kline http %d: %s", resp.StatusCode, body)
	}
	var parsed klineResp
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, "", err
	}
	n := len(parsed.Data)
	if n == 0 {
		return nil, "", nil
	}
	hist := make([]float64, n)
	for i, row := range parsed.Data { // ascending → reverse to newest-first
		hist[n-1-i] = row.Close
	}
	asOf := parsed.Data[n-1].TradeDate // last ascending row = newest
	return hist, asOf, nil
}

// crossAssetSpec maps a canonical symbol to the struct fields it populates.
type crossAssetSpec struct {
	canonical string
	price     *float64
	history   *[]float64
	asOf      *string
}

// FetchCrossAssetFromServices pulls QQQ/SPY/GLD/UUP/TLT/VIXY/USO from the
// resident services: history from warehouse-svc, realtime price from
// market-data-svc. Returns an error only when nothing could be fetched, so the
// caller falls back to Yahoo.
func FetchCrossAssetFromServices(days int) (*CrossAssetFutuPrices, error) {
	if days <= 0 {
		days = 1000
	}
	out := &CrossAssetFutuPrices{}

	specs := []crossAssetSpec{
		{"US:QQQ", &out.QQQPrice, &out.QQQHistory, &out.QQQPriceAsOf},
		{"US:SPY", &out.SPYPrice, &out.SPYHistory, &out.SPYPriceAsOf},
		{"US:GLD", &out.GLDPrice, &out.GLDHistory, &out.GLDPriceAsOf},
		{"US:UUP", &out.UUPPrice, &out.UUPHistory, &out.UUPPriceAsOf},
		{"US:TLT", &out.TLTPrice, &out.TLTHistory, &out.TLTPriceAsOf},
		{"US:VIXY", &out.VIXYPrice, &out.VIXYHistory, &out.VIXYPriceAsOf},
		{"US:USO", &out.WTIPrice, &out.WTIHistory, &out.WTIPriceAsOf},
	}

	canonicals := make([]string, len(specs))
	for i, s := range specs {
		canonicals[i] = s.canonical
	}
	quotes, qErr := fetchQuotes(canonicals)
	if qErr != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("market-data /quote: %v", qErr))
	}

	gotAny := false
	for _, s := range specs {
		hist, asOf, hErr := fetchKlineHistory(s.canonical, days)
		if hErr != nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("kline %s: %v", s.canonical, hErr))
		} else if len(hist) > 0 {
			*s.history = hist
			*s.price = hist[0] // newest close; overridden by realtime quote below
			*s.asOf = asOf
			gotAny = true
		}
		// Prefer realtime quote price/asOf when available.
		if q, ok := quotes[s.canonical]; ok && q.Last > 0 {
			*s.price = q.Last
			if d := dateOf(q.Ts); d != "" {
				*s.asOf = d
			}
			gotAny = true
		}
	}

	if out.WTIPrice > 0 {
		out.OilPriceSource = "services:US:USO"
	}
	if !gotAny {
		return nil, fmt.Errorf("services cross-asset: no data (%v)", out.Warnings)
	}
	return out, nil
}

// FetchEquityValuationFromServices reads QQQ/SPY PE/PB from market-data-svc.
// Null PE/PB (Futu returns none for ETFs) map to 0, which the panel renders as
// the existing "待接入" placeholder — behaviour-identical to the Futu path.
func FetchEquityValuationFromServices() (*FutuEquityValuation, error) {
	quotes, err := fetchQuotes([]string{"US:QQQ", "US:SPY"})
	if err != nil {
		return nil, err
	}
	out := &FutuEquityValuation{}
	if q, ok := quotes["US:QQQ"]; ok {
		out.QQQPE = deref(q.PE)
		out.QQQPB = deref(q.PB)
	}
	if q, ok := quotes["US:SPY"]; ok {
		out.SPYPE = deref(q.PE)
		out.SPYPB = deref(q.PB)
	}
	return out, nil
}

func deref(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

// dateOf extracts the YYYY-MM-DD prefix from a quote timestamp like
// "2026-06-26 20:01:38.875" or an RFC3339 string.
func dateOf(ts string) string {
	ts = strings.TrimSpace(ts)
	if ts == "" {
		return ""
	}
	if i := strings.IndexAny(ts, " T"); i > 0 {
		return ts[:i]
	}
	return ts
}
