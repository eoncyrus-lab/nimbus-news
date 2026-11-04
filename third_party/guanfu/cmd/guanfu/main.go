// guanfu / 观复: BTC 投资盘面 CLI
//
// 「致虚极，守静笃。万物并作，吾以观复。」——《道德经》第十六章
//
// 默认不输出评分 / action / state；--verdict 仅输出结构化读盘，不给交易指令。
// 万物并作 = 8 域指标同时呈现；观复 = 在历史分位中看其往复回归。
//
// Usage:
//
//	guanfu                       # 人类盘面（markdown 表）
//	guanfu --json                # JSON 扁平结构（喂程序 / Claude）
//	guanfu --pretty              # pretty JSON
//	guanfu --domain cycle        # 仅看单 domain
//	guanfu --halflife 730        # AHR 拟合半衰期（默认 1460 = 4 年）
//	guanfu --timeout 180s        # 拉数据超时
//	guanfu --plain               # 纯文本输出（无 emoji / box drawing）
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/application"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/client"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/engine"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast/features"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/version"
)

var appService = application.NewDefault()

// annotateBaselines reads the latest DGS3MO from PriceStore (F4) and
// annotates every HorizonForecast with T-bill + passive 60-40 baselines.
// On missing data falls back to a flat 4.5% / 6.0% assumption — keeps
// the field populated so SKILL consumers always see a comparison.
func annotateBaselines(fc *forecast.Forecast) {
	appService.AnnotateForecast(fc)
}

// emitClaim writes forecast → claim ledger when claim persistence is on.
// Silent no-op on any failure — claim recording is non-critical and must
// not break the user-visible forecast output.
func emitClaim(fc *forecast.Forecast, asset string, panel *model.IndicatorPanel) {
	appService.RecordForecast(fc, asset, panel)
}

// resolveHorizonsArg parses --forecast-horizons. The sentinel "auto"
// (or empty) defers to the asset's per-asset default (B5);
// other values are parsed as a comma-separated horizon list.
func resolveHorizonsArg(raw, assetKey string) ([]int, error) {
	s := strings.TrimSpace(raw)
	if s == "" || strings.EqualFold(s, "auto") {
		return forecast.HorizonsForAsset(assetKey), nil
	}
	return forecast.ParseHorizons(s)
}

// domain 中英文显示名
var domainNames = []struct {
	Key   string
	Title string
	Icon  string
}{
	{"cycle", "Cycle 周期定位", "🌊"},
	{"valuation", "Valuation 估值", "💰"},
	{"network", "Network 网络", "⛏️"},
	{"positioning", "Positioning 杠杆 & 情绪", "📊"},
	{"macro", "Macro 宏观", "🌍"},
	{"flow", "Flow 资金流", "💸"},
	{"technical", "Technical 技术指标", "📈"},
	{"cross_asset", "CrossAsset 跨资产", "🔗"},
}

func main() {
	jsonOut := flag.Bool("json", false, "JSON 输出")
	pretty := flag.Bool("pretty", false, "pretty JSON 输出")
	verdict := flag.Bool("verdict", false, "输出综合判断（牛熊/顶底/读盘标签）")
	verdictOnly := flag.Bool("verdict-only", false, "仅输出 verdict（隐藏指标盘）")
	forecastOut := flag.Bool("forecast", false, "输出 BTC 历史相似盘面走势推演")
	forecastOnly := flag.Bool("forecast-only", false, "仅输出 forecast（隐藏指标盘）")
	readingOnly := flag.Bool("reading", false, "仅输出市场读盘（panel + verdict，不跑 forecast）")
	forecastHorizons := flag.String("forecast-horizons", "auto", "走势推演周期，逗号分隔天数，如 30,90,180；'auto' 用资产专属默认（QQQ/SPY 30/63/90/180/252，Gold 30/60/90/120，其余 30/90/180）")
	forecastTop := flag.Int("forecast-top", 21, "走势推演使用的历史相似样本数")
	forecastPath := flag.Bool("forecast-path", false, "输出历史相似盘面路径推演 (ASCII fan chart)")
	recencyWeighted := flag.Bool("forecast-recency-weighted", false, "kNN 候选按近 5 年加权 1.25× (G5),适合 BTC ETF 后 / 2024+ 新 regime")
	regimeGate := flag.Bool("forecast-regime-gate", false, "跨 regime (bull/bear/fracture) analog 距离 ×1.2 惩罚 (G2),适合 Gold 2022+ regime 切换后")
	domainFilter := flag.String("domain", "", "仅看单个 domain: cycle/valuation/network/positioning/macro/flow/technical/cross_asset")
	timeout := flag.Duration("timeout", 90*time.Second, "拉数据超时")
	halfLife := flag.Int("halflife", 0, "AHR 拟合半衰期（天，默认 1460）")
	historyDB := flag.String("history-db", "", "history.db 路径（默认 ~/.guanfu/history.db；GUANFU_NO_HISTORY=1 禁用）")
	plain := flag.Bool("plain", false, "纯文本输出（无 emoji / box drawing）")
	noEmoji := flag.Bool("no-emoji", false, "等同 --plain")
	full := flag.Bool("full", false, "输出完整 40+ 指标盘面（默认只输出 --brief 摘要）")
	showVersion := flag.Bool("version", false, "打印版本并退出")
	refreshOnly := flag.String("only", "", "[refresh] 仅刷新指定 key（逗号分隔），如 btc,fred_dxy")
	refreshSkip := flag.String("skip", "", "[refresh] 跳过指定 key（逗号分隔）")
	refreshDryRun := flag.Bool("dry-run", false, "[refresh] 列出将要执行的 source，不真正拉数据")
	flag.Parse()

	// Subcommand detection from trailing (non-flag) args.
	// Supports both "guanfu qqq --forecast" and "guanfu --forecast qqq".
	trailing := flag.Args()
	subcmd := ""
	backtestAsset := "btc"
	if len(trailing) > 0 {
		subcmd = trailing[0]
		if subcmd == "backtest" && len(trailing) > 1 {
			backtestAsset = trailing[1]
		}
	}

	// Re-parse trailing flags that came after the subcommand
	// (flag.Parse stops at first non-flag arg, so these are unparsed).
	if len(trailing) > 1 {
		for i := 1; i < len(trailing); i++ {
			arg := trailing[i]
			switch {
			case arg == "--forecast" || arg == "-forecast":
				*forecastOut = true
			case arg == "--forecast-only" || arg == "-forecast-only":
				*forecastOnly = true
			case arg == "--forecast-path" || arg == "-forecast-path":
				*forecastPath = true
			case arg == "--forecast-recency-weighted" || arg == "-forecast-recency-weighted":
				*recencyWeighted = true
			case arg == "--forecast-regime-gate" || arg == "-forecast-regime-gate":
				*regimeGate = true
			case arg == "--verdict" || arg == "-verdict":
				*verdict = true
			case arg == "--verdict-only" || arg == "-verdict-only":
				*verdictOnly = true
			case arg == "--json" || arg == "-json":
				*jsonOut = true
			case arg == "--pretty" || arg == "-pretty":
				*pretty = true
			case arg == "--plain" || arg == "-plain":
				*plain = true
			case arg == "--no-emoji" || arg == "-no-emoji":
				*noEmoji = true
			case arg == "--full" || arg == "-full":
				*full = true
			case strings.HasPrefix(arg, "--domain="):
				*domainFilter = strings.TrimPrefix(arg, "--domain=")
			case strings.HasPrefix(arg, "-domain="):
				*domainFilter = strings.TrimPrefix(arg, "-domain=")
			case strings.HasPrefix(arg, "--forecast-top="):
				fmt.Sscanf(strings.TrimPrefix(arg, "--forecast-top="), "%d", forecastTop)
			}
		}
	}

	// Route subcommands
	switch subcmd {
	case "qqq", "spy", "gold":
		runEquityAsset(subcmd, *jsonOut, *pretty, *verdict, *verdictOnly, *forecastOut && !*readingOnly, *forecastOnly && !*readingOnly, *forecastHorizons, *forecastTop, *timeout, *plain || *noEmoji, *full, *domainFilter, *recencyWeighted, *regimeGate)
		return
	case "stock":
		ticker := "AAPL"
		if len(trailing) > 1 && !strings.HasPrefix(trailing[1], "-") {
			ticker = strings.ToUpper(trailing[1])
		}
		readStock(ticker, *jsonOut, *pretty, *forecastOut && !*readingOnly, *forecastOnly && !*readingOnly, *forecastHorizons, *forecastTop, *timeout, *plain || *noEmoji)
		return
	case "screen":
		screenMarket := "HK"
		screenStyle := "value"
		screenTop := 10
		screenListProfiles := false
		screenVerdictOnly := false
		for i := 1; i < len(trailing); i++ {
			arg := trailing[i]
			switch {
			case strings.HasPrefix(arg, "--market="):
				screenMarket = strings.ToUpper(strings.TrimPrefix(arg, "--market="))
			case strings.HasPrefix(arg, "--style="):
				screenStyle = strings.TrimPrefix(arg, "--style=")
			case strings.HasPrefix(arg, "--top="):
				fmt.Sscanf(strings.TrimPrefix(arg, "--top="), "%d", &screenTop)
			case arg == "--list-profiles" || arg == "-list-profiles":
				screenListProfiles = true
			case arg == "--verdict-only" || arg == "-verdict-only":
				screenVerdictOnly = true
			case arg == "--json" || arg == "-json":
				*jsonOut = true
			case arg == "--pretty" || arg == "-pretty":
				*pretty = true
			}
		}
		runScreen(screenMarket, screenStyle, *jsonOut, *pretty, screenVerdictOnly, screenTop, screenListProfiles, *timeout)
		return
	case "import-stock":
		if len(trailing) < 2 || strings.HasPrefix(trailing[1], "-") {
			fmt.Fprintln(os.Stderr, "usage: guanfu import-stock TICKER [DAYS]")
			os.Exit(1)
		}
		ticker := strings.ToUpper(trailing[1])
		days := 3650
		if len(trailing) >= 3 {
			fmt.Sscanf(trailing[2], "%d", &days)
		}
		runImportStock(ticker, days, *timeout)
		return
	case "market":
		runMarketOverview(*jsonOut, *pretty, *plain || *noEmoji)
		return
	case "dca":
		runDCA(*jsonOut, *pretty, *plain || *noEmoji)
		return
	case "allocate":
		runAllocate(*jsonOut, *pretty, *plain || *noEmoji)
		return
	case "backtest":
		if hasArg(trailing[1:], "--ablate-putcall", "-ablate-putcall") {
			runPutCallAblation(*jsonOut, *pretty, *plain || *noEmoji)
			return
		}
		if backtestAsset == "all" {
			runBacktestAll(*jsonOut, *pretty, *plain || *noEmoji)
		} else {
			runBacktest(backtestAsset, *jsonOut, *pretty, *plain || *noEmoji)
		}
		return
	case "status":
		frank := false
		for i := 1; i < len(trailing); i++ {
			if trailing[i] == "--frank" || trailing[i] == "-frank" {
				frank = true
			}
		}
		if frank {
			runStatusFrank(*jsonOut, *pretty, *plain || *noEmoji)
			return
		}
		runStatus(*jsonOut, *pretty, *plain || *noEmoji)
		return
	case "watch":
		runWatch(trailing[1:])
		return
	case "digest":
		runDigest(trailing[1:])
		return
	case "calibrate":
		runCalibrate(trailing[1:])
		return
	case "stress":
		runStress(trailing[1:])
		return
	case "joint":
		runJoint(trailing[1:])
		return
	case "refresh":
		// Refresh data flags can also appear after the subcommand, so re-scan
		// trailing args (flag.Parse stops at the first positional).
		for i := 1; i < len(trailing); i++ {
			arg := trailing[i]
			switch {
			case arg == "--dry-run" || arg == "-dry-run":
				*refreshDryRun = true
			case strings.HasPrefix(arg, "--only="):
				*refreshOnly = strings.TrimPrefix(arg, "--only=")
			case strings.HasPrefix(arg, "-only="):
				*refreshOnly = strings.TrimPrefix(arg, "-only=")
			case strings.HasPrefix(arg, "--skip="):
				*refreshSkip = strings.TrimPrefix(arg, "--skip=")
			case strings.HasPrefix(arg, "-skip="):
				*refreshSkip = strings.TrimPrefix(arg, "-skip=")
			}
		}
		runRefresh(*refreshOnly, *refreshSkip, *refreshDryRun, *jsonOut, *pretty, *timeout)
		return
	case "btc", "":
		// default: BTC panel (existing behavior)
	default:
		fmt.Fprintf(os.Stderr, "guanfu: unknown subcommand %q\n", subcmd)
		fmt.Fprintf(os.Stderr, "  available: btc, qqq, spy, gold, stock, screen, import-stock, market, dca, allocate, backtest [btc|gold|qqq|spy|all], status, refresh, watch, digest, calibrate, stress, joint\n")
		os.Exit(1)
	}

	if *showVersion {
		version.Print(os.Stdout, "guanfu")
		return
	}

	runBTCPanel(*jsonOut, *pretty, *verdict, *verdictOnly, *forecastOut && !*readingOnly, *forecastOnly && !*readingOnly, *forecastHorizons, *forecastTop, *timeout, *halfLife, *historyDB, *domainFilter, *plain || *noEmoji, *forecastPath, *full, *recencyWeighted, *regimeGate)
}

// runBTCPanel is the BTC panel flow. Default human output is --brief (10 lines);
// --full restores the pre-v3 40-row panel. --verdict / --forecast / --domain
// all implicitly opt into full mode (they need the detailed numbers).
func runBTCPanel(jsonOut, pretty, verdict, verdictOnly, forecastOut, forecastOnly bool, forecastHorizonsArg string, forecastTop int, timeout time.Duration, halfLife int, historyDB, domainFilter string, plain bool, forecastPath, full, recencyWeighted, regimeGate bool) {
	cfg := &model.Config{
		Weights: model.Weights{Trend: 0.30, Reversal: 0.25, Valuation: 0.25, Structure: 0.20},
		Thresholds: model.Thresholds{
			BTCMAFast:       120,
			BTCMASlow:       200,
			TopCoinCount:    50,
			AHRHalfLifeDays: halfLife,
		},
		API: model.APIConfig{Timeout: "10s", Retries: 3, Mock: false},
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	analysis, err := appService.BTCPanel(ctx, application.BTCPanelOptions{
		Config:         cfg,
		HistoryDB:      historyDB,
		DisableHistory: os.Getenv("GUANFU_NO_HISTORY") == "1",
		UsePriceStore:  true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "fetch snapshot failed: %v\n", err)
		os.Exit(1)
	}
	if analysis.HistoryError != nil {
		log.Printf("history.Open failed (continuing without history quantiles): %v", analysis.HistoryError)
	}
	snap := analysis.Snapshot
	panel := analysis.Panel

	// Brief mode implicitly needs a verdict to surface TOP3 supports /
	// TOP2 counter-evidence. --verdict / --verdict-only / --forecast-only
	// / --domain / --full all suppress brief auto-render.
	briefMode := !full && !verdict && !verdictOnly && !forecastOnly && !forecastOut && domainFilter == "" && !jsonOut && !pretty

	var v *engine.Verdict
	if verdict || verdictOnly || briefMode {
		v = engine.BuildVerdict(panel)
	}

	var fc *forecast.Forecast
	if forecastOut || forecastOnly {
		horizons, err := resolveHorizonsArg(forecastHorizonsArg, "btc")
		if err != nil {
			fmt.Fprintf(os.Stderr, "parse forecast horizons failed: %v\n", err)
			os.Exit(1)
		}
		opts := forecast.DefaultOptions()
		opts.Horizons = horizons
		opts.TopK = forecastTop
		opts.RecencyWeighted = recencyWeighted
		opts.RegimeGate = regimeGate
		fc, err = appService.BTCForecastFromSnapshot(snap, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "build forecast failed: %v\n", err)
			os.Exit(1)
		}
		annotateBaselines(fc)
		emitClaim(fc, "btc", panel)
	}

	// JSON output
	if jsonOut || pretty {
		var out interface{} = panel
		if domainFilter != "" && !verdict && !verdictOnly && !forecastOut && !forecastOnly {
			out = filterDomain(panel, domainFilter)
		}
		if forecastOnly {
			out = forecastOnlyOutput(fc, panel)
		} else if verdictOnly {
			out = v
		} else if verdict || forecastOut {
			out = struct {
				Panel    *model.IndicatorPanel `json:"panel"`
				Verdict  *engine.Verdict       `json:"verdict,omitempty"`
				Forecast *forecast.Forecast    `json:"forecast,omitempty"`
			}{Panel: panel, Verdict: v, Forecast: fc}
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

	// Human panel
	if briefMode {
		printHumanBrief(panel, v, plain)
		return
	}
	if !verdictOnly && !forecastOnly {
		printHumanPanel(panel, domainFilter, plain)
	}
	if v != nil && !forecastOnly {
		printHumanVerdict(v, plain)
	}
	if fc != nil {
		printHumanForecast(fc, plain, panel)
	}
	if forecastPath && fc != nil {
		fmt.Println(forecast.BuildPathProjection(fc, 180).ASCIIFan(70))
	}
}

func hasArg(args []string, names ...string) bool {
	for _, arg := range args {
		for _, name := range names {
			if arg == name {
				return true
			}
		}
	}
	return false
}

// readStock reads an arbitrary US stock by ticker (D3 + A6).
//
// Auto-fetches via Yahoo (D1) when PriceStore has no cached data
// or the cache is stale (>30h). Builds a neutral market panel via
// BuildMarketPanel (A6: technical/macro indicators) and runs the
// kNN forecast with USStockExtractors (D2).
func readStock(ticker string, jsonOut, pretty, forecastOut, forecastOnly bool, forecastHorizonsArg string, forecastTop int, timeout time.Duration, plain bool) {
	s := &store.PriceStore{}
	if err := client.ValidateStockTicker(s, ticker); err != nil {
		fmt.Fprintf(os.Stderr, "stock %s: %v\n", ticker, err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	raw, err := client.FetchAndCacheStock(ctx, s, ticker, 3650)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stock %s: fetch failed: %v\n", ticker, err)
		os.Exit(1)
	}
	if len(raw) < 200 {
		fmt.Fprintf(os.Stderr, "stock %s: only %d data points (need ≥200)\n", ticker, len(raw))
		os.Exit(1)
	}

	points := make([]forecast.Point, len(raw))
	for i, p := range raw {
		points[i] = forecast.Point{Date: p.Date, Close: p.Close, Source: p.Source}
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Date < points[j].Date })

	// PriceHistory for BuildMarketPanel (newest-first, []float64).
	history := make([]float64, len(points))
	for i := range points {
		history[i] = points[len(points)-1-i].Close
	}
	latest := points[len(points)-1]

	stockKey := client.StockKey(ticker)
	panel := engine.BuildMarketPanel(&engine.MarketPanelInput{
		Asset:        stockKey,
		Date:         latest.Date,
		Price:        latest.Close,
		PriceAsOf:    latest.Date,
		PriceHistory: history,
	})
	engine.EnrichGlobalInvestorMacro(panel, s)

	horizons, err := resolveHorizonsArg(forecastHorizonsArg, stockKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse horizons: %v\n", err)
		os.Exit(1)
	}
	opts := forecast.DefaultOptions()
	opts.Horizons = horizons
	opts.TopK = forecastTop
	opts.Asset = stockKey
	opts.Extractors = features.ExtractorsForAsset(stockKey, s)

	fc, err := forecast.Build(points, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forecast failed for %s: %v\n", ticker, err)
		os.Exit(1)
	}
	annotateBaselines(fc)
	emitClaim(fc, client.StockKey(ticker), panel)

	if jsonOut || pretty {
		out := struct {
			Panel    *model.IndicatorPanel `json:"panel"`
			Forecast *forecast.Forecast    `json:"forecast,omitempty"`
		}{Panel: panel, Forecast: fc}
		var b []byte
		if pretty {
			b, _ = json.MarshalIndent(out, "", "  ")
		} else {
			b, _ = json.Marshal(out)
		}
		fmt.Println(string(b))
		return
	}

	upper := strings.ToUpper(ticker)
	if !plain {
		fmt.Printf("观复 · %s  (%s)   价格: $%.2f\n\n", upper, latest.Date, latest.Close)
	} else {
		fmt.Printf("guanfu stock %s (%s)   price: $%.2f\n\n", upper, latest.Date, latest.Close)
	}

	if !forecastOnly {
		printDomainTable(panel.Technical)
		fmt.Println()
		if len(panel.Macro) > 0 {
			fmt.Println("🌍 Macro 宏观")
			printDomainTable(panel.Macro)
			fmt.Println()
		}
	}

	if forecastOut || forecastOnly {
		printHumanForecast(fc, plain, panel)
	}
}

// runImportStock implements the import-stock subcommand (D4):
// triggers a Yahoo full-window fetch and persists to PriceStore.
func runImportStock(ticker string, days int, timeout time.Duration) {
	s := &store.PriceStore{}
	if err := client.ValidateStockTicker(s, ticker); err != nil {
		fmt.Fprintf(os.Stderr, "import-stock %s: %v\n", ticker, err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	points, err := client.FetchAndCacheStock(ctx, s, ticker, days)
	if err != nil {
		fmt.Fprintf(os.Stderr, "import-stock %s: %v\n", ticker, err)
		os.Exit(1)
	}
	first, last := points[0].Date, points[len(points)-1].Date
	fmt.Printf("✓ %s: %d days (%s → %s) saved to %s\n",
		strings.ToUpper(ticker), len(points), first, last, client.StockKey(ticker))
}

// runEquityAsset runs the equity flow for QQQ/SPY through the Asset interface.
func runEquityAsset(assetKey string, jsonOut, pretty, verdict, verdictOnly, forecastOut, forecastOnly bool, forecastHorizonsArg string, forecastTop int, timeout time.Duration, plain, full bool, domainFilter string, recencyWeighted, regimeGate bool) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	analysis, err := appService.Panel(ctx, assetKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	panel := analysis.Panel

	briefMode := !full && !verdict && !verdictOnly && !forecastOnly && !forecastOut && domainFilter == "" && !jsonOut && !pretty

	var v *engine.Verdict
	if verdict || verdictOnly || briefMode {
		v, err = appService.Verdict(assetKey, panel)
		if err != nil {
			fmt.Fprintf(os.Stderr, "build verdict failed: %v\n", err)
			os.Exit(1)
		}
	}

	var fc *forecast.Forecast
	if forecastOut || forecastOnly {
		horizons, err := resolveHorizonsArg(forecastHorizonsArg, assetKey)
		if err != nil {
			fmt.Fprintf(os.Stderr, "parse forecast horizons failed: %v\n", err)
			os.Exit(1)
		}
		opts := forecast.DefaultOptions()
		opts.Horizons = horizons
		opts.TopK = forecastTop
		opts.RecencyWeighted = recencyWeighted
		opts.RegimeGate = regimeGate
		fc, err = appService.ForecastFromSnapshot(assetKey, analysis.Snapshot, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "build forecast failed: %v\n", err)
			os.Exit(1)
		}
		annotateBaselines(fc)
		emitClaim(fc, assetKey, panel)
	}

	// JSON output
	if jsonOut || pretty {
		var out interface{} = panel
		if forecastOnly {
			out = forecastOnlyOutput(fc, panel)
		} else if verdictOnly {
			out = v
		} else if verdict || forecastOut {
			out = struct {
				Panel    *model.IndicatorPanel `json:"panel"`
				Verdict  *engine.Verdict       `json:"verdict,omitempty"`
				Forecast *forecast.Forecast    `json:"forecast,omitempty"`
			}{Panel: panel, Verdict: v, Forecast: fc}
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

	// Human panel
	if briefMode {
		printHumanBrief(panel, v, plain)
		return
	}
	if !verdictOnly && !forecastOnly {
		printEquityPanel(panel, plain)
	}
	if v != nil && !forecastOnly {
		printHumanVerdict(v, plain)
	}
	if fc != nil {
		printHumanForecast(fc, plain, panel)
	}
}

// printEquityPanel prints a human-readable equity panel.
func printEquityPanel(p *model.IndicatorPanel, plain bool) {
	price, plainTitle, fancyTitle := equityPanelHeader(p)
	if plain {
		fmt.Printf("guanfu %s (%s)   price: $%.2f\n", plainTitle, p.Date, price)
	} else {
		fmt.Printf("观复 · %s (%s)   价格: $%.2f\n", fancyTitle, p.Date, price)
	}
	fmt.Println()

	domains := p.DomainMeta
	if len(domains) == 0 {
		domains = []model.PanelDomainMeta{
			{Key: "technical", Title: "Technical 技术指标", Icon: "📈"},
			{Key: "macro", Title: "Macro 宏观", Icon: "🌍"},
			{Key: "positioning", Title: "Sentiment 情绪", Icon: "📊"},
			{Key: "valuation", Title: "Valuation 估值", Icon: "💰"},
		}
	}

	for _, d := range domains {
		indicators := panelDomainIndicators(p, d.Key)
		if len(indicators) == 0 {
			continue
		}
		if plain {
			fmt.Println(d.Title)
		} else {
			fmt.Printf("%s %s\n", d.Icon, d.Title)
		}
		printDomainTable(indicators)
		fmt.Println()
	}
}

func panelDomainIndicators(p *model.IndicatorPanel, key string) map[string]model.Indicator {
	switch key {
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
	default:
		return nil
	}
}

// equityPanelHeader picks the right Snapshot price field and title for an asset.
func equityPanelHeader(p *model.IndicatorPanel) (price float64, plainTitle, fancyTitle string) {
	switch p.Asset {
	case "qqq":
		return p.Snapshot.QQQPrice, "QQQ panel", "QQQ 盘面"
	case "spy":
		return p.Snapshot.SPYPrice, "SPY panel", "SPY 盘面"
	case "gold":
		return p.Snapshot.GoldPrice, "gold panel", "黄金盘面"
	default:
		// Legacy fallback: pre-A0 panels lacked Asset; prefer SPY+QQQ sum (one of them is 0).
		return p.Snapshot.SPYPrice + p.Snapshot.QQQPrice, "equity panel", "权益 ETF 盘面"
	}
}

// printHumanBrief is the default terse output — a 10-line summary designed
// to be the first thing a user sees and often the only thing they need:
//   - asset / price / date line
//   - verdict stance + net direction
//   - TOP 3 supports, TOP 2 counter-evidence (from Verdict)
//   - most concerning source_health issue
//
// Use --full to unlock the 40+ indicator panel. --verdict/--forecast/--domain
// implicitly opt into full mode (they already carry detail).
func printHumanBrief(p *model.IndicatorPanel, v *engine.Verdict, plain bool) {
	asset := strings.ToUpper(p.Asset)
	if asset == "" {
		asset = "BTC"
	}
	price := 0.0
	switch p.Asset {
	case "qqq":
		price = p.Snapshot.QQQPrice
	case "spy":
		price = p.Snapshot.SPYPrice
	case "gold":
		price = p.Snapshot.GoldPrice
	default:
		price = p.Snapshot.BTCPrice
	}
	if plain {
		fmt.Printf("guanfu %s brief (%s)   price: $%.2f\n", asset, p.Date, price)
	} else {
		fmt.Printf("观复 · %s 摘要 (%s)   价格: $%.2f\n", asset, p.Date, price)
	}
	if v == nil {
		fmt.Println()
		fmt.Println("  (无 verdict 数据,建议 --full 查看完整指标盘)")
		return
	}
	fmt.Printf("  读盘      : %s  (净方向 %+d / 8,覆盖率 %.0f%%,置信度 %s)\n",
		v.Stance, v.NetDirection, v.Coverage*100, v.Confidence)
	if len(v.Reasons) > 0 {
		fmt.Println("  支持      :")
		for _, r := range v.Reasons {
			fmt.Printf("    + %s\n", r)
		}
	}
	if len(v.CounterEvidence) > 0 {
		fmt.Println("  反证      :")
		for _, c := range v.CounterEvidence {
			fmt.Printf("    - %s\n", c)
		}
	}
	if len(v.KillCriteria) > 0 {
		fmt.Printf("  失效条件  : %s\n", v.KillCriteria[0])
	}
	// Most concerning source_health (first non-ok)
	for _, h := range p.SourceHealth {
		if h.Status != "" && h.Status != "ok" {
			line := fmt.Sprintf("  数据源    : %s=%s", h.Source, h.Status)
			if h.Note != "" {
				line += " — " + h.Note
			}
			fmt.Println(line)
			break
		}
	}
	fmt.Println("  (完整盘面:guanfu --full;详细读盘:--verdict;走势推演:--forecast)")
}

func printHumanVerdict(v *engine.Verdict, plain bool) {
	bar := "═══════════════════════════════════════════════════════════"
	if plain {
		bar = "==========================================================="
	}
	fmt.Println()
	fmt.Println(bar)
	if plain {
		fmt.Printf("VERDICT  %s\n", v.Date)
	} else {
		fmt.Printf("⚖  读盘结论  %s\n", v.Date)
	}
	fmt.Println(bar)
	fmt.Printf("  Stance       : %s\n", v.Stance)
	fmt.Printf("  Regime       : %s\n", v.Regime)
	fmt.Printf("  净方向       : %+d / 8\n", v.NetDirection)
	fmt.Printf("  覆盖率       : %.0f%%   置信度：%s\n", v.Coverage*100, v.Confidence)
	fmt.Printf("  顶部接近度   : %.0f%%\n", v.TopProximity*100)
	fmt.Printf("  底部接近度   : %.0f%%\n", v.BottomProximity*100)
	fmt.Println()
	fmt.Println("  域级一致性：")
	for _, d := range v.Domains {
		fmt.Printf("    %-12s %+d  bull=%d bear=%d skip=%d  cov=%.0f%%\n",
			d.Domain, d.Vote, len(d.Bullish), len(d.Bearish), len(d.Skipped), d.Coverage*100)
	}
	if len(v.ClusterNotes) > 0 {
		fmt.Println()
		fmt.Println("  簇级去重：")
		for _, n := range v.ClusterNotes {
			fmt.Printf("    · %s\n", n)
		}
	}
	if len(v.Reasons) > 0 {
		fmt.Println()
		fmt.Println("  支持当前结论 TOP 3：")
		for _, r := range v.Reasons {
			fmt.Printf("    + %s\n", r)
		}
	}
	if len(v.CounterEvidence) > 0 {
		fmt.Println()
		fmt.Println("  反证 TOP 2：")
		for _, c := range v.CounterEvidence {
			fmt.Printf("    - %s\n", c)
		}
	}
	if len(v.KillCriteria) > 0 {
		fmt.Println()
		fmt.Println("  失效条件：")
		for _, k := range v.KillCriteria {
			fmt.Printf("    ⚠ %s\n", k)
		}
	}
	if v.MissingNote != "" {
		fmt.Println()
		fmt.Printf("  数据缺失提示：%s\n", v.MissingNote)
	}
	fmt.Println(bar)
	fmt.Println()
}

func forecastOnlyOutput(fc *forecast.Forecast, panel *model.IndicatorPanel) any {
	return struct {
		*forecast.Forecast
		SourceHealth []model.SourceHealth `json:"source_health,omitempty"`
	}{
		Forecast:     fc,
		SourceHealth: forecastRelevantSourceHealth(panel),
	}
}

func forecastRelevantSourceHealth(panel *model.IndicatorPanel) []model.SourceHealth {
	if panel == nil {
		return nil
	}
	out := make([]model.SourceHealth, 0, len(panel.SourceHealth))
	for _, h := range panel.SourceHealth {
		if h.Impact == "forecast" || h.Impact == "both" || h.Status != "ok" {
			out = append(out, h)
		}
	}
	return out
}

func printForecastSourceHealth(panel *model.IndicatorPanel) {
	items := forecastRelevantSourceHealth(panel)
	if len(items) == 0 {
		return
	}
	fmt.Println("  Source health:")
	limit := len(items)
	if limit > 8 {
		limit = 8
	}
	for i := 0; i < limit; i++ {
		h := items[i]
		line := fmt.Sprintf("    - %s=%s", h.Source, h.Status)
		if h.Impact != "" {
			line += " impact=" + h.Impact
		}
		if h.AsOf != "" {
			line += " as_of=" + h.AsOf
		}
		if h.FallbackUsed {
			line += " fallback=true"
		}
		if h.Note != "" && h.Status != "ok" {
			line += " — " + h.Note
		}
		fmt.Println(line)
	}
}

func printHumanForecast(fc *forecast.Forecast, plain bool, panels ...*model.IndicatorPanel) {
	bar := "═══════════════════════════════════════════════════════════"
	title := "走势推演"
	if plain {
		bar = "==========================================================="
		title = "FORECAST"
	}
	fmt.Println()
	fmt.Println(bar)
	if plain {
		fmt.Printf("%s  %s\n", title, fc.Date)
	} else {
		fmt.Printf("🔮  %s  %s\n", title, fc.Date)
	}
	fmt.Println(bar)
	fmt.Printf("  Method       : %s\n", fc.Method)
	fmt.Printf("  Price        : $%.2f\n", fc.CurrentPrice)
	fmt.Printf("  Coverage     : %.0f%% (%d/%d features), analogs=%d/%d, similarity=%.1f%%, confidence=%s\n",
		fc.Coverage.FeatureCoverage*100,
		fc.Coverage.FeatureCount,
		fc.Coverage.ExpectedFeatures,
		fc.Coverage.SelectedAnalogs,
		fc.Coverage.CandidateCount,
		fc.Coverage.AverageSimilarity,
		fc.Coverage.Confidence)
	if len(panels) > 0 {
		printForecastSourceHealth(panels[0])
	}
	fmt.Println()
	fmt.Println("  Horizon scenarios:")
	for _, h := range fc.Horizons {
		if h.HardBlocked {
			fmt.Printf("    %3dd  信号强度低于随机阈值，不显示数值预测\n", h.Days)
			if h.ReliabilityNote != "" {
				fmt.Printf("          %s\n", h.ReliabilityNote)
			}
			continue
		}
		fmt.Printf("    %3dd  %-12s  up=%.0f%% range=%.0f%% down=%.0f%%  median=%+.2f%%  p10/p90=%+.2f%%/%+.2f%%  median_price=$%.0f\n",
			h.Days,
			h.DominantLabel,
			h.ProbabilityUpsideContinuation*100,
			h.ProbabilityRange*100,
			h.ProbabilityDownsidePressure*100,
			h.MedianReturnPct,
			h.P10ReturnPct,
			h.P90ReturnPct,
			h.MedianPrice)
		if h.RiskFreeReturnPct != 0 || h.PassiveReturnPct != 0 {
			fmt.Printf("          vs T-bill %.2f%% / 60-40 %.2f%%  → 风险调整差 %+.2f%% (%s)\n",
				h.RiskFreeReturnPct, h.PassiveReturnPct, h.RiskAdjustedDeltaPct, h.BaselineNote)
		}
		if h.ConformalAlpha > 0 {
			fmt.Printf("          conformal %.0f%% 区间: [%+.2f%%, %+.2f%%]  (achieved %.0f%%)\n",
				(1-h.ConformalAlpha)*100,
				h.ConformalLowPct, h.ConformalHighPct,
				h.ConformalCoverage*100)
		}
		if h.EnsembleLinearPct != 0 || h.EnsembleDisagreementPct != 0 {
			disagreement := h.EnsembleDisagreementPct
			if disagreement < 0 {
				disagreement = -disagreement
			}
			alignment := "一致"
			if disagreement > 5 {
				alignment = "⚠ 分歧较大,forecast 置信度下降"
			}
			fmt.Printf("          ensemble 线性: %+.2f%%  (vs kNN median 差异 %+.2f%% — %s)\n",
				h.EnsembleLinearPct, h.EnsembleDisagreementPct, alignment)
		}
		if h.ReliabilityNote != "" {
			fmt.Printf("          %s\n", h.ReliabilityNote)
		}
	}
	if len(fc.Analogs) > 0 {
		fmt.Println()
		fmt.Println("  Closest analogues:")
		limit := len(fc.Analogs)
		if limit > 8 {
			limit = 8
		}
		for i := 0; i < limit; i++ {
			a := fc.Analogs[i]
			fmt.Printf("    %2d. %s  similarity=%.1f%%  price=$%.0f\n", i+1, a.Date, a.Similarity, a.Price)
		}
	}
	if len(fc.Caveats) > 0 {
		fmt.Println()
		fmt.Println("  Caveats:")
		for _, c := range fc.Caveats {
			fmt.Printf("    - %s\n", c)
		}
	}
	fmt.Println(bar)
	fmt.Println()
}

func printHumanPanel(p *model.IndicatorPanel, filter string, plain bool) {
	if plain {
		fmt.Printf("guanfu BTC panel (%s)   price: $%.2f\n", p.Date, p.Snapshot.BTCPrice)
		fmt.Printf("BTC dominance: %.2f%%   F&G: %.0f   total cap: $%.1fT\n",
			p.Snapshot.BTCDominance*100, p.Snapshot.FearGreed,
			p.Snapshot.TotalMarketCap/1e12)
	} else {
		fmt.Printf("观复 · BTC 盘面 (%s)   价格: $%.2f\n", p.Date, p.Snapshot.BTCPrice)
		fmt.Printf("├─ BTC dominance: %.2f%%   F&G: %.0f   总市值: $%.1fT\n",
			p.Snapshot.BTCDominance*100, p.Snapshot.FearGreed,
			p.Snapshot.TotalMarketCap/1e12)
	}
	fmt.Println()

	for _, d := range domainNames {
		if filter != "" && filter != d.Key {
			continue
		}
		var indicators map[string]model.Indicator
		switch d.Key {
		case "cycle":
			indicators = p.Cycle
		case "valuation":
			indicators = p.Valuation
		case "network":
			indicators = p.Network
		case "positioning":
			indicators = p.Positioning
		case "macro":
			indicators = p.Macro
		case "flow":
			indicators = p.Flow
		case "technical":
			indicators = p.Technical
		case "cross_asset":
			indicators = p.CrossAsset
		}
		if len(indicators) == 0 {
			continue
		}

		if plain {
			fmt.Println(d.Title)
		} else {
			fmt.Printf("%s %s\n", d.Icon, d.Title)
		}
		printDomainTable(indicators)
		fmt.Println()
	}

	// 数据时效性 / 待接入提示
	stale := collectStale(p)
	stale = append(stale, p.StaleWarnings...)
	stale = dedupeStrings(stale)
	if len(stale) > 0 {
		if plain {
			fmt.Println("Data tips:")
		} else {
			fmt.Println("⚠ 数据提示:")
		}
		for _, s := range stale {
			fmt.Printf("  - %s\n", s)
		}
	}

	sourceIssues := collectSourceHealthIssues(p)
	if len(sourceIssues) > 0 {
		if plain {
			fmt.Println("Source health:")
		} else {
			fmt.Println("数据源状态:")
		}
		for _, h := range sourceIssues {
			line := fmt.Sprintf("%s=%s", h.Source, h.Status)
			if h.AsOf != "" {
				line += fmt.Sprintf(" as_of=%s", h.AsOf)
			}
			if h.FallbackUsed {
				line += " fallback=true"
			}
			if h.Impact != "" {
				line += fmt.Sprintf(" impact=%s", h.Impact)
			}
			if h.Note != "" {
				line += fmt.Sprintf(" — %s", h.Note)
			}
			fmt.Printf("  - %s\n", line)
		}
	}
}

// printDomainTable 输出单个 domain 的指标表
func printDomainTable(indicators map[string]model.Indicator) {
	// 按 key 字典序输出（稳定显示）
	keys := make([]string, 0, len(indicators))
	for k := range indicators {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		ind := indicators[k]
		// 跳过完全 placeholder（无 value 也无 label）— 已在 stale 提示
		if ind.Value == 0 && ind.Label == "" {
			continue
		}
		// label-only 指标（如 cycle phase）单独显示
		if ind.Value == 0 && ind.Label != "" && ind.Quantile == 0 {
			fmt.Printf("  %-26s %s\n", k, ind.Label)
			continue
		}
		valStr := formatValue(k, ind.Value)
		qStr := ""
		if ind.Quantile > 0 {
			qStr = fmt.Sprintf("  q%02.0f", ind.Quantile*100)
		}
		labelStr := ""
		if ind.Label != "" {
			labelStr = fmt.Sprintf("  %s", ind.Label)
		}
		fmt.Printf("  %-26s %s%s%s\n", k, valStr, qStr, labelStr)
	}
}

// formatValue 按指标类型格式化数值
func formatValue(key string, v float64) string {
	switch {
	case key == "oil_proxy_usd" || key == "wti_crude_usd":
		return fmt.Sprintf("$%7.2f", v)
	case strings.Contains(key, "_pct") || strings.Contains(key, "_yoy") || strings.Contains(key, "_return_"):
		return fmt.Sprintf("%+7.2f%%", v)
	case strings.Contains(key, "days_"):
		return fmt.Sprintf("%7.0f", v)
	case key == "cny_usd" || key == "usd_cny":
		return fmt.Sprintf("%7.4f", v)
	case strings.Contains(key, "_cny"):
		return fmt.Sprintf("¥%.2f", v)
	case strings.Contains(key, "_usd"):
		return fmt.Sprintf("$%.2fM", v/1e6)
	case strings.Contains(key, "ratio") || strings.Contains(key, "multiple") || strings.Contains(key, "ahr") || strings.Contains(key, "nupl") || strings.Contains(key, "skew"):
		return fmt.Sprintf("%7.4f", v)
	case strings.Contains(key, "sma") && strings.Contains(key, "_dev"):
		return fmt.Sprintf("%+7.2f%%", v)
	case strings.Contains(key, "sma"):
		return fmt.Sprintf("$%.0f", v)
	default:
		// large-magnitude check
		if v >= 100 {
			return fmt.Sprintf("%7.2f", v)
		}
		return fmt.Sprintf("%7.4f", v)
	}
}

// collectStale 汇总所有 placeholder 指标的 note
func collectStale(p *model.IndicatorPanel) []string {
	var out []string
	scan := func(domain string, m map[string]model.Indicator) {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			ind := m[k]
			if ind.Value == 0 && ind.Label == "" && strings.Contains(ind.Source, "待接入") {
				out = append(out, fmt.Sprintf("[%s] %s — %s", domain, k, ind.Note))
			}
		}
	}
	scan("cycle", p.Cycle)
	scan("valuation", p.Valuation)
	scan("network", p.Network)
	scan("positioning", p.Positioning)
	scan("macro", p.Macro)
	scan("flow", p.Flow)
	scan("technical", p.Technical)
	scan("cross_asset", p.CrossAsset)
	return out
}

func collectSourceHealthIssues(p *model.IndicatorPanel) []model.SourceHealth {
	out := make([]model.SourceHealth, 0, len(p.SourceHealth))
	for _, h := range p.SourceHealth {
		if h.Status != "" && h.Status != "ok" {
			out = append(out, h)
		}
	}
	return out
}

// filterDomain 仅保留指定 domain
func filterDomain(p *model.IndicatorPanel, name string) *model.IndicatorPanel {
	out := &model.IndicatorPanel{
		Asset:           p.Asset,
		ProfileKey:      p.ProfileKey,
		ProfileVersion:  p.ProfileVersion,
		AssetClass:      p.AssetClass,
		SkillProfileURI: p.SkillProfileURI,
		DomainMeta:      append([]model.PanelDomainMeta(nil), p.DomainMeta...),
		Date:            p.Date,
		Snapshot:        p.Snapshot,
		StaleWarnings:   append([]string(nil), p.StaleWarnings...),
		SourceHealth:    append([]model.SourceHealth(nil), p.SourceHealth...),
	}
	switch name {
	case "cycle":
		out.Cycle = p.Cycle
	case "valuation":
		out.Valuation = p.Valuation
	case "network":
		out.Network = p.Network
	case "positioning":
		out.Positioning = p.Positioning
	case "macro":
		out.Macro = p.Macro
	case "flow":
		out.Flow = p.Flow
	case "technical":
		out.Technical = p.Technical
	case "cross_asset":
		out.CrossAsset = p.CrossAsset
	}
	return out
}

func dedupeStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
