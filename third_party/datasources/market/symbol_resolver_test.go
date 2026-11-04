package market

import (
	"testing"
)

func TestSymbolResolver_Resolve_Aliases(t *testing.T) {
	resolver := NewSymbolResolver()

	tests := []struct {
		input    string
		expected string
	}{
		// 加密货币别名
		{"比特币", "BTCUSDT"},
		{"以太坊", "ETHUSDT"},
		{"狗狗币", "DOGEUSDT"},

		// 指数别名
		{"上证", "000001.SS"},
		{"道指", "^DJI"},
		{"纳指", "^IXIC"},
		{"标普500", "^GSPC"},
		{"恒指", "^HSI"},

		// 商品别名
		{"黄金", "GC=F"},
		{"原油", "CL=F"},
		{"白银", "SI=F"},

		// 股票别名
		{"茅台", "600519.SS"},
		{"腾讯", "0700.HK"},
		{"苹果", "AAPL"},
		{"特斯拉", "TSLA"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := resolver.Resolve(tt.input)
			if result != tt.expected {
				t.Errorf("Resolve(%s) = %s, expected %s", tt.input, result, tt.expected)
			}
		})
	}
}

func TestSymbolResolver_Resolve_Patterns(t *testing.T) {
	resolver := NewSymbolResolver()

	tests := []struct {
		input    string
		expected string
	}{
		// 上海股票 (6开头)
		{"600519", "600519.SS"},
		{"601318", "601318.SS"},

		// 深圳股票 (0/3开头)
		{"000001", "000001.SZ"},
		{"000858", "000858.SZ"},
		{"300750", "300750.SZ"},

		// 港股
		{"0700HK", "0700.HK"},
		{"9988.HK", "9988.HK"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := resolver.Resolve(tt.input)
			if result != tt.expected {
				t.Errorf("Resolve(%s) = %s, expected %s", tt.input, result, tt.expected)
			}
		})
	}
}

func TestSymbolResolver_Resolve_ExchangePrefix(t *testing.T) {
	resolver := NewSymbolResolver()

	tests := []struct {
		input    string
		expected string
	}{
		// Futu/Longport 交易所前缀 → Yahoo 风格
		{"US.SPY", "SPY"},
		{"us.spy", "SPY"}, // 前缀大小写不敏感
		{"SH.000001", "000001.SS"},
		{"SZ.000001", "000001.SZ"},
		{"HK.700", "0700.HK"},
		{"HK.00700", "0700.HK"},

		// 无前缀的裸符号应原样透传
		{"SPY", "SPY"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := resolver.Resolve(tt.input)
			if result != tt.expected {
				t.Errorf("Resolve(%s) = %s, expected %s", tt.input, result, tt.expected)
			}
		})
	}
}

func TestSymbolResolver_Resolve_Passthrough(t *testing.T) {
	resolver := NewSymbolResolver()

	// 已经是标准格式的符号应该原样返回
	tests := []struct {
		input    string
		expected string
	}{
		{"AAPL", "AAPL"},
		{"GOOGL", "GOOGL"},
		{"^DJI", "^DJI"},
		{"GC=F", "GC=F"},
		{"BTCUSDT", "BTCUSDT"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := resolver.Resolve(tt.input)
			if result != tt.expected {
				t.Errorf("Resolve(%s) = %s, expected %s", tt.input, result, tt.expected)
			}
		})
	}
}

func TestSymbolResolver_AddAlias(t *testing.T) {
	resolver := NewSymbolResolver()

	// 添加自定义别名
	resolver.AddAlias("我的股票", "CUSTOM.SS")

	result := resolver.Resolve("我的股票")
	if result != "CUSTOM.SS" {
		t.Errorf("expected CUSTOM.SS, got %s", result)
	}
}

func TestSymbolResolver_HasAlias(t *testing.T) {
	resolver := NewSymbolResolver()

	if !resolver.HasAlias("比特币") {
		t.Error("expected HasAlias(比特币) to be true")
	}

	if resolver.HasAlias("不存在的别名") {
		t.Error("expected HasAlias(不存在的别名) to be false")
	}
}

func TestSymbolResolver_TrimSpace(t *testing.T) {
	resolver := NewSymbolResolver()

	result := resolver.Resolve("  比特币  ")
	if result != "BTCUSDT" {
		t.Errorf("expected BTCUSDT, got %s", result)
	}
}
