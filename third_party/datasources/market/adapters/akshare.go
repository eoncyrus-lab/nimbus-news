// Package adapters — akshare adapter for A-share real-time quotes
//
// Calls the resident openbb-worker (py-worker) which wraps the Sina Finance
// real-time API. Priority 5 — above Yahoo (10) but below market-data-svc (1),
// so A-shares prefer Sina over Yahoo when both are available.
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

// AkshareAdapter fetches A-share quotes from the Sina Finance API via
// the openbb-worker HTTP bridge.
type AkshareAdapter struct {
	BaseAdapter
	baseURL string
}

const _DEFAULT_AKSHARE_URL = "http://127.0.0.1:8824"

// NewAkshareAdapter creates the adapter. Base URL read from PY_WORKER_URL env.
func NewAkshareAdapter() *AkshareAdapter {
	base := os.Getenv("PY_WORKER_URL")
	if base == "" {
		base = _DEFAULT_AKSHARE_URL
	}
	return &AkshareAdapter{
		BaseAdapter: NewBaseAdapter("akshare", 5),
		baseURL:     strings.TrimRight(base, "/"),
	}
}

func (a *AkshareAdapter) SupportedTypes() []string {
	return []string{AssetTypeStock}
}

// CanHandle returns true only for A-share symbols (.SS / .SZ suffixes).
func (a *AkshareAdapter) CanHandle(symbol string) bool {
	upper := strings.ToUpper(symbol)
	return strings.HasSuffix(upper, ".SS") || strings.HasSuffix(upper, ".SZ")
}

// GetQuote fetches a single A-share quote from the Sina API via py-worker.
func (a *AkshareAdapter) GetQuote(ctx context.Context, symbol string) (*types.MarketQuote, error) {
	// Convert .SS/.SZ back to the bare CN code for the akshare endpoint.
	local := strings.TrimSuffix(symbol, ".SS")
	local = strings.TrimSuffix(local, ".SZ")
	local = strings.TrimSuffix(local, ".ss")
	local = strings.TrimSuffix(local, ".sz")

	reqURL := fmt.Sprintf("%s/akshare/quote?symbol=%s", a.baseURL, url.QueryEscape(local))

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("akshare /quote req: %w", err)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("akshare /quote: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("akshare /quote status %d: %s", resp.StatusCode, body)
	}

	var result struct {
		Data []struct {
			Symbol     string   `json:"symbol"`
			Name       string   `json:"name"`
			Last       *float64 `json:"last"`
			PrevClose  *float64 `json:"prev_close"`
			Open       *float64 `json:"open"`
			High       *float64 `json:"high"`
			Low        *float64 `json:"low"`
			Volume     *float64 `json:"volume"`
			Turnover   *float64 `json:"turnover"`
			Change     *float64 `json:"change"`
			ChangePct  *float64 `json:"change_pct"`
			Provider   string   `json:"provider"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("akshare /quote json: %w", err)
	}

	if len(result.Data) == 0 {
		return nil, fmt.Errorf("akshare: no data for %s", symbol)
	}

	r := result.Data[0]
	if r.Last == nil {
		return nil, fmt.Errorf("akshare: nil price for %s", symbol)
	}

	change := 0.0
	changePct := 0.0
	if r.Change != nil {
		change = *r.Change
	}
	if r.ChangePct != nil {
		changePct = *r.ChangePct
	}

	volume := int64(0)
	if r.Volume != nil {
		volume = int64(*r.Volume)
	}

	return &types.MarketQuote{
		Symbol:        symbol,
		Name:          r.Name,
		Price:         *r.Last,
		Change:        change,
		ChangePercent: changePct,
		PrevClose:     float64OrDefault(r.PrevClose),
		Open:          float64OrDefault(r.Open),
		High:          float64OrDefault(r.High),
		Low:           float64OrDefault(r.Low),
		Volume:        volume,
		UpdateTime:    time.Now(),
		Currency:      "CNY",
		Exchange:      exchangeForSymbol(symbol),
		AssetType:     AssetTypeStock,
	}, nil
}

func float64OrDefault(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

func exchangeForSymbol(symbol string) string {
	if strings.HasSuffix(strings.ToUpper(symbol), ".SS") {
		return "SSE"
	}
	if strings.HasSuffix(strings.ToUpper(symbol), ".SZ") {
		return "SZSE"
	}
	return ""
}
