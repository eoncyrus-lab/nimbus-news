// guanfu-mcp — 观复 MCP Server
//
// 通过 stdio 提供 MCP 协议接口，让 Claude Desktop / Cursor / 任何 MCP 客户端
// 直接调用 guanfu 引擎获取多资产盘面数据，无需 Bash 权限。
//
// 提供的 Tools (传 asset=btc/qqq/spy/gold 切资产):
//   get_panel / get_btc_panel          — 完整域盘面 (JSON)
//   get_verdict / get_btc_verdict      — 结构化多域读盘 (JSON)
//   get_forecast / get_btc_forecast    — 历史相似盘面走势推演 (JSON)
//   get_domain                         — 单个域 (cycle/valuation/network/...)
//   get_indicator                      — 单个指标 (ahr999, rsi_14, vix_level, ...)
//
// 旧的 get_btc_* 名字向后兼容保留 (alias 同 handler)；新代码用 get_*。
//
// 提供的 Resources:
//   guanfu://knowledge/skill.md        — SKILL.md 知识库
//   guanfu://skill/tier1               — 必载数据契约
//   guanfu://skill/profiles/{profile}  — 资产 profile 读盘契约
//   guanfu://panel/latest/{asset}      — 缓存的最新盘面
//   guanfu://verdict/latest/{asset}    — 缓存盘面的结构化读盘
//   guanfu://forecast/latest/{asset}   — 缓存盘面的走势推演
//
// 部署: 在 claude_desktop_config.json 中添加:
//
//	{
//	  "mcpServers": {
//	    "guanfu": {
//	      "command": "/path/to/guanfu-mcp",
//	      "env": {
//	        "GUANFU_HISTORY_DB": "/path/to/history.db",
//	        "GUANFU_SKILL_PATH": "/path/to/skill/SKILL.md",
//	        "FUTU_GATEWAY": "127.0.0.1:11111"
//	      }
//	    }
//	  }
//	}

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/application"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/claim"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/client"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/engine"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/version"
)

var appService = application.NewDefault()

// ─── JSON-RPC types ───────────────────────────────────

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ─── MCP tool definitions ─────────────────────────────

type toolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

var tools = json.RawMessage(`
[
  {
    "name": "get_panel",
    "description": "获取完整指标盘面（多资产，传 asset 切资产）。BTC 走 8 域 40+ 指标；股票/ETF/黄金走 6 域。每个指标含原始值、历史分位(q)、解读标签、数据源。",
    "inputSchema": {
      "type": "object",
      "properties": {
        "asset": {"type": "string", "description": "资产标识: btc(默认) / qqq / spy / gold", "enum": ["btc","qqq","spy","gold"]},
        "timeout_seconds": {"type": "integer", "description": "拉数据超时秒数，默认 90"}
      }
    }
  },
  {
    "name": "get_verdict",
    "description": "获取结构化多域读盘（多资产，传 asset 切资产）。基于盘面输出域级一致性、覆盖率、风险状态、顶/底接近度、证据和失效条件；不输出交易指令或仓位指令。",
    "inputSchema": {
      "type": "object",
      "properties": {
        "asset": {"type": "string", "description": "资产标识: btc(默认) / qqq / spy / gold", "enum": ["btc","qqq","spy","gold"]},
        "timeout_seconds": {"type": "integer", "description": "拉数据超时秒数，默认 90"},
        "include_panel": {"type": "boolean", "description": "是否同时返回原始指标盘面，默认 false"}
      }
    }
  },
  {
    "name": "get_forecast",
    "description": "获取历史相似盘面走势推演（多资产，传 asset 切资产）。输出 30/90/180 天等周期的前向收益分布、情景概率、相似样本和覆盖率；不是确定性价格预测，也不输出交易指令。",
    "inputSchema": {
      "type": "object",
      "properties": {
        "asset": {"type": "string", "description": "资产标识: btc(默认) / qqq / spy / gold", "enum": ["btc","qqq","spy","gold"]},
        "timeout_seconds": {"type": "integer", "description": "拉数据超时秒数，默认 90"},
        "horizons": {"type": "array", "items": {"type": "integer"}, "description": "推演周期天数；省略时使用资产专属默认（QQQ/SPY 30/63/90/180/252，Gold 30/60/90/120，BTC 30/90/180）"},
        "top_k": {"type": "integer", "description": "使用的历史相似样本数，默认 21"},
        "include_panel": {"type": "boolean", "description": "是否同时返回原始指标盘面，默认 false"}
      }
    }
  },
  {
    "name": "get_btc_panel",
    "description": "[Deprecated alias of get_panel] 旧版 BTC 专用名字，向后兼容保留。新代码请用 get_panel。",
    "inputSchema": {"type": "object", "properties": {"asset": {"type": "string"}, "timeout_seconds": {"type": "integer"}}}
  },
  {
    "name": "get_btc_verdict",
    "description": "[Deprecated alias of get_verdict] 旧版 BTC 专用名字，向后兼容保留。新代码请用 get_verdict。",
    "inputSchema": {"type": "object", "properties": {"asset": {"type": "string"}, "timeout_seconds": {"type": "integer"}, "include_panel": {"type": "boolean"}}}
  },
  {
    "name": "get_btc_forecast",
    "description": "[Deprecated alias of get_forecast] 旧版 BTC 专用名字，向后兼容保留。新代码请用 get_forecast。",
    "inputSchema": {"type": "object", "properties": {"asset": {"type": "string"}, "timeout_seconds": {"type": "integer"}, "horizons": {"type": "array", "items": {"type": "integer"}}, "top_k": {"type": "integer"}, "include_panel": {"type": "boolean"}}}
  },
  {
    "name": "get_stock_forecast",
    "description": "对任意美股 ticker 跑 kNN 走势推演。自动从 Yahoo 拉日频数据缓存到 PriceStore，使用 USStockExtractors（无 CAPE）做特征。Ticker 不能与核心资产 (btc/qqq/spy/gold) 或 feature data key (vixy/fred_*/...) 撞名。",
    "inputSchema": {
      "type": "object",
      "properties": {
        "ticker": {"type": "string", "description": "美股代码，如 AAPL / MSFT / NVDA"},
        "days": {"type": "integer", "description": "拉历史天数，默认 3650 (~10y)；Yahoo 上限决定上界"},
        "horizons": {"type": "array", "items": {"type": "integer"}, "description": "推演周期天数，默认 [30,90,180]（任意 ticker 走通用默认）"},
        "top_k": {"type": "integer", "description": "使用的历史相似样本数，默认 21，最小 5"},
        "timeout_seconds": {"type": "integer", "description": "拉数据超时秒数，默认 90"}
      },
      "required": ["ticker"]
    }
  },
  {
    "name": "get_domain",
    "description": "获取单个域的指标。domain 可选: cycle, valuation, network, positioning, macro, flow, technical, cross_asset。",
    "inputSchema": {
      "type": "object",
      "properties": {
        "asset": {"type": "string", "description": "资产标识: btc, qqq, spy, gold"},
        "domain": {"type": "string", "enum": ["cycle","valuation","network","positioning","macro","flow","technical","cross_asset"]},
        "timeout_seconds": {"type": "integer", "description": "拉数据超时秒数，默认 90"}
      },
      "required": ["domain"]
    }
  },
  {
    "name": "get_indicator",
    "description": "获取单个指标的值。name 可选: ahr999, mayer_multiple, mvrv_z_score, hash_ribbons, funding_rate_pct, fear_greed, etf_net_flow_30d_usd, rsi_14, btc_gold_ratio, 等。",
    "inputSchema": {
      "type": "object",
      "properties": {
        "asset": {"type": "string", "description": "资产标识: btc, qqq, spy, gold"},
        "name": {"type": "string", "description": "指标 key 名称，如 ahr999, hash_ribbons, fear_greed"},
        "timeout_seconds": {"type": "integer", "description": "拉数据超时秒数，默认 90"}
      },
      "required": ["name"]
    }
  },
  {
    "name": "screen_candidates",
    "description": "价值投资选股：对指定市场+策略运行粗筛→精筛，返回 Top N 候选标的和打分。市场: HK/US/A。策略: value/growth/dividend/potential。需要 Longbridge MCP server 在线。",
    "inputSchema": {
      "type": "object",
      "properties": {
        "market": {"type": "string", "description": "市场: HK, US, A", "enum": ["HK","US","A"]},
        "style": {"type": "string", "description": "策略风格: value/growth/dividend/turnaround/small_value/quality/balanced/potential", "enum": ["value","growth","dividend","turnaround","small_value","quality","balanced","potential"]},
        "top_n": {"type": "integer", "description": "返回前 N 只,默认 10"},
        "timeout_seconds": {"type": "integer", "description": "超时秒数,默认 90"}
      },
      "required": ["market"]
    }
  },
  {
    "name": "screen_verdict",
    "description": "获取选股候选池的整体读盘结论（估值水位/质量分布/风险统计/行业分布），不输出单一评分或交易指令。",
    "inputSchema": {
      "type": "object",
      "properties": {
        "market": {"type": "string", "description": "市场: HK, US, A", "enum": ["HK","US","A"]},
        "style": {"type": "string", "description": "策略风格", "enum": ["value","growth","dividend","turnaround","small_value","quality","balanced","potential"]},
        "timeout_seconds": {"type": "integer", "description": "超时秒数,默认 90"}
      },
      "required": ["market"]
    }
  },
  {
    "name": "screen_profiles",
    "description": "列出所有可用的选股策略 profile（市场、风格、粗筛策略、权重、描述）。",
    "inputSchema": {
      "type": "object",
      "properties": {
        "market": {"type": "string", "description": "按市场过滤 (HK/US/A),省略则返回全部"}
      }
    }
  }
]`)

// ─── Main ──────────────────────────────────────────────

var (
	panelCache    *model.IndicatorPanel
	panelCacheMu  sync.RWMutex
	panelCacheTTL = 5 * time.Minute
	panelCacheAt  time.Time
)

const defaultPanelTimeout = 90 * time.Second

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	cacheTTL := flag.Duration("cache-ttl", panelCacheTTL, "in-memory panel cache TTL")
	flag.Parse()
	if *showVersion {
		version.Print(os.Stdout, "guanfu-mcp")
		return
	}
	if *cacheTTL > 0 {
		panelCacheTTL = *cacheTTL
	}

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	writer := os.Stdout

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		resp := handleRequest(&req)
		if resp == nil {
			continue
		}
		data, _ := json.Marshal(resp)
		fmt.Fprintln(writer, string(data))
	}
}

func handleRequest(req *rpcRequest) *rpcResponse {
	switch req.Method {
	case "initialize":
		serverVersion, _, _ := version.Get()
		return ok(req.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]any{
				"tools":     map[string]bool{},
				"resources": map[string]bool{},
			},
			"serverInfo": map[string]any{
				"name":    "guanfu",
				"version": serverVersion,
			},
		})

	case "notifications/initialized":
		return nil // no response for notifications

	case "tools/list":
		return ok(req.ID, map[string]any{"tools": json.RawMessage(tools)})

	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return errResp(req.ID, -32602, "invalid tools/call params: "+err.Error())
		}
		result, rpcErr := handleToolCall(params.Name, params.Arguments)
		if rpcErr != nil {
			return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: rpcErr}
		}
		return ok(req.ID, map[string]any{"content": []map[string]any{{"type": "text", "text": result}}})

	case "resources/list":
		return ok(req.ID, map[string]any{"resources": buildResourcesList()})

	case "resources/read":
		var params struct {
			URI string `json:"uri"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return errResp(req.ID, -32602, "invalid resources/read params: "+err.Error())
		}
		content, rpcErr := handleResourceRead(params.URI)
		if rpcErr != nil {
			return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: rpcErr}
		}
		return ok(req.ID, map[string]any{"contents": []map[string]any{{
			"uri":      params.URI,
			"mimeType": resourceMimeType(params.URI),
			"text":     content,
		}}})

	default:
		return errResp(req.ID, -32601, "unknown method: "+req.Method)
	}
}

func handleToolCall(name string, args json.RawMessage) (string, *rpcError) {
	// Extract asset parameter (common to all tools)
	asset := extractAsset(args)
	if asset == "" {
		asset = "btc"
	}

	// Enforce the schema enum at dispatch for tools that accept asset.
	// get_stock_forecast uses ticker, not asset, so skip there.
	if name != "get_stock_forecast" && name != "get_domain" && name != "get_indicator" {
		if rpcErr := validateToolAsset(asset); rpcErr != nil {
			return "", rpcErr
		}
	}

	switch name {
	case "get_panel", "get_btc_panel":
		timeout, rpcErr := timeoutFromArgs(args)
		if rpcErr != nil {
			return "", rpcErr
		}
		panel := getOrFetchPanel(asset, timeout)
		b, _ := json.MarshalIndent(panel, "", "  ")
		return string(b), nil

	case "get_verdict", "get_btc_verdict":
		var p struct {
			TimeoutSeconds int  `json:"timeout_seconds,omitempty"`
			IncludePanel   bool `json:"include_panel,omitempty"`
		}
		if rpcErr := decodeArgs(args, &p); rpcErr != nil {
			return "", rpcErr
		}
		timeout, rpcErr := timeoutFromSeconds(p.TimeoutSeconds)
		if rpcErr != nil {
			return "", rpcErr
		}
		panel := getOrFetchPanel(asset, timeout)
		b, _ := json.MarshalIndent(buildVerdictPayload(asset, panel, p.IncludePanel), "", "  ")
		return string(b), nil

	case "get_forecast", "get_btc_forecast":
		var p struct {
			TimeoutSeconds int   `json:"timeout_seconds,omitempty"`
			Horizons       []int `json:"horizons,omitempty"`
			TopK           int   `json:"top_k,omitempty"`
			IncludePanel   bool  `json:"include_panel,omitempty"`
		}
		if rpcErr := decodeArgs(args, &p); rpcErr != nil {
			return "", rpcErr
		}
		timeout, rpcErr := timeoutFromSeconds(p.TimeoutSeconds)
		if rpcErr != nil {
			return "", rpcErr
		}
		opts := forecast.DefaultOptions()
		if len(p.Horizons) > 0 {
			opts.Horizons = p.Horizons
		} else {
			// B5: defer to asset-specific default. Asset.BuildForecast
			// fills Horizons via forecast.HorizonsForAsset when empty.
			opts.Horizons = nil
		}
		if p.TopK != 0 {
			if p.TopK < minForecastTopK() {
				return "", &rpcError{Code: -32602, Message: fmt.Sprintf("top_k must be >= %d", minForecastTopK())}
			}
			opts.TopK = p.TopK
		}
		if rpcErr := validateForecastHorizons(opts.Horizons); rpcErr != nil {
			return "", rpcErr
		}
		panel := getOrFetchPanel(asset, timeout)
		fc, err := buildForecast(asset, timeout, opts)
		if err != nil {
			return "", &rpcError{Code: -32603, Message: "build forecast failed: " + err.Error()}
		}
		annotateBaselines(fc)
		emitClaim(fc, asset, panel)
		b, _ := json.MarshalIndent(buildForecastPayload(asset, panel, fc, p.IncludePanel), "", "  ")
		return string(b), nil

	case "get_stock_forecast":
		var p struct {
			Ticker         string `json:"ticker"`
			Days           int    `json:"days,omitempty"`
			Horizons       []int  `json:"horizons,omitempty"`
			TopK           int    `json:"top_k,omitempty"`
			TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
		}
		if rpcErr := decodeArgs(args, &p); rpcErr != nil {
			return "", rpcErr
		}
		if strings.TrimSpace(p.Ticker) == "" {
			return "", &rpcError{Code: -32602, Message: "ticker is required"}
		}
		timeout, rpcErr := timeoutFromSeconds(p.TimeoutSeconds)
		if rpcErr != nil {
			return "", rpcErr
		}
		opts := forecast.DefaultOptions()
		if len(p.Horizons) > 0 {
			opts.Horizons = p.Horizons
		} else {
			opts.Horizons = forecast.HorizonsForAsset(client.StockKey(p.Ticker))
		}
		if rpcErr := validateForecastHorizons(opts.Horizons); rpcErr != nil {
			return "", rpcErr
		}
		if p.TopK != 0 {
			if p.TopK < minForecastTopK() {
				return "", &rpcError{Code: -32602, Message: fmt.Sprintf("top_k must be >= %d", minForecastTopK())}
			}
			opts.TopK = p.TopK
		}
		fc, err := buildStockForecast(p.Ticker, p.Days, timeout, opts)
		if err != nil {
			return "", &rpcError{Code: -32603, Message: "stock forecast failed: " + err.Error()}
		}
		b, _ := json.MarshalIndent(fc, "", "  ")
		return string(b), nil

	case "get_domain":
		var p struct {
			Domain         string `json:"domain"`
			TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
		}
		if rpcErr := decodeArgs(args, &p); rpcErr != nil {
			return "", rpcErr
		}
		if !validDomain(p.Domain) {
			return "", &rpcError{Code: -32602, Message: "invalid domain: " + p.Domain}
		}
		timeout, rpcErr := timeoutFromSeconds(p.TimeoutSeconds)
		if rpcErr != nil {
			return "", rpcErr
		}
		panel := getOrFetchPanel(asset, timeout)
		dom := getDomainJSON(panel, p.Domain)
		b, _ := json.MarshalIndent(dom, "", "  ")
		return string(b), nil

	case "get_indicator":
		var p struct {
			Name           string `json:"name"`
			TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
		}
		if rpcErr := decodeArgs(args, &p); rpcErr != nil {
			return "", rpcErr
		}
		if p.Name == "" {
			return "", &rpcError{Code: -32602, Message: "indicator name is required"}
		}
		timeout, rpcErr := timeoutFromSeconds(p.TimeoutSeconds)
		if rpcErr != nil {
			return "", rpcErr
		}
		panel := getOrFetchPanel(asset, timeout)
		ind := findIndicator(panel, p.Name)
		if ind != nil {
			b, _ := json.MarshalIndent(ind, "", "  ")
			return string(b), nil
		}
		return "", &rpcError{Code: -32602, Message: fmt.Sprintf("indicator %q not found", p.Name)}

	default:
		return "", &rpcError{Code: -32601, Message: "unknown tool: " + name}
	}
}

// supportedResourceAssets lists the asset keys that resources/* recognizes.
// First entry is the legacy default for unsuffixed URIs (panel/verdict/forecast/latest).
var supportedResourceAssets = []string{"btc", "qqq", "spy", "gold"}

// parseResourceURI splits a guanfu://{kind}/latest[/{asset}] URI into kind+asset.
// kind is "panel" | "verdict" | "forecast"; asset defaults to "btc" for the
// unsuffixed legacy form. Returns ok=false for non-matching URIs.
func parseResourceURI(uri string) (kind, asset string, ok bool) {
	const prefix = "guanfu://"
	if !strings.HasPrefix(uri, prefix) {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(uri, prefix), "/")
	if len(parts) < 2 || parts[1] != "latest" {
		return "", "", false
	}
	kind = parts[0]
	switch kind {
	case "panel", "verdict", "forecast":
	default:
		return "", "", false
	}
	asset = "btc"
	if len(parts) >= 3 && parts[2] != "" {
		asset = strings.ToLower(parts[2])
	}
	for _, a := range supportedResourceAssets {
		if asset == a {
			return kind, asset, true
		}
	}
	return "", "", false
}

func buildResourcesList() json.RawMessage {
	type res struct {
		URI         string `json:"uri"`
		Name        string `json:"name"`
		MIMEType    string `json:"mimeType"`
		Description string `json:"description"`
	}
	out := []res{{
		URI:         "guanfu://knowledge/skill.md",
		Name:        "SKILL.md (完整版)",
		MIMEType:    "text/markdown",
		Description: "完整解读知识库 (~900 行,占 context 较大;新客户端建议改用 tier1/2/3 分层)",
	}, {
		URI:         "guanfu://skill/tier1",
		Name:        "Tier 1 — 数据契约 + 关键阈值",
		MIMEType:    "text/markdown",
		Description: "必载最小上下文 (~200 行):字段定义 / 可靠性标注 / source_health / 必出规则。每次读盘都应先读",
	}, {
		URI:         "guanfu://skill/tier2",
		Name:        "Tier 2 — 决策框架 + 行为护栏",
		MIMEType:    "text/markdown",
		Description: "读盘流程 / 域级方向 / 输出模板 / 行为护栏。需要给建议 / 做判断时必读",
	}, {
		URI:         "guanfu://skill/tier3",
		Name:        "Tier 3 — 术语 + 机制 + 类比(当前同 SKILL.md)",
		MIMEType:    "text/markdown",
		Description: "深度参考:指标定义 / 因果机制 / 历史类比。用户追问原理 / 历史时按需读",
	}, {
		URI:         "guanfu://skill/profiles/btc",
		Name:        "Asset Profile — BTC",
		MIMEType:    "text/markdown",
		Description: "BTC 资产画像:8 域 crypto-cycle lens、forecast/source/skill 规则",
	}, {
		URI:         "guanfu://skill/profiles/equity_index",
		Name:        "Asset Profile — Equity Index",
		MIMEType:    "text/markdown",
		Description: "QQQ/SPY 权益指数画像:估值、利率、信用、期权、技术",
	}, {
		URI:         "guanfu://skill/profiles/gold",
		Name:        "Asset Profile — Gold",
		MIMEType:    "text/markdown",
		Description: "黄金画像:实际利率、美元、通胀、COT、避险、技术",
	}, {
		URI:         "guanfu://skill/profiles/us_stock",
		Name:        "Asset Profile — US Stock",
		MIMEType:    "text/markdown",
		Description: "任意美股画像:单一个股读盘、事件风险、保守 forecast 规则",
	}, {
		URI:         "guanfu://skill/contracts/asset-profile",
		Name:        "Contract — Asset Profile",
		MIMEType:    "text/markdown",
		Description: "新增资产必须满足的数据/读盘/forecast/skill/validation 合同",
	}, {
		URI:         "guanfu://skill/contracts/adding_asset",
		Name:        "Contract — Adding Asset",
		MIMEType:    "text/markdown",
		Description: "新增核心资产或资产类的 checklist 与硬规则",
	}, {
		URI:         "guanfu://ledger/summary",
		Name:        "Claim ledger summary (K9)",
		MIMEType:    "application/json",
		Description: "最近 90d 工具 claim 校准汇总。本地 ledger 聚合,不触网。",
	}}
	for _, kind := range []struct{ k, name, desc string }{
		{"panel", "盘面", "完整指标盘面 JSON"},
		{"verdict", "结构化读盘", "盘面 verdict JSON"},
		{"forecast", "走势推演", "kNN 历史相似走势 JSON"},
	} {
		// Legacy unsuffixed URI (BTC default) preserved for backward compat.
		out = append(out, res{
			URI:         "guanfu://" + kind.k + "/latest",
			Name:        "最新" + kind.name + " (BTC, 默认)",
			MIMEType:    "application/json",
			Description: "[Deprecated alias] 同 guanfu://" + kind.k + "/latest/btc",
		})
		for _, a := range supportedResourceAssets {
			out = append(out, res{
				URI:         "guanfu://" + kind.k + "/latest/" + a,
				Name:        strings.ToUpper(a) + " 最新" + kind.name,
				MIMEType:    "application/json",
				Description: kind.desc + " (asset=" + a + ")",
			})
		}
	}
	b, _ := json.Marshal(out)
	return b
}

func handleResourceRead(uri string) (string, *rpcError) {
	if uri == "guanfu://knowledge/skill.md" {
		data, err := os.ReadFile(skillPath())
		if err != nil {
			return "", &rpcError{Code: -32603, Message: "SKILL.md not found: " + err.Error()}
		}
		return string(data), nil
	}
	if uri == "guanfu://ledger/summary" {
		return readLedgerSummary()
	}
	if key, ok := strings.CutPrefix(uri, "guanfu://skill/"); ok {
		p, err := skillResourcePath(key)
		if err != nil {
			return "", &rpcError{Code: -32602, Message: err.Error()}
		}
		data, err := os.ReadFile(p)
		if err != nil {
			// tier3 falls back to SKILL.md until the explicit tier3.md is
			// written out; behave like an alias so consumers that read
			// tier3 don't error.
			if key == "tier3" {
				if skillData, sErr := os.ReadFile(skillPath()); sErr == nil {
					return string(skillData), nil
				}
			}
			return "", &rpcError{Code: -32603, Message: "skill resource " + key + " not found: " + err.Error()}
		}
		return string(data), nil
	}
	kind, asset, ok := parseResourceURI(uri)
	if !ok {
		return "", &rpcError{Code: -32602, Message: "unknown resource: " + uri}
	}
	switch kind {
	case "panel":
		panel := getOrFetchPanel(asset, defaultPanelTimeout)
		b, _ := json.MarshalIndent(panel, "", "  ")
		return string(b), nil
	case "verdict":
		panel := getOrFetchPanel(asset, defaultPanelTimeout)
		b, _ := json.MarshalIndent(buildVerdictPayload(asset, panel, false), "", "  ")
		return string(b), nil
	case "forecast":
		panel := getOrFetchPanel(asset, defaultPanelTimeout)
		// B5 (resource): leave Horizons empty so Asset.BuildForecast
		// fills via forecast.HorizonsForAsset (matches the get_forecast
		// tool path; otherwise QQQ/SPY/Gold resources would silently
		// return 3 horizons while the tool returns 5).
		opts := forecast.DefaultOptions()
		opts.Horizons = nil
		fc, err := buildForecast(asset, defaultPanelTimeout, opts)
		if err != nil {
			return "", &rpcError{Code: -32603, Message: "build forecast failed: " + err.Error()}
		}
		annotateBaselines(fc)
		emitClaim(fc, asset, panel)
		b, _ := json.MarshalIndent(buildForecastPayload(asset, panel, fc, false), "", "  ")
		return string(b), nil
	}
	return "", &rpcError{Code: -32602, Message: "unknown resource: " + uri}
}

// extractAsset parses the "asset" field from tool arguments.
// Returns "btc" when missing/empty (default per schema).
func extractAsset(args json.RawMessage) string {
	var p struct {
		Asset string `json:"asset"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Asset == "" {
		return "btc"
	}
	return strings.ToLower(strings.TrimSpace(p.Asset))
}

// validateToolAsset enforces the JSON-schema enum at dispatch time.
// Without it, an unsupported asset bubbles up as a cryptic engine
// "no asset registered" — clients deserve a -32602 with the list.
func validateToolAsset(asset string) *rpcError {
	for _, a := range supportedResourceAssets {
		if asset == a {
			return nil
		}
	}
	return &rpcError{
		Code:    -32602,
		Message: fmt.Sprintf("invalid asset %q; supported: %s", asset, strings.Join(supportedResourceAssets, ", ")),
	}
}

func resourceMimeType(uri string) string {
	if uri == "guanfu://knowledge/skill.md" {
		return "text/markdown"
	}
	if strings.HasPrefix(uri, "guanfu://skill/") {
		return "text/markdown"
	}
	if uri == "guanfu://ledger/summary" {
		return "application/json"
	}
	if _, _, ok := parseResourceURI(uri); ok {
		return "application/json"
	}
	return "text/plain"
}

func buildVerdictPayload(asset string, panel *model.IndicatorPanel, includePanel bool) any {
	verdict, err := appService.Verdict(asset, panel)
	if err != nil {
		verdict = &engine.Verdict{Date: panel.Date, Stance: "unknown asset"}
	}
	if !includePanel {
		return verdict
	}
	return struct {
		Verdict *engine.Verdict       `json:"verdict"`
		Panel   *model.IndicatorPanel `json:"panel"`
	}{
		Verdict: verdict,
		Panel:   panel,
	}
}

func buildForecastPayload(asset string, panel *model.IndicatorPanel, fc *forecast.Forecast, includePanel bool) any {
	if !includePanel {
		return fc
	}
	return struct {
		Forecast *forecast.Forecast    `json:"forecast"`
		Panel    *model.IndicatorPanel `json:"panel"`
	}{
		Forecast: fc,
		Panel:    panel,
	}
}

// buildStockForecast pulls a ticker via FetchAndCacheStock (D1)
// and runs the kNN forecast through USStockExtractors (D2).
func buildStockForecast(ticker string, days int, timeout time.Duration, opts forecast.Options) (*forecast.Forecast, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return appService.StockForecast(ctx, ticker, days, opts)
}

func buildForecast(asset string, timeout time.Duration, opts forecast.Options) (*forecast.Forecast, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if asset != "btc" {
		return appService.Forecast(ctx, asset, opts)
	}
	return appService.BTCForecast(ctx, opts)
}

// ─── Panel fetch with cache ───────────────────────────

func getOrFetchPanel(asset string, timeout time.Duration) *model.IndicatorPanel {
	// Only cache BTC panels (most frequently accessed)
	if asset == "btc" || asset == "" {
		panelCacheMu.RLock()
		if panelCache != nil && time.Since(panelCacheAt) < panelCacheTTL {
			defer panelCacheMu.RUnlock()
			return panelCache
		}
		panelCacheMu.RUnlock()
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Equity assets use the Asset interface
	if asset != "btc" && asset != "" {
		analysis, err := appService.Panel(ctx, asset)
		if err != nil {
			return &model.IndicatorPanel{Date: "error", StaleWarnings: []string{err.Error()}}
		}
		return analysis.Panel
	}

	// BTC flow
	analysis, err := appService.BTCPanel(ctx, application.BTCPanelOptions{
		Config:         &model.Config{},
		HistoryDB:      os.Getenv("GUANFU_HISTORY_DB"),
		DisableHistory: os.Getenv("GUANFU_NO_HISTORY") == "1",
	})
	if err != nil {
		return &model.IndicatorPanel{Date: "error", StaleWarnings: []string{err.Error()}}
	}
	if analysis.HistoryError != nil {
		log.Printf("history.Open failed (continuing without history quantiles): %v", analysis.HistoryError)
	}
	panel := analysis.Panel

	panelCacheMu.Lock()
	panelCache = panel
	panelCacheAt = time.Now()
	panelCacheMu.Unlock()

	return panel
}

// ─── Helpers ──────────────────────────────────────────

func getDomainJSON(p *model.IndicatorPanel, domain string) map[string]model.Indicator {
	domainValue, _ := appService.Domain(p, domain)
	return domainValue
}

func validDomain(domain string) bool {
	switch domain {
	case "cycle", "valuation", "network", "positioning", "macro", "flow", "technical", "cross_asset":
		return true
	default:
		return false
	}
}

func findIndicator(p *model.IndicatorPanel, name string) *model.Indicator {
	indicator, _ := appService.Indicator(p, name)
	return indicator
}

// emitClaim writes the forecast to the claim ledger. Silent no-op on
// any failure — claim recording must not break forecast responses.
func emitClaim(fc *forecast.Forecast, asset string, panel *model.IndicatorPanel) {
	appService.RecordForecast(fc, asset, panel)
}

// annotateBaselines attaches T-bill + passive comparisons to each horizon
// (H3/J14). See cmd/guanfu/main.go:annotateBaselines for the rationale;
// MCP mirrors that logic so every client gets baseline-aware forecasts.
func annotateBaselines(fc *forecast.Forecast) {
	appService.AnnotateForecast(fc)
}

// readLedgerSummary aggregates ~/.guanfu/claims/ into a small
// JSON payload for MCP consumers. 90-day lookback; safe offline.
func readLedgerSummary() (string, *rpcError) {
	ledger, err := claim.Open("")
	if err != nil {
		return "", &rpcError{Code: -32603, Message: "open ledger: " + err.Error()}
	}
	cutoff := time.Now().AddDate(0, 0, -90)

	type ClaimBucket struct {
		Asset   string `json:"asset"`
		Horizon int    `json:"horizon"`
		N       int    `json:"n"`
	}
	buckets := map[string]*ClaimBucket{}
	claims, _ := ledger.ListClaims(func(c claim.Claim) bool {
		return !c.AsOf.Before(cutoff)
	})
	for _, c := range claims {
		key := c.Asset + "|" + strconvItoa(c.Horizon)
		b := buckets[key]
		if b == nil {
			b = &ClaimBucket{Asset: c.Asset, Horizon: c.Horizon}
			buckets[key] = b
		}
		b.N++
	}
	bucketList := make([]ClaimBucket, 0, len(buckets))
	for _, b := range buckets {
		bucketList = append(bucketList, *b)
	}

	out := map[string]any{
		"lookback_days": 90,
		"claim_buckets": bucketList,
		"claim_total":   len(claims),
		"note":          "For per-claim calibration run `guanfu calibrate --json`.",
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b), nil
}

func strconvItoa(i int) string {
	// avoid pulling strconv into this file; Itoa is trivial for small ints
	return fmt.Sprintf("%d", i)
}

func skillPath() string {
	if p := os.Getenv("GUANFU_SKILL_PATH"); p != "" {
		return p
	}
	return "skill/SKILL.md"
}

// skillResourcePath resolves guanfu://skill/* resource keys to files.
// Resolution order:
//  1. $GUANFU_SKILL_DIR/{key}.md (if env set)
//  2. sibling of $GUANFU_SKILL_PATH (common deployment: SKILL/tier live together)
//  3. "skill/{key}.md" (repo-relative default)
func skillResourcePath(key string) (string, error) {
	if strings.Contains(key, "..") || strings.HasPrefix(key, "/") || strings.HasSuffix(key, "/") {
		return "", fmt.Errorf("invalid skill resource %q", key)
	}
	switch key {
	case "tier1", "tier2", "tier3":
	case "profiles/btc", "profiles/equity_index", "profiles/gold", "profiles/us_stock":
	case "contracts/asset-profile", "contracts/adding_asset":
	default:
		return "", fmt.Errorf("unknown skill resource %q", key)
	}
	if d := os.Getenv("GUANFU_SKILL_DIR"); d != "" {
		return d + "/" + key + ".md", nil
	}
	// Use skillPath's directory if GUANFU_SKILL_PATH is set so tier files sit
	// next to the main SKILL.md deployment.
	sp := skillPath()
	if i := strings.LastIndex(sp, "/"); i >= 0 {
		return sp[:i+1] + key + ".md", nil
	}
	return "skill/" + key + ".md", nil
}

// ─── RPC helpers ──────────────────────────────────────

func ok(id any, result any) *rpcResponse {
	return &rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func errResp(id any, code int, msg string) *rpcResponse {
	return &rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
}

func decodeArgs(args json.RawMessage, dest any) *rpcError {
	if len(args) == 0 || string(args) == "null" {
		return nil
	}
	if err := json.Unmarshal(args, dest); err != nil {
		return &rpcError{Code: -32602, Message: "invalid tool arguments: " + err.Error()}
	}
	return nil
}

func timeoutFromArgs(args json.RawMessage) (time.Duration, *rpcError) {
	var p struct {
		TimeoutSeconds int `json:"timeout_seconds"`
	}
	if rpcErr := decodeArgs(args, &p); rpcErr != nil {
		return 0, rpcErr
	}
	return timeoutFromSeconds(p.TimeoutSeconds)
}

func timeoutFromSeconds(seconds int) (time.Duration, *rpcError) {
	if seconds < 0 {
		return 0, &rpcError{Code: -32602, Message: "timeout_seconds must be non-negative"}
	}
	if seconds == 0 {
		return defaultPanelTimeout, nil
	}
	if seconds > 300 {
		return 0, &rpcError{Code: -32602, Message: "timeout_seconds must be <= 300"}
	}
	return time.Duration(seconds) * time.Second, nil
}

func validateForecastHorizons(horizons []int) *rpcError {
	for _, h := range horizons {
		if h <= 0 {
			return &rpcError{Code: -32602, Message: "forecast horizons must be positive"}
		}
		if h > 730 {
			return &rpcError{Code: -32602, Message: "forecast horizons must be <= 730 days"}
		}
	}
	return nil
}

func minForecastTopK() int {
	return 5
}
