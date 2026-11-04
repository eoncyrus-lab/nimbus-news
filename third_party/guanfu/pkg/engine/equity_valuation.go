// equity_valuation.go — Futu snapshot integration for QQQ/SPY PE/PB.
//
// Try to fetch PE/PB from Futu OpenD when available. Falls back gracefully:
//   - If OpenD is not running → valuation domain shows "待接入" placeholder
//   - If snapshot API fails → same graceful fallback
//   - PE=0 or PB=0 → treated as unavailable

package engine

import (
	"log"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/client"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
)

// tryFetchEquityValuation attempts to get QQQ and SPY PE/PB from Futu.
// Returns nil if unavailable (OpenD not running, API error, etc).
func tryFetchEquityValuation() *client.FutuEquityValuation {
	v, err := client.FetchEquityValuationFromServices()
	if err != nil {
		log.Printf("equity valuation: market-data snapshot unavailable (%v), using placeholder", err)
		return nil
	}
	return v
}

// enrichEquityPanelWithValuation adds PE/PB indicators to the valuation domain
// if valuation data is available.
func enrichEquityPanelWithValuation(panel *model.IndicatorPanel, asset string, pe, pb float64) {
	if panel.Valuation == nil {
		panel.Valuation = make(map[string]model.Indicator)
	}

	if pe > 0 {
		panel.Valuation["pe"] = model.Indicator{
			Value:  pe,
			Label:  peLabel(pe),
			Source: "futu:snapshot",
		}
	} else {
		panel.Valuation["pe"] = model.Indicator{
			Missing: true,
			Label:   "待接入 (需Futu OpenD)",
			Source:  "待接入",
			Note:    "启动 Futu OpenD 后自动获取 PE",
		}
	}

	if pb > 0 {
		panel.Valuation["pb"] = model.Indicator{
			Value:  pb,
			Label:  pbLabel(pb),
			Source: "futu:snapshot",
		}
	} else {
		panel.Valuation["pb"] = model.Indicator{
			Missing: true,
			Label:   "待接入 (需Futu OpenD)",
			Source:  "待接入",
			Note:    "启动 Futu OpenD 后自动获取 PB",
		}
	}

	// Add PEG placeholder (requires earnings growth rate)
	panel.Valuation["peg"] = model.Indicator{
		Missing: true,
		Label:   "待接入",
		Source:  "待接入",
		Note:    "PEG 需要盈利增长率数据 (远期)",
	}
}

func pbLabel(pb float64) string {
	if pb > 5 {
		return "偏高"
	} else if pb > 3 {
		return "中性偏高"
	} else if pb > 1 {
		return "中性"
	}
	return "偏低"
}
