// Package coinbase is the independent Coinbase Exchange data source. Fetch-only
// and stateless. Uses the free public candles endpoint (no key, no auth):
//
//	GET https://api.exchange.coinbase.com/products/{product}/candles
//
// Returns up to 300 candles per call; DailyCandles chunks a longer range.
package coinbase

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/Ricaardo/nimbus-os/datasources/internal/httpx"
)

// BaseURL is the API host. It is a var (not const) so tests can point it at an
// httptest server.
var BaseURL = "https://api.exchange.coinbase.com"

// maxCandlesPerRequest is Coinbase's per-request cap.
const maxCandlesPerRequest = 300

// Candle is one OHLCV bar.
type Candle struct {
	Time   time.Time
	Low    float64
	High   float64
	Open   float64
	Close  float64
	Volume float64
}

// Candles fetches a single window (≤300 bars) of OHLCV candles for a product
// at the given granularity in seconds, oldest-first. Empty → (nil, nil).
func Candles(ctx context.Context, product string, granularitySec int, start, end time.Time) ([]Candle, error) {
	url := fmt.Sprintf("%s/products/%s/candles?granularity=%d&start=%s&end=%s",
		BaseURL, product, granularitySec,
		start.Format(time.RFC3339), end.Format(time.RFC3339))

	// Response shape: [[time, low, high, open, close, volume], ...]
	var raw [][]float64
	if err := httpx.Default.GetJSON(ctx, url, nil, &raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]Candle, 0, len(raw))
	for _, row := range raw {
		if len(row) < 6 {
			continue
		}
		out = append(out, Candle{
			Time:   time.Unix(int64(row[0]), 0).UTC(),
			Low:    row[1],
			High:   row[2],
			Open:   row[3],
			Close:  row[4],
			Volume: row[5],
		})
	}
	return out, nil
}

// DailyCandles fetches daily (granularity 86400) candles over [start, end] by
// chunking into 300-day windows, then merges, sorts oldest-first, and dedupes
// by date. A single window failure aborts (no partial gap-filled result).
func DailyCandles(ctx context.Context, product string, start, end time.Time) ([]Candle, error) {
	if !end.After(start) {
		return nil, nil
	}
	var all []Candle
	cur := start
	for cur.Before(end) {
		windowEnd := cur.AddDate(0, 0, maxCandlesPerRequest-1)
		if windowEnd.After(end) {
			windowEnd = end
		}
		pts, err := Candles(ctx, product, 86400, cur, windowEnd)
		if err != nil {
			return nil, err
		}
		all = append(all, pts...)
		cur = windowEnd.AddDate(0, 0, 1)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Time.Before(all[j].Time) })
	out := make([]Candle, 0, len(all))
	seen := map[string]bool{}
	for _, c := range all {
		d := c.Time.Format("2006-01-02")
		if seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, c)
	}
	return out, nil
}
