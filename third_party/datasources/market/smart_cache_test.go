package market

import (
	"context"
	"testing"
	"time"

	types "github.com/Ricaardo/nimbus-os/datasources/market/types"
)

func TestSmartCache_DetectMarketType(t *testing.T) {
	sc := NewSmartCache(nil)

	tests := []struct {
		symbol   string
		quote    *types.MarketQuote
		expected MarketType
	}{
		// 加密货币
		{"BTCUSDT", nil, MarketTypeCrypto},
		{"ETHUSDT", nil, MarketTypeCrypto},
		{"BTC-USD", nil, MarketTypeCrypto},
		{"SOL", &types.MarketQuote{Exchange: "Binance"}, MarketTypeCrypto},

		// 美股
		{"AAPL", nil, MarketTypeUS},
		{"MSFT", nil, MarketTypeUS},
		{"NVDA", nil, MarketTypeUS},

		// 港股
		{"0700.HK", nil, MarketTypeHK},
		{"9988.HK", nil, MarketTypeHK},

		// A 股
		{"600519.SS", nil, MarketTypeCN},
		{"000001.SZ", nil, MarketTypeCN},

		// 商品期货
		{"GC=F", nil, MarketTypeCommodity},
		{"CL=F", nil, MarketTypeCommodity},

		// 外汇
		{"EURUSD=X", nil, MarketTypeForex},
		{"USDJPY=X", nil, MarketTypeForex},
	}

	for _, tt := range tests {
		t.Run(tt.symbol, func(t *testing.T) {
			result := sc.detectMarketType(tt.symbol, tt.quote)
			if result != tt.expected {
				t.Errorf("detectMarketType(%s) = %v, want %v", tt.symbol, result, tt.expected)
			}
		})
	}
}

func TestSmartCache_GetQuote(t *testing.T) {
	config := &SmartCacheConfig{
		TradingTTL:   100 * time.Millisecond,
		ClosedTTL:    500 * time.Millisecond,
		CryptoTTL:    100 * time.Millisecond,
		CacheSize:    100,
	}
	sc := NewSmartCache(config)

	callCount := 0
	fetcher := func(ctx context.Context, symbol string) (*types.MarketQuote, error) {
		callCount++
		return &types.MarketQuote{
			Symbol:   symbol,
			Name:     "Test",
			Price:    100.0,
			Exchange: "Binance",
		}, nil
	}

	ctx := context.Background()

	// 第一次调用，应该调用 fetcher
	quote1, err := sc.GetQuote(ctx, "BTCUSDT", fetcher)
	if err != nil {
		t.Fatalf("GetQuote failed: %v", err)
	}
	if callCount != 1 {
		t.Errorf("Expected 1 call, got %d", callCount)
	}
	if quote1.Price != 100.0 {
		t.Errorf("Expected price 100.0, got %f", quote1.Price)
	}

	// 第二次调用，应该从缓存返回
	quote2, err := sc.GetQuote(ctx, "BTCUSDT", fetcher)
	if err != nil {
		t.Fatalf("GetQuote failed: %v", err)
	}
	if callCount != 1 {
		t.Errorf("Expected 1 call (cached), got %d", callCount)
	}
	if quote2.Price != 100.0 {
		t.Errorf("Expected price 100.0, got %f", quote2.Price)
	}

	// 等待缓存过期
	time.Sleep(150 * time.Millisecond)

	// 第三次调用，缓存已过期，应该再次调用 fetcher
	_, err = sc.GetQuote(ctx, "BTCUSDT", fetcher)
	if err != nil {
		t.Fatalf("GetQuote failed: %v", err)
	}
	if callCount != 2 {
		t.Errorf("Expected 2 calls (cache expired), got %d", callCount)
	}
}

func TestSmartCache_Stats(t *testing.T) {
	sc := NewSmartCache(&SmartCacheConfig{
		CryptoTTL: time.Second,
		CacheSize: 100,
	})

	fetcher := func(ctx context.Context, symbol string) (*types.MarketQuote, error) {
		return &types.MarketQuote{Symbol: symbol, Price: 100.0, Exchange: "Binance"}, nil
	}

	ctx := context.Background()

	// 第一次调用 - miss
	sc.GetQuote(ctx, "BTCUSDT", fetcher)

	// 第二次调用 - hit
	sc.GetQuote(ctx, "BTCUSDT", fetcher)

	// 第三次调用 - hit
	sc.GetQuote(ctx, "BTCUSDT", fetcher)

	stats := sc.Stats()
	if stats.SmartHits != 2 {
		t.Errorf("Expected 2 hits, got %d", stats.SmartHits)
	}
	if stats.SmartMisses != 1 {
		t.Errorf("Expected 1 miss, got %d", stats.SmartMisses)
	}
	if stats.SmartHitRate < 0.66 || stats.SmartHitRate > 0.67 {
		t.Errorf("Expected hit rate ~0.66, got %f", stats.SmartHitRate)
	}
}

func TestSmartCache_Invalidate(t *testing.T) {
	sc := NewSmartCache(&SmartCacheConfig{
		CryptoTTL: time.Hour,
		CacheSize: 100,
	})

	callCount := 0
	fetcher := func(ctx context.Context, symbol string) (*types.MarketQuote, error) {
		callCount++
		return &types.MarketQuote{Symbol: symbol, Price: float64(callCount * 100), Exchange: "Binance"}, nil
	}

	ctx := context.Background()

	// 第一次调用
	quote1, _ := sc.GetQuote(ctx, "BTCUSDT", fetcher)
	if quote1.Price != 100.0 {
		t.Errorf("Expected price 100.0, got %f", quote1.Price)
	}

	// 使缓存失效
	sc.Invalidate("BTCUSDT")

	// 再次调用，应该获取新数据
	quote2, _ := sc.GetQuote(ctx, "BTCUSDT", fetcher)
	if quote2.Price != 200.0 {
		t.Errorf("Expected price 200.0 after invalidation, got %f", quote2.Price)
	}
}

func TestGetMarketStatusString(t *testing.T) {
	tests := []struct {
		status   MarketStatus
		expected string
	}{
		{MarketStatusOpen, "交易中"},
		{MarketStatusClosed, "休市"},
		{MarketStatusPreMarket, "盘前"},
		{MarketStatusAfterHours, "盘后"},
		{MarketStatusUnknown, "未知"},
	}

	for _, tt := range tests {
		result := GetMarketStatusString(tt.status)
		if result != tt.expected {
			t.Errorf("GetMarketStatusString(%v) = %s, want %s", tt.status, result, tt.expected)
		}
	}
}

func TestGetMarketTypeString(t *testing.T) {
	tests := []struct {
		marketType MarketType
		expected   string
	}{
		{MarketTypeCrypto, "加密货币"},
		{MarketTypeUS, "美股"},
		{MarketTypeHK, "港股"},
		{MarketTypeCN, "A股"},
		{MarketTypeCommodity, "商品期货"},
		{MarketTypeForex, "外汇"},
		{MarketTypeUnknown, "未知"},
	}

	for _, tt := range tests {
		result := GetMarketTypeString(tt.marketType)
		if result != tt.expected {
			t.Errorf("GetMarketTypeString(%v) = %s, want %s", tt.marketType, result, tt.expected)
		}
	}
}

func TestIsWeekend(t *testing.T) {
	// 周一
	monday := time.Date(2024, 1, 8, 10, 0, 0, 0, time.UTC)
	if isWeekend(monday) {
		t.Error("Monday should not be weekend")
	}

	// 周六
	saturday := time.Date(2024, 1, 6, 10, 0, 0, 0, time.UTC)
	if !isWeekend(saturday) {
		t.Error("Saturday should be weekend")
	}

	// 周日
	sunday := time.Date(2024, 1, 7, 10, 0, 0, 0, time.UTC)
	if !isWeekend(sunday) {
		t.Error("Sunday should be weekend")
	}
}

func TestSymbolPatternDetection(t *testing.T) {
	// 加密货币
	if !isCryptoSymbol("BTCUSDT") {
		t.Error("BTCUSDT should be crypto")
	}
	if !isCryptoSymbol("ETH-USD") {
		t.Error("ETH-USD should be crypto")
	}

	// 美股
	if !isUSSymbol("AAPL") {
		t.Error("AAPL should be US stock")
	}
	if !isUSSymbol("MSFT") {
		t.Error("MSFT should be US stock")
	}

	// 港股
	if !isHKSymbol("0700.HK") {
		t.Error("0700.HK should be HK stock")
	}

	// A 股
	if !isCNSymbol("600519.SS") {
		t.Error("600519.SS should be CN stock")
	}
	if !isCNSymbol("000001.SZ") {
		t.Error("000001.SZ should be CN stock")
	}

	// 商品期货
	if !isCommoditySymbol("GC=F") {
		t.Error("GC=F should be commodity")
	}

	// 外汇
	if !isForexSymbol("EURUSD=X") {
		t.Error("EURUSD=X should be forex")
	}
}
