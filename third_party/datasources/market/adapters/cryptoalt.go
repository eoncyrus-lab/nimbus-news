// cryptoalt.go — alt-crypto quote adapter: Binance Alpha (early listings) and
// OKX DEX (on-chain tokens). Complements the binance/okx CEX-spot adapters as a
// lower-priority fallback for tokens the centralized exchanges can't price.
// Ported from the former market/yahoo.go (which was dead/unreachable) into the
// live adapter registry.
package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	types "github.com/Ricaardo/nimbus-os/datasources/market/types"
)

// CryptoAltAdapter serves Binance Alpha + OKX DEX tokens. Priority 7 → tried
// after the binance(5)/okx(6) CEX adapters fall through.
type CryptoAltAdapter struct {
	BaseAdapter
}

func NewCryptoAltAdapter() *CryptoAltAdapter {
	return &CryptoAltAdapter{BaseAdapter: NewBaseAdapter("crypto-alt", 7)}
}

func (a *CryptoAltAdapter) SupportedTypes() []string {
	return []string{AssetTypeCrypto}
}

// CanHandle matches crypto-ish symbols so this acts as a fallback for tokens the
// CEX adapters could not serve (alpha listings / on-chain DEX tokens).
func (a *CryptoAltAdapter) CanHandle(symbol string) bool {
	upper := strings.ToUpper(symbol)
	if strings.HasSuffix(upper, "USDT") || strings.HasSuffix(upper, "USDC") || strings.HasSuffix(upper, "BUSD") {
		return true
	}
	sym := strings.TrimSuffix(upper, "USDT")
	if _, ok := binanceAlphaTokens[sym]; ok {
		return true
	}
	for _, tokens := range okxDexTokenContracts {
		if _, ok := tokens[sym]; ok {
			return true
		}
	}
	return false
}

// GetQuote tries Binance Alpha first, then OKX DEX across supported chains.
func (a *CryptoAltAdapter) GetQuote(ctx context.Context, symbol string) (*types.MarketQuote, error) {
	if q, err := getBinanceAlphaPrice(symbol); err == nil {
		return q, nil
	}
	if q, err := tryOkxDexAllChains(symbol); err == nil {
		return q, nil
	}
	return nil, fmt.Errorf("crypto-alt: no quote for %s (not an alpha/dex token)", symbol)
}

type BinanceAlphaTokenList struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Data    []struct {
		AlphaID         string `json:"alphaId"`
		Symbol          string `json:"symbol"`
		Name            string `json:"name"`
		ChainID         string `json:"chainId"`
		ContractAddress string `json:"contractAddress"`
	} `json:"data"`
}

type BinanceAlphaQuote struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Symbol      string `json:"symbol"`
		Price       string `json:"price"`
		PriceChange string `json:"priceChange"`
		High        string `json:"high"`
		Low         string `json:"low"`
		Volume      string `json:"volume"`
		QuoteVolume string `json:"quoteVolume"`
	} `json:"data"`
}

// Binance Alpha 代币列表缓存
var (
	binanceAlphaTokens   map[string]string // symbol -> contractAddress
	binanceAlphaTokensMu sync.RWMutex
	binanceAlphaLoaded   bool
)

func loadBinanceAlphaTokens() error {
	binanceAlphaTokensMu.Lock()
	defer binanceAlphaTokensMu.Unlock()

	if binanceAlphaLoaded && len(binanceAlphaTokens) > 0 {
		return nil
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequest("GET", "https://www.binance.com/bapi/defi/v1/public/wallet-direct/buw/wallet/cex/alpha/all/token/list", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("binance alpha api error: %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var tokenList BinanceAlphaTokenList
	if err := json.Unmarshal(body, &tokenList); err != nil {
		return err
	}

	if tokenList.Code != "000000" {
		return fmt.Errorf("binance alpha api error: %s", tokenList.Message)
	}

	binanceAlphaTokens = make(map[string]string)
	for _, token := range tokenList.Data {
		binanceAlphaTokens[strings.ToUpper(token.Symbol)] = token.ContractAddress
	}
	binanceAlphaLoaded = true

	return nil
}

func getBinanceAlphaPrice(symbol string) (*types.MarketQuote, error) {
	// 确保代币列表已加载
	if err := loadBinanceAlphaTokens(); err != nil {
		return nil, err
	}

	// 标准化 symbol
	sym := strings.ToUpper(strings.TrimSuffix(symbol, "USDT"))

	binanceAlphaTokensMu.RLock()
	_, exists := binanceAlphaTokens[sym]
	binanceAlphaTokensMu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("token %s not found in binance alpha", sym)
	}

	// 调用 Binance Alpha 行情 API
	client := &http.Client{Timeout: 10 * time.Second}
	apiURL := fmt.Sprintf("https://www.binance.com/bapi/defi/v1/public/wallet-direct/buw/wallet/cex/alpha/token/ticker/24hr?symbol=%s", sym)
	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("binance alpha ticker api error: %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var quote BinanceAlphaQuote
	if err := json.Unmarshal(body, &quote); err != nil {
		return nil, err
	}

	if quote.Code != "000000" {
		return nil, fmt.Errorf("binance alpha api error: %s", quote.Message)
	}

	price, _ := strconv.ParseFloat(quote.Data.Price, 64)
	priceChange, _ := strconv.ParseFloat(quote.Data.PriceChange, 64)
	high, _ := strconv.ParseFloat(quote.Data.High, 64)
	low, _ := strconv.ParseFloat(quote.Data.Low, 64)
	volume, _ := strconv.ParseFloat(quote.Data.Volume, 64)

	// 计算涨跌幅
	prevPrice := price - priceChange
	changePct := 0.0
	if prevPrice != 0 {
		changePct = (priceChange / prevPrice) * 100
	}

	return &types.MarketQuote{
		Symbol:        sym,
		Name:          sym,
		Price:         price,
		Change:        priceChange,
		ChangePercent: changePct,
		High:          high,
		Low:           low,
		Volume:        int64(volume),
		UpdateTime:    time.Now(),
		Exchange:      "Binance Alpha",
	}, nil
}

// OKX DEX 常用代币合约地址映射 (chainIndex -> symbol -> contractAddress)
var okxDexTokenContracts = map[string]map[string]string{
	// Ethereum (chainIndex: 1)
	"1": {
		"PEPE":  "0x6982508145454ce325ddbe47a25d4ec3d2311933",
		"SHIB":  "0x95aD61b0a150d79219dCF64E1E6Cc01f0B64C4cE",
		"FLOKI": "0xcf0c122c6b73ff809c693db761e7baebe62b6a2e",
		"LINK":  "0x514910771af9ca656af840dff83e8264ecf986ca",
		"UNI":   "0x1f9840a85d5af5bf1d1762f925bdaddc4201f984",
		"AAVE":  "0x7fc66500c84a76ad7e9c93437bfc5ac33e2ddae9",
		"MKR":   "0x9f8f72aa9304c8b593d555f12ef6589cc3a579a2",
		"MATIC": "0x7d1afa7b718fb893db30a3abc0cfc608aacfebb0",
		"LDO":   "0x5a98fcbea516cf06857215779fd812ca3bef1b32",
		"APE":   "0x4d224452801aced8b2f0aebe155379bb5d594381",
	},
	// BSC (chainIndex: 56)
	"56": {
		"CAKE":   "0x0e09fabb73bd3ade0a17ecc321fd13a19e81ce82",
		"BAKE":   "0xe02df9e3e622debdd69fb838bb799e3f168902c5",
		"XVS":    "0xcf6bb5389c92bdda8a3747ddb454cb7a64626c63",
		"ALPACA": "0x8f0528ce5ef7b51152a59745befdd91d97091d2f",
		"FLOKI":  "0xfb5b838b6cfeedc2873ab27866079ac55363d37e",
	},
	// Solana (chainIndex: 501)
	"501": {
		"BONK":   "DezXAZ8z7PnrnRJjz3wXBoRgixCa6xjnB7YaB1pPB263",
		"WIF":    "EKpQGSJtjMFqKZ9KQanSqYXRcF8fBopzLHYxdM65zcjm",
		"POPCAT": "7GCihgDB8fe6KNjn2MYtkzZcRjQy3t9GHdC8uHYmW2hr",
		"MEW":    "MEW1gQWJ3nEXg2qgERiKu7FAFj79PHvQVREQUzScPP5",
		"BOME":   "ukHH6c7mMyiWCf1b9pnWe25TSpkDDt3H5pQZgZ74J82",
		"SLERF":  "7BgBvyjrZX1YKz4oh9mjb8ZScatkkwb8DzFx7LoiVkM3",
	},
	// Base (chainIndex: 8453)
	"8453": {
		"BRETT":  "0x532f27101965dd16442E59d40670FaF5eBB142E4",
		"DEGEN":  "0x4ed4E862860beD51a9570b96d89aF5E1B0Efefed",
		"TOSHI":  "0xAC1Bd2486aAf3B5C0fc3Fd868558b082a531B2B4",
		"HIGHER": "0x0578d8A44db98B23BF096A382e016e29a5Ce0ffe",
	},
	// Arbitrum (chainIndex: 42161)
	"42161": {
		"ARB":   "0x912ce59144191c1204e64559fe8253a0e49e6548",
		"GMX":   "0xfc5a1a6eb076a2c7ad06ed22c90d7e710e35ad0a",
		"MAGIC": "0x539bde0d7dbd336b79148aa742883198bbf60342",
		"RDNT":  "0x3082cc23568ea640225c2467653db90e9250aaa0",
	},
}

func getOkxDexPrice(symbol string, chainIndex string) (*types.MarketQuote, error) {
	sym := strings.ToUpper(strings.TrimSuffix(symbol, "USDT"))

	// 查找合约地址
	chainTokens, ok := okxDexTokenContracts[chainIndex]
	if !ok {
		return nil, fmt.Errorf("chain %s not supported", chainIndex)
	}

	contractAddr, ok := chainTokens[sym]
	if !ok {
		return nil, fmt.Errorf("token %s not found on chain %s", sym, chainIndex)
	}

	// 调用 OKX DEX API
	client := &http.Client{Timeout: 10 * time.Second}
	apiURL := fmt.Sprintf("https://www.okx.com/api/v5/dex/aggregator/all-tokens?chainId=%s", chainIndex)
	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("okx dex api error: %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)

	// 解析响应获取代币信息
	var result struct {
		Code string `json:"code"`
		Data []struct {
			TokenContractAddress string `json:"tokenContractAddress"`
			TokenSymbol          string `json:"tokenSymbol"`
			TokenName            string `json:"tokenName"`
			TokenUnitPrice       string `json:"tokenUnitPrice"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	if result.Code != "0" {
		return nil, fmt.Errorf("okx dex api error: code %s", result.Code)
	}

	// 查找对应的代币
	for _, token := range result.Data {
		if strings.EqualFold(token.TokenContractAddress, contractAddr) ||
			strings.EqualFold(token.TokenSymbol, sym) {
			price, _ := strconv.ParseFloat(token.TokenUnitPrice, 64)

			return &types.MarketQuote{
				Symbol:     token.TokenSymbol,
				Name:       token.TokenName,
				Price:      price,
				UpdateTime: time.Now(),
				Exchange:   fmt.Sprintf("OKX DEX (Chain %s)", chainIndex),
			}, nil
		}
	}

	return nil, fmt.Errorf("token %s not found in okx dex response", sym)
}

func tryOkxDexAllChains(symbol string) (*types.MarketQuote, error) {
	sym := strings.ToUpper(strings.TrimSuffix(symbol, "USDT"))

	// 按优先级尝试各链
	chainPriority := []string{"1", "56", "501", "8453", "42161"} // ETH, BSC, SOL, Base, ARB

	for _, chainIndex := range chainPriority {
		if tokens, ok := okxDexTokenContracts[chainIndex]; ok {
			if _, exists := tokens[sym]; exists {
				if quote, err := getOkxDexPrice(symbol, chainIndex); err == nil {
					return quote, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("token %s not found on any supported chain", sym)
}
