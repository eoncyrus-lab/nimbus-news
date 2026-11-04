> **已归档（2026-07-03）**：本文为历史方案/快照，现状以 [docs/ARCHITECTURE.md](../../docs/ARCHITECTURE.md) 为准。

# Go-first 数据源迁移清单

> 状态基线：2026-06-27。本文件是 source-by-source 迁移的总账本与优先级。
> 原则见 `../README.md`：**一源一独立包**，Go 上层 `import` 直连，Python 上层经
> `cmd/datagw`(8821) → 数据访问 facade(8800) 同契约消费。`futu`/`longbridge`
> SDK-bound，**永久留 Python**，不在本清单内。

## 四条硬要求（验收口径）

1. 行情/宏观/链上等数据源**优先在 Go 实现**（Python 侧逐步经 datagw 收敛）。
2. **无重复**：同一源只有一处实现，上层一律消费共享包。
3. 每个源**不止挖一个端点**：补齐高价值字段（深度/taker/大户/基本面…）。
4. 每个源**独立成包**，上层（guanfu、**流动性评估**[真实在建]、news、nimbus skills）消费它。

---

## A. 现状盘点

### 已成独立包（datasources/）
| 包 | 现有端点 | 已消费方 |
|---|---|---|
| `yahoo` | GetQuote / GetHistory / GetHistoryDays | news adapter、guanfu yahoo_history+fetch_stock |
| `binance` | Ticker24h / Klines / KlinesDeep / FundingRate / OpenInterestNow / LongShortRatio | guanfu klines 路径（btc/eth/paxg/top50） |
| `okx` | GetTicker / GetCandles / GetFunding / GetOpenInterest / LongShortRatio | datagw |
| `deribit` | GetDVOL / GetOptionTicker | datagw、guanfu |
| `envelope` | Wrap / UTCNow | 全部 |

datagw 路由：`/quote /history /crypto-quote /funding /open-interest /long-short /dvol /option`。

### 仍是私有/重复实现（待收敛）
- **guanfu `pkg/client/*.go` 私有源**（非独立包，别处无法复用）：
  `coinmetrics` `coingecko` `cmc` `defillama` `mempool` `cboe` `stooq` `coinbase`
  `sosovalue` `fred` `alternative.me`（链上+宏观核心）。
- **news raw 适配器**（未消费共享包）：
  `adapters/binance.go`(179行,0 import) · `adapters/okx.go`(147行,0 import) ·
  `adapters/yahoo.go`(search 端点仍 raw) · `adapters/cryptoalt.go`(OKX DEX 聚合,链上,独立功能)。
- **guanfu 残留 raw**：`fetchFuturesData`(binance 衍生品) · `cross_asset.go`/`gold.go`(yahoo)。
- **跨语言双实现**：`cboe` `stooq` `alternative.me` `coinbase`（Go+Py 各一份）。
- **Python 侧重复**：`fred_client.py`/`finnhub_client.py` 各在 market-pulse 与 news-dashboard
  重复；多个 skill 各自 `market_client.py`（earnings/ftd/macro-regime/market-top/pead…）；
  `services/data-gateway` 的 `fred/finnhub/finra/edgar/openbb` providers；
  `services/reference-data` 自持 binance `/funding` + alternative.me `/feargreed`。

## B1. 抽包模式（W2 coinmetrics 定稿，后续源照此复制）

- **包 = fetch-only、无状态**：一个泛型 fetch 函数 + `Query`/`Point` 类型；分页在包内
  处理（跟 `next_page_url` / endTime 等）。**累积/缓存/派生数学全留消费方**（§B2）。
- **空数据返回 `(nil, nil)` 不报错**——空区间对部分源是合法的；由消费方决定空是否失败
  （guanfu 各 fetch 自带 empty→error 检查，行为不变）。
- **community/paid host 由 `APIKey` 切换**；走 `httpx.Default`（非 2xx 自动 error + 429/5xx 重试），
  不再自己查 status。
- **无 Python 消费方的源跳过 datagw 路由**（YAGNI，等真有 Python 消费方再加）。
- 数值字段用 `map[string]string`/string 原样透传，消费方自己 parse（CoinMetrics 数字是字符串）。

---

## B. 端点富化清单（要求 3）

每个包目前只挖 1–5 个端点。**深度/order book 是"流动性评估"消费方的前置依赖，优先级最高。**

| 包 | 待加端点（高价值优先） |
|---|---|
| **binance** | `/api/v3/depth`(深度) ⭐ · `/futures/data/takerlongshortRatio` · `/futures/data/topLongShortPositionRatio`(大户) · `/fapi/v1/fundingRate`(历史 funding) · taker buy/sell volume（包注释已许诺、未实现） |
| **okx** | `/market/books`(深度) ⭐ · `/rubik/stat/taker-volume` · `/rubik/.../long-short-account-ratio`(top-trader,区别 global) · `/public/option-summary`(IV/greeks) · `/public/mark-price` · `/public/instruments` · `/dex/aggregator`(链上,cryptoalt 在用) |
| **deribit** | `get_book_summary_by_currency` · `get_instruments`(期权链) · `get_order_book` · `get_historical_volatility` · `get_index_price` |
| **yahoo** | `quoteSummary`(基本面:财务/估值/盈利) · `v1/finance/search`(news 需要) · options 链 |

---

## B2. 历史数据库守则（高价值，**不可丢弃**）

`~/.guanfu/prices/*.json`（gold 1968+、btc、fred_* 到 1.2M、btc_mvrv、defillama、
coinbase_btc…）+ `~/.guanfu/history.db` 是多年累积、**远超 live API 可回溯范围**的资产。
迁移**只替换 HTTP 抓取层，绝不动持久化/累积层**：

1. **包 = 纯抓取（fetch-only）**，无状态、无落盘。累积/合并仍由消费方
   （guanfu `PriceStore` + `refresh.go` 增量协议 `last_date+1 → today`）负责。
2. **优先本地长历史**：当本地 store 比 API 回溯更长时，本地优先、API 仅做近端 overlay。
   范例（已落地、勿回退）：London gold(1968+) 先于 PAXG(2019+)；BTC 缓存+CoinMetrics
   seed + Binance 近端 overlay。
3. **append-only 源严禁 fetch-overwrite**：CMC 自由层/fear&greed/部分链上快照 API **只返回当前值**，
   `cmc_*.json` 这类**日追加 JSON 就是唯一历史档**。抽包后消费方必须继续"取当前值→append 到
   store"，不得用一次抓取覆盖整段。
4. 每个抽包 slice 的 DoD 增加一条：**store 行数/起始日期前后不变（或只增不减）**。

## C. 迁移波次（每波 = 一个 slice，含 DoD）

> 每波 DoD：`go build/vet` 双仓干净 · 受影响包测试通过 · **guanfu 改动必过**
> `go test ./pkg/engine -run TestBacktestBundles`（dir_hit 掉 ≥3pp → 回滚）·
> live 冒烟 · reviewer APPROVE · 删掉被取代的重复实现。

### Wave 1 — 纯删重复（低风险、快）
- **W1a news binance/okx dedup**：`news/adapters/binance.go`、`okx.go` 改消费 `datasources/binance|okx`，删 raw client。
- **W1b yahoo Search 端点 + news 收敛**：`datasources/yahoo` 加 `Search(ctx,q)`；`news/adapters/yahoo.go` search 改用之。
- **W1c guanfu binance 衍生品**：`fetchFuturesData` funding/OI/long-short → 共享包（端点已存在）。
- **W1d guanfu yahoo fallback**：`cross_asset.go`/`gold.go` → `datasources/yahoo`（收尾 yahoo dedup）。

### Wave 2 — 链上/crypto 抽包（要求1+2+4 主攻，guanfu 私有源外提）✅ DONE (2026-06-28)
按依赖与复用价值排序，各：新建 `datasources/<src>`（own types + internal/httpx）+ guanfu 改消费 + 删 guanfu 私有实现。
- ✅ `coinmetrics`(BTC 链上核心) · ✅ `mempool` · ✅ `coingecko` · ✅ `cmc` · ✅ `defillama` · ✅ `coinbase`(全 OHLCV+Volume，预备 Wave4 流动性) · ✅ `sosovalue`(ETF，加了 httpx.PostJSON 共享) · ✅ `alternative.me`(fear&greed)。
- §B2：cmc/defillama/coinbase 有 PriceStore 历史，逐源 live 验证 rows 只增不减、起始日不变；sosovalue/alternative.me 是 live-only（无 store，只下游 history.db 指标归档）。
- 收尾事项：reference-data(8812) 仍有自己的 /feargreed Python 实现 — 让它也消费 `alternativeme` 包属 Wave 5（Python 收敛）；datagw 路由这些源暂缓（当前无 Python 消费方）。

### Wave 3 — 宏观抽包 + 退役 data-gateway Python providers
- `fred` · `cboe` · `finra` · `edgar/SEC` 各成包；退役 `data-gateway` 对应 provider；
  guanfu `fred*.go`/`cboe*.go`/`stooq`/`cape` 改消费。`openbb` 评估是否保留（聚合器，可能留 Python）。

### Wave 4 — 流动性评估前置 + 股票基本面（要求3核心）
- **depth/order book 端点**（binance `/depth`、okx `/market/books`、deribit book）→ 解锁"流动性评估"消费方。
- **equity-fundamentals**：yahoo `quoteSummary` → 新 capability。
- taker/大户比、期权链等富化端点按消费方需求拉入。

### Wave 5 — Python skill 客户端收敛（要求1长尾）
- nimbus skills 各自的 `market_client.py`/`finnhub_client.py`/`fred_client.py` 改经 facade(8800)
  消费 Go 包，删重复私有 client；`stock-screener` 的 `liquidity_calculator.py` 可接 depth 端点。

---

## D. 优先级建议
**Wave 1 ✅ + Wave 2 ✅ 完成（2026-06-28）。下一步二选一：**
- **Wave 4 depth 端点**（binance `/depth`、okx `/market/books`）— 解锁"流动性评估"消费方（用户已确认是 real upcoming consumer）；coinbase 包已出全 OHLCV+Volume 作前置。
- **Wave 3 宏观抽包**（fred/cboe/finra/edgar）+ 退役 data-gateway Python providers — guanfu 宏观源仍私有实现，退役 Python 重复收益大。
Wave 5（Python 长尾）随各 skill 迭代顺手收敛，不单独排期。
