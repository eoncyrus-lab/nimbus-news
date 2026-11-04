package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	types "github.com/Ricaardo/nimbus-os/datasources/market/types"
)

// AlpacaAdapter wraps the Alpaca Markets Data API v2 for US stock/ETF quotes
// and daily bars. Free tier is IEX-only (delayed 15min), 200 req/min.
// Non-IEX symbols return empty data → adapter fast-fails and the chain continues.
//
// Env: ALPACA_API_KEY, ALPACA_SECRET_KEY, ALPACA_ENABLED (set to "0" to disable).
type AlpacaAdapter struct {
	BaseAdapter
	baseURL  string
	key      string
	secret   string
	enabled  bool
	negCache map[string]time.Time // non-IEX symbols → skip for 1h
	negMu    sync.RWMutex
}

// NewAlpacaAdapter creates the adapter. Disabled if ALPACA_ENABLED=0 or key is empty.
func NewAlpacaAdapter() *AlpacaAdapter {
	key := os.Getenv("ALPACA_API_KEY")
	secret := os.Getenv("ALPACA_SECRET_KEY")
	enabled := os.Getenv("ALPACA_ENABLED") != "0" && key != ""
	return &AlpacaAdapter{
		BaseAdapter: NewBaseAdapter("alpaca", 2),
		baseURL:     "https://data.alpaca.markets",
		key:         key,
		secret:      secret,
		enabled:     enabled,
		negCache:    make(map[string]time.Time),
	}
}

func (a *AlpacaAdapter) SupportedTypes() []string {
	return []string{AssetTypeStock, AssetTypeETF}
}

// CanHandle returns true for US bare tickers (1–5 uppercase alpha, no special chars).
// Non-IEX symbols that previously failed are cached and skipped for 1 hour to
// avoid burning rate-limit budget on calls that will always fail.
func (a *AlpacaAdapter) CanHandle(symbol string) bool {
	if !a.enabled {
		return false
	}
	upper := strings.ToUpper(symbol)
	if len(upper) < 1 || len(upper) > 5 {
		return false
	}
	if strings.ContainsAny(upper, "^=.") {
		return false
	}
	for _, c := range upper {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	// Check negative cache: known non-IEX tickers are skipped for 1h.
	a.negMu.RLock()
	deadline, ok := a.negCache[upper]
	a.negMu.RUnlock()
	if ok && time.Now().Before(deadline) {
		return false
	}
	return true
}

// GetQuote fetches a real-time quote via Alpaca Data API v2.
func (a *AlpacaAdapter) GetQuote(ctx context.Context, symbol string) (*types.MarketQuote, error) {
	if !a.enabled {
		return nil, fmt.Errorf("alpaca disabled")
	}

	body, err := a.doRequest(ctx, "/v2/stocks/"+symbol+"/quotes/latest")
	if err != nil {
		return nil, err
	}

	var resp struct {
		Quote struct {
			Ap float64 `json:"ap"` // ask price
			As float64 `json:"as"` // ask size
			Bp float64 `json:"bp"` // bid price
			Bs float64 `json:"bs"` // bid size
			T  string  `json:"t"`  // timestamp
		} `json:"quote"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("alpaca quote %s: %w", symbol, err)
	}

	// Empty response → likely non-IEX, cache and skip next time.
	if resp.Quote.Ap == 0 && resp.Quote.Bp == 0 {
		a.cacheNegative(symbol)
		return nil, fmt.Errorf("alpaca: no quote for %s (non-IEX?)", symbol)
	}

	ts := time.Now()
	if t, err := time.Parse(time.RFC3339Nano, resp.Quote.T); err == nil {
		ts = t
	}

	mid := (resp.Quote.Ap + resp.Quote.Bp) / 2
	if mid == 0 {
		// If only one side is available, use it.
		mid = resp.Quote.Ap
		if mid == 0 {
			mid = resp.Quote.Bp
		}
	}

	return &types.MarketQuote{
		Symbol:        symbol,
		Name:          symbol,
		Price:         mid,
		Change:        0,         // Alpaca latest quote doesn't include prev close
		ChangePercent: 0,         // must be computed from bars if needed
		High:          0,
		Low:           0,
		Open:          0,
		PrevClose:     0,
		Volume:        0,
		UpdateTime:    ts,
		Currency:      "USD",
		Exchange:      "US",
		AssetType:     AssetTypeStock,
	}, nil
}

// GetKLines fetches daily bars.
func (a *AlpacaAdapter) GetKLines(ctx context.Context, symbol string, interval string, limit int) ([]types.KLineItem, error) {
	if !a.enabled {
		return nil, fmt.Errorf("alpaca disabled")
	}
	if interval != "1d" && interval != "1D" && interval != "day" && interval != "" {
		// Alpaca free tier only supports 1Day timeframe. Higher resolutions
		// (1Min, 1Hour) require a paid subscription.
		return nil, fmt.Errorf("alpaca: interval %q not supported (free tier: 1Day only)", interval)
	}

	url := fmt.Sprintf("/v2/stocks/%s/bars?timeframe=1Day&limit=%d&adjustment=raw&feed=iex", symbol, limit)
	body, err := a.doRequest(ctx, url)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Bars  []alpacaBar `json:"bars"`
		Error string      `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("alpaca bars %s: %w", symbol, err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("alpaca bars %s: %s", symbol, resp.Error)
	}
	if len(resp.Bars) == 0 {
		a.cacheNegative(symbol)
		return nil, fmt.Errorf("alpaca: no bars for %s", symbol)
	}

	bars := make([]types.KLineItem, len(resp.Bars))
	for i, b := range resp.Bars {
		bars[i] = types.KLineItem{
			Time:   b.T,
			Open:   b.O,
			High:   b.H,
			Low:    b.L,
			Close:  b.C,
			Volume: int64(b.V),
		}
	}
	return bars, nil
}

// --- internal helpers ---

func (a *AlpacaAdapter) doRequest(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", a.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("APCA-API-KEY-ID", a.key)
	req.Header.Set("APCA-API-SECRET-KEY", a.secret)
	req.Header.Set("Accept", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("alpaca %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("alpaca rate limit exceeded (429)")
	}

	var buf []byte
	// LimitedReader to avoid memory blowup on unexpected responses.
	lr := &io.LimitedReader{R: resp.Body, N: 1 << 18} // 256KB
	buf, err = io.ReadAll(lr)
	if err != nil {
		return nil, fmt.Errorf("alpaca read: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("alpaca HTTP %d: %s", resp.StatusCode, string(buf))
	}
	return buf, nil
}

func (a *AlpacaAdapter) cacheNegative(symbol string) {
	a.negMu.Lock()
	a.negCache[strings.ToUpper(symbol)] = time.Now().Add(1 * time.Hour)
	a.negMu.Unlock()
}

// alpacaBar is a single daily bar from the Alpaca bars endpoint.
type alpacaBar struct {
	T time.Time `json:"t"`
	O float64   `json:"o"`
	H float64   `json:"h"`
	L float64   `json:"l"`
	C float64   `json:"c"`
	V int       `json:"v"`
}
