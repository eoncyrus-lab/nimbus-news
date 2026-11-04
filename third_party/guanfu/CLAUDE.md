# guanfu — AI 维护者 / coder 上下文

> 此文件面向**修改 guanfu 代码的 AI / 维护者**，不是给最终消费方。最终消费方（"我该不该买 BTC"类问题）看 `skill/SKILL.md`。
>
> 致虚极，守静笃。万物并作，吾以观复。

---

## 项目一句话

多资产投资盘面 CLI + MCP 服务。覆盖 **BTC / QQQ / SPY / Gold + 任意美股**。**不输出单一评分、不输出交易指令**——输出 8 域 40+ 指标的原始值 + 历史分位 + kNN 历史相似走势推演。

## 二进制入口

| 路径 | 用途 |
|---|---|
| `cmd/guanfu` | 主 CLI。子命令：`btc`(默认) / `qqq` / `spy` / `gold` / `stock TICKER` / `import-stock TICKER` / `market` / `dca` / `allocate` / `backtest [ASSET\|all]` / `status` / `refresh` |
| `cmd/guanfu-mcp` | MCP stdio server。Claude Desktop / Cursor 可直调 |
| `cmd/guanfu-similar` | 历史相似度独立工具 |
| `cmd/guanfu-backtest` | 回测 CLI（独立于 `guanfu backtest` 子命令） |

ad-hoc 调参/研究脚本不要放进 `cmd/guanfu/` 主 CLI 目录；一律放 `cmd/experiments/<task>/`（现有 threshold-search）。

## 核心抽象

### `engine.Asset` 接口（`pkg/engine/asset.go`）

每个资产实现 4 个方法：

```go
Key() string
Name() string
FetchSnapshot(ctx) (*AssetSnapshot, error)        // 拉所有需要的数据
BuildPanel(snap) (*model.IndicatorPanel, error)   // 数据 → 8 域指标盘
BuildVerdict(panel) *Verdict                       // 盘面 → 结构化读盘
BuildForecast(snap, opts) (*forecast.Forecast, error)  // kNN 历史相似推演
```

注册在 `init()` 里通过 `engine.Register(NewXAsset(...))`。CLI / MCP 通过 `engine.GetAsset(key)` 解析。

**接口里没有的**：评分函数、交易信号、仓位建议。**不要加**——v1 (CoinMan) 试过，把多维信息压缩成 0-100 总分必然丢失，已弃。

### `forecast.FeatureExtractor`（`pkg/forecast/forecast.go`）

```go
type FeatureExtractor func(points []Point, i int) ([]FeatureValue, bool)
```

输入：oldest-first 价格序列 + 当前 index；输出：0+ 个 normalized feature。返回 `false` 表示数据不够（自动 skip）。

**Bundles**（`pkg/forecast/features/bundles.go`）——唯一入口是 **profile 驱动的 dispatcher**
（2026-07 起旧的 `EquityExtractors`/`GoldExtractors`/`USStockExtractors` 无参包装已删）：
- `ExtractorsForAsset(asset, store)` — 生产路径（CLI/MCP/回归门都走它）：查
  `assetprofile.For(asset)` 拿 profile → `ExtractorsForProfile(p, store)` 按
  `p.FeatureBundle` 分发；未知资产回退 `GenericTechnicalExtractors()`
- bundle 内容：`btc_core`（halving cycle / AHR999）· `equity_index`（CAPE + DGS10 +
  DXY + HY + curve + VIX + Put/Call）· `gold`（real yield + breakeven + DXY + COT + VIX）·
  `us_stock`（同 equity 但**无 CAPE**——单股没有 per-name CAPE）
- 加特征改 `*WithScales` 变体 + profile 的 `FeatureScales`，不要再造无参包装

### `store.PriceStore`（`pkg/store/price.go`）

JSON 日频文件：`~/.guanfu/prices/<key>.json`。**Namespace 约定**：

| 前缀 / 形态 | 含义 | 示例 |
|---|---|---|
| 无前缀核心资产 | 4 个核心资产价格 | `btc.json` `qqq.json` `spy.json` `gold.json` |
| 无前缀链上 / 估值 | BTC 链上 + Shiller CAPE | `btc_mvrv.json` `spx_cape.json` |
| `fred_*` | FRED 宏观 | `fred_dxy.json` `fred_dgs10.json` `fred_dfii10.json` `fred_yield_curve.json` |
| `gold_*` | 黄金附加 | `gold_cot.json` |
| 其他 ETF | Futu/Yahoo 拉取 | `vixy.json` `tlt.json` `uup.json` |
| `stock_<ticker>` | 任意美股（D1 namespace） | `stock_aapl.json` `stock_msft.json` |

**任意美股必须走 `client.FetchAndCacheStock`**（`pkg/client/fetch_stock.go`），它做：
1. `ValidateStockTicker` 防撞名（用 `s.ListAssets()` 动态查，**不要硬编码**核心资产列表）
2. `StockKey` 加 `stock_` 前缀
3. 30h TTL（覆盖周末 gap）

**不要 bypass `FetchAndCacheStock` 直接 `s.Save("aapl", ...)`**——会污染核心资产 namespace。

## 数据刷新 (`guanfu refresh`)

26 个 source 统一刷新，首次全量、后续增量。每个 source 实现 `client.Source` 接口（Key/DisplayName/Refresh），按 dependency order 串行执行（外部 API 各有 rate limit，串行更易调试）。

| 类别 | Source 实现 | 文件 | 数据键 |
|---|---|---|---|
| 核心价格 | `BTCSource` / `GoldSource` | `refresh_native.go` | `btc` / `gold` |
| Yahoo ETF | `YahooETFSource` | `yahoo_history.go` | `qqq` `spy` `vixy` `tlt` `uup` `usd_cny`(CNY=X) |
| FRED 宏观 | `FREDSource` | `fred_history.go` | `fred_{dxy,fed_funds,dgs10,dgs3mo,dfii10,yield_curve,breakeven,hy_spread,tga,rrp,ecb_deposit_rate,boj_call_rate,pboc_interbank_rate}` |
| Shiller CAPE | `CAPESource` | `cape_history.go` | `spx_cape`（走 `scripts/import_cape.py`，月频 28d TTL） |
| 任意美股 | `StockKeysSource` | `refresh_native.go` | 已 import 的 `stock_*`（依靠 `FetchAndCacheStock` 30h TTL 自动跳过） |

**增量协议** (`refresh.go: staleThreshold`)：
- `last_date` 在 24h 以内 → `mode="skip"`，不发请求
- `last_date` 缺失 / 超过 24h → fetch（FRED/Yahoo 拉 `lastDate+1 → today`）
- CAPE 单独走 28 天 TTL（月频）

**失败容忍**：单个 source 失败不影响其他；`fail` 状态在最终表中显式 surface，CLI exit code 1，但 stdout 表保留所有结果。

**新加 source 时**：
1. 实现 `Source` 接口（在已有 `*_history.go` 里加，或新建文件）
2. 在 `cli_refresh.go: allRefreshSources()` 注册
3. 跑 `go test ./pkg/client/ -run TestRefresh` 验证

## 数据流

```
FetchSnapshot → AssetSnapshot          (cmd/guanfu/main.go 或 cmd/guanfu-mcp/main.go 入口调用)
              ↓
         BuildPanel    → IndicatorPanel  (原始指标 + 历史分位)
              ↓
         BuildVerdict  → Verdict         (结构化读盘，不带交易指令)
              ↓
         BuildForecast → Forecast        (kNN 历史相似 + 前向收益分布)
```

`AssetSnapshot` 是泛型容器（`pkg/engine/asset.go:47`），每个 asset 只填自己需要的字段。`BTCMarketSnapshot` 是 BTC 独占；`CrossAssetPrices` 是 map，让权益类资产装 DXY/VIX 等代理。

## kNN forecast 流水线

1. 每个 extractor 在每个历史 index 计算 `[]FeatureValue`（`Name`/`Value`/`Normalized`/`Weight`）。
2. 对每个候选历史日，距离 = `Σ weight_i × (norm_today - norm_hist)²`（或 Mahalanobis，若 `UseMahalanobis=true`）。
3. 取距离最近 `TopK` 个，按 `DiversifyWindowDays` 去重相邻日。
4. 对每个 horizon `h`，统计 analogs 在 `+h` 天的前向收益 → quantiles + 概率桶 + dominant scenario label。

**关键 invariant**：feature 数量 = `len(opts.Extractors)`，不是固定 11。G3 已落地：`probeExpectedFeatures`（`forecast.go:489`）在过去 `expectedProbeWindowDays = 60` 天窗口里探测单日 extractor bundle 能达到的最大 feature count 与 weight 总和，作为 `featureCoverage` 分母。旧常量 `expectedFeatureCount = 11` 已删除。加新 feature / 改 weight 会直接反映到 coverage 分母——回测恶化超预算（见 § 回归预算）时回滚即可。

## 每资产 horizons（B5，2026-05-09 修订）

`forecast.HorizonsForAsset(asset)` 返回 per-asset 默认 horizons：

| Asset | Horizons | 说明 |
|---|---|---|
| QQQ / SPY | `30, 63, 90, 180, 252` | 季度 + 年度 |
| Gold | `30, 60, 90, 120` | **180d 已删除**——n=51 上 49% dir_hit ≈ 硬币 |
| BTC / 任意 stock | `30, 90, 180` | 默认 |

## Horizon 可靠性提示（reliability.go）

`forecast.HorizonCaveat(asset, days)` 查 `assetHorizonReliability` 静态表：dir_hit < 0.55 或 n < 10 时返回中文 caveat 字符串。值来自 `TestBacktestBundles` 输出，AsOf 字段记录最后更新日期。Build 时自动写入 `HorizonForecast.ReliabilityNote`，CLI 在每个 horizon 行下方一行打印。

更新规则：跑 `TestBacktestBundles` 后**手动**同步 `assetHorizonReliability` 数字 + AsOf 字段。不在 Build 时实时跑回测（IO 重 + 慢）。

## Walk-forward 视图（backtest）

`TestBacktestBundles` 现按 `(year, horizon)` 输出 dir_hit/n 矩阵。低整体命中率可能是：
- **均匀差**（各年都接近或低于 50%）→ 策略本身没 alpha
- **regime-依赖**（如 Gold 2017-2022 ≤50%，2023-2025 50-100%）→ 在某些 regime 下能用，过拟合到训练分布之外

现有结论：BTC/QQQ/SPY 大多数年份稳健；Gold 强 regime 依赖。

**DefaultOptions 陷阱**（B5 fix）：旧代码里 `if len(opts.Horizons) == 0 { opts = forecast.DefaultOptions() }` 会**整个替换 opts**，理论上能 clobber 调用方设置的 `Extractors`/`TopK`。在当前所有调用路径里，asset 在这一行之后立刻又写一次 `opts.Extractors`，所以**事实上没有 bug 触发过**——是防御性修正而不是热修。新代码统一只补字段，避免后续重构无意中触雷：

```go
if len(opts.Horizons) == 0 {
    opts.Horizons = forecast.HorizonsForAsset("qqq")  // 只补 Horizons
}
opts.Extractors = features.ExtractorsForAsset("qqq", a.store)   // 这行不会被 clobber 了
```

CLI flag `--forecast-horizons` 默认 `auto` → 走 per-asset；显式传 `30,90,180` 覆盖默认。MCP `get_forecast` 同理：缺 `horizons` 参数时 → 资产默认。

## MCP 工具

`cmd/guanfu-mcp/main.go` 通过 stdio 实现 MCP（无外部 SDK，自己解析 JSON-RPC）。

| 工具名 | 别名 | 说明 |
|---|---|---|
| `get_panel` | `get_btc_panel`（旧名向后兼容） | 完整盘面 |
| `get_verdict` | `get_btc_verdict` | 结构化读盘 |
| `get_forecast` | `get_btc_forecast` | kNN 推演 |
| `get_stock_forecast` | — | 任意美股 ticker（自动 Yahoo fetch） |
| `get_domain` | — | 单 domain |
| `get_indicator` | — | 单指标 |

Resources：`guanfu://panel/latest/{btc,qqq,spy,gold}` `guanfu://verdict/latest/{...}` `guanfu://forecast/latest/{...}` + `guanfu://knowledge/skill.md`。

**别名维护规则**：alias case 必须在 `main_test.go: TestHandleToolCallAliasesDispatchSameAsBTCNames` 里有断言。**不要悄悄删**——deprecated 但保留至少一个版本。

## 测试 / 回测

| 命令 | 用途 |
|---|---|
| `go test ./...` | 全套（store 包有日期敏感 fixture，2026 年偶尔会假阳性失败） |
| `go test ./pkg/engine -run TestBacktestBundles -v` | **核心回归基线**，核心资产 × horizons 方向命中率 + PIT |
| `go test ./pkg/forecast/features -run TestUSStockExtractors_AcceptanceAAPL` | D2 任意 stock 路径验收 |
| `go vet ./...` | 静态检查 |
| `make build` / `make mcp` / `make all` | 二进制构建 |

**回归预算**(来自 `docs/archive/v3/guanfu-v3-todo.md`):
- 任一 horizon dir hit 下降 ≥ 3pp → 回滚改动单独 review
- PIT 偏离 0.5 加大 ≥ 0.05 → 回滚或调权重
- backtest panic / 失败 → 立即回滚

权重总和**目前不归一化**——kNN 距离里的归一化是隐式的，weight 只控制 feature 间相对权。Wave 0 加 VIX (0.55) 后总和 ~3.25，回测仍稳，所以暂不引规一化层；如未来加 feature 把回测打穿再走 RFC。

## 加新资产 checklist

1. `pkg/engine/asset_<key>.go`：实现 `Asset` 接口 4 方法 + `init()` `Register(...)`。
2. `pkg/forecast/features/bundles.go`：写一个 `<Key>Extractors(s *PriceStore)` 函数。
3. `pkg/model/types.go`：如果 `IndicatorPanel.Asset` 字段需要新值，加 const；如果 `SnapshotData` 需要新价格字段，加上去（**不要 bump SchemaVersion**——`SnapshotData` 不是落盘类型）。
4. `cmd/guanfu/main.go`：在 subcommand switch 加分支，复用 `runEquityAsset` 或写专用 runner。
5. `cmd/guanfu/main.go: equityPanelHeader`：加 case 处理标题 + price 字段映射。
6. `pkg/forecast/forecast.go: assetHorizons`：如果该资产想要 per-asset horizons，加 entry。
7. `pkg/engine/backtest_bundles_test.go`：加 baseline 测试 case。
8. `cmd/guanfu-mcp/main.go`：tool description 的 `enum` 列表加新 key；resources/list 加 per-asset URI。
9. `skill/SKILL.md` + `README.md`：消费方文档。

`cmd/guanfu-mcp/main_test.go` 现有断言会自动覆盖 alias / per-asset URI 等。

## 加新 feature extractor checklist

1. `pkg/forecast/features/{enhanced,china_macro,cross_asset}.go` 任一里写 `func XExtractor(s *PriceStore) FeatureExtractor`。
2. extractor 内部如果数据缺失 **必须返回 `nil, false`**——bundles.go 会自动 skip 而不是 panic。
3. 加进对应资产的 bundle 函数。
4. 跑 `TestBacktestBundles`，看回测有没有恶化超预算。

## 已知陷阱（写代码前先看）

| 陷阱 | 后果 | 出处 |
|---|---|---|
| `opts = forecast.DefaultOptions()` 替换整个 opts | clobber 掉 `Extractors` / `TopK` | B5 fix |
| 用硬编码核心资产列表防撞名（btc/qqq/...） | 新加核心资产时 stock 校验脱节 | D1 设计 |
| `SnapshotData` ≠ `MarketSnapshot`，前者改字段不要 bump SchemaVersion | bump 会触发 history.db 重建，浪费 | A0 review |
| `printEquityPanel` 标题硬编码 | 用 `panel.Asset` switch | A1 fix |
| sma_200_dev 在 BuildEquityPanel 已 ×100，display 层不能再 ×100 | 双重缩放 | A2 fix |
| `--forecast-horizons "30,90,180"` 当默认 → per-asset 默认永远不生效 | B5 引入 `auto` sentinel | B5 fix |
| `pkg/store/price_test.go: TestIncrementalFetchDays` 依赖 today 距离 fixture date | 时间漂移会假失败，**不是回归** | 历史遗留 |
| 任何 v1 backwards-compat shim（如 `// removed` 占位、再导出已删类型） | 增加心智负担，没有消费方 | global rule |

## 文档 / 决策参考

| 文档 | 内容 |
|---|---|
| `README.md` | 用户视角的功能 + 安装 |
| `skill/SKILL.md` | **skill 消费方文档**(AI 用 guanfu 数据回答用户的问题,不是改 guanfu) |
| `skill/tier1.md` / `tier2.md` | MCP 分层必载上下文(tier1 数据契约,tier2 决策框架 + 行为护栏) |
| `docs/archive/v3/guanfu-v3-todo.md` | **v3 已完成 roadmap + 改动审计**——已归档，参考用 |
| `docs/v4-thinking.md` | v4 方向 decision log(不是 roadmap,等 90d 实测再拍板) |
| `docs/audience.md` | 用户画像 Primary/Secondary/Tertiary + 设计优先级 |
| `docs/DATA-SOURCES.md` | 30+ 数据源一览 + refresh 框架 |
| `docs/backtest-methodology.md` | 回测口径 + walk-forward 矩阵 |
| `docs/internals.md` | 隐式约定:weight 归一化 / BTC ad-hoc 源 / schema 演化 |
| `docs/deployment.md` | 部署 + MCP 集成 + cron 定时 |
| `docs/archive/v2/` | v2 历史文档 + 调研(btcdca / forecast-roadmap 等) |

## 文件索引（quick jump）

| 文件 | 责任 |
|---|---|
| `pkg/engine/asset.go` | Asset 接口 + 注册表 |
| `pkg/engine/asset_{btc,qqq,spy,gold}.go` | 4 个核心资产实现 |
| `pkg/engine/equity_panel.go` / `equity_dashboard.go` / `equity_valuation.go` | 权益类共用 panel 构造 |
| `pkg/engine/calculator.go` | 各类指标计算（MA / RSI / Mayer / drawdown ...） |
| `pkg/engine/verdict.go` | BTC verdict 构造 |
| `pkg/engine/backtest.go` / `backtest_bundles_test.go` | 回测引擎 + 基线测试 |
| `pkg/forecast/forecast.go` | kNN 主流程 + Options + HorizonsForAsset |
| `pkg/forecast/features/bundles.go` | 每资产 extractor bundle |
| `pkg/forecast/features/{core,enhanced,cross_asset,positioning}.go` | extractor 实现 |
| `pkg/forecast/projection.go` | ASCII fan chart 路径推演 |
| `pkg/forecast/regime.go` | regime classification |
| `pkg/forecast/backtest/backtest.go` | 独立回测引擎（vs `pkg/engine/backtest.go`） |
| `pkg/client/btc_history.go` | BTC 历史拉取（CoinMetrics + Binance） |
| `pkg/client/fetch_stock.go` | 任意美股 Yahoo fetch + namespace（D1） |
| `pkg/client/fred.go` `gold.go` `cross_asset.go` | 各数据源专用 |
| `pkg/client/refresh.go` | 统一刷新框架（Source 接口 + RefreshAll） |
| `pkg/client/refresh_native.go` | BTC/Gold/Stock 的 Source 实现 |
| `pkg/client/yahoo_history.go` | YahooETFSource（QQQ/SPY/VIXY/TLT/UUP/CNY=X） |
| `pkg/client/fred_history.go` | FREDSource（DXY/DGS10/DFII10/curve/breakeven/HY） |
| `pkg/client/cape_history.go` | CAPESource（Shiller CAPE 走 Python script） |
| `scripts/import_cape.py` | Shiller CAPE 一次性导入（XLS → JSON）|
| `pkg/store/price.go` | PriceStore JSON 持久化 |
| `pkg/model/types.go` | `MarketSnapshot` / `IndicatorPanel` / `Indicator` / `SnapshotData` |
| `pkg/history/` | history.db SQLite（指标历史归档） |
| `cmd/guanfu/main.go` | CLI 入口 + subcommand routing |
| `cmd/guanfu/cli_backtest.go` `cli_commands.go` | 子命令实现 |
| `cmd/guanfu-mcp/main.go` | MCP server |
| `scripts/import_cape.py` | Shiller CAPE 一次性导入 |

## 风格 / 写代码默认

- **不写没必要的注释**。函数名 + 类型已表达 what；只在 *why* 非显著时才加（隐藏约束 / workaround / 反直觉的 invariant）。
- **不写 v1 backwards-compat shim**。删的就删干净，包括 `// removed` 占位。
- **不写 defensive validation 给内部代码**——只在系统边界（用户输入、外部 API）校验。
- **不要批量并行调多个功能重叠的 skill**（这是 skill 消费方规则，对维护者写代码也有提醒意义：抽象重叠会导致改动扩散）。
- 优先**编辑现有文件**，不要新建小文件加重 cognitive load。
- 测试加在最近的 `_test.go` 里；若该资产还没 test 文件，新建 `pkg/<dir>/<key>_test.go`。
