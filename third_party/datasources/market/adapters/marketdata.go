// Package adapters — marketdata adapter
// Delegates STOCK / INDEX / ETF for US, HK, and CN markets to the resident
// market-data-svc HTTP service (default http://127.0.0.1:8810).
// Base URL is read from MARKET_DATA_URL env (default http://127.0.0.1:8810).
package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	types "github.com/Ricaardo/nimbus-os/datasources/market/types"
)

// MarketDataAdapter calls the resident market-data-svc HTTP service (port 8810).
// Covers US and HK stocks/ETFs. Indices (^VIX) and futures (CL=F) are handled
// by CBOE/Yahoo adapters instead — CanHandle rejects them to avoid recursive
// 8810→dataplane→8810 loops after the dataplane migration.
// Priority 1 — highest equity priority.
type MarketDataAdapter struct {
	BaseAdapter
	baseURL string
}

// NewMarketDataAdapter creates the adapter.
// Base URL is read from MARKET_DATA_URL (default http://127.0.0.1:8810).
func NewMarketDataAdapter() *MarketDataAdapter {
	base := os.Getenv("MARKET_DATA_URL")
	if base == "" {
		base = "http://127.0.0.1:8810"
	}
	return &MarketDataAdapter{
		BaseAdapter: NewBaseAdapter("market-data-svc", 1),
		baseURL:     strings.TrimRight(base, "/"),
	}
}

func (a *MarketDataAdapter) SupportedTypes() []string {
	return []string{AssetTypeStock, AssetTypeETF}
}

// CanHandle returns true for US bare tickers and HK stocks (.HK).
// A-shares (.SS/.SZ) are handled by the akshare adapter.
func (a *MarketDataAdapter) CanHandle(symbol string) bool {
	upper := strings.ToUpper(symbol)
	if strings.HasSuffix(upper, ".HK") {
		return true
	}
	// 指数 (^VIX, ^GSPC 等) → CBOE adapter (pri 5) 处理
	// 期货 (CL=F, GC=F 等) → Yahoo adapter (pri 10) 兜底
	// 不再由 MarketDataAdapter 转发（8810 内部会立即 bounce 到 Yahoo，
	// 造成名义上走 pri 1 实际走 Yahoo，且与 dataplane 结合产生递归 loop）
	if strings.HasSuffix(upper, ".SS") || strings.HasSuffix(upper, ".SZ") {
		return false
	}
	return isMarketDataBareTicker(upper)
}

// isMarketDataBareTicker reports whether s is a US bare ticker (1–5 uppercase
// alpha chars, no ^ / = / . special prefixes).
func isMarketDataBareTicker(upper string) bool {
	if len(upper) < 1 || len(upper) > 5 {
		return false
	}
	if strings.HasPrefix(upper, "^") || strings.Contains(upper, "=") || strings.Contains(upper, ".") {
		return false
	}
	for _, c := range upper {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}

// toCanonical converts a news-internal symbol to the market-data-svc canonical
// MARKET:LOCAL format. Only called for symbols that pass CanHandle (HK + US bare
// tickers; ^ indices and =F futures are now handled by CBOE/Yahoo adapters).
//
//	00700.HK → HK:00700
//	AAPL     → US:AAPL
func (a *MarketDataAdapter) toCanonical(symbol string) string {
	upper := strings.ToUpper(symbol)
	if strings.HasSuffix(upper, ".HK") {
		loc := strings.TrimSuffix(upper, ".HK")
		return "HK:" + loc
	}
	// US bare ticker (SS/SZ handled by akshare adapter, not us)
	return "US:" + upper
}

// GetQuote fetches a single real-time quote from the /quote endpoint.
func (a *MarketDataAdapter) GetQuote(ctx context.Context, symbol string) (*types.MarketQuote, error) {
	canonical := a.toCanonical(symbol)
	reqURL := a.baseURL + "/quote?symbols=" + url.QueryEscape(canonical)

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("market-data-svc /quote: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("market-data-svc /quote status %d: %s", resp.StatusCode, body)
	}

	var result mdQuoteResp
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("market-data-svc /quote json: %w", err)
	}
	if len(result.Data) == 0 {
		return nil, fmt.Errorf("market-data-svc: no data for %s", symbol)
	}

	return a.parseQuote(symbol, &result.Data[0])
}

// GetKLines fetches daily bars from the /kline endpoint.
func (a *MarketDataAdapter) GetKLines(ctx context.Context, symbol string, interval string, limit int) ([]types.KLineItem, error) {
	canonical := a.toCanonical(symbol)
	reqURL := fmt.Sprintf("%s/kline?symbol=%s&limit=%d", a.baseURL, url.QueryEscape(canonical), limit)

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("market-data-svc /kline: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("market-data-svc /kline status %d: %s", resp.StatusCode, body)
	}

	var result mdKlineResp
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("market-data-svc /kline json: %w", err)
	}

	bars := make([]types.KLineItem, 0, len(result.Data))
	for _, row := range result.Data {
		t, _ := time.Parse("2006-01-02", row.TradeDate)
		bars = append(bars, types.KLineItem{
			Time:   t,
			Open:   row.Open,
			High:   row.High,
			Low:    row.Low,
			Close:  row.Close,
			Volume: int64(row.Volume),
		})
	}
	return bars, nil
}

func (a *MarketDataAdapter) parseQuote(originalSymbol string, r *mdQuoteItem) (*types.MarketQuote, error) {
	if r.Last == 0 && r.PrevClose == 0 {
		return nil, fmt.Errorf("market-data-svc: zero price for %s", originalSymbol)
	}

	exchange := "US"
	currency := "USD"
	assetType := AssetTypeStock

	upper := strings.ToUpper(originalSymbol)
	// Only HK + US bare tickers reach here (CanHandle rejects ^, =F, .SS, .SZ).
	if strings.HasSuffix(upper, ".HK") {
		exchange = "HKEX"
		currency = "HKD"
	}

	change := r.Last - r.PrevClose
	changePct := 0.0
	if r.PrevClose != 0 {
		changePct = (change / r.PrevClose) * 100
	}

	ts := time.Now()
	if r.Ts != "" {
		if t, err := time.Parse(time.RFC3339, r.Ts); err == nil {
			ts = t
		}
	}

	// Use the original symbol as display name; callers may enrich later.
	name := originalSymbol

	return &types.MarketQuote{
		Symbol:        originalSymbol,
		Name:          name,
		Price:         r.Last,
		Change:        change,
		ChangePercent: changePct,
		High:          r.High,
		Low:           r.Low,
		Open:          r.Open,
		PrevClose:     r.PrevClose,
		Volume:        int64(r.Volume),
		UpdateTime:    ts,
		Currency:      currency,
		Exchange:      exchange,
		AssetType:     assetType,
	}, nil
}

// --- JSON response types for market-data-svc ---

type mdQuoteResp struct {
	Data        []mdQuoteItem `json:"data"`
	RetrievedAt string        `json:"retrieved_at"`
}

type mdQuoteItem struct {
	Symbol    string   `json:"symbol"`
	FutuSymbol string  `json:"futu_symbol"`
	Last      float64  `json:"last"`
	PrevClose float64  `json:"prev_close"`
	Open      float64  `json:"open"`
	High      float64  `json:"high"`
	Low       float64  `json:"low"`
	Volume    float64  `json:"volume"`
	Turnover  float64  `json:"turnover"`
	PE        *float64 `json:"pe"`
	PETTM     *float64 `json:"pe_ttm"`
	PB        *float64 `json:"pb"`
	Ts        string   `json:"ts"`
	Provider  string   `json:"provider"`
}

type mdKlineResp struct {
	Data      []mdKlineItem `json:"data"`
	Provenance interface{}  `json:"provenance"`
}

type mdKlineItem struct {
	Market    string  `json:"market"`
	Symbol    string  `json:"symbol"`
	TradeDate string  `json:"trade_date"`
	Open      float64 `json:"open"`
	High      float64 `json:"high"`
	Low       float64 `json:"low"`
	Close     float64 `json:"close"`
	Volume    float64 `json:"volume"`
	Amount    float64 `json:"amount"`
}
