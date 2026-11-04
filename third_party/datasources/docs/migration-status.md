> **已归档（2026-07-03）**：本文为历史方案/快照，现状以 [docs/ARCHITECTURE.md](../../docs/ARCHITECTURE.md) 为准。

# Go-first 数据源迁移 — 竣工状态 & 清理登记

> 配套 `migration-plan.md`（计划口径）。本文是 **as-built 实况**：已收敛什么、谁消费谁、
> 死代码/重复/冗余登记、以及清理 TODO。最后更新 2026-06-28。

---

## 1. 一句话状态

Wave 1–5 全部落地。`行情/宏观/情绪/期权/SEC/短卖/新闻` 的取数已收敛到 Go 层
（`github.com/Ricaardo/nimbus-datasources` + `datagw` 8821）。FRED / finnhub /
alternative.me / cboe 在 Go 与 Python 两侧均已统一取数口。剩余的直连 vendor 只有
少数低价值/独立脚本（见 §5）。

四条硬要求验收：① 行情/宏观 Go-first ✅ ② 消除重复实现 ✅（残留见 §4–§5）
③ 端点富化（depth / IV30 / 13F / RegSHO …）✅ ④ 每源独立包 ✅。

---

## 2. 架构（as-built）

```
Go 上层 (guanfu / news)  ──import──►  datasources/<pkg>      （直接导入，自带 key）
                                          │
Python 消费方            ──HTTP──►  data-access facade (8800)
  (nimbus skills /                        │
   equity-screener /        ┌─────────────┴─────────────┐
   market-pulse)            ▼                           ▼
                    reference-data (8812)        data-gateway (8820)
                            │                           │  (fred/finra/edgar/
                            └────────►  datagw (8821) ◄──┘   finnhub providers 现为代理)
                                          │
                                    datasources/<pkg> （单一取数口；持 FRED/FINNHUB key）
```

- **Go 上层**：`import` 包，自带凭证。例：news `us_macro_report.go`→`fred`，
  `finnhub.go`/`morning_scan.go`→`finnhub`，guanfu `pkg/client/*`→各 crypto/宏观包。
- **Python 消费方**：经 facade(8800) → reference-data(8812) / data-gateway(8820) → **datagw(8821)**。
  datagw 是唯一对外 fetch 点，并集中持有 `FRED_API_KEY` / `FINNHUB_API_KEY`。

### datagw(8821) 路由一览

| 路由 | 源 | 说明 |
|---|---|---|
| `/quote` `/history` | yahoo | 行情 / K 线 |
| `/crypto-quote` `/funding` `/open-interest` `/long-short` | binance/okx | 现货 + 衍生品持仓 |
| `/depth?venue=&symbol=&limit=` | binance/okx | **订单簿深度（Wave 4，流动性评估）** |
| `/dvol` `/option` | deribit | 期权 IV / greeks |
| `/feargreed` | alternative.me | 加密贪恐 |
| `/macro?series=&limit=` | fred | 宏观（keyed，持 FRED_API_KEY） |
| `/short-file?date=` | finra | RegSHO 短卖 |
| `/edgar-filings` `/form-13f` | edgar | SEC filings / 13F |
| `/finnhub?endpoint=&...` | finnhub | **认证透传**（持 FINNHUB_API_KEY） |
| `/cboe-quote?symbol=` | cboe | VIX 期限结构等延迟报价 |

`/health` sources = `yahoo binance okx deribit alternative.me fred finra edgar finnhub cboe`。

---

## 3. 各源收敛状态

| 源 | Go 包 | Go 消费方 | Python 消费方收敛 | 备注 |
|---|---|---|---|---|
| yahoo | ✅ | news/guanfu 直 import | facade /history | |
| binance/okx | ✅ | news/guanfu 直 import | facade /crypto-quote 等 | depth=Wave4 |
| deribit | ✅ | — | facade /dvol /option | |
| coinmetrics/mempool/coingecko/cmc/defillama/coinbase/sosovalue | ✅ | guanfu `pkg/client/*` | — | Wave 2 |
| alternative.me | ✅ | guanfu real.go（+gold.go 已收敛但**死**，见 §4） | nimbus crypto_calculator → facade /feargreed | |
| fred | ✅ | news us_macro_report 直 import | data-gateway / reference-data → datagw /macro | |
| finra | ✅ | — | data-gateway /short-file | |
| edgar | ✅ | — | data-gateway /edgar-filings /form-13f | XBRL 基本面**未**收敛，见 §5 |
| finnhub | ✅ | news finnhub.go/morning_scan 直 import | data-gateway finnhub_provider → datagw /finnhub | |
| cboe | ✅ | guanfu CBOEPutCallSource | nimbus cboe_vix → datagw /cboe-quote | |
| stooq | ⚠️ | guanfu StooqPutCallSource（**死/未注册**，见 §4） | — | |

---

## 4. 死代码登记（已核实，**已清理 2026-06-28**）

> 工具：`deadcode ./...` + `staticcheck` + grep 交叉验证。

| 代码 | 证据 | 状态 |
|---|---|---|
| guanfu `pkg/client/gold.go` `FetchFearGreedHistory` | 无任何调用者；`allRefreshSources()` 未注册 fng-history 源（实时读数走 real.go 的 `alternativeme.FearGreedIndex(ctx,1)`）。 | ✅ **已删 #22**（连带 orphaned `alternativeme`/`sort` import） |
| guanfu `pkg/client/stooq_putcall.go` `StooqPutCallSource`(+test) | 未注册——`cli_refresh.go` 仅 `append CBOEPutCallSource{}`；全仓无 `STOOQ_APIKEY`，从未实例化。 | ✅ **已删 #23** |
| `datasources/stooq` 包 | 仅被上面这个死源消费 → 实际死代码。 | ✅ **已删 #23** |

清理结论：经核实 STOOQ put/call **从未接线**（无 `STOOQ_APIKEY`、未注册），并非有意兜底
而是废弃——已删。**活跃的 `stooq_putcall` 存储 key 不受影响**：它由 `CBOEPutCallSource`
生产（沿用旧 key 名，见 `global_macro.go:142` 注释「CBOE official; legacy storage key」），
被 forecast/engine 消费。回归门 + pkg/client/forecast/engine 测试全绿。Wave 3 的 stooq 迁移
事后看是作用在已死消费方上（无害）。

### ⚠️ 看似无用但**有意保留**（勿删）

- `binance.Depth` + 流动性 helper、`okx.GetOrderBook` —— Wave 4，等「流动性评估」消费方落地。
- `cboe.GetQuote`、`finnhub.Raw/MarketNews/CompanyNews`、`sosovalue.HistoricalInflowChart`
  —— 均经 datagw 路由 / real.go **活跃消费**，只是从 `cmd/datagw` 自身 main 不可达
  （deadcode 对「被外部模块消费的库函数」会误报）。

---

## 5. 重复/冗余残留（待收敛，已 defer）

| 重复点 | 消费方 | 方案 | TODO |
|---|---|---|---|
| SEC XBRL 基本面（data.sec.gov 直连） | equity-screener ×3（us_client/china_concept/valuation_enrich）+ news superinvestors.py | equity-screener 是纯 Python → 加 datagw `data.sec.gov` 透传路由（仿 `/finnhub`），4 个消费方指过去 | **#24** |
| Binance 全量 `/ticker/24hr` | news ingest_daily.py | 需新增 `binance.AllTickers24h()` 批量方法；1 个独立脚本，价值低 | **#25** |
| OKX DEX 聚合器 | news cryptoalt.go | 链上聚合，**有意 out-of-scope**，不收敛 | — |

### 另：guanfu 自有死代码（**非本次迁移**，独立卫生）

`deadcode ./...` 另报 ~20 个 guanfu 内部不可达函数：`pkg/forecast/*`（BuildTwoStage /
featuresAt / returnOver / smaDeviation / realizedVol / rsi / compressedAHR /
halvingProgress / stddev / clip）、`pkg/engine/*`（scoreEquityTechnical/Macro/
Positioning、BuildEquityVerdictEnhanced）、`pkg/assetprofile/*`、`pkg/allocate`
ComputeRiskMetrics 等。**注意**部分可能经 MCP/反射可达或为有意 API，删前逐个核对
`cmd/guanfu-mcp` + 测试。 → **#26**

---

## 6. TODO 映射

| # | 任务 | 优先级 | 风险 |
|---|---|---|---|
| 22 | 删死代码 guanfu `FetchFearGreedHistory` | 高（已验证零风险纯删） | 低 |
| 23 | 确认并清理 `StooqPutCallSource` + `datasources/stooq` | 中（需用户确认意图） | 低 |
| 24 | Tier-2：SEC XBRL 基本面收敛到 datagw | 中（defer） | 中 |
| 25 | ingest_daily binance 批量收敛（或保留） | 低 | 低 |
| 26 | guanfu 内部死代码清扫（非迁移） | 低 | 中（反射/MCP 误删） |
| 27 | 补测试（finnhub_provider 解包 / news toMessages / SSRF 回归） | 低（nice-to-have） | — |

---

## 7. 运维 & 守则（务必记住）

- **§B2 历史库守则**：`~/.guanfu/prices/*.json` 是多年累积，迁移**只换 HTTP fetch 层，
  不动 Save/Append 累积**。每个收敛 slice 的 DoD 含「store 行数只增不减、起始日不变」实测。
- **launchd 改 env 必须 `bootout`+`bootstrap`**，`kickstart -k` 会复用旧 service 定义→
  读到陈旧 env（FINNHUB_API_KEY 注入时踩过）。
- **密钥**：`FRED_API_KEY` / `FINNHUB_API_KEY` 仅注入 `com.nimbus.datagw.plist`
  EnvironmentVariables（本地、未入库）；datagw 是唯一持 key 方。
- **密钥泄漏防护**：keyed 源把 token/api_key 放 query string，传输层错误（DNS/TLS/超时）
  的 `*url.Error` 会带出完整 URL→`internal/httpx.redactURLError` 已统一脱敏
  （token/api_key/apikey/key→REDACTED），保护整个 keyed 层（finnhub+fred）。
- **datagw 只绑 127.0.0.1**，`/finnhub` 等透传路由**不做调用方认证**——访问控制全靠回环绑定，
  勿暴露到 0.0.0.0。
- **回归门**：动 guanfu 须 `go test ./pkg/engine -run TestBacktestBundles` 保持绿。
