// Package defillama is the independent DefiLlama data source. Fetch-only and
// stateless — endpoints return the full series each call, so callers persist
// and dedupe their own history.
//
// No API key: DefiLlama is permissive for unauthenticated GET.
package defillama

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Ricaardo/nimbus-os/datasources/internal/httpx"
)

// BaseURL is the stablecoins API host. It is a var (not const) so tests can
// point it at an httptest server.
var BaseURL = "https://stablecoins.llama.fi"

// StablecoinPoint is one daily aggregate. Pegged holds DefiLlama's
// totalCirculatingUSD breakdown (peggedUSD, peggedEUR, ...), each already
// converted to USD, so TotalUSD is a straight sum. Values are typed as any to
// tolerate the occasional non-numeric field DefiLlama emits.
type StablecoinPoint struct {
	Time   time.Time
	Pegged map[string]any
}

// TotalUSD sums every pegged currency's USD-denominated circulating supply,
// skipping any non-numeric field.
func (p StablecoinPoint) TotalUSD() float64 {
	total := 0.0
	for _, v := range p.Pegged {
		if f, ok := v.(float64); ok {
			total += f
		}
	}
	return total
}

// StablecoinChartAll returns the all-stablecoins aggregate USD supply series,
// oldest-first. Empty response → (nil, nil).
func StablecoinChartAll(ctx context.Context) ([]StablecoinPoint, error) {
	var raw []struct {
		Date                any            `json:"date"`
		TotalCirculatingUSD map[string]any `json:"totalCirculatingUSD"`
	}
	if err := httpx.Default.GetJSON(ctx, BaseURL+"/stablecoincharts/all", nil, &raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}

	out := make([]StablecoinPoint, 0, len(raw))
	for _, r := range raw {
		ts, ok := parseDate(r.Date)
		if !ok {
			continue
		}
		out = append(out, StablecoinPoint{Time: ts, Pegged: r.TotalCirculatingUSD})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out, nil
}

// parseDate handles both numeric unix-seconds and string date forms DefiLlama
// has historically emitted.
func parseDate(v any) (time.Time, bool) {
	switch x := v.(type) {
	case float64:
		return time.Unix(int64(x), 0).UTC(), true
	case int64:
		return time.Unix(x, 0).UTC(), true
	case string:
		s := strings.TrimSpace(x)
		if unix, err := strconv.ParseInt(s, 10, 64); err == nil {
			return time.Unix(unix, 0).UTC(), true
		}
		if t, err := time.Parse("2006-01-02", s); err == nil {
			return t.UTC(), true
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}
