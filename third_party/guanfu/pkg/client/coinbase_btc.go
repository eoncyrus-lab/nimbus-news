// Coinbase BTC-USD daily close source (F8).
//
// Coinbase spot price alone isn't particularly interesting; what matters
// is the *premium over Binance BTC-USDT*, used as a proxy for US
// institutional demand vs. global retail. We persist just the Coinbase
// side here; the differential is computed at display/extractor time
// against the existing `btc` key (CoinMetrics + Binance blend).
//
// Coinbase has a free public candles endpoint (no key, no auth):
//
//   GET https://api.exchange.coinbase.com/products/BTC-USD/candles?granularity=86400
//
// Returns up to 300 candles per call, oldest-first. For a full history
// we chunk by 300-day windows back from today. The API is stable — it's
// survived unchanged since 2019 — so hardcoding the endpoint is safe.

package client

import (
	"context"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
	"github.com/Ricaardo/nimbus-os/datasources/coinbase"
)

const coinbaseFullYears = 6 // how far back we go on initial import

// CoinbaseBTCSource pulls BTC-USD daily close from Coinbase for the
// premium-vs-Binance signal. Key: coinbase_btc.
type CoinbaseBTCSource struct{}

func (CoinbaseBTCSource) Key() string { return "coinbase_btc" }
func (CoinbaseBTCSource) DisplayName() string {
	return "coinbase_btc (BTC-USD daily, F8 premium proxy)"
}

func (c CoinbaseBTCSource) Refresh(ctx context.Context, ps *store.PriceStore) (*RefreshResult, error) {
	stale, lastDate := staleThreshold(ps, c.Key())
	if !stale {
		return freshSkipResult(c.Key(), c.DisplayName(), lastDate, ps), nil
	}

	// Determine fetch window. For incremental: since lastDate + 1 day.
	// For full: coinbaseFullYears ago.
	now := time.Now().UTC()
	var start time.Time
	mode := "full"
	if lastDate != "" {
		if t, err := time.Parse("2006-01-02", lastDate); err == nil {
			start = t.AddDate(0, 0, 1)
			mode = "incremental"
		}
	}
	if start.IsZero() {
		start = now.AddDate(-coinbaseFullYears, 0, 0)
	}

	points, err := fetchCoinbaseBTCDaily(ctx, start, now)
	if err != nil {
		return nil, err
	}
	if len(points) == 0 {
		return &RefreshResult{
			Key: c.Key(), DisplayName: c.DisplayName(),
			Mode: mode, Added: 0, LastDate: lastDate,
		}, nil
	}

	var added int
	if lastDate == "" {
		if err := ps.Save(c.Key(), points); err != nil {
			return nil, err
		}
		added = len(points)
	} else {
		before, _ := ps.Count(c.Key())
		if err := ps.Append(c.Key(), points); err != nil {
			return nil, err
		}
		after, _ := ps.Count(c.Key())
		added = after - before
	}

	total, _ := ps.Count(c.Key())
	last, _ := ps.LastDate(c.Key())
	return &RefreshResult{
		Key: c.Key(), DisplayName: c.DisplayName(),
		Mode: mode, Added: added, Total: total, LastDate: last,
	}, nil
}

// fetchCoinbaseBTCDaily fetches BTC-USD daily candles over [start, end] and
// maps positive closes to PriceStore points. The 300-day chunking, sort and
// dedupe live in the coinbase package.
func fetchCoinbaseBTCDaily(ctx context.Context, start, end time.Time) ([]store.PricePoint, error) {
	candles, err := coinbase.DailyCandles(ctx, "BTC-USD", start, end)
	if err != nil {
		return nil, err
	}
	out := make([]store.PricePoint, 0, len(candles))
	for _, c := range candles {
		if c.Close <= 0 {
			continue
		}
		out = append(out, store.PricePoint{
			Date:   c.Time.Format("2006-01-02"),
			Close:  c.Close,
			Source: "coinbase:BTC-USD",
		})
	}
	return out, nil
}
