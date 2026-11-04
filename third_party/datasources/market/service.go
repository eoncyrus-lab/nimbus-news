package market

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/Ricaardo/nimbus-os/datasources/cache"
	"github.com/Ricaardo/nimbus-os/datasources/market/adapters"
	types "github.com/Ricaardo/nimbus-os/datasources/market/types"
)

// MarketService 行情服务（实现 Service 接口）
type MarketService struct {
	registry *adapters.AdapterRegistry
	resolver *SymbolResolver
	analyzer *TechnicalAnalyzer
	cache    *cache.LRUCache

	llmProvider LLMProvider

	mu sync.RWMutex
}

// NewMarketService 创建行情服务
func NewMarketService() *MarketService {
	registry := adapters.NewAdapterRegistry()

	// 创建适配器
	marketDataAdapter := adapters.NewMarketDataAdapter()
	alpacaAdapter := adapters.NewAlpacaAdapter()
	forexAdapter := adapters.NewForexAdapter()
	cboeAdapter := adapters.NewCBOEAdapter()
	akshareAdapter := adapters.NewAkshareAdapter()
	binanceAdapter := adapters.NewBinanceAdapter()
	okxAdapter := adapters.NewOKXAdapter()
	cryptoAltAdapter := adapters.NewCryptoAltAdapter()
	yahooAdapter := adapters.NewYahooAdapter()

	// 注册适配器（按优先级：market-data-svc(1) > Alpaca(2) > Forex(4) > CBOE(5) > akshare(5) > Binance(5) > OKX(6) > CryptoAlt(7) > Yahoo(10)）
	registry.Register(marketDataAdapter) // 港股/美股 (resident market-data-svc :8810)
	registry.Register(alpacaAdapter)      // US stock/ETF fallback (Alpaca Data API v2)
	registry.Register(forexAdapter)       // 外汇 =X (exchangerate-api.com, free)
	registry.Register(cboeAdapter)        // 主要指数 ^VIX/^GSPC/^IXIC (CBOE delayed quote)
	registry.Register(akshareAdapter)    // A股 (akshare/Sina via py-worker :8824)
	registry.Register(binanceAdapter)    // 加密货币 (CEX 现货)
	registry.Register(okxAdapter)        // 加密货币 (CEX 现货)
	registry.Register(cryptoAltAdapter)  // 加密货币 (Binance Alpha + OKX DEX 兜底)
	registry.Register(yahooAdapter)      // Fallback

	return &MarketService{
		registry: registry,
		resolver: NewSymbolResolver(),
		analyzer: NewTechnicalAnalyzer(),
		cache:    cache.NewLRUCache(1000, 60*1000*1000*1000), // 1分钟 TTL
	}
}

// NewMarketServiceWithLLM 创建行情服务（带LLM）
func NewMarketServiceWithLLM(provider LLMProvider) *MarketService {
	s := NewMarketService()
	s.llmProvider = provider
	return s
}

// GetMarketQuote 获取实时行情
func (s *MarketService) GetMarketQuote(ctx context.Context, symbol string) (*types.MarketQuote, error) {
	// 解析符号
	resolvedSymbol := s.resolver.Resolve(symbol)

	// 检查缓存
	cacheKey := "quote:" + resolvedSymbol
	if cached, ok := s.cache.Get(cacheKey); ok {
		return cached.(*types.MarketQuote), nil
	}

	// 查找适配器
	adapterList := s.registry.FindAdapters(resolvedSymbol)
	if len(adapterList) == 0 {
		return nil, fmt.Errorf("no eligible adapter for %s", symbol)
	}

	// 按优先级尝试
	var lastErr error
	for _, adapter := range adapterList {
		quote, err := adapter.GetQuote(ctx, resolvedSymbol)
		if err == nil {
			// 缓存结果
			s.cache.Set(cacheKey, quote)
			return quote, nil
		}
		lastErr = err
	}

	return nil, fmt.Errorf("all %d eligible adapters failed for %s: %v", len(adapterList), symbol, lastErr)
}

// GetKLines 获取K线数据（通过注册表查找 KLineAdapter）
func (s *MarketService) GetKLines(ctx context.Context, symbol string, interval string, limit int) ([]types.KLineItem, error) {
	resolvedSymbol := s.resolver.Resolve(symbol)

	for _, adapter := range s.registry.FindAdapters(resolvedSymbol) {
		if kla, ok := adapter.(adapters.KLineAdapter); ok {
			klines, err := kla.GetKLines(ctx, resolvedSymbol, interval, limit)
			if err == nil && len(klines) > 0 {
				return klines, nil
			}
		}
	}

	return nil, fmt.Errorf("no KLineAdapter available for %s", symbol)
}

// GetMarketIndex 获取主要市场指数
func (s *MarketService) GetMarketIndex(ctx context.Context) ([]types.IndexQuote, error) {
	indices := []string{
		"^DJI",   // 道琼斯
		"^GSPC",  // 标普500
		"^IXIC",  // 纳斯达克
		"^HSI",   // 恒生指数
		"^N225",  // 日经225
		"^FTSE",  // 富时100
		"^GDAXI", // 德国DAX
	}

	var result []types.IndexQuote
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, idx := range indices {
		wg.Add(1)
		go func(symbol string) {
			defer wg.Done()

			quote, err := s.GetMarketQuote(ctx, symbol)
			if err != nil {
				return
			}

			mu.Lock()
			result = append(result, types.IndexQuote{
				Symbol:        quote.Symbol,
				Name:          quote.Name,
				Price:         quote.Price,
				Change:        quote.Change,
				ChangePercent: quote.ChangePercent,
				UpdateTime:    quote.UpdateTime,
			})
			mu.Unlock()
		}(idx)
	}

	wg.Wait()
	return result, nil
}

// GetSecurityAnalysis 获取证券技术分析
func (s *MarketService) GetSecurityAnalysis(ctx context.Context, symbol string, assetType string) (*types.SecurityAnalysis, error) {
	// 解析符号
	resolvedSymbol := s.resolver.Resolve(symbol)

	// 获取K线数据（通过注册表查找支持K线的适配器）
	klines, err := s.GetKLines(ctx, resolvedSymbol, "1d", 100)
	if err != nil {
		// 如果 K线获取失败，返回基础分析
		quote, qErr := s.GetMarketQuote(ctx, symbol)
		if qErr != nil {
			return nil, fmt.Errorf("failed to get data for analysis: %v", err)
		}
		return &types.SecurityAnalysis{
			Symbol: quote.Symbol,
			Name:   quote.Name,
			Price:  quote.Price,
			Trend:  "unknown",
		}, nil
	}

	// 提取价格数据
	var closes, highs, lows []float64
	var volumes []int64
	for _, k := range klines {
		closes = append(closes, k.Close)
		highs = append(highs, k.High)
		lows = append(lows, k.Low)
		volumes = append(volumes, k.Volume)
	}

	if len(closes) == 0 {
		return nil, fmt.Errorf("no price data available")
	}

	currentPrice := closes[len(closes)-1]

	// 计算技术指标
	analysis := &types.SecurityAnalysis{
		Symbol:     resolvedSymbol,
		Name:       resolvedSymbol,
		Price:      currentPrice,
		Trend:      s.analyzer.DetermineTrend(closes),
		RSI:        s.analyzer.CalculateRSI(closes, 14),
		MA5:        s.analyzer.CalculateSMA(closes, 5),
		MA10:       s.analyzer.CalculateSMA(closes, 10),
		MA20:       s.analyzer.CalculateSMA(closes, 20),
		MA50:       s.analyzer.CalculateSMA(closes, 50),
		Support:    s.analyzer.CalculateSupport(lows, 20),
		Resistance: s.analyzer.CalculateResistance(highs, 20),
	}

	// 计算 MACD
	analysis.MACD, analysis.MACDSignal, analysis.MACDHist = s.analyzer.CalculateMACD(closes, 12, 26, 9)

	// 计算 KDJ
	analysis.KDJ_K, analysis.KDJ_D, analysis.KDJ_J = s.analyzer.CalculateKDJ(highs, lows, closes, 9, 3, 3)

	// 计算布林带
	analysis.BollUpper, analysis.BollMiddle, analysis.BollLower = s.analyzer.CalculateBollingerBands(closes, 20, 2)

	// 计算 ATR
	analysis.ATR = s.analyzer.CalculateATR(highs, lows, closes, 14)

	// 计算成交量指标
	if len(volumes) > 0 {
		analysis.Volume = volumes[len(volumes)-1]
		if len(volumes) >= 5 {
			var volumeSum int64
			for _, v := range volumes[len(volumes)-5:] {
				volumeSum += v
			}
			analysis.VolumeMA5 = volumeSum / 5
			if analysis.VolumeMA5 > 0 {
				analysis.VolumeRatio = float64(analysis.Volume) / float64(analysis.VolumeMA5)
			}
		}
	}

	// 确定趋势信号
	analysis.TrendSignal = s.determineTrendSignal(analysis)

	return analysis, nil
}

func (s *MarketService) determineTrendSignal(analysis *types.SecurityAnalysis) string {
	signals := 0

	// RSI 信号
	if analysis.RSI > 70 {
		signals--
	} else if analysis.RSI < 30 {
		signals++
	}

	// MACD 信号
	if analysis.MACDHist > 0 {
		signals++
	} else if analysis.MACDHist < 0 {
		signals--
	}

	// 均线信号
	if analysis.MA5 > analysis.MA20 {
		signals++
	} else if analysis.MA5 < analysis.MA20 {
		signals--
	}

	if signals >= 2 {
		return "强买入"
	} else if signals == 1 {
		return "买入"
	} else if signals <= -2 {
		return "强卖出"
	} else if signals == -1 {
		return "卖出"
	}
	return "观望"
}

// GetAIAnalysis 获取AI分析
func (s *MarketService) GetAIAnalysis(ctx context.Context, symbol string, assetType string) (string, error) {
	if s.llmProvider == nil {
		return "", fmt.Errorf("LLM provider not available")
	}

	quote, err := s.GetMarketQuote(ctx, symbol)
	if err != nil {
		return "", err
	}

	analysis, err := s.GetSecurityAnalysis(ctx, symbol, assetType)
	if err != nil {
		analysis = &types.SecurityAnalysis{}
	}

	prompt := fmt.Sprintf(`你是一位资深技术分析师。请对以下标的进行专业的技术分析解读（150字以内）：

标的：%s
当前价格：%.2f
涨跌幅：%+.2f%%

技术指标：
- 趋势：%s（%s）
- RSI(14)：%.1f
- MACD：%.4f（信号线：%.4f，柱状图：%.4f）
- KDJ：K=%.1f D=%.1f J=%.1f
- 均线：MA5=%.2f MA10=%.2f MA20=%.2f MA50=%.2f
- 布林带：上轨=%.2f 中轨=%.2f 下轨=%.2f
- 支撑位：%.2f
- 阻力位：%.2f
- 成交量对比：%.2fx

要求：
1. 综合分析当前技术面状态
2. 判断短期走势（1-3天）
3. 给出操作建议（买入/卖出/观望）
4. 提示风险
5. 不超过150字
6. 用中文回复，直接输出分析内容，不要添加标题或解释`,
		quote.Name, quote.Price, quote.ChangePercent,
		analysis.Trend, analysis.TrendSignal,
		analysis.RSI, analysis.MACD, analysis.MACDSignal, analysis.MACDHist,
		analysis.KDJ_K, analysis.KDJ_D, analysis.KDJ_J,
		analysis.MA5, analysis.MA10, analysis.MA20, analysis.MA50,
		analysis.BollUpper, analysis.BollMiddle, analysis.BollLower,
		analysis.Support, analysis.Resistance,
		analysis.VolumeRatio)

	messages := []LLMMessage{
		{Role: "user", Content: prompt},
	}

	resp, err := s.llmProvider.Chat(ctx, messages)
	if err != nil {
		return "", err
	}

	return resp, nil
}

// GetMarketSentiment 获取市场情绪
func (s *MarketService) GetMarketSentiment(ctx context.Context, market string) (*types.SentimentData, error) {
	// 这里可以集成 Alternative.me Fear & Greed Index API
	// 暂时返回模拟数据
	return &types.SentimentData{
		FearGreed:    50,
		FearGreedTag: "Neutral",
		UpdateTime:   time.Time{},
	}, nil
}

// GetHistoricalQuotes 获取历史行情
func (s *MarketService) GetHistoricalQuotes(ctx context.Context, symbol string, interval string, rangeStr string) ([]types.KLineItem, error) {
	resolvedSymbol := s.resolver.Resolve(symbol)

	// 转换 interval 格式
	binanceInterval := interval
	limit := 100

	switch rangeStr {
	case "1d":
		limit = 24
	case "5d":
		limit = 120
	case "1mo":
		limit = 30
	case "3mo":
		limit = 90
	case "1y":
		limit = 365
	}

	return s.GetKLines(ctx, resolvedSymbol, binanceInterval, limit)
}

// SearchNews 搜索新闻（暂未实现）
func (s *MarketService) SearchNews(ctx context.Context, query string) ([]types.NewsItem, error) {
	return nil, fmt.Errorf("not implemented")
}

// GetIPOList 获取IPO列表（暂未实现）
func (s *MarketService) GetIPOList(ctx context.Context) ([]types.IPOInfo, error) {
	return nil, fmt.Errorf("not implemented")
}

// USMarketLiquidity 美股市场流动性数据
type USMarketLiquidity struct {
	Symbol       string  // 参考标地 (SPY/QQQ)
	Volume       int64   // 当前成交量
	VolumeMA5    int64   // 5日平均成交量
	VolumeRatio  float64 // 量比
	AvgDailyVol  int64   // 平均日成交量
	TurnoverRate float64 // 换手率
	UpdateTime   time.Time
}

// GetUSMarketLiquidity 获取美股市场流动性数据
func (s *MarketService) GetUSMarketLiquidity(ctx context.Context) (*USMarketLiquidity, error) {
	// 使用 SPY (标普500 ETF) 作为市场流动性参考
	symbol := "^GSPC"

	// 获取K线数据
	klines, err := s.GetKLines(ctx, symbol, "1d", 20)
	if err != nil {
		return nil, fmt.Errorf("failed to get klines for liquidity: %v", err)
	}

	if len(klines) == 0 {
		return nil, fmt.Errorf("no kline data available")
	}

	// 计算成交量指标
	var volumes []int64
	for _, k := range klines {
		volumes = append(volumes, k.Volume)
	}

	currentVolume := volumes[len(volumes)-1]
	var volumeMA5 int64
	if len(volumes) >= 5 {
		var sum int64
		for _, v := range volumes[len(volumes)-5:] {
			sum += v
		}
		volumeMA5 = sum / 5
	}

	// 计算20日平均成交量
	var avgVol int64
	if len(volumes) >= 20 {
		var sum int64
		for _, v := range volumes {
			sum += v
		}
		avgVol = sum / int64(len(volumes))
	}

	volumeRatio := 1.0
	if volumeMA5 > 0 {
		volumeRatio = float64(currentVolume) / float64(volumeMA5)
	}

	return &USMarketLiquidity{
		Symbol:      symbol,
		Volume:      currentVolume,
		VolumeMA5:   volumeMA5,
		VolumeRatio: volumeRatio,
		AvgDailyVol: avgVol,
		UpdateTime:  time.Now(),
	}, nil
}

// GetUSIndexSnapshot 获取美股主要指数快照
func (s *MarketService) GetUSIndexSnapshot(ctx context.Context) (map[string]*types.IndexQuote, error) {
	indices := []string{
		"^DJI",  // 道琼斯
		"^GSPC", // 标普500
		"^IXIC", // 纳斯达克
		"^RUT",  // 罗素2000
		"^VIX",  // 恐慌指数
	}

	result := make(map[string]*types.IndexQuote)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, idx := range indices {
		wg.Add(1)
		go func(symbol string) {
			defer wg.Done()

			quote, err := s.GetMarketQuote(ctx, symbol)
			if err != nil {
				return
			}

			mu.Lock()
			result[symbol] = &types.IndexQuote{
				Symbol:        quote.Symbol,
				Name:          quote.Name,
				Price:         quote.Price,
				Change:        quote.Change,
				ChangePercent: quote.ChangePercent,
				UpdateTime:    quote.UpdateTime,
			}
			mu.Unlock()
		}(idx)
	}

	wg.Wait()

	if len(result) == 0 {
		return nil, fmt.Errorf("failed to get any index data")
	}

	return result, nil
}

// SectorETF 板块ETF
type SectorETF struct {
	Symbol string  // ETF代码
	Name   string  // ETF名称
	Sector string  // 所属板块
	Change float64 // 涨跌幅
	Volume int64   // 成交量
	Price  float64 // 当前价格
}

// GetSectorRotation 获取板块轮动数据
func (s *MarketService) GetSectorRotation(ctx context.Context) ([]SectorETF, error) {
	// 主要板块ETF
	sectorETFs := []struct {
		Symbol string
		Name   string
		Sector string
	}{
		{"QQQ", "Invesco QQQ Trust", "科技成长"},
		{"XLK", "Technology Select Sector", "科技"},
		{"XLF", "Financial Select Sector", "金融"},
		{"XLV", "Health Care Select Sector", "医疗保健"},
		{"XLE", "Energy Select Sector", "能源"},
		{"XLY", "Consumer Discretionary", "可选消费"},
		{"XLP", "Consumer Staples", "必需消费"},
		{"XLI", "Industrial Select Sector", "工业"},
		{"XLB", "Materials Select Sector", "原材料"},
		{"XLRE", "Real Estate Select Sector", "房地产"},
		{"XLU", "Utilities Select Sector", "公用事业"},
		{"XLC", "Communication Services", "通信"},
		{"IWM", "iShares Russell 2000", "小盘股"},
		{"SPY", "SPDR S&P 500", "大盘股"},
	}

	var result []SectorETF
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, etf := range sectorETFs {
		wg.Add(1)
		go func(sym, name, sector string) {
			defer wg.Done()

			quote, err := s.GetMarketQuote(ctx, sym)
			if err != nil {
				return
			}

			mu.Lock()
			result = append(result, SectorETF{
				Symbol: sym,
				Name:   name,
				Sector: sector,
				Change: quote.ChangePercent,
				Volume: quote.Volume,
				Price:  quote.Price,
			})
			mu.Unlock()
		}(etf.Symbol, etf.Name, etf.Sector)
	}

	wg.Wait()

	// 按涨跌幅排序
	if len(result) > 0 {
		sort.Slice(result, func(i, j int) bool {
			return result[i].Change > result[j].Change
		})
	}

	return result, nil
}

// SectorRotationReport 板块轮动报告
type SectorRotationReport struct {
	LeadingSectors []SectorETF // 领涨板块
	LaggingSectors []SectorETF // 领跌板块
	MarketTrend    string      // 市场趋势: "growth" / "value" / "neutral"
	RotationSignal string      // 轮动信号
	UpdateTime     time.Time
}

// AnalyzeSectorRotation 分析板块轮动
func (s *MarketService) AnalyzeSectorRotation(ctx context.Context) (*SectorRotationReport, error) {
	sectors, err := s.GetSectorRotation(ctx)
	if err != nil {
		return nil, err
	}

	if len(sectors) == 0 {
		return nil, fmt.Errorf("no sector data")
	}

	// 找出领涨和领跌板块
	var leading, lagging []SectorETF
	for i, s := range sectors {
		if i < 3 {
			leading = append(leading, s)
		}
		if i >= len(sectors)-3 {
			lagging = append(lagging, s)
		}
	}

	// 判断市场风格
	// 科技/成长股表现好 = 成长风格
	// 公用事业/消费/医疗表现好 = 防御风格
	// 金融/能源/工业表现好 = 周期风格
	marketTrend := "neutral"
	rotationSignal := "观望"

	// 计算平均涨跌幅
	var growthAvg, valueAvg float64
	var growthCount, valueCount int

	for _, s := range sectors {
		switch s.Sector {
		case "科技成长", "科技", "通信", "可选消费":
			growthAvg += s.Change
			growthCount++
		case "金融", "能源", "工业", "原材料":
			valueAvg += s.Change
			valueCount++
		}
	}

	if growthCount > 0 {
		growthAvg /= float64(growthCount)
	}
	if valueCount > 0 {
		valueAvg /= float64(valueCount)
	}

	if growthAvg > valueAvg+1 {
		marketTrend = "growth"
		rotationSignal = "成长风格占优"
	} else if valueAvg > growthAvg+1 {
		marketTrend = "value"
		rotationSignal = "价值/周期风格占优"
	}

	// 判断是否在轮动
	if len(leading) > 0 && len(lagging) > 0 {
		diff := leading[0].Change - lagging[0].Change
		if diff > 5 {
			rotationSignal = "极端分化，注意风险"
		} else if diff < 2 {
			rotationSignal = "板块轮动快，建议观望"
		}
	}

	return &SectorRotationReport{
		LeadingSectors: leading,
		LaggingSectors: lagging,
		MarketTrend:    marketTrend,
		RotationSignal: rotationSignal,
		UpdateTime:     time.Now(),
	}, nil
}
