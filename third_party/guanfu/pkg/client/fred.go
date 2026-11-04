// FRED (St. Louis Fed) macro data fetcher for guanfu.
//
// 拉 7 个序列：
//   - DTWEXBGS: Trade-Weighted USD Index (Broad)，daily — DXY 代理
//   - DFII10:   10-Year TIPS 收益率，daily
//   - M2SL:     M2 货币供应（季调），monthly — YoY 同比
//   - SP500:    标普 500，daily — 与 BTC 做 30d 收益相关
//   - WALCL:    Fed 总资产（每周三），weekly — QT 监控
//   - RRPONTSYD: 隔夜逆回购（daily），daily — 流动性蓄水池
//   - WTREGEN:  财政部现金余额（daily），daily — TGA 注水/抽水
//
// # Net Liquidity ≈ WALCL - RRPONTSYD - WTREGEN
//
// 需要环境变量 FRED_API_KEY。无 key 时 FetchMacroData 返回 nil + nil error，
// 由 RealClient 决定是否填 placeholder。
package client

import (
	"context"
	"fmt"
	"math"
	"os"
	"strconv"
	"time"

	"github.com/Ricaardo/nimbus-os/datasources/fred"
	"github.com/shopspring/decimal"
)

// MacroData FRED 衍生指标 + 时效性
type MacroData struct {
	DXY60dTrendPct     float64
	DXYLatest          float64
	DXYAsOf            string
	RealYield10YPct    float64
	RealYield10YAsOf   string
	M2YoYPct           float64
	M2LatestB          float64
	M2AsOf             string
	SPXCorrelation30d  float64
	SPXAsOf            string
	HYSpreadBps        float64
	HYSpreadAsOf       string
	YieldCurve10Y2YBps float64
	YieldCurveAsOf     string
	// US Liquidity (Priority: Fed assets, RRP, TGA)
	FedAssetsB    float64 // WALCL: Fed total assets (millions USD), converted to billions
	FedAssetsAsOf string
	RRPB          float64 // RRPONTSYD: ON reverse repo (billions USD)
	RRPAsOf       string
	TGA_B         float64 // WTREGEN: Treasury General Account (billions USD)
	TGAAsOf       string
	NetLiquidityB float64 // FedAssets - RRP - TGA (billions)
	// 60d trends
	FedAssets60dTrendPct float64
	RRP60dTrendPct       float64
	TGA60dTrendPct       float64
	NetLiq60dTrendPct    float64
	StaleWarnings        []string
}

// FetchMacroData 并发拉 4 个序列并计算衍生指标。
// btcHistoryNewestFirst: BTC 历史价格（idx 0 = 今天，与 RealClient 一致），用于 SPX 相关。
// 没设 FRED_API_KEY → 返回 (nil, nil)。
func FetchMacroData(ctx context.Context, btcHistoryNewestFirst []decimal.Decimal) (*MacroData, error) {
	apiKey := os.Getenv("FRED_API_KEY")
	if apiKey == "" {
		return nil, nil
	}

	type result struct {
		series string
		obs    []fred.Observation
		err    error
	}
	ch := make(chan result, 9)

	fetch := func(series string, limit int) {
		obs, err := fred.Observations(ctx, apiKey, fred.Query{Series: series, Limit: limit, SortDesc: true})
		ch <- result{series: series, obs: obs, err: err}
	}

	go fetch("DTWEXBGS", 90)    // 60d 趋势 + buffer
	go fetch("DFII10", 1)       // 仅最新
	go fetch("M2SL", 14)        // YoY 同比，留 buffer
	go fetch("SP500", 45)       // 30 trading days + 周末 buffer
	go fetch("BAMLH0A0HYM2", 3) // HY spread, monthly
	go fetch("T10Y2Y", 3)       // 10Y-2Y spread, daily
	go fetch("WALCL", 20)       // weekly: 20 weeks → ~140d buffer for 60d trend
	go fetch("RRPONTSYD", 90)   // daily: 90d buffer
	go fetch("WTREGEN", 90)     // daily (business): 90d buffer

	results := map[string][]fred.Observation{}
	var stales []string
	for i := 0; i < 9; i++ {
		r := <-ch
		if r.err != nil {
			stales = append(stales, fmt.Sprintf("%s 拉取失败: %v", r.series, r.err))
			continue
		}
		results[r.series] = r.obs
	}

	out := &MacroData{StaleWarnings: stales}

	if obs := results["DTWEXBGS"]; len(obs) >= 61 {
		latest := parseFloat(obs[0].Value)
		past := parseFloat(obs[60].Value)
		if latest > 0 && past > 0 {
			out.DXYLatest = latest
			out.DXY60dTrendPct = (latest - past) / past * 100
			out.DXYAsOf = obs[0].Date
		}
	}

	if obs := results["DFII10"]; len(obs) >= 1 {
		v := parseFloat(obs[0].Value)
		if !math.IsNaN(v) && obs[0].Value != "." {
			out.RealYield10YPct = v
			out.RealYield10YAsOf = obs[0].Date
		}
	}

	if obs := results["M2SL"]; len(obs) >= 13 {
		latest := parseFloat(obs[0].Value)
		past := parseFloat(obs[12].Value)
		if latest > 0 && past > 0 {
			out.M2LatestB = latest
			out.M2YoYPct = (latest - past) / past * 100
			out.M2AsOf = obs[0].Date
		}
	}

	if obs := results["SP500"]; len(obs) >= 31 && len(btcHistoryNewestFirst) > 60 {
		corr, asof, ok := computeBTCSPXCorrelation30d(obs, btcHistoryNewestFirst)
		if ok {
			out.SPXCorrelation30d = corr
			out.SPXAsOf = asof
		}
	}

	// HY spread (basis points → float)
	if obs := results["BAMLH0A0HYM2"]; len(obs) >= 1 {
		v := parseFloat(obs[0].Value)
		if !math.IsNaN(v) && obs[0].Value != "." {
			out.HYSpreadBps = v * 100 // FRED returns %, convert to bps
			out.HYSpreadAsOf = obs[0].Date
		}
	}

	// 10Y-2Y yield curve spread (percentage points → basis points)
	if obs := results["T10Y2Y"]; len(obs) >= 1 {
		v := parseFloat(obs[0].Value)
		if !math.IsNaN(v) && obs[0].Value != "." {
			out.YieldCurve10Y2YBps = v * 100 // convert pp to bp
			out.YieldCurveAsOf = obs[0].Date
		}
	}

	// ── US Liquidity: Fed Assets (WALCL, weekly, millions USD → billions) ──
	if obs := results["WALCL"]; len(obs) >= 2 {
		latest := parseFloat(obs[0].Value) / 1000 // M → B
		if latest > 0 {
			out.FedAssetsB = latest
			out.FedAssetsAsOf = obs[0].Date
			// Approx 60d trend: WALCL is weekly, ~9 weeks ≈ 63 days
			if len(obs) >= 10 {
				past := parseFloat(obs[9].Value) / 1000
				if past > 0 {
					out.FedAssets60dTrendPct = (latest - past) / past * 100
				}
			}
		}
	}

	// ── RRP (daily, billions USD) ──
	if obs := results["RRPONTSYD"]; len(obs) >= 1 {
		latest := parseFloat(obs[0].Value) // already in billions
		if !math.IsNaN(latest) {
			out.RRPB = latest
			out.RRPAsOf = obs[0].Date
			if len(obs) >= 61 {
				past := parseFloat(obs[60].Value)
				if !math.IsNaN(past) && past > 0 {
					out.RRP60dTrendPct = (latest - past) / past * 100
				}
			}
		}
	}

	// ── TGA (daily, billions USD) ──
	if obs := results["WTREGEN"]; len(obs) >= 1 {
		latest := parseFloat(obs[0].Value) / 1000 // M → B
		if !math.IsNaN(latest) {
			out.TGA_B = latest
			out.TGAAsOf = obs[0].Date
			if len(obs) >= 61 {
				past := parseFloat(obs[60].Value) / 1000
				if !math.IsNaN(past) && past > 0 {
					out.TGA60dTrendPct = (latest - past) / past * 100
				}
			}
		}
	}

	// ── Net Liquidity = Fed Assets - RRP - TGA ──
	if out.FedAssetsB > 0 && out.RRPAsOf != "" {
		out.NetLiquidityB = out.FedAssetsB - out.RRPB - out.TGA_B
		// 60d trend: approximate from component trends weighted by current composition
		if out.FedAssets60dTrendPct != 0 || out.RRP60dTrendPct != 0 || out.TGA60dTrendPct != 0 {
			wFed := out.FedAssetsB / (out.FedAssetsB + abs(out.RRPB) + abs(out.TGA_B) + 1)
			wRRP := abs(out.RRPB) / (out.FedAssetsB + abs(out.RRPB) + abs(out.TGA_B) + 1)
			wTGA := abs(out.TGA_B) / (out.FedAssetsB + abs(out.RRPB) + abs(out.TGA_B) + 1)
			out.NetLiq60dTrendPct = wFed*out.FedAssets60dTrendPct + wRRP*out.RRP60dTrendPct + wTGA*out.TGA60dTrendPct
			// RRP下降 = 流动性释出 = 正贡献；TGA下降同理
			if out.RRPB > 0 && out.RRP60dTrendPct < 0 {
				out.NetLiq60dTrendPct += abs(out.RRP60dTrendPct) * wRRP
			}
			if out.TGA_B > 0 && out.TGA60dTrendPct < 0 {
				out.NetLiq60dTrendPct += abs(out.TGA60dTrendPct) * wTGA
			}
		}
	}

	return out, nil
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// computeBTCSPXCorrelation30d 用最近 31 个 SPX 交易日 + BTC 同日收盘做日收益相关
//
// SPX obs 是 desc 排序（idx 0 = 最新）。BTC 历史也是 newest first，但 BTC 7×24，
// SPX 5×7。我们按 SPX 的日期匹配 BTC 的天数偏移（today.Sub(SPXdate).Days）。
func computeBTCSPXCorrelation30d(spxObs []fred.Observation, btcHistory []decimal.Decimal) (float64, string, bool) {
	now := time.Now().UTC().Truncate(24 * time.Hour)

	type pair struct{ btc, spx float64 }
	pairs := make([]pair, 0, 31)

	for i := 0; i < len(spxObs) && len(pairs) < 31; i++ {
		val := parseFloat(spxObs[i].Value)
		if val <= 0 || spxObs[i].Value == "." {
			continue
		}
		date, err := time.Parse("2006-01-02", spxObs[i].Date)
		if err != nil {
			continue
		}
		offset := int(now.Sub(date).Hours() / 24)
		if offset < 0 || offset >= len(btcHistory) {
			continue
		}
		btc, _ := btcHistory[offset].Float64()
		if btc <= 0 {
			continue
		}
		pairs = append(pairs, pair{btc: btc, spx: val})
	}

	if len(pairs) < 31 {
		return 0, "", false
	}

	// pairs 当前是 newest-first；计算日收益要 chrono 顺序
	btcRet := make([]float64, 0, 30)
	spxRet := make([]float64, 0, 30)
	for i := 0; i < 30; i++ {
		// pairs[i] 是 newer，pairs[i+1] 是 older（desc 排）
		newer := pairs[i]
		older := pairs[i+1]
		if older.btc <= 0 || older.spx <= 0 {
			continue
		}
		btcRet = append(btcRet, math.Log(newer.btc/older.btc))
		spxRet = append(spxRet, math.Log(newer.spx/older.spx))
	}
	if len(btcRet) < 20 {
		return 0, "", false
	}
	return pearson(btcRet, spxRet), spxObs[0].Date, true
}

// pearson 皮尔逊相关系数
func pearson(xs, ys []float64) float64 {
	n := len(xs)
	if n != len(ys) || n == 0 {
		return 0
	}
	var sx, sy float64
	for i := 0; i < n; i++ {
		sx += xs[i]
		sy += ys[i]
	}
	mx, my := sx/float64(n), sy/float64(n)
	var num, dx2, dy2 float64
	for i := 0; i < n; i++ {
		dx := xs[i] - mx
		dy := ys[i] - my
		num += dx * dy
		dx2 += dx * dx
		dy2 += dy * dy
	}
	denom := math.Sqrt(dx2 * dy2)
	if denom == 0 {
		return 0
	}
	return num / denom
}

func parseFloat(s string) float64 {
	if s == "" || s == "." {
		return math.NaN()
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return math.NaN()
	}
	return v
}
