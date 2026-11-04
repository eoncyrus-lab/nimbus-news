package client

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/cache"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
	"github.com/Ricaardo/nimbus-os/datasources/alternativeme"
	"github.com/Ricaardo/nimbus-os/datasources/binance"
	"github.com/Ricaardo/nimbus-os/datasources/coingecko"
	"github.com/go-resty/resty/v2"
	"github.com/shopspring/decimal"
)

type RealClient struct {
	client   *resty.Client
	cache    *cache.Cache
	cacheDir string
}

const (
	btcHistoryTargetDays   = 3000
	btcHistoryMinFreshDays = 1200
	marketCacheMaxAge      = 4 * time.Hour
)

// NewRealClient 创建RealClient实例并初始化缓存
func NewRealClient() *RealClient {
	cacheDir := cache.DefaultDir()

	cache, err := cache.NewCache(cacheDir)
	if err != nil {
		log.Printf("Failed to create cache: %v, proceeding without cache", err)
		cache = nil
	}

	return &RealClient{
		client: resty.New().
			SetTimeout(10 * time.Second).
			SetRetryCount(3),
		cache:    cache,
		cacheDir: cacheDir,
	}
}

// Responses

type CGMarketItem struct {
	ID           string          `json:"id"`
	Symbol       string          `json:"symbol"`
	CurrentPrice decimal.Decimal `json:"current_price"`
	MarketCap    decimal.Decimal `json:"market_cap"`
}

type ExchangeRateResp struct {
	Rates map[string]decimal.Decimal `json:"rates"`
}

func (c *RealClient) GetSnapshot(ctx context.Context) (*model.MarketSnapshot, error) {
	log.Println("Fetching data from real APIs...")

	// 检查缓存中是否有今天的快照
	today := time.Now().Truncate(24 * time.Hour)
	if c.cache != nil {
		if cached, exists := c.cache.Get(today); exists {
			if ok, reason := usableCachedSnapshot(cached); ok {
				log.Println("Using cached data for today")
				return cached, nil
			} else {
				log.Printf("Ignoring cached coinman data: %s", reason)
			}
		}
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	snap := &model.MarketSnapshot{
		Date:                  time.Now(),
		SnapshotSchemaVersion: model.CurrentMarketSnapshotSchemaVersion,
		FetchedAt:             time.Now().UTC().Format(time.RFC3339),
	}
	var errList []error

	// Helper to collect errors
	addErr := func(e error) {
		mu.Lock()
		errList = append(errList, e)
		mu.Unlock()
	}
	addWarning := func(format string, args ...interface{}) {
		mu.Lock()
		snap.SourceWarnings = append(snap.SourceWarnings, fmt.Sprintf(format, args...))
		mu.Unlock()
	}

	// 1. Binance BTC Data (Price, Volume, History)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := c.fetchBTCData(ctx, snap); err != nil {
			log.Printf("Error fetching BTC data: %v", err)
			addErr(err)
			addWarning("binance BTC data fetch failed: %v", err)
		}
	}()

	// 2. Binance ETH Data (Price, Volume, History)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := c.fetchETHData(ctx, snap); err != nil {
			log.Printf("Error fetching ETH data: %v", err)
			addWarning("binance ETH data fetch failed: %v", err)
			// 不将ETH获取错误作为致命错误，因为BTC数据更重要
		}
	}()

	// 3. CoinGecko Top 50 List
	// We need this list first to fetch their histories
	// So we can't fully parallelize the history fetching yet.
	// But we can start fetching the list.
	var top50 []CGMarketItem
	wg.Add(1)
	go func() {
		defer wg.Done()
		var err error
		top50, err = c.fetchTop50List(ctx)
		if err != nil {
			log.Printf("Error fetching Top 50: %v", err)
			addWarning("coingecko top50 fetch failed: %v", err)
			addErr(err)
		} else {
			mu.Lock()
			snap.Top50Fetched = true
			mu.Unlock()
		}
	}()

	// 4. Fear & Greed
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := c.fetchFearGreed(ctx, snap); err != nil {
			log.Printf("Error fetching FearGreed: %v", err)
			addWarning("alternative.me fear & greed fetch failed: %v", err)
			// Fear & Greed is non-critical
		}
	}()

	// 5. USDT & USD Rates
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := c.fetchRates(ctx, snap); err != nil {
			log.Printf("Error fetching Rates: %v", err)
			addWarning("fx/usdt rates fetch failed: %v", err)
			addErr(err)
		}
	}()

	// 6. Global Market Cap
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := c.fetchGlobalCap(ctx, snap); err != nil {
			log.Printf("Error fetching Global Cap: %v", err)
			addWarning("coingecko global market cap fetch failed: %v", err)
			addErr(err)
		}
	}()

	// 7. Stablecoin Market Cap
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := c.fetchStablecoinCap(ctx, snap); err != nil {
			log.Printf("Error fetching Stablecoin Cap: %v", err)
			addWarning("coingecko stablecoin market cap fetch failed: %v", err)
			// 不将稳定币数据获取作为致命错误
		}
	}()

	// 8. Altcoin Season Index — 在 Step 10 拿到 Top50 历史后计算，此处跳过

	// 9. Binance Futures Data (Funding Rate & OI)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := c.fetchFuturesData(ctx, snap); err != nil {
			log.Printf("Error fetching Futures Data: %v", err)
			addWarning("binance futures data fetch failed: %v", err)
			addErr(err)
		}
	}()

	// 10. mempool.space network data (hash rate, difficulty, mempool depth)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if mp, err := FetchMempoolData(ctx); err == nil && mp != nil {
			snap.MempoolFetched = true
			snap.HashRateEHs = decimal.NewFromFloat(mp.HashRateNowEHs)
			snap.HashRibbonsLabel = mp.HashRibbons30vs60
			snap.DifficultyChangePct = decimal.NewFromFloat(mp.DifficultyChangePct)
			snap.MempoolMB = decimal.NewFromFloat(mp.MempoolMB)
			if !mp.AsOf.IsZero() {
				snap.MempoolAsOf = mp.AsOf.Format(time.RFC3339)
			}
			for _, w := range mp.Warnings {
				addWarning("%s", w)
			}
		} else if err != nil {
			log.Printf("Error fetching mempool.space: %v", err)
			addWarning("mempool.space fetch failed: %v", err)
		}
	}()

	// 11. SoSoValue BTC Spot ETF flows (7d/30d net inflow)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if etf, err := FetchBTCETFData(ctx); err == nil && etf != nil {
			snap.ETFNetFlow7dUSD = decimal.NewFromFloat(etf.NetInflow7dUSD)
			snap.ETFNetFlow30dUSD = decimal.NewFromFloat(etf.NetInflow30dUSD)
			snap.ETFTotalAssetUSD = decimal.NewFromFloat(etf.TotalAssetsUSD)
			snap.ETFStaleDays = etf.StaleDays
			if !etf.LatestDate.IsZero() {
				snap.ETFAsOf = etf.LatestDate.Format("2006-01-02")
			}
			if etf.StaleDays >= 2 {
				addWarning("sosovalue ETF data is stale: %d days old", etf.StaleDays)
			}
		} else if err != nil {
			log.Printf("Error fetching ETF data: %v", err)
			addWarning("sosovalue ETF data fetch failed: %v", err)
		}
	}()

	// 12.5 Deribit 期权 (DVOL + 25Δ skew). Best-effort, never errors.
	wg.Add(1)
	go func() {
		defer wg.Done()
		opt := FetchBTCDeribitOptions(ctx)
		snap.DVOLAvailable = opt.DVOLAvailable
		if opt.DVOLAvailable {
			snap.DVOL = decimal.NewFromFloat(opt.DVOL)
			snap.DVOL60dTrendPct = decimal.NewFromFloat(opt.DVOL60dTrendPct)
			snap.DVOLAsOf = opt.DVOLAsOf.Format("2006-01-02")
			snap.DVOLHistory = make([]decimal.Decimal, len(opt.DVOLHistory))
			for i, v := range opt.DVOLHistory {
				snap.DVOLHistory[i] = decimal.NewFromFloat(v)
			}
		}
		snap.SkewAvailable = opt.SkewAvailable
		if opt.SkewAvailable {
			snap.Skew25dNearTermPct = decimal.NewFromFloat(opt.Skew25dNearTermPct)
			snap.SkewAsOf = opt.SkewAsOf.Format("2006-01-02")
			snap.SkewExpiry = opt.SkewExpiry
		}
		for _, w := range opt.Warnings {
			log.Printf("Deribit warning: %s", w)
			addWarning("deribit warning: %s", w)
		}
	}()

	// 12. CoinMetrics on-chain valuation (MVRV / NUPL / MVRV Z)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if onchain, err := FetchBTCOnchainValuation(ctx); err == nil && onchain != nil {
			snap.OnchainValuationFetched = true
			snap.CapMVRVCur = decimal.NewFromFloat(onchain.MVRV)
			snap.MVRVZScore = decimal.NewFromFloat(onchain.MVRVZScore)
			snap.NUPL = decimal.NewFromFloat(onchain.NUPL)
			snap.MVRVQuantile = decimal.NewFromFloat(onchain.MVRVQuantile)
			snap.NUPLQuantile = decimal.NewFromFloat(onchain.NUPLQuantile)
			snap.MarketCapCurUSD = decimal.NewFromFloat(onchain.MarketCapUSD)
			snap.RealizedCapUSD = decimal.NewFromFloat(onchain.RealizedCapUSD)
			if !onchain.LatestDate.IsZero() {
				snap.OnchainValuationAsOf = onchain.LatestDate.Format("2006-01-02")
			}
			if onchain.StaleDays >= 2 {
				addWarning("coinmetrics on-chain valuation data is stale: %d days old", onchain.StaleDays)
			}
			if onchain.RealizedCapMode != "" && onchain.RealizedCapMode != "direct_CapRealUSD" {
				addWarning("coinmetrics realized cap is implied from CapMVRVCur because CapRealUSD is not available on the current endpoint")
			}
			for _, w := range onchain.Warnings {
				addWarning("%s", w)
			}
		} else if err != nil {
			log.Printf("Error fetching CoinMetrics valuation data: %v", err)
			addWarning("coinmetrics valuation fetch failed: %v", err)
		}
	}()

	wg.Wait()

	// 13. FRED 宏观（依赖 BTC history 已就绪，故 wg.Wait 后串行执行）
	if macro, err := FetchMacroData(ctx, snap.BTCPriceHistory); err == nil && macro != nil {
		snap.MacroFetched = true
		snap.DXY60dTrendPct = decimal.NewFromFloat(macro.DXY60dTrendPct)
		snap.DXYLatest = decimal.NewFromFloat(macro.DXYLatest)
		snap.DXYAsOf = macro.DXYAsOf
		snap.RealYield10YPct = decimal.NewFromFloat(macro.RealYield10YPct)
		snap.RealYield10YAsOf = macro.RealYield10YAsOf
		snap.M2YoYPct = decimal.NewFromFloat(macro.M2YoYPct)
		snap.M2LatestB = decimal.NewFromFloat(macro.M2LatestB)
		snap.M2AsOf = macro.M2AsOf
		snap.SPXCorrelation30d = decimal.NewFromFloat(macro.SPXCorrelation30d)
		snap.SPXAsOf = macro.SPXAsOf
		// US Liquidity
		snap.FedAssetsB = decimal.NewFromFloat(macro.FedAssetsB)
		snap.FedAssetsAsOf = macro.FedAssetsAsOf
		snap.RRPB = decimal.NewFromFloat(macro.RRPB)
		snap.RRPAsOf = macro.RRPAsOf
		snap.TGA_B = decimal.NewFromFloat(macro.TGA_B)
		snap.TGAAsOf = macro.TGAAsOf
		snap.NetLiquidityB = decimal.NewFromFloat(macro.NetLiquidityB)
		snap.FedAssets60dTrendPct = decimal.NewFromFloat(macro.FedAssets60dTrendPct)
		snap.RRP60dTrendPct = decimal.NewFromFloat(macro.RRP60dTrendPct)
		snap.TGA60dTrendPct = decimal.NewFromFloat(macro.TGA60dTrendPct)
		snap.NetLiq60dTrendPct = decimal.NewFromFloat(macro.NetLiq60dTrendPct)
		snap.HYSpreadBps = decimal.NewFromFloat(macro.HYSpreadBps)
		snap.HYSpreadAsOf = macro.HYSpreadAsOf
		snap.YieldCurve10Y2YBps = decimal.NewFromFloat(macro.YieldCurve10Y2YBps)
		snap.YieldCurveAsOf = macro.YieldCurveAsOf
		for _, w := range macro.StaleWarnings {
			log.Printf("FRED warning: %s", w)
			addWarning("fred warning: %s", w)
		}
	} else if err != nil {
		log.Printf("Error fetching FRED macro data: %v", err)
		addWarning("fred macro data fetch failed: %v", err)
	} else {
		addWarning("fred macro data unavailable: FRED_API_KEY is not set")
	}

	// 14. cross-asset fetch (Binance PAXG + Futu/Yahoo QQQ/SPY/GLD/UUP/VIXY)
	if ca, err := FetchCrossAssetData(ctx, btcHistoryTargetDays); err == nil && ca != nil {
		snap.CrossAssetFetched = true
		snap.GoldPriceUSD = decimal.NewFromFloat(ca.GoldPrice)
		snap.QQQPrice = decimal.NewFromFloat(ca.QQQPrice)
		snap.SPYPrice = decimal.NewFromFloat(ca.SPYPrice)
		snap.GoldPriceAsOf = ca.GoldPriceAsOf
		snap.QQQPriceAsOf = ca.QQQPriceAsOf
		snap.SPYPriceAsOf = ca.SPYPriceAsOf
		snap.GoldHistory = toDecimalSlice(ca.GoldHistory)
		snap.QQQHistory = toDecimalSlice(ca.QQQHistory)
		snap.SPYHistory = toDecimalSlice(ca.SPYHistory)
		// v3.1 extended (Futu)
		snap.GoldETFPriceUSD = decimal.NewFromFloat(ca.GLDPrice)
		snap.GoldETFHistory = toDecimalSlice(ca.GLDHistory)
		snap.GoldETFAsOf = ca.GLDPriceAsOf
		snap.WTIPrice = decimal.NewFromFloat(ca.WTIPrice)
		snap.WTIHistory = toDecimalSlice(ca.WTIHistory)
		snap.WTIPriceAsOf = ca.WTIPriceAsOf
		snap.OilPriceSource = ca.OilPriceSource
		snap.UUPPrice = decimal.NewFromFloat(ca.UUPPrice)
		snap.UUPHistory = toDecimalSlice(ca.UUPHistory)
		snap.UUPPriceAsOf = ca.UUPPriceAsOf
		snap.VIXYPrice = decimal.NewFromFloat(ca.VIXYPrice)
		snap.VIXYHistory = toDecimalSlice(ca.VIXYHistory)
		snap.VIXYPriceAsOf = ca.VIXYPriceAsOf
		snap.TLTPrice = decimal.NewFromFloat(ca.TLTPrice)
		snap.TLTHistory = toDecimalSlice(ca.TLTHistory)
		snap.TLTPriceAsOf = ca.TLTPriceAsOf
		for _, w := range ca.Warnings {
			addWarning("cross-asset: %s", w)
		}
	} else if err != nil {
		log.Printf("Error fetching cross-asset data: %v", err)
		addWarning("cross-asset fetch failed: %v", err)
	}

	// If critical errors (BTC data missing), return error
	if len(snap.BTCPriceHistory) == 0 {
		return nil, fmt.Errorf("failed to fetch critical BTC data: %v", errList)
	}

	// Log ETH data status
	if len(snap.ETHPriceHistory) == 0 {
		log.Println("Warning: ETH data not available, will use BTC-only calculation")
	}

	// 10. Fetch Histories for Top 50 (Dependent on Step 2)
	if len(top50) > 0 {
		log.Printf("Fetching history for %d coins...", len(top50))
		snap.Top50Fetched = true
		coins := c.fetchCoinsHistory(ctx, top50)
		snap.Top50Coins = coins
		// 山寨季指数：Top 50 中 90 日跑赢 BTC 的占比 × 100
		if asi, ok := calculateAltcoinSeason(coins, snap.BTCPriceHistory); ok {
			snap.AltcoinSeasonIndex = asi
			snap.AltcoinSeasonAvailable = true
		} else {
			addWarning("binance top50 history unavailable: altcoin season omitted")
		}
	} else {
		addWarning("coingecko top50 unavailable: altcoin season omitted")
	}

	// TotalMarketCapHistory 目前不被引擎消费，不再合成。

	// 如果启用了缓存，保存快照
	if c.cache != nil {
		if err := c.cache.Save(snap); err != nil {
			log.Printf("Failed to save snapshot to cache: %v", err)
		}
	}

	// Sync cross-asset price data to PriceStore for incremental reuse
	SyncSnapshotToPriceStore(snap)

	return snap, nil
}

func usableCachedSnapshot(snap *model.MarketSnapshot) (bool, string) {
	if snap == nil {
		return false, "snapshot is nil"
	}
	if snap.SnapshotSchemaVersion != model.CurrentMarketSnapshotSchemaVersion {
		return false, fmt.Sprintf("schema version %d != %d", snap.SnapshotSchemaVersion, model.CurrentMarketSnapshotSchemaVersion)
	}
	requiredBTCHistoryDays := btcMinFullHistoryDays(time.Now())
	if len(snap.BTCPriceHistory) < requiredBTCHistoryDays {
		return false, fmt.Sprintf("BTC history has %d days, need at least %d for full 2010-to-latest AHR999 inputs", len(snap.BTCPriceHistory), requiredBTCHistoryDays)
	}
	if snap.BTCPriceAsOf == "" {
		return false, "missing BTC source timestamp"
	}
	if snap.BTCPrice.IsZero() {
		return false, "missing BTC price"
	}
	if snap.FetchedAt == "" {
		return false, "missing fetch timestamp"
	}
	fetchedAt, err := time.Parse(time.RFC3339, snap.FetchedAt)
	if err != nil {
		return false, fmt.Sprintf("invalid fetch timestamp %q", snap.FetchedAt)
	}
	if age := time.Since(fetchedAt); age > marketCacheMaxAge {
		return false, fmt.Sprintf("cached snapshot is %s old, max %s", age.Truncate(time.Second), marketCacheMaxAge)
	}
	return true, ""
}

// --- Implementation Methods ---

func (c *RealClient) fetchBTCData(ctx context.Context, snap *model.MarketSnapshot) error {
	points, err := c.loadOrUpdateBTCDailyHistory(ctx)
	if err != nil {
		return err
	}
	return applyBTCDailyHistoryToSnapshot(points, snap)
}

// fetchETHData 获取ETH数据（与 BTC 同量级，3000 天）
func (c *RealClient) fetchETHData(ctx context.Context, snap *model.MarketSnapshot) error {
	klines, err := binance.KlinesDeep(ctx, "ETHUSDT", btcHistoryTargetDays)
	if err != nil {
		return err
	}

	if len(klines) == 0 {
		return fmt.Errorf("empty ETH klines")
	}

	// klines are ascending; store newest-first (index 0 = latest).
	n := len(klines)
	snap.ETHPriceHistory = make([]decimal.Decimal, n)
	snap.ETHVolumeHistory = make([]decimal.Decimal, n)

	for i, k := range klines {
		idx := n - 1 - i
		snap.ETHPriceHistory[idx] = decimal.NewFromFloat(k.Close)
		snap.ETHVolumeHistory[idx] = decimal.NewFromFloat(k.Volume)
		if idx == 0 {
			snap.ETHPriceAsOf = time.UnixMilli(k.OpenTime).UTC().Format("2006-01-02")
		}
	}

	snap.ETHPrice = snap.ETHPriceHistory[0]
	snap.ETHVolume24h = snap.ETHVolumeHistory[0]

	return nil
}

func (c *RealClient) fetchTop50List(ctx context.Context) ([]CGMarketItem, error) {
	coins, err := coingecko.CoinsMarkets(ctx, coingecko.MarketsParams{PerPage: 50})
	if err != nil {
		return nil, err
	}
	return cgCoinsToItems(coins), nil
}

// cgCoinsToItems maps shared coingecko.Coin (float64) to the local decimal item.
func cgCoinsToItems(coins []coingecko.Coin) []CGMarketItem {
	items := make([]CGMarketItem, len(coins))
	for i, c := range coins {
		items[i] = CGMarketItem{
			ID:           c.ID,
			Symbol:       c.Symbol,
			CurrentPrice: decimal.NewFromFloat(c.CurrentPrice),
			MarketCap:    decimal.NewFromFloat(c.MarketCap),
		}
	}
	return items
}

func (c *RealClient) fetchFearGreed(ctx context.Context, snap *model.MarketSnapshot) error {
	readings, err := alternativeme.FearGreedIndex(ctx, 1)
	if err != nil {
		return err
	}
	if len(readings) > 0 {
		fg := readings[0]
		snap.FearGreedIndex = decimal.NewFromInt(int64(fg.Value))
		snap.FearGreedFetched = true
		if !fg.Timestamp.IsZero() {
			snap.FearGreedAsOf = fg.Timestamp.Format(time.RFC3339)
		}
	}
	return nil
}

func (c *RealClient) fetchRates(ctx context.Context, snap *model.MarketSnapshot) error {
	var wg sync.WaitGroup

	// 1. USD/CNY
	wg.Add(1)
	go func() {
		defer wg.Done()
		var rateResp ExchangeRateResp
		// https://api.exchangerate-api.com/v4/latest/USD
		_, err := c.client.R().SetContext(ctx).SetResult(&rateResp).Get("https://api.exchangerate-api.com/v4/latest/USD")
		if err == nil {
			if r, ok := rateResp.Rates["CNY"]; ok {
				snap.USDPriceCNY = r
			}
		} else {
			log.Printf("Failed to fetch USD/CNY: %v", err)
		}
	}()

	// 2. USDT/CNY
	wg.Add(1)
	go func() {
		defer wg.Done()
		m, err := coingecko.SimplePrice(ctx, []string{"tether"}, []string{"cny"})
		if err == nil {
			snap.USDTPriceCNY = decimal.NewFromFloat(m["tether"]["cny"])
		} else {
			log.Printf("Failed to fetch USDT/CNY: %v", err)
		}
	}()

	wg.Wait()
	return nil
}

func (c *RealClient) fetchGlobalCap(ctx context.Context, snap *model.MarketSnapshot) error {
	g, err := coingecko.GetGlobal(ctx)
	if err != nil {
		return err
	}

	snap.TotalMarketCap = decimal.NewFromFloat(g.TotalMarketCapUSD)
	snap.GlobalMarketFetched = true
	if dom, ok := g.MarketCapPct["btc"]; ok {
		snap.BTCDominance = decimal.NewFromFloat(dom).Div(decimal.NewFromInt(100)) // 52.1 -> 0.521
	}

	return nil
}

func (c *RealClient) fetchCoinsHistory(ctx context.Context, items []CGMarketItem) []model.CoinSnapshot {
	// We need to fetch history for these coins.
	// Use Binance for speed. Map Symbol -> SymbolUSDT.
	// Limit concurrency to avoid rate limits (Binance 1200/min is plenty, but let's be safe).

	var coins []model.CoinSnapshot
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10) // Concurrency 10

	for _, item := range items {
		// Filter out stablecoins/wrapped coins based on Symbol if possible?
		// Doc says: exclude USDT, USDC, DAI, FDUSD, WBTC, stETH
		s := strings.ToUpper(item.Symbol)
		if s == "USDT" || s == "USDC" || s == "DAI" || s == "FDUSD" || s == "WBTC" || s == "STETH" {
			continue
		}

		wg.Add(1)
		go func(it CGMarketItem) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			if err := ctx.Err(); err != nil {
				return
			}

			// Try to fetch from Binance
			// Symbol mapping: "btc" -> "BTCUSDT"
			symbol := strings.ToUpper(it.Symbol) + "USDT"

			// We only need Close prices for MA120 (so 120 days)
			klines, err := binance.Klines(ctx, symbol, "1d", 121)

			if err == nil && len(klines) > 0 {
				// Reverse to newest-first.
				hist := make([]decimal.Decimal, len(klines))
				for i, k := range klines {
					idx := len(klines) - 1 - i
					hist[idx] = decimal.NewFromFloat(k.Close)
				}

				mu.Lock()
				coins = append(coins, model.CoinSnapshot{
					Symbol:       it.Symbol,
					Price:        it.CurrentPrice,
					PriceHistory: hist,
				})
				mu.Unlock()
			} else {
				// Failed to fetch from Binance (maybe pair doesn't exist, e.g. STETHUSDT?)
				// Just ignore for now
				// log.Printf("Skipped %s: %v", symbol, err)
			}
		}(item)
	}
	wg.Wait()
	return coins
}

func (c *RealClient) fetchFuturesData(ctx context.Context, snap *model.MarketSnapshot) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	markFetched := func() {
		mu.Lock()
		snap.FuturesFetched = true
		mu.Unlock()
	}

	// 1. Funding Rate
	wg.Add(1)
	go func() {
		defer wg.Done()
		f, err := binance.FundingRate(ctx, "BTCUSDT")
		if err != nil {
			log.Printf("Failed to fetch Funding Rate: %v", err)
			return
		}
		snap.BTCFundingRate = decimal.NewFromFloat(f.Rate)
		markFetched()
	}()

	// 2. Open Interest
	wg.Add(1)
	go func() {
		defer wg.Done()
		// API 返回的是 BTC 计价的合约数量。本字段只存 quantity（BTC），
		// Engine 在 BuildPanel 里用此时已就绪的 BTCPrice 折算 USD 价值。
		// 修了原代码并发条件竞争 bug：原逻辑在 BTCPrice 未到时把 qty 直接当 USD 值存。
		oi, err := binance.OpenInterestNow(ctx, "BTCUSDT")
		if err != nil {
			log.Printf("Failed to fetch OI: %v", err)
			return
		}
		snap.BTCOpenInterest = decimal.NewFromFloat(oi.OI)
		markFetched()
	}()

	wg.Wait()
	return nil
}

// fetchStablecoinCap 获取稳定币市值数据
func (c *RealClient) fetchStablecoinCap(ctx context.Context, snap *model.MarketSnapshot) error {
	// 稳定币市值：CoinGecko coins/markets 过滤主要稳定币
	coins, err := coingecko.CoinsMarkets(ctx, coingecko.MarketsParams{
		IDs:     []string{"tether", "usd-coin", "dai", "first-digital-usd", "frax"},
		PerPage: 100,
	})
	if err != nil {
		return err
	}

	// 计算总稳定币市值
	totalStablecoinCap := decimal.Zero
	for _, sc := range coins {
		totalStablecoinCap = totalStablecoinCap.Add(decimal.NewFromFloat(sc.MarketCap))
	}

	// 保存当前稳定币市值
	snap.StablecoinMarketCap = totalStablecoinCap
	snap.StablecoinMarketCapFetched = true

	return nil
}

// calculateAltcoinSeason 计算山寨季指数 — 顶层函数，不依赖 Receiver。
//
// 定义：Top 50（排除稳定币/wrapped）中，90 日价格涨幅跑赢 BTC 的币的占比 × 100。
// 与 blockchaincenter.net 的定义一致。我们已经有 Top 50 的 121 天历史，
// 可以独立计算，无需依赖外部 API。
func calculateAltcoinSeason(coins []model.CoinSnapshot, btcHistory []decimal.Decimal) (decimal.Decimal, bool) {
	if len(coins) < 10 || len(btcHistory) <= 90 {
		return decimal.Zero, false
	}

	btcPrice90dAgo := btcHistory[90]
	btcPriceNow := btcHistory[0]
	if btcPrice90dAgo.IsZero() {
		return decimal.Zero, false
	}
	btcReturn := btcPriceNow.Sub(btcPrice90dAgo).Div(btcPrice90dAgo)

	outperformed := 0
	total := 0
	for _, coin := range coins {
		if len(coin.PriceHistory) <= 90 {
			continue
		}
		coinPrice90d := coin.PriceHistory[90]
		coinPriceNow := coin.PriceHistory[0]
		if coinPrice90d.IsZero() {
			continue
		}
		coinReturn := coinPriceNow.Sub(coinPrice90d).Div(coinPrice90d)
		total++
		if coinReturn.GreaterThan(btcReturn) {
			outperformed++
		}
	}

	if total == 0 {
		return decimal.Zero, false
	}

	pct := float64(outperformed) / float64(total) * 100
	return decimal.NewFromFloat(pct), true
}

// toDecimalSlice converts []float64 -> []decimal.Decimal
func toDecimalSlice(vals []float64) []decimal.Decimal {
	out := make([]decimal.Decimal, len(vals))
	for i, v := range vals {
		out[i] = decimal.NewFromFloat(v)
	}
	return out
}
