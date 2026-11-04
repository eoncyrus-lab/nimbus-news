// CLI commands for allocate, market, DCA, and consensus.

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/allocate"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/dca"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

func runDCA(jsonOut, pretty, plain bool) {
	s := &store.PriceStore{}
	pricePoints, err := s.Load("btc")
	if err != nil || len(pricePoints) < 365 {
		fmt.Fprintf(os.Stderr, "dca: need at least 365 days of BTC price data in PriceStore\n")
		os.Exit(1)
	}
	points := make([]dca.Point, len(pricePoints))
	for i, p := range pricePoints {
		points[i] = dca.Point{Date: p.Date, Close: p.Close}
	}

	result, err := dca.RunComparison(points, 1000, 1460)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dca error: %v\n", err)
		os.Exit(1)
	}

	zoneResult, _ := dca.ZoneReplay(points)

	if jsonOut || pretty {
		out := map[string]interface{}{
			"comparison":  result,
			"zone_replay": zoneResult,
		}
		var b []byte
		if pretty {
			b, _ = json.MarshalIndent(out, "", "  ")
		} else {
			b, _ = json.Marshal(out)
		}
		fmt.Println(string(b))
		return
	}

	printDCAComparison(result, plain)
	if zoneResult != nil {
		printDCAZoneReplay(zoneResult, plain)
	}
	printDCACostWarning(plain)
}

// printDCACostWarning (L8): remind the user that the simulated ROI ignores
// brokerage / ETF / wrapping costs that compound meaningfully over multi-
// year horizons. We don't have per-user cost data, so just surface the
// typical range + say "subtract this from ROI in your head".
func printDCACostWarning(plain bool) {
	fmt.Println()
	if plain {
		fmt.Println("Cost awareness (not simulated):")
	} else {
		fmt.Println("成本提示(以下未计入上表 ROI):")
	}
	fmt.Println("  - 交易所手续费: 0.1-0.5% per trade → 每月 DCA 长期累积 ~0.5-1% 年化拖累")
	fmt.Println("  - BTC 现货 ETF (IBIT/FBTC): 0.12-0.25% expense ratio → 20y 复合 -2.4% 至 -4.8%")
	fmt.Println("  - 托管钱包 / 硬件: 一次性 0-0.2%,忽略不计")
	fmt.Println("  - 点差 / 税: 视辖区差异大;短线频繁 DCA 需特别关注")
	fmt.Println("  真实净 ROI = 上表 ROI - 年化成本拖累 × 年数。短 horizon 影响小,长期显著。")
}

func printDCAComparison(cr *dca.ComparisonResult, plain bool) {
	title := "观复 · DCA 定投策略对比"
	if plain {
		title = "DCA Strategy Comparison"
	}
	fmt.Printf("%s  (BTC: $%.0f)\n", title, cr.Price)
	fmt.Println(strings.Repeat("=", 72))
	fmt.Printf("%-12s %12s %10s %10s %9s %8s\n", "策略", "总投入", "持仓BTC", "现值", "ROI", "成本锚")
	fmt.Println(strings.Repeat("-", 72))
	for _, r := range cr.Results {
		marker := ""
		if r.Strategy == cr.Best {
			marker = " *"
		}
		fmt.Printf("%-12s $%10.0f %9.4f $%9.0f %+8.1f%% $%7.0f%s\n",
			r.Strategy, r.TotalInvested, r.TotalBTC,
			r.CurrentValue, r.ROIPct, r.CostBasis, marker)
	}
	fmt.Println(strings.Repeat("-", 72))
	fmt.Printf("Best: %s\n", cr.Best)
	fmt.Println()
	fmt.Println("Strategies:")
	fmt.Println("  fixed - same amount every month")
	fmt.Println("  ahr   - AHR999 < 0.8 accelerate (2x), > 1.2 decelerate (0.5x)")
	fmt.Println("  mayer - Mayer < 0.8 accelerate (2x), > 1.5 decelerate (0.5x)")
	fmt.Println()
	fmt.Println("Best = 历史回测 ROI 最高,不代表未来最优或推荐使用。")
	fmt.Println("Not investment advice.")
}

func printDCAZoneReplay(zr *dca.ZoneReplayResult, plain bool) {
	title := "Valuation Zone Historical Replay"
	if !plain {
		title = "估值区间历史回放"
	}
	fmt.Printf("\n%s: %s\n", title, zr.CurrentZone)
	fmt.Println(strings.Repeat("-", 64))
	fmt.Printf("%-8s %10s %10s %10s %10s\n", "Period", "Win Rate", "Median ROI", "Max DD", "Ann.")
	fmt.Println(strings.Repeat("-", 64))
	for _, p := range zr.Periods {
		fmt.Printf("%4dy    %8.0f%% %+9.1f%% %+9.1f%% %+9.1f%%  (n=%d)\n",
			p.Years, p.WinRate, p.MedianROI, p.MaxDrawdown, p.MedianAnnual, p.SampleCount)
	}
	fmt.Println(strings.Repeat("-", 64))
	fmt.Printf("DCA entries in zone [%s] historical performance.\n", zr.CurrentZone)
	fmt.Println("Not investment advice.")
	fmt.Println()
}

func runAllocate(jsonOut, pretty, plain bool) {
	pf := allocate.PortfolioAllWeather
	status, err := allocate.Analyze(pf)
	if err != nil {
		fmt.Fprintf(os.Stderr, "allocate error: %v\n", err)
		os.Exit(1)
	}
	if jsonOut || pretty {
		var b []byte
		if pretty {
			b, _ = json.MarshalIndent(status, "", "  ")
		} else {
			b, _ = json.Marshal(status)
		}
		fmt.Println(string(b))
		return
	}
	title := "Guanfu · Lazy Portfolio"
	if !plain {
		title = "观复 · 懒人组合配置"
	}
	fmt.Printf("%s  (%s)\n", title, status.Date)
	fmt.Println(strings.Repeat("=", 72))
	fmt.Printf("%-10s %8s %12s %s\n", "Asset", "Target%", "Price", "Hint")
	fmt.Println(strings.Repeat("-", 72))
	for _, a := range status.Assets {
		fmt.Printf("%-10s %7.0f%% $%10.2f %s\n", a.Asset, a.TargetPct, a.CurrentPrice, a.Hint)
	}
	fmt.Println(strings.Repeat("-", 72))
	fmt.Printf("Overall: %s", status.OverallZone)
	if status.RebalanceNeeded {
		fmt.Printf("  [!] rebalance threshold triggered")
	}
	fmt.Println()
	fmt.Println()
	fmt.Println("Not investment advice.")
}

func runMarketOverview(jsonOut, pretty, plain bool) {
	ov, err := allocate.MultiAssetOverview()
	if err != nil {
		fmt.Fprintf(os.Stderr, "market overview error: %v\n", err)
		os.Exit(1)
	}

	cs, _ := allocate.ConsensusScan()

	if jsonOut || pretty {
		out := map[string]interface{}{
			"overview":  ov,
			"consensus": cs,
		}
		var b []byte
		if pretty {
			b, _ = json.MarshalIndent(out, "", "  ")
		} else {
			b, _ = json.Marshal(out)
		}
		fmt.Println(string(b))
		return
	}

	title := "Guanfu · Multi-Asset Overview"
	if !plain {
		title = "观复 · 多资产一览"
	}
	fmt.Printf("%s  (%s)\n", title, ov.Date)
	fmt.Println(strings.Repeat("=", 56))
	fmt.Printf("%-10s %12s %10s %s\n", "Asset", "Price", "History", "Freshness")
	fmt.Println(strings.Repeat("-", 56))
	for _, item := range ov.Items {
		stale := ""
		if item.StaleDays > 2 {
			stale = fmt.Sprintf("! %dd", item.StaleDays)
		}
		fmt.Printf("%-10s $%10.2f %9dd %s\n", item.Asset, item.Price, item.HistoryDays, stale)
	}
	fmt.Println(strings.Repeat("-", 56))

	if cs != nil && len(cs.Signals) > 0 {
		fmt.Println()
		conTitle := "Multi-Asset Consensus"
		if !plain {
			conTitle = "多资产共识"
		}
		fmt.Printf("%s: %s (confidence: %.0f%%)\n", conTitle, cs.Direction, cs.Confidence*100)
		fmt.Printf("  %s\n", cs.Summary)
		fmt.Println(strings.Repeat("-", 56))
		fmt.Printf("%-8s %10s %10s %s\n", "Asset", "Price", "30d Mom.", "Signal")
		for _, sig := range cs.Signals {
			fmt.Printf("%-8s $%9.2f %+9.1f%% %s\n", sig.Asset, sig.Price, sig.Momentum, sig.Signal)
		}
		fmt.Println(strings.Repeat("-", 56))
		fmt.Println("Signal 仅表达当前 30d 动量方向标签,不是买卖指令。读盘见 --verdict。")
	}
}

func runStatus(jsonOut, pretty, plain bool) {
	s := &store.PriceStore{}
	assets, _ := s.ListAssets()

	if jsonOut || pretty {
		type statusItem struct {
			Asset     string `json:"asset"`
			Count     int    `json:"count"`
			LastDate  string `json:"last_date"`
			StaleDays int    `json:"stale_days"`
		}
		items := make([]statusItem, 0, len(assets))
		for _, asset := range assets {
			count, _ := s.Count(asset)
			lastDate, _ := s.LastDate(asset)
			stale := s.DaysSinceLastUpdate(asset)
			items = append(items, statusItem{asset, count, lastDate, stale})
		}
		out := map[string]interface{}{
			"status":       "ok",
			"prices_dir":   store.DefaultPricesDir(),
			"total_assets": len(assets),
			"assets":       items,
		}
		var b []byte
		if pretty {
			b, _ = json.MarshalIndent(out, "", "  ")
		} else {
			b, _ = json.Marshal(out)
		}
		fmt.Println(string(b))
		return
	}

	title := "Guanfu · Data Status"
	if !plain {
		title = "观复 · 数据状态"
	}
	fmt.Printf("%s\n", title)
	fmt.Printf("  Data dir: %s\n", store.DefaultPricesDir())
	fmt.Printf("  Tracked assets: %d\n", len(assets))
	fmt.Println(strings.Repeat("=", 64))
	fmt.Printf("%-10s %8s %12s %s\n", "Asset", "Days", "Last Date", "Freshness")
	fmt.Println(strings.Repeat("-", 64))

	allFresh := true
	for _, asset := range assets {
		count, _ := s.Count(asset)
		lastDate, _ := s.LastDate(asset)
		stale := s.DaysSinceLastUpdate(asset)
		staleStr := "ok"
		if stale > 2 {
			staleStr = fmt.Sprintf("! %dd ago", stale)
			allFresh = false
		}
		fmt.Printf("%-10s %7dd %12s %s\n", asset, count, lastDate, staleStr)
	}
	fmt.Println(strings.Repeat("-", 64))

	if len(assets) == 0 {
		fmt.Println()
		fmt.Println("No data in PriceStore. To import:")
		fmt.Println("  1. guanfu btc             # fetch BTC history (auto-import)")
		fmt.Println("  2. Ensure Futu OpenD runs  # import QQQ/SPY/GLD etc.")
		fmt.Println("  3. guanfu market           # view all imported assets")
	} else if allFresh {
		fmt.Println("All data fresh.")
	}
}

// parseKV extracts --key=value / --key value from a flag slice.
// (Shared by calibrate/joint/stress/watch; moved from the removed cli_intent.go.)
func parseKV(args []string) map[string]string {
	out := map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "--") && !strings.HasPrefix(a, "-") {
			continue
		}
		a = strings.TrimLeft(a, "-")
		if eq := strings.IndexByte(a, '='); eq >= 0 {
			out[a[:eq]] = a[eq+1:]
			continue
		}
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			out[a] = args[i+1]
			i++
		} else {
			out[a] = "true"
		}
	}
	return out
}
