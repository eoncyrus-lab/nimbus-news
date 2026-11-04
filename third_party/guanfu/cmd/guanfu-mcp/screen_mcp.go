// MCP handlers for screen_* tools.
// Called from main.go's tools/call dispatch.

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/screener"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/screener/fine"
)

func runScreenMCP(market, style string, topN int, timeout time.Duration) ([]model.ScreenedCandidate, error) {
	var profile *model.StrategyProfile
	for _, p := range model.BuiltinStrategyProfiles() {
		if p.Market == market && p.Style == style {
			profile = &p
			break
		}
	}
	if profile == nil {
		return nil, fmt.Errorf("no profile for market=%s style=%s", market, style)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	asset := screener.NewScreenedEquitiesAsset(market, style, profile)
	_, err := asset.FetchSnapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("screen fetch: %w (Longbridge MCP required)", err)
	}

	candidates, err := asset.Candidates()
	if err != nil || len(candidates) == 0 {
		return nil, fmt.Errorf("no candidates: %w", err)
	}

	fine.ScoreAll(candidates, profile)
	if len(candidates) > topN {
		candidates = candidates[:topN]
	}
	return candidates, nil
}

func runScreenVerdictMCP(market, style string, timeout time.Duration) (interface{}, error) {
	var profile *model.StrategyProfile
	for _, p := range model.BuiltinStrategyProfiles() {
		if p.Market == market && p.Style == style {
			profile = &p
			break
		}
	}
	if profile == nil {
		return nil, fmt.Errorf("no profile for market=%s style=%s", market, style)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	asset := screener.NewScreenedEquitiesAsset(market, style, profile)
	snap, err := asset.FetchSnapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("screen fetch: %w", err)
	}

	panel, err := asset.BuildPanel(snap)
	if err != nil {
		return nil, fmt.Errorf("build panel: %w", err)
	}

	verdict := asset.BuildVerdict(panel)
	buckets := fine.Bucketize(allScored(asset))

	return struct {
		Stance   string                    `json:"stance"`
		Regime   string                    `json:"regime"`
		Buckets  map[string]int            `json:"buckets"`
		Verdict  interface{}               `json:"verdict"`
		Warnings []string                  `json:"warnings,omitempty"`
	}{
		Stance:  verdict.Stance,
		Regime:  verdict.Regime,
		Buckets: bucketCounts(buckets),
		Verdict: verdict,
		Warnings: panel.StaleWarnings,
	}, nil
}

func allScored(a *screener.ScreenedEquitiesAsset) []model.ScreenedCandidate {
	c, _ := a.Candidates()
	return c
}

func bucketCounts(buckets map[string][]model.ScreenedCandidate) map[string]int {
	return map[string]int{
		"core":    len(buckets[fine.BucketCore]),
		"watch":   len(buckets[fine.BucketWatch]),
		"reserve": len(buckets[fine.BucketReserve]),
		"reject":  len(buckets[fine.BucketReject]),
	}
}
