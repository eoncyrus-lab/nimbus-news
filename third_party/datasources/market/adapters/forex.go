package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	types "github.com/Ricaardo/nimbus-os/datasources/market/types"
)

// ForexAdapter fetches live exchange rates from exchangerate-api.com (free,
// no key, 166 currencies, updates ~daily). Caches the full rate table for
// 1 hour — rates change slowly and this avoids hitting per-symbol limits.
//
// Env: FOREX_ENABLED=0 disables it.
type ForexAdapter struct {
	BaseAdapter
	enabled bool

	mu       sync.RWMutex
	cachedAt time.Time
	rates    map[string]float64 // currency → USD rate (e.g. CNY: 6.77 means 1 USD = 6.77 CNY)
}

const forexBaseURL = "https://api.exchangerate-api.com/v4/latest/USD"
const forexCacheTTL = 1 * time.Hour

// NewForexAdapter creates the adapter. Disabled if FOREX_ENABLED=0.
func NewForexAdapter() *ForexAdapter {
	return &ForexAdapter{
		BaseAdapter: NewBaseAdapter("forex", 4),
		enabled:     os.Getenv("FOREX_ENABLED") != "0",
		rates:       make(map[string]float64),
	}
}

func (a *ForexAdapter) SupportedTypes() []string {
	return []string{AssetTypeForex}
}

// CanHandle returns true for Yahoo-style forex symbols (e.g. CNY=X, EURUSD=X).
func (a *ForexAdapter) CanHandle(symbol string) bool {
	if !a.enabled {
		return false
	}
	return strings.HasSuffix(strings.ToUpper(symbol), "=X")
}

func (a *ForexAdapter) GetQuote(ctx context.Context, symbol string) (*types.MarketQuote, error) {
	rates, err := a.getRates(ctx)
	if err != nil {
		return nil, err
	}

	upper := strings.ToUpper(symbol)
	// Parse "FROMTO=X" → FROM, TO
	pair := strings.TrimSuffix(upper, "=X")
	rate, from, to, err := resolveForex(pair, rates)
	if err != nil {
		return nil, err
	}

	return &types.MarketQuote{
		Symbol:        symbol,
		Name:          fmt.Sprintf("%s/%s", from, to),
		Price:         rate,
		Change:        0, // daily-resolution free API, no real-time change
		ChangePercent: 0,
		High:          0,
		Low:           0,
		Open:          0,
		PrevClose:     0,
		Volume:        0,
		UpdateTime:    a.cachedAt,
		Currency:      to,
		Exchange:      "FOREX",
		AssetType:     AssetTypeForex,
	}, nil
}

// getRates fetches + caches the full USD rate table.
func (a *ForexAdapter) getRates(ctx context.Context) (map[string]float64, error) {
	a.mu.RLock()
	if time.Since(a.cachedAt) < forexCacheTTL && len(a.rates) > 0 {
		defer a.mu.RUnlock()
		// Copy to avoid races on the cached map.
		out := make(map[string]float64, len(a.rates))
		for k, v := range a.rates {
			out[k] = v
		}
		return out, nil
	}
	a.mu.RUnlock()

	a.mu.Lock()
	defer a.mu.Unlock()
	// Double-check after acquiring write lock.
	if time.Since(a.cachedAt) < forexCacheTTL && len(a.rates) > 0 {
		out := make(map[string]float64, len(a.rates))
		for k, v := range a.rates {
			out[k] = v
		}
		return out, nil
	}

	req, err := http.NewRequestWithContext(ctx, "GET", forexBaseURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("forex: %w", err)
	}
	defer resp.Body.Close()

	var body struct {
		Base  string             `json:"base"`
		Rates map[string]float64 `json:"rates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("forex parse: %w", err)
	}
	if len(body.Rates) == 0 {
		return nil, fmt.Errorf("forex: empty rates")
	}

	a.rates = body.Rates
	a.cachedAt = time.Now()

	out := make(map[string]float64, len(body.Rates))
	for k, v := range body.Rates {
		out[k] = v
	}
	return out, nil
}

// resolveForex converts a pair like "CNY" (implied USDCNY), "USDCNY", or
// "EURUSD" into a rate, using the USD-based rate table. Returns (rate, from, to, error).
func resolveForex(pair string, rates map[string]float64) (float64, string, string, error) {
	p := strings.ToUpper(strings.TrimSpace(pair))
	if len(p) < 3 {
		return 0, "", "", fmt.Errorf("forex: invalid pair %q", pair)
	}

	// "CNY" → USD/CNY
	if len(p) == 3 {
		r, ok := rates[p]
		if !ok {
			return 0, "", "", fmt.Errorf("forex: unknown currency %q", p)
		}
		return r, "USD", p, nil
	}

	// "USDCNY" → USD/CNY
	if len(p) == 6 {
		from, to := p[:3], p[3:]
		if from == "USD" {
			r, ok := rates[to]
			if !ok {
				return 0, "", "", fmt.Errorf("forex: unknown currency %q", to)
			}
			return r, from, to, nil
		}
		if to == "USD" {
			r, ok := rates[from]
			if !ok {
				return 0, "", "", fmt.Errorf("forex: unknown currency %q", from)
			}
			if r == 0 {
				return 0, "", "", fmt.Errorf("forex: zero rate for %q", from)
			}
			return 1.0 / r, from, to, nil
		}
		// Cross rate: rates are "1 USD = X units of CCY".
		// FROM/TO = (rates[TO] / rates[FROM]) — because:
		//   1 FROM = 1/rates[FROM] USD = rates[TO]/rates[FROM] TO
		rf, okf := rates[from]
		rto, okto := rates[to]
		if !okf || !okto {
			return 0, "", "", fmt.Errorf("forex: unknown currency %q or %q", from, to)
		}
		if rf == 0 {
			return 0, "", "", fmt.Errorf("forex: zero rate for %q", from)
		}
		return rto / rf, from, to, nil
	}

	return 0, "", "", fmt.Errorf("forex: unsupported pair %q", pair)
}
