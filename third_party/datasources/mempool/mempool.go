// Package mempool is the independent mempool.space data source: BTC network
// metrics (hashrate series, difficulty retarget, mempool congestion). Free, no
// API key. Fetch-only — callers own any derived analysis (hash ribbons, etc.).
package mempool

import (
	"context"
	"sort"

	"github.com/Ricaardo/nimbus-os/datasources/internal/httpx"
)

const base = "https://mempool.space"

// HashratePoint is one daily average-hashrate observation, in EH/s.
type HashratePoint struct {
	Timestamp int64   `json:"timestamp"`
	HashEHs   float64 `json:"hash_ehs"`
}

// Hashrate3y returns the ~3-year daily average hashrate series, ascending, in EH/s.
func Hashrate3y(ctx context.Context) ([]HashratePoint, error) {
	var resp struct {
		Hashrates []struct {
			Timestamp   int64   `json:"timestamp"`
			AvgHashrate float64 `json:"avgHashrate"`
		} `json:"hashrates"`
	}
	if err := httpx.Default.GetJSON(ctx, base+"/api/v1/mining/hashrate/3y", nil, &resp); err != nil {
		return nil, err
	}
	out := make([]HashratePoint, 0, len(resp.Hashrates))
	for _, h := range resp.Hashrates {
		out = append(out, HashratePoint{
			Timestamp: h.Timestamp,
			HashEHs:   h.AvgHashrate / 1e18, // hash/s -> EH/s
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp < out[j].Timestamp })
	return out, nil
}

// DifficultyChangePct returns the previous difficulty-retarget change percent.
func DifficultyChangePct(ctx context.Context) (float64, error) {
	var resp struct {
		PreviousRetarget float64 `json:"previousRetarget"`
	}
	if err := httpx.Default.GetJSON(ctx, base+"/api/v1/difficulty-adjustment", nil, &resp); err != nil {
		return 0, err
	}
	return resp.PreviousRetarget, nil
}

// MempoolDepth returns the current mempool virtual size (bytes) and tx count.
func MempoolDepth(ctx context.Context) (vsize int64, count int, err error) {
	var resp struct {
		Count int   `json:"count"`
		Vsize int64 `json:"vsize"`
	}
	if err := httpx.Default.GetJSON(ctx, base+"/api/mempool", nil, &resp); err != nil {
		return 0, 0, err
	}
	return resp.Vsize, resp.Count, nil
}
