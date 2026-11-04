package client

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/cache"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
	"github.com/Ricaardo/nimbus-os/datasources/binance"
	"github.com/Ricaardo/nimbus-os/datasources/coinmetrics"
	"github.com/shopspring/decimal"
)

const (
	BTCFullHistoryStart = "2010-07-18"

	btcDailyHistoryCacheSchema = 1
	btcDailyHistoryCacheFile   = "btc_daily_history.json"
	btcRecentRefreshDays       = 30
	btcHistoryFreshSlackDays   = 3
	coinMetricsPriceLookback   = 10000
)

// BTCDailyPoint is an oldest-first daily BTC close used by production
// indicators and backtests. Volume is available only for Binance-covered days.
type BTCDailyPoint struct {
	Date   string          `json:"date"`
	Close  decimal.Decimal `json:"close"`
	Volume decimal.Decimal `json:"volume,omitempty"`
	Source string          `json:"source,omitempty"`
}

type btcDailyHistoryCache struct {
	SchemaVersion int             `json:"schema_version"`
	UpdatedAt     string          `json:"updated_at"`
	Points        []BTCDailyPoint `json:"points"`
}

// BTCDailyHistoryCachePath returns the on-disk cache path for full BTC daily
// history. GUANFU_BTC_KLINE_CACHE is intentionally compatible with backtest
// workflows that want a stable, explicit kline cache.
func BTCDailyHistoryCachePath(cacheDir string) string {
	if path := strings.TrimSpace(os.Getenv("GUANFU_BTC_KLINE_CACHE")); path != "" {
		return expandHome(path)
	}
	if cacheDir == "" {
		cacheDir = cache.DefaultDir()
	}
	return filepath.Join(cacheDir, btcDailyHistoryCacheFile)
}

func (c *RealClient) loadOrUpdateBTCDailyHistory(ctx context.Context) ([]BTCDailyPoint, error) {
	return LoadOrUpdateBTCDailyHistory(ctx, c.cacheDir)
}

// LoadOrUpdateBTCDailyHistory loads the persistent BTC daily cache, seeds it
// from CoinMetrics PriceUSD when needed, and overlays recent Binance candles so
// the latest daily/intraday close is refreshed on each uncached run.
func LoadOrUpdateBTCDailyHistory(ctx context.Context, cacheDir string) ([]BTCDailyPoint, error) {
	path := BTCDailyHistoryCachePath(cacheDir)

	points, cacheErr := LoadBTCDailyHistoryCache(path)
	if cacheErr != nil && !os.IsNotExist(cacheErr) {
		// A corrupt cache should not permanently block a fresh rebuild.
		points = nil
	}

	if ok, _ := btcDailyHistoryCoversFullRange(points, time.Now()); !ok {
		full, err := FetchCoinMetricsBTCPriceUSDHistory(ctx, btcFullHistoryStartDate(), time.Now().UTC())
		if err != nil {
			if len(points) == 0 {
				return nil, err
			}
		} else {
			points = mergeBTCDailyHistory(points, full)
		}
	}

	recent, err := fetchBinanceBTCDailyHistory(ctx, btcRecentRefreshDays)
	if err == nil && len(recent) > 0 {
		points = mergeBTCDailyHistory(points, recent)
	} else if len(points) == 0 {
		return nil, fmt.Errorf("binance BTC daily refresh failed with no cached history: %w", err)
	}

	points = normalizeBTCDailyHistory(points)
	if ok, reason := btcDailyHistoryCoversFullRange(points, time.Now()); !ok {
		return nil, fmt.Errorf("BTC daily history is incomplete: %s", reason)
	}

	if err := SaveBTCDailyHistoryCache(path, points); err != nil {
		return nil, err
	}
	return points, nil
}

func LoadBTCDailyHistoryCache(path string) ([]BTCDailyPoint, error) {
	data, err := os.ReadFile(expandHome(path))
	if err != nil {
		return nil, err
	}

	var wrapped btcDailyHistoryCache
	if err := json.Unmarshal(data, &wrapped); err == nil && len(wrapped.Points) > 0 {
		if wrapped.SchemaVersion != 0 && wrapped.SchemaVersion != btcDailyHistoryCacheSchema {
			return nil, fmt.Errorf("BTC daily cache schema %d != %d", wrapped.SchemaVersion, btcDailyHistoryCacheSchema)
		}
		return normalizeBTCDailyHistory(wrapped.Points), nil
	}

	// Backward-compatible cache format used by guanfu-backtest:
	// {"YYYY-MM-DD": close_price, ...}
	var legacy map[string]float64
	if err := json.Unmarshal(data, &legacy); err != nil {
		return nil, err
	}
	points := make([]BTCDailyPoint, 0, len(legacy))
	for date, close := range legacy {
		if close <= 0 || math.IsNaN(close) || math.IsInf(close, 0) {
			continue
		}
		points = append(points, BTCDailyPoint{
			Date:   date,
			Close:  decimal.NewFromFloat(close),
			Source: "legacy:kline_cache",
		})
	}
	return normalizeBTCDailyHistory(points), nil
}

func SaveBTCDailyHistoryCache(path string, points []BTCDailyPoint) error {
	path = expandHome(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload := btcDailyHistoryCache{
		SchemaVersion: btcDailyHistoryCacheSchema,
		UpdatedAt:     time.Now().UTC().Format(time.RFC3339),
		Points:        normalizeBTCDailyHistory(points),
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return cache.WriteFileAtomic(path, data, 0o644)
}

func FetchCoinMetricsBTCPriceUSDHistory(ctx context.Context, from, to time.Time) ([]BTCDailyPoint, error) {
	pts, err := coinmetrics.AssetMetrics(ctx, coinmetrics.Query{
		Asset:   "btc",
		Metrics: []string{"PriceUSD"},
		Start:   from,
		End:     to,
		Limit:   coinMetricsPriceLookback,
	})
	if err != nil {
		return nil, err
	}

	var out []BTCDailyPoint
	for _, p := range pts {
		close, err := decimal.NewFromString(p.Metrics["PriceUSD"])
		if err != nil || close.LessThanOrEqual(decimal.Zero) {
			continue
		}
		out = append(out, BTCDailyPoint{
			Date:   p.Time.UTC().Format("2006-01-02"),
			Close:  close,
			Source: "coinmetrics:PriceUSD",
		})
	}

	out = normalizeBTCDailyHistory(out)
	if len(out) == 0 {
		return nil, fmt.Errorf("coinmetrics PriceUSD returned empty BTC history")
	}
	return out, nil
}

func fetchBinanceBTCDailyHistory(ctx context.Context, targetDays int) ([]BTCDailyPoint, error) {
	if targetDays <= 0 {
		targetDays = btcRecentRefreshDays
	}
	bars, err := binance.Klines(ctx, "BTCUSDT", "1d", targetDays)
	if err != nil {
		return nil, err
	}
	out := make([]BTCDailyPoint, 0, len(bars))
	for _, b := range bars {
		p, ok := btcDailyPointFromKline(b)
		if ok {
			out = append(out, p)
		}
	}
	return normalizeBTCDailyHistory(out), nil
}

func btcDailyPointFromKline(k binance.Kline) (BTCDailyPoint, bool) {
	if k.Close <= 0 {
		return BTCDailyPoint{}, false
	}
	return BTCDailyPoint{
		Date:   time.UnixMilli(k.OpenTime).UTC().Format("2006-01-02"),
		Close:  decimal.NewFromFloat(k.Close),
		Volume: decimal.NewFromFloat(k.Volume),
		Source: "binance:BTCUSDT",
	}, true
}

func applyBTCDailyHistoryToSnapshot(points []BTCDailyPoint, snap *model.MarketSnapshot) error {
	points = normalizeBTCDailyHistory(points)
	if len(points) == 0 {
		return fmt.Errorf("empty BTC daily history")
	}

	n := len(points)
	snap.BTCPriceHistory = make([]decimal.Decimal, n)
	snap.BTCVolumeHistory = make([]decimal.Decimal, n)
	for i, p := range points {
		idx := n - 1 - i
		snap.BTCPriceHistory[idx] = p.Close
		snap.BTCVolumeHistory[idx] = p.Volume
	}

	latest := points[n-1]
	snap.BTCPrice = latest.Close
	snap.BTCVolume24h = latest.Volume
	if t, err := time.Parse("2006-01-02", latest.Date); err == nil {
		snap.BTCPriceAsOf = t.UTC().Format(time.RFC3339)
	} else {
		snap.BTCPriceAsOf = latest.Date
	}
	return nil
}

func normalizeBTCDailyHistory(points []BTCDailyPoint) []BTCDailyPoint {
	byDate := make(map[string]BTCDailyPoint, len(points))
	for _, p := range points {
		t, err := time.Parse("2006-01-02", p.Date)
		if err != nil || p.Close.LessThanOrEqual(decimal.Zero) {
			continue
		}
		p.Date = t.UTC().Format("2006-01-02")
		byDate[p.Date] = p
	}
	out := make([]BTCDailyPoint, 0, len(byDate))
	for _, p := range byDate {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

func mergeBTCDailyHistory(base, updates []BTCDailyPoint) []BTCDailyPoint {
	merged := make([]BTCDailyPoint, 0, len(base)+len(updates))
	merged = append(merged, base...)
	merged = append(merged, updates...)
	return normalizeBTCDailyHistory(merged)
}

func btcDailyHistoryCoversFullRange(points []BTCDailyPoint, now time.Time) (bool, string) {
	points = normalizeBTCDailyHistory(points)
	if len(points) == 0 {
		return false, "empty history"
	}

	minDays := btcMinFullHistoryDays(now)
	if len(points) < minDays {
		return false, fmt.Sprintf("has %d days, need at least %d days from %s", len(points), minDays, BTCFullHistoryStart)
	}

	first, _ := time.Parse("2006-01-02", points[0].Date)
	start := btcFullHistoryStartDate()
	if first.After(start.AddDate(0, 0, btcHistoryFreshSlackDays)) {
		return false, fmt.Sprintf("starts at %s, need %s", points[0].Date, BTCFullHistoryStart)
	}

	last, _ := time.Parse("2006-01-02", points[len(points)-1].Date)
	requiredLatest := btcLatestRequiredDate(now)
	if last.Before(requiredLatest.AddDate(0, 0, -btcHistoryFreshSlackDays)) {
		return false, fmt.Sprintf("latest daily close is %s, need at least %s", points[len(points)-1].Date, requiredLatest.Format("2006-01-02"))
	}
	return true, ""
}

func btcMinFullHistoryDays(now time.Time) int {
	start := btcFullHistoryStartDate()
	latest := btcLatestRequiredDate(now)
	if latest.Before(start) {
		return btcHistoryMinFreshDays
	}
	days := int(latest.Sub(start).Hours()/24) + 1 - btcHistoryFreshSlackDays
	if days < btcHistoryMinFreshDays {
		return btcHistoryMinFreshDays
	}
	return days
}

func btcFullHistoryStartDate() time.Time {
	t, _ := time.Parse("2006-01-02", BTCFullHistoryStart)
	return t.UTC()
}

func btcLatestRequiredDate(now time.Time) time.Time {
	todayUTC := now.UTC().Truncate(24 * time.Hour)
	return todayUTC.AddDate(0, 0, -1)
}

func expandHome(path string) string {
	if path == "" || path[0] != '~' {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if len(path) == 1 {
		return home
	}
	if path[1] == '/' {
		return filepath.Join(home, path[2:])
	}
	return path
}
