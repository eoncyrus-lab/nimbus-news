package adapters

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	dsyahoo "github.com/Ricaardo/nimbus-os/datasources/yahoo"

	types "github.com/Ricaardo/nimbus-os/datasources/market/types"
)

// YahooAdapter Yahoo Finance 适配器
type YahooAdapter struct {
	BaseAdapter
	searchCache   map[string]string
	searchCacheMu sync.RWMutex
}

// NewYahooAdapter 创建 Yahoo 适配器
func NewYahooAdapter() *YahooAdapter {
	return &YahooAdapter{
		BaseAdapter: NewBaseAdapter("yahoo", 10),
		searchCache: make(map[string]string),
	}
}

func (a *YahooAdapter) SupportedTypes() []string {
	return []string{AssetTypeStock, AssetTypeETF, AssetTypeIndex, AssetTypeFutures, AssetTypeForex}
}

func (a *YahooAdapter) CanHandle(symbol string) bool {
	upper := strings.ToUpper(symbol)
	// Yahoo 不处理加密货币（有 USDT/BUSD 后缀的）
	if strings.HasSuffix(upper, "USDT") || strings.HasSuffix(upper, "BUSD") {
		return false
	}
	// Yahoo 处理其他所有资产
	return true
}

func (a *YahooAdapter) GetQuote(ctx context.Context, symbol string) (*types.MarketQuote, error) {
	originalInput := symbol

	// 检查是否包含中文字符
	hasChinese := containsChinese(symbol)

	// 如果有中文，尝试搜索
	if hasChinese {
		searchResult, err := a.searchSymbol(ctx, originalInput)
		if err == nil && searchResult != "" {
			return a.getQuoteV8(ctx, searchResult)
		}
	}

	// 直接查询
	quote, err := a.getQuoteV8(ctx, symbol)
	if err == nil {
		return quote, nil
	}

	// 如果直接查询失败，尝试搜索
	searchResult, searchErr := a.searchSymbol(ctx, originalInput)
	if searchErr == nil && searchResult != "" && searchResult != symbol {
		return a.getQuoteV8(ctx, searchResult)
	}

	return nil, err
}

// getQuoteV8 delegates the Yahoo chart fetch+parse to the shared datasources
// yahoo package (the single Go impl), then maps to the news types. News-specific
// asset-type classification stays here.
func (a *YahooAdapter) getQuoteV8(ctx context.Context, symbol string) (*types.MarketQuote, error) {
	q, err := dsyahoo.GetQuote(ctx, symbol)
	if err != nil {
		return nil, err
	}
	assetType := AssetTypeStock
	switch {
	case strings.HasSuffix(symbol, "=F"):
		assetType = AssetTypeFutures
	case strings.HasPrefix(symbol, "^"):
		assetType = AssetTypeIndex
	case strings.Contains(symbol, "=X"):
		assetType = AssetTypeForex
	}
	change := q.Last - q.PrevClose
	changePct := 0.0
	if q.PrevClose != 0 {
		changePct = (change / q.PrevClose) * 100
	}
	return &types.MarketQuote{
		Symbol:        symbol,
		Name:          q.Name,
		Price:         q.Last,
		Change:        change,
		ChangePercent: changePct,
		PrevClose:     q.PrevClose,
		Open:          q.Open,
		High:          q.High,
		Low:           q.Low,
		Volume:        q.Volume,
		UpdateTime:    time.Unix(q.Time, 0),
		Currency:      q.Currency,
		Exchange:      q.Exchange,
		AssetType:     assetType,
	}, nil
}

func (a *YahooAdapter) searchSymbol(ctx context.Context, query string) (string, error) {
	// 检查缓存
	a.searchCacheMu.RLock()
	if cached, ok := a.searchCache[strings.ToLower(query)]; ok {
		a.searchCacheMu.RUnlock()
		return cached, nil
	}
	a.searchCacheMu.RUnlock()

	// 搜索走共享 datasources/yahoo 包；news 自己的最佳匹配策略保留在下方。
	results, err := dsyahoo.Search(ctx, query)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return "", fmt.Errorf("no results found for: %s", query)
	}

	bestMatch := a.selectBestMatch(results)

	// 缓存结果
	a.searchCacheMu.Lock()
	a.searchCache[strings.ToLower(query)] = bestMatch
	a.searchCacheMu.Unlock()

	return bestMatch, nil
}

func (a *YahooAdapter) selectBestMatch(quotes []dsyahoo.SearchResult) string {
	var hkMatch, shMatch, szMatch, equityMatch string

	for _, q := range quotes {
		if q.QuoteType != "EQUITY" && q.QuoteType != "ETF" && q.QuoteType != "INDEX" {
			continue
		}
		sym := q.Symbol

		if strings.HasSuffix(sym, ".HK") && hkMatch == "" {
			hkMatch = sym
		}
		if strings.HasSuffix(sym, ".SS") && shMatch == "" {
			shMatch = sym
		}
		if strings.HasSuffix(sym, ".SZ") && szMatch == "" {
			szMatch = sym
		}
		if equityMatch == "" && (q.QuoteType == "EQUITY" || q.QuoteType == "ETF") {
			equityMatch = sym
		}
	}

	// 按优先级选择：港股 > A股 > 其他
	if hkMatch != "" {
		return hkMatch
	}
	if shMatch != "" {
		return shMatch
	}
	if szMatch != "" {
		return szMatch
	}
	if equityMatch != "" {
		return equityMatch
	}
	if len(quotes) > 0 {
		return quotes[0].Symbol
	}
	return ""
}

// GetKLines fetches daily bars via the shared datasources/yahoo package.
func (a *YahooAdapter) GetKLines(ctx context.Context, symbol string, interval string, limit int) ([]types.KLineItem, error) {
	if interval != "" && interval != "1d" && interval != "1D" && interval != "day" {
		// Yahoo chart API only serves daily bars through this package.
		return nil, fmt.Errorf("yahoo: interval %q not supported", interval)
	}
	bars, err := dsyahoo.GetHistory(ctx, symbol, limit)
	if err != nil {
		return nil, err
	}
	klines := make([]types.KLineItem, 0, len(bars))
	for _, b := range bars {
		t, parseErr := time.Parse("2006-01-02", b.Date)
		if parseErr != nil {
			continue
		}
		klines = append(klines, types.KLineItem{
			Time: t, Open: b.Open, High: b.High,
			Low: b.Low, Close: b.Close, Volume: b.Volume,
		})
	}
	if len(klines) == 0 {
		return nil, fmt.Errorf("yahoo: no bars parsed for %s", symbol)
	}
	return klines, nil
}

// containsChinese 检查字符串是否包含中文
func containsChinese(s string) bool {
	for _, r := range s {
		if r >= 0x4e00 && r <= 0x9fff {
			return true
		}
	}
	return false
}
