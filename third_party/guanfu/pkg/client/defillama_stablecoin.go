// DefiLlama stablecoin net-flow source (F2).
//
// Why this source:
//   PriceStore already has `stablecoin_market_cap_usd` — a single snapshot
//   of total supply. DefiLlama's /stablecoincharts/all endpoint gives the
//   per-day aggregate USD supply time series, free, no key. From that we
//   derive what extractors actually need: the 30-day change (net inflow
//   proxy), which is "fresh crypto-native dollars landing on chain."
//
// Storage key: `defillama_stablecoin_supply` (single daily point stream
// of aggregate stablecoin market cap in USD). Derived features (30d pct
// change, 7d roll) live in forecast extractors; we keep the raw series here
// so callers can compute their own windows.
//
// Rate limits: DefiLlama is permissive for unauthenticated GET; no key.

package client

import (
	"context"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
	"github.com/Ricaardo/nimbus-os/datasources/defillama"
)

// DefillamaStablecoinSource pulls the aggregate stablecoin supply (USD)
// daily history from DefiLlama. Key: defillama_stablecoin_supply.
type DefillamaStablecoinSource struct{}

func (DefillamaStablecoinSource) Key() string         { return "defillama_stablecoin_supply" }
func (DefillamaStablecoinSource) DisplayName() string { return "defillama_stablecoin_supply (F2)" }

// Refresh fetches the full DefiLlama chart when the store is empty;
// otherwise just appends any new points. The endpoint returns the entire
// series in one call (~1000 rows as of 2026), so incremental is really
// "re-fetch then dedupe via Save/Append" — cheap enough on this cadence.
func (d DefillamaStablecoinSource) Refresh(ctx context.Context, s *store.PriceStore) (*RefreshResult, error) {
	stale, lastDate := staleThreshold(s, d.Key())
	if !stale {
		return freshSkipResult(d.Key(), d.DisplayName(), lastDate, s), nil
	}

	points, err := fetchDefillamaStablecoin(ctx)
	if err != nil {
		return nil, err
	}
	if len(points) == 0 {
		return &RefreshResult{
			Key: d.Key(), DisplayName: d.DisplayName(),
			Mode: "full", Added: 0, LastDate: lastDate,
		}, nil
	}

	mode := "full"
	var added int
	if lastDate == "" {
		if err := s.Save(d.Key(), points); err != nil {
			return nil, err
		}
		added = len(points)
	} else {
		// Append new ones only; Save dedupes by date.
		before, _ := s.Count(d.Key())
		if err := s.Append(d.Key(), points); err != nil {
			return nil, err
		}
		after, _ := s.Count(d.Key())
		added = after - before
		mode = "incremental"
	}

	total, _ := s.Count(d.Key())
	last, _ := s.LastDate(d.Key())
	return &RefreshResult{
		Key: d.Key(), DisplayName: d.DisplayName(),
		Mode: mode, Added: added, Total: total, LastDate: last,
	}, nil
}

// fetchDefillamaStablecoin returns the all-stablecoins aggregate time
// series, oldest-first, mapped to PriceStore points. DefiLlama already
// converts every pegged currency to USD, so TotalUSD is the right aggregate.
func fetchDefillamaStablecoin(ctx context.Context) ([]store.PricePoint, error) {
	pts, err := defillama.StablecoinChartAll(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]store.PricePoint, 0, len(pts))
	for _, p := range pts {
		total := p.TotalUSD()
		if total <= 0 {
			continue
		}
		out = append(out, store.PricePoint{
			Date:   p.Time.UTC().Format("2006-01-02"),
			Close:  total,
			Source: "defillama:stablecoin_supply",
		})
	}
	return out, nil
}
