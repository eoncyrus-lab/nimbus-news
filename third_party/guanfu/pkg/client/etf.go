// SoSoValue BTC 现货 ETF 净流入 — 拉每日序列并汇总 7d/30d。
//
// 取数走独立的 sosovalue 数据源包（POST historicalInflowChart，返回全量每日
// 序列）；这里只做面板需要的窗口汇总与时效判断。

package client

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Ricaardo/nimbus-os/datasources/sosovalue"
)

// ETFData 整理后的 ETF 数据汇总
type ETFData struct {
	NetInflow7dUSD   float64   `json:"net_inflow_7d_usd"`
	NetInflow30dUSD  float64   `json:"net_inflow_30d_usd"`
	NetInflowMA7dUSD float64   `json:"net_inflow_ma7d_usd"` // 7 日均值（vs 30d 比较）
	TotalAssetsUSD   float64   `json:"total_assets_usd"`
	LatestDate       time.Time `json:"-"`
	StaleDays        int       `json:"-"` // 距今天数（>=2 警告）
}

// FetchBTCETFData 拉 SoSoValue ETF 数据并汇总 7d/30d
func FetchBTCETFData(ctx context.Context) (*ETFData, error) {
	flows, err := sosovalue.HistoricalInflowChart(ctx, sosoAPIKey(), "us-btc-spot")
	if err != nil {
		return nil, err
	}
	if len(flows) == 0 {
		return nil, fmt.Errorf("empty etf flows")
	}

	out := &ETFData{
		LatestDate: flows[len(flows)-1].Date,
		StaleDays:  int(time.Since(flows[len(flows)-1].Date).Hours() / 24),
	}

	// 累计 7 日 / 30 日
	for i := len(flows) - 1; i >= 0 && i >= len(flows)-30; i-- {
		days := len(flows) - 1 - i
		if days < 7 {
			out.NetInflow7dUSD += flows[i].NetInflowUSD
		}
		out.NetInflow30dUSD += flows[i].NetInflowUSD
	}
	out.NetInflowMA7dUSD = out.NetInflow7dUSD / 7
	out.TotalAssetsUSD = flows[len(flows)-1].TotalAssetsUSD

	return out, nil
}

func sosoAPIKey() string {
	if key := os.Getenv("SOSOVALUE_API_KEY"); key != "" {
		return key
	}
	return os.Getenv("SOSO_VALUE_API_KEY")
}
