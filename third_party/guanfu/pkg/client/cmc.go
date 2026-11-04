// CoinMarketCap market-context refresh source.
//
// Scope: market reading only. CMC is useful for independent crypto market
// context (global metrics, BTC quote, exchange/DEX coverage later), but this
// source deliberately does not replace the canonical BTC CoinMetrics+Binance
// price history or any forecast feature bundle.

package client

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
	"github.com/Ricaardo/nimbus-os/datasources/cmc"
)

const (
	cmcAPIKeyEnv    = "CMC_API_KEY"
	cmcAPIKeyEnvAlt = "CMC_PRO_API_KEY"
	cmcSourceKey    = "cmc_market_context"
)

// CMCMarketContextSource pulls a small latest-only market context set from
// CoinMarketCap. It stores daily snapshots under cmc_* PriceStore keys.
type CMCMarketContextSource struct{}

func (CMCMarketContextSource) Key() string { return cmcSourceKey }
func (CMCMarketContextSource) DisplayName() string {
	return "cmc_market_context (CMC global metrics + BTC quote)"
}

func (c CMCMarketContextSource) Refresh(ctx context.Context, s *store.PriceStore) (*RefreshResult, error) {
	stale, lastDate := staleThreshold(s, "cmc_total_market_cap_usd")
	apiKey := cmcAPIKey()
	if apiKey == "" {
		count, _ := s.Count("cmc_total_market_cap_usd")
		return &RefreshResult{
			Key:         c.Key(),
			DisplayName: c.DisplayName(),
			Mode:        "skip",
			SkipReason:  "config",
			Stale:       stale,
			Action:      "configure",
			Total:       count,
			LastDate:    lastDate,
			Error:       cmcAPIKeyEnv + " not set",
		}, nil
	}
	if !stale {
		count, _ := s.Count("cmc_total_market_cap_usd")
		return &RefreshResult{
			Key: c.Key(), DisplayName: c.DisplayName(),
			Mode: "skip", SkipReason: "fresh", Action: "ignore",
			Total: count, LastDate: lastDate,
		}, nil
	}

	points, err := fetchCMCMarketContext(ctx, apiKey)
	if err != nil {
		return nil, err
	}
	if len(points) == 0 {
		count, _ := s.Count("cmc_total_market_cap_usd")
		return &RefreshResult{
			Key: c.Key(), DisplayName: c.DisplayName(),
			Mode: "incremental", SkipReason: "no_new_data",
			Total: count, LastDate: lastDate,
		}, nil
	}

	mode := "full"
	if lastDate != "" {
		mode = "incremental"
	}
	added := 0
	for key, pt := range points {
		before, _ := s.Count(key)
		if err := s.Append(key, []store.PricePoint{pt}); err != nil {
			return nil, fmt.Errorf("%s append: %w", key, err)
		}
		after, _ := s.Count(key)
		if after > before {
			added += after - before
		}
	}
	total, _ := s.Count("cmc_total_market_cap_usd")
	last, _ := s.LastDate("cmc_total_market_cap_usd")
	return &RefreshResult{
		Key: c.Key(), DisplayName: c.DisplayName(),
		Mode: mode, Added: added, Total: total, LastDate: last,
	}, nil
}

func fetchCMCMarketContext(ctx context.Context, apiKey string) (map[string]store.PricePoint, error) {
	global, err := cmc.GetGlobalMetrics(ctx, apiKey)
	if err != nil {
		return nil, err
	}
	btc, err := cmc.GetQuote(ctx, apiKey, 1)
	if err != nil {
		return nil, err
	}

	out := map[string]store.PricePoint{}
	addPoint := func(key string, value float64, date, source string) {
		if value <= 0 || date == "" {
			return
		}
		out[key] = store.PricePoint{Date: date, Close: value, Source: source}
	}

	gDate := dateFromRFC3339(global.LastUpdated)
	addPoint("cmc_total_market_cap_usd", global.TotalMarketCapUSD, gDate, "cmc:global-metrics")
	addPoint("cmc_total_volume_24h_usd", global.TotalVolume24hUSD, gDate, "cmc:global-metrics")
	addPoint("cmc_altcoin_market_cap_usd", global.AltcoinMarketCapUSD, gDate, "cmc:global-metrics")
	addPoint("cmc_altcoin_volume_24h_usd", global.AltcoinVolume24hUSD, gDate, "cmc:global-metrics")
	addPoint("cmc_btc_dominance_pct", global.BTCDominance, gDate, "cmc:global-metrics")
	addPoint("cmc_eth_dominance_pct", global.ETHDominance, gDate, "cmc:global-metrics")

	bDate := dateFromRFC3339(btc.LastUpdated)
	addPoint("cmc_btc_price_usd", btc.PriceUSD, bDate, "cmc:cryptocurrency-quotes")
	addPoint("cmc_btc_volume_24h_usd", btc.Volume24hUSD, bDate, "cmc:cryptocurrency-quotes")
	addPoint("cmc_btc_market_cap_usd", btc.MarketCapUSD, bDate, "cmc:cryptocurrency-quotes")
	return out, nil
}

func cmcAPIKey() string {
	if key := strings.TrimSpace(os.Getenv(cmcAPIKeyEnv)); key != "" {
		return key
	}
	if key := strings.TrimSpace(os.Getenv(cmcAPIKeyEnvAlt)); key != "" {
		return key
	}
	path := os.Getenv("GUANFU_ENV_FILE")
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		path = filepath.Join(home, ".guanfu", "env")
	}
	if key := readEnvFileValue(path, cmcAPIKeyEnv); key != "" {
		return key
	}
	return readEnvFileValue(path, cmcAPIKeyEnvAlt)
}

func readEnvFileValue(path, key string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	prefix := key + "="
	exportPrefix := "export " + prefix
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		switch {
		case strings.HasPrefix(line, exportPrefix):
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, exportPrefix)), `"'`)
		case strings.HasPrefix(line, prefix):
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, prefix)), `"'`)
		}
	}
	return ""
}

func dateFromRFC3339(value string) string {
	if value == "" {
		return time.Now().UTC().Format("2006-01-02")
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Now().UTC().Format("2006-01-02")
	}
	return t.UTC().Format("2006-01-02")
}
