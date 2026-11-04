package engine

import (
	"fmt"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
)

// historyTracked 列出从 history.db 取分位的指标 → 所属 domain。
// 这些指标缺少长 BTC-kline 派生历史，必须靠每日采集。
var historyTracked = map[string]string{
	"etf_net_flow_7d_usd":       "flow",
	"etf_net_flow_30d_usd":      "flow",
	"etf_total_assets_usd":      "flow",
	"stablecoin_market_cap_usd": "flow",
	"stablecoin_supply_30d_pct": "flow",
	"mempool_mb":                "network",
	"hash_rate_ehs":             "network",
	"difficulty_change_pct":     "network",
	"funding_rate_pct":          "positioning",
	"oi_to_mc":                  "positioning",
	"fear_greed":                "positioning",
	"dxy_60d_trend_pct":         "macro",
	"real_yield_10y_pct":        "macro",
	"m2_yoy":                    "macro",
	"spx_correlation_30d":       "macro",
	"fed_assets_b":              "macro",
	"rrp_b":                     "macro",
	"tga_b":                     "macro",
	"net_liquidity_b":           "macro",
}

const (
	historyLookbackDays = 730 // 2 年
	historyMinSamples   = 30  // 至少 30 天才算有意义
)

// persistAndAnnotateHistory 把今天的 tracked 指标写入 history.db，
// 同时为已有足够样本的指标回填 Quantile。
//
// 没有 Store（c.History == nil）时跳过 — Calculator 仍输出指标盘，仅缺历史分位。
func (c *Calculator) persistAndAnnotateHistory(p *model.IndicatorPanel, date string) {
	if c.History == nil {
		return
	}

	// 1) 收集今日值并批量写
	todayKV := map[string]float64{}
	for k, dom := range historyTracked {
		ind, ok := getDomainIndicator(p, dom, k)
		if !ok || ind.Missing || (ind.Value == 0 && ind.Label == "") {
			continue
		}
		todayKV[k] = ind.Value
	}
	if len(todayKV) > 0 {
		if err := c.History.RecordMany(date, todayKV); err != nil {
			// 写失败不致命：仍输出本次盘面
			return
		}
	}

	// 2) 回填 Quantile（仅当样本 >= 30）
	for k, dom := range historyTracked {
		ind, ok := getDomainIndicator(p, dom, k)
		if !ok {
			continue
		}
		val, present := todayKV[k]
		if !present {
			continue
		}
		q, n, err := c.History.QuantileAsOf(k, val, historyLookbackDays, date)
		if err != nil || n < historyMinSamples {
			continue
		}
		ind.Quantile = q
		ind.Note = fmt.Sprintf("%s（历史分位基于 %d 天采集）", ind.Note, n)
		setDomainIndicator(p, dom, k, ind)
	}
}

// stablecoinMarketCapNDaysAgo 从 history.db 查 N 天前的稳定币市值
func (c *Calculator) stablecoinMarketCapNDaysAgo(asOfDate string, daysAgo int) (float64, bool) {
	if c.History == nil {
		return 0, false
	}
	t, err := time.Parse("2006-01-02", asOfDate)
	if err != nil {
		return 0, false
	}
	pastDate := t.AddDate(0, 0, -daysAgo).Format("2006-01-02")
	return c.History.ValueAt(pastDate, "stablecoin_market_cap_usd")
}

func getDomainIndicator(p *model.IndicatorPanel, domain, key string) (model.Indicator, bool) {
	m := domainMap(p, domain)
	if m == nil {
		return model.Indicator{}, false
	}
	ind, ok := m[key]
	return ind, ok
}

func setDomainIndicator(p *model.IndicatorPanel, domain, key string, ind model.Indicator) {
	m := domainMap(p, domain)
	if m == nil {
		return
	}
	m[key] = ind
}

func domainMap(p *model.IndicatorPanel, domain string) map[string]model.Indicator {
	switch domain {
	case "cycle":
		return p.Cycle
	case "valuation":
		return p.Valuation
	case "network":
		return p.Network
	case "positioning":
		return p.Positioning
	case "macro":
		return p.Macro
	case "flow":
		return p.Flow
	case "technical":
		return p.Technical
	case "cross_asset":
		return p.CrossAsset
	}
	return nil
}
