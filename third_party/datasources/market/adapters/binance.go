package adapters

import (
	"context"
	"strings"
	"time"

	dsbinance "github.com/Ricaardo/nimbus-os/datasources/binance"
	types "github.com/Ricaardo/nimbus-os/datasources/market/types"
)

// BinanceAdapter Binance 交易所适配器（行情走共享 datasources/binance 包）
type BinanceAdapter struct {
	BaseAdapter
}

// NewBinanceAdapter 创建 Binance 适配器
func NewBinanceAdapter() *BinanceAdapter {
	return &BinanceAdapter{
		BaseAdapter: NewBaseAdapter("binance", 5),
	}
}

func (a *BinanceAdapter) SupportedTypes() []string {
	return []string{AssetTypeCrypto}
}

func (a *BinanceAdapter) CanHandle(symbol string) bool {
	upper := strings.ToUpper(symbol)
	// Binance 处理加密货币
	if strings.HasSuffix(upper, "USDT") || strings.HasSuffix(upper, "BUSD") || strings.HasSuffix(upper, "BTC") {
		return true
	}
	// 常见加密货币
	cryptoSymbols := []string{"BTC", "ETH", "BNB", "SOL", "XRP", "DOGE", "ADA", "AVAX", "DOT", "MATIC", "LINK", "UNI", "ATOM", "LTC", "ETC"}
	for _, crypto := range cryptoSymbols {
		if upper == crypto || strings.HasPrefix(upper, crypto) {
			return true
		}
	}
	return false
}

func (a *BinanceAdapter) GetQuote(ctx context.Context, symbol string) (*types.MarketQuote, error) {
	symbol = a.normalizeSymbol(symbol)
	t, err := dsbinance.Ticker24h(ctx, symbol)
	if err != nil {
		return nil, err
	}

	// 从交易对中提取基础货币名称
	name := strings.TrimSuffix(symbol, "USDT")
	name = strings.TrimSuffix(name, "BUSD")
	name = strings.TrimSuffix(name, "BTC")

	return &types.MarketQuote{
		Symbol:        symbol,
		Name:          name,
		Price:         t.Last,
		Change:        t.Last - t.Open,
		ChangePercent: t.ChangePct,
		High:          t.High,
		Low:           t.Low,
		Volume:        int64(t.QuoteVolume), // 24h 成交额（USD）
		MarketCap:     0,                    // Ticker API 不提供真实市值
		UpdateTime:    time.Now(),
		Currency:      "USD",
		Exchange:      "Binance",
		AssetType:     AssetTypeCrypto,
	}, nil
}

func (a *BinanceAdapter) normalizeSymbol(symbol string) string {
	symbol = strings.ToUpper(symbol)
	symbol = strings.ReplaceAll(symbol, "-", "")
	symbol = strings.ReplaceAll(symbol, "/", "")
	if !strings.HasSuffix(symbol, "USDT") && !strings.HasSuffix(symbol, "BUSD") && !strings.HasSuffix(symbol, "BTC") {
		symbol = symbol + "USDT"
	}
	return symbol
}

// GetKLines 获取K线数据
func (a *BinanceAdapter) GetKLines(ctx context.Context, symbol string, interval string, limit int) ([]types.KLineItem, error) {
	symbol = a.normalizeSymbol(symbol)
	bars, err := dsbinance.Klines(ctx, symbol, interval, limit)
	if err != nil {
		return nil, err
	}

	klines := make([]types.KLineItem, 0, len(bars))
	for _, k := range bars {
		klines = append(klines, types.KLineItem{
			Time:   time.UnixMilli(k.OpenTime),
			Open:   k.Open,
			High:   k.High,
			Low:    k.Low,
			Close:  k.Close,
			Volume: int64(k.Volume),
		})
	}

	return klines, nil
}
