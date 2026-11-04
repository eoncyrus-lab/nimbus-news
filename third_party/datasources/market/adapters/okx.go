package adapters

import (
	"context"
	"strings"
	"time"

	dsokx "github.com/Ricaardo/nimbus-os/datasources/okx"
	types "github.com/Ricaardo/nimbus-os/datasources/market/types"
)

// OKXAdapter OKX 交易所适配器（行情走共享 datasources/okx 包）
type OKXAdapter struct {
	BaseAdapter
}

// NewOKXAdapter 创建 OKX 适配器
func NewOKXAdapter() *OKXAdapter {
	return &OKXAdapter{
		BaseAdapter: NewBaseAdapter("okx", 6),
	}
}

func (a *OKXAdapter) SupportedTypes() []string {
	return []string{AssetTypeCrypto}
}

func (a *OKXAdapter) CanHandle(symbol string) bool {
	upper := strings.ToUpper(symbol)
	// OKX 处理加密货币
	if strings.HasSuffix(upper, "USDT") || strings.HasSuffix(upper, "USDC") {
		return true
	}
	// 常见加密货币
	cryptoSymbols := []string{"BTC", "ETH", "OKB", "SOL", "XRP", "DOGE", "ADA", "AVAX", "DOT", "MATIC"}
	for _, crypto := range cryptoSymbols {
		if upper == crypto || strings.HasPrefix(upper, crypto) {
			return true
		}
	}
	return false
}

func (a *OKXAdapter) GetQuote(ctx context.Context, symbol string) (*types.MarketQuote, error) {
	instId := a.normalizeSymbol(symbol)
	t, err := dsokx.GetTicker(ctx, instId)
	if err != nil {
		return nil, err
	}

	// 从交易对中提取基础货币名称
	name := strings.Split(t.InstID, "-")[0]

	return &types.MarketQuote{
		Symbol:        symbol,
		Name:          name,
		Price:         t.Last,
		Change:        t.Last - t.Open24h,
		ChangePercent: t.ChangePct,
		High:          t.High24h,
		Low:           t.Low24h,
		Volume:        int64(t.VolCcy24h), // 24h 成交额（USD）
		MarketCap:     0,                  // Ticker API 不提供真实市值
		UpdateTime:    time.Now(),
		Currency:      "USD",
		Exchange:      "OKX",
		AssetType:     AssetTypeCrypto,
	}, nil
}

func (a *OKXAdapter) normalizeSymbol(symbol string) string {
	symbol = strings.ToUpper(symbol)
	symbol = strings.ReplaceAll(symbol, "/", "-")

	// 如果没有交易对后缀，添加 -USDT
	if !strings.Contains(symbol, "-") {
		symbol = strings.TrimSuffix(symbol, "USDT")
		symbol = strings.TrimSuffix(symbol, "USDC")
		symbol = symbol + "-USDT"
	}

	return symbol
}
