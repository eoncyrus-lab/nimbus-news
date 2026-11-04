// fetch_cross_asset.go — 拉取黄金、QQQ、SPY、GLD、UUP、VIXY 价格数据用于跨资产对比。
//
// 数据源:
//   - 黄金: Binance PAXG/USDT (tokenized gold, 无 API key, 稳定)
//   - QQQ:  Yahoo Finance (纳斯达克 100 ETF)
//   - SPY:  Yahoo Finance (标普 500 ETF)
//
// 价格历史拉取量与 BTC 对齐 (3000d)，用于计算相关性和相对强弱。

package client

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
	"github.com/Ricaardo/nimbus-os/datasources/binance"
	dsyahoo "github.com/Ricaardo/nimbus-os/datasources/yahoo"
)

// FetchCrossAssetData 拉取黄金(London Gold > PAXG) + QQQ/SPY/GLD/UUP/VIXY(Futu>Yahoo) 的近期价格和历史。
//
// 数据源优先级:
//   - 黄金: London Gold (PriceStore, 1968+) > Binance PAXG/USDT (tokenized gold, 2019+)
//   - QQQ/SPY: Futu OpenD (本地网关) > Yahoo Finance (公网备份)
//   - 油价: Futu USO ETF proxy > Yahoo CL=F WTI futures
//
// 环境变量:
//
//	FUTU_GATEWAY=127.0.0.1:11111  (默认)
//	FUTU_ENABLED=0                禁用富途，直接用 Yahoo
func FetchCrossAssetData(ctx context.Context, targetDays int) (*CrossAssetPrices, error) {
	if targetDays <= 0 {
		targetDays = 3000
	}
	out := &CrossAssetPrices{}

	// 黄金: London Gold (PriceStore) > Binance PAXG/USDT klines
	goldFromStore := fetchLondonGoldForCrossAsset(targetDays)
	if goldFromStore != nil && len(goldFromStore) > 0 {
		n := len(goldFromStore)
		out.GoldPrice = goldFromStore[n-1]
		out.GoldHistory = goldFromStore
		// Mark that London gold is available
		out.Warnings = append(out.Warnings, "gold: using London gold (PriceStore, 1968+)")
	} else if err := fetchBinancePAXG(ctx, targetDays, out); err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("Binance PAXG fetch failed: %v", err))
	}

	// QQQ/SPY/GLD/UUP/TLT/VIXY: 先试常驻服务 (market-data + warehouse)，再降级 Yahoo
	futuOK := false
	if os.Getenv("FUTU_ENABLED") != "0" {
		if futuData, err := FetchCrossAssetFromServices(targetDays); err == nil && futuData != nil {
			out.QQQPrice = futuData.QQQPrice
			out.QQQHistory = futuData.QQQHistory
			out.QQQPriceAsOf = futuData.QQQPriceAsOf
			out.SPYPrice = futuData.SPYPrice
			out.SPYHistory = futuData.SPYHistory
			out.SPYPriceAsOf = futuData.SPYPriceAsOf
			out.GLDPrice = futuData.GLDPrice
			out.GLDHistory = futuData.GLDHistory
			out.GLDPriceAsOf = futuData.GLDPriceAsOf
			out.UUPPrice = futuData.UUPPrice
			out.UUPHistory = futuData.UUPHistory
			out.UUPPriceAsOf = futuData.UUPPriceAsOf
			out.TLTPrice = futuData.TLTPrice
			out.TLTHistory = futuData.TLTHistory
			out.TLTPriceAsOf = futuData.TLTPriceAsOf
			out.VIXYPrice = futuData.VIXYPrice
			out.VIXYHistory = futuData.VIXYHistory
			out.VIXYPriceAsOf = futuData.VIXYPriceAsOf
			out.WTIPrice = futuData.WTIPrice
			out.WTIHistory = futuData.WTIHistory
			out.WTIPriceAsOf = futuData.WTIPriceAsOf
			out.OilPriceSource = futuData.OilPriceSource
			out.Warnings = append(out.Warnings, futuData.Warnings...)
			if out.QQQPrice > 0 && out.SPYPrice > 0 {
				futuOK = true
			}
		} else if err != nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("services fetch failed (will try Yahoo): %v", err))
		}
	}

	// Yahoo Finance fallback (QQQ, SPY only — WTI fetched below)
	if !futuOK {
		type result struct {
			symbol  string
			price   float64
			history []float64
			asOf    string
			err     error
		}
		ch := make(chan result, 2)
		for _, sym := range []string{"QQQ", "SPY"} {
			go func(symbol string) {
				p, h, a, e := fetchYahooChart(ctx, symbol, targetDays)
				ch <- result{symbol, p, h, a, e}
			}(sym)
		}
		for i := 0; i < 2; i++ {
			r := <-ch
			if r.err != nil {
				out.Warnings = append(out.Warnings, fmt.Sprintf("Yahoo %s: %v", r.symbol, r.err))
				continue
			}
			switch r.symbol {
			case "QQQ":
				out.QQQPrice = r.price
				out.QQQHistory = r.history
				out.QQQPriceAsOf = r.asOf
			case "SPY":
				out.SPYPrice = r.price
				out.SPYHistory = r.history
				out.SPYPriceAsOf = r.asOf
			}
		}
	}

	// Oil price/proxy: Futu US.USO 优先，失败时用 Yahoo CL=F 降级。
	// USO is an ETF proxy, not a barrel-denominated WTI futures price.
	if out.WTIPrice <= 0 {
		if p, h, a, e := fetchYahooChart(ctx, "CL=F", targetDays); e != nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("WTI fetch failed: %v", e))
		} else if p > 0 {
			out.WTIPrice = p
			out.WTIHistory = h
			out.WTIPriceAsOf = a
			out.OilPriceSource = "yahoo:CL=F"
		}
	}

	return out, nil
}

// fetchBinancePAXG 从 Binance 拉取 PAXG/USDT 价格历史和最新价。
// 使用与 BTC/ETH 相同的 klines API，免费、无频率限制。
func fetchBinancePAXG(ctx context.Context, targetDays int, out *CrossAssetPrices) error {
	bars, err := binance.KlinesDeep(ctx, "PAXGUSDT", targetDays)
	if err != nil {
		return err
	}
	if len(bars) == 0 {
		return fmt.Errorf("empty PAXG klines")
	}

	n := len(bars)
	history := make([]float64, n)
	// bars are ascending; index 0 = newest.
	for i, k := range bars {
		idx := n - 1 - i
		history[idx] = k.Close
		if idx == 0 {
			out.GoldPriceAsOf = time.UnixMilli(k.OpenTime).UTC().Format("2006-01-02")
		}
	}

	out.GoldPrice = history[0]
	out.GoldHistory = history
	return nil
}

func fetchYahooChart(ctx context.Context, symbol string, targetDays int) (price float64, history []float64, asOf string, err error) {
	bars, err := dsyahoo.GetHistoryDays(ctx, symbol, targetDays)
	if err != nil {
		return 0, nil, "", err
	}
	if len(bars) == 0 {
		return 0, nil, "", fmt.Errorf("yahoo %s empty result", symbol)
	}

	// bars are ascending and dense (GetHistoryDays drops nil/<=0 closes), so
	// history is newest-first with no zero-filled gaps. index 0 = latest.
	n := len(bars)
	history = make([]float64, n)
	for i, b := range bars {
		history[n-1-i] = b.Close
	}
	price = history[0]
	asOf = bars[n-1].Date

	return price, history, asOf, nil
}

// CrossAssetPrices 跨资产价格数据聚合
type CrossAssetPrices struct {
	// Gold (Binance PAXG — tokenized gold)
	GoldPrice     float64
	GoldHistory   []float64
	GoldPriceAsOf string
	// Gold ETF (Futu GLD — physical gold ETF)
	GLDPrice     float64
	GLDHistory   []float64
	GLDPriceAsOf string
	// US equities (Futu > Yahoo)
	QQQPrice     float64
	QQQHistory   []float64
	QQQPriceAsOf string
	SPYPrice     float64
	SPYHistory   []float64
	SPYPriceAsOf string
	// DXY proxy (Futu UUP — USD bull ETF)
	UUPPrice     float64
	UUPHistory   []float64
	UUPPriceAsOf string
	// Treasuries (Futu TLT — 20Y+)
	TLTPrice     float64
	TLTHistory   []float64
	TLTPriceAsOf string
	// Volatility (Futu VIXY — VIX ETF)
	VIXYPrice     float64
	VIXYHistory   []float64
	VIXYPriceAsOf string
	// Oil proxy / WTI futures. Source identifies whether the value is USO ETF
	// proxy or actual CL=F WTI futures data.
	WTIPrice       float64
	WTIHistory     []float64
	WTIPriceAsOf   string
	OilPriceSource string
	Warnings       []string
}

// fetchLondonGoldForCrossAsset reads London gold prices from PriceStore
// for use as the primary gold source in cross-asset features.
// Returns newest-first float64 slice, or nil if unavailable.
func fetchLondonGoldForCrossAsset(targetDays int) []float64 {
	s := &store.PriceStore{}
	history, err := s.LoadHistory("gold")
	if err != nil || len(history) == 0 {
		return nil
	}
	if len(history) > targetDays {
		history = history[:targetDays]
	}
	return history
}
