# 观复 v2 多资产完整改造方案

> **状态: CLOSED (2026-05-09)** — v2 所有 Track (A/B/C/D) 已全部合入 main。后续工作见 [`guanfu-v3-todo.md`](./guanfu-v3-todo.md)。此文件冻结保留作为历史记录,不再更新。

```text
P0 ▸ 显示/逻辑 bug         P2 ▸ 预测精度提升
P1 ▸ 功能/数据缺失         P3 ▸ 架构扩展/调研
```

---

## Track A — 读盘修复（剩 4 项 active；A2 ✅ Done eb69e98；A5 ❌ Removed v5 - 实测无 gap；A6 已合入 D3）

| 优先级 | # | 问题 | 根因 | 修法 | 依赖 | 文件 |
|---|---|---|---|---|---|---|
| **P0** | A0 | `SnapshotData` 没有 `HS300Price`；`IndicatorPanel` 没有 `Asset` 字段——A1 没法识别该读哪个 Snapshot 字段 | 多资产架构遗留缺口 | 在 `pkg/model/types.go`：(1) `SnapshotData` 加 `HS300Price float64 \`json:"hs300_price,omitempty"\``；(2) `IndicatorPanel` 加 `Asset string \`json:"asset,omitempty"\``。**不**动 `MarketSnapshot`/`CurrentMarketSnapshotSchemaVersion`——前者是 BTC 链路市场快照缓存，与此无关；只改 `SnapshotData` 不影响落盘 | — | `pkg/model/types.go` |
| **P0** | A1 | Gold/HS300 价格显示 $0.00 + panel 标题硬编码 "权益 ETF 盘面" 对 HS300/BTC/Gold 错标 | `printEquityPanel` 行 427/429 硬编码 `SPYPrice+QQQPrice`；标题硬编码 "权益 ETF" | (1) 用 `panel.Asset`（A0 加）switch 选字段：qqq→QQQPrice、spy→SPYPrice、gold→GoldPrice、hs300→HS300Price；fallback 走 `BTCPrice`；(2) **同时参数化标题**：qqq/spy→"权益 ETF 盘面"、gold→"黄金盘面"、hs300→"沪深300盘面"、btc→"BTC 盘面"；(3) 所有 BuildPanel 路径必须填 `panel.Asset` | A0 | `cmd/guanfu/main.go` + 所有 `asset_*.go` BuildPanel |
| ~~P0~~ | ~~A2~~ | ~~sma_200_dev 显示 100x~~ | ~~`formatValue` 行 727 `v*100` 双倍算~~ | ✅ **Done in commit `eb69e98`** | — | — |
| **P1** | A3 | Gold 读盘缺 DXY 显示 | `asset_gold.go:83` 读 `as.CrossAssetPrices["uup"]`，但 PriceStore 里没有 `uup.json`；`fred_dxy.json` 存在却没注入；现有 `BuildEquityPanel` 已有 `if in.DXY > 0 { panel.Macro["dxy_proxy"] = ... }` (equity_panel.go:110) | 在 `asset_gold.go.FetchSnapshot` 中读 `store.Latest("fred_dxy")` → 注入 `CrossAssetPrices["uup"]`（key 沿用现有 "uup" 命名）。**复用现有路径**写 `panel.Macro["dxy_proxy"]`；**不要**新建 `panel.CrossAsset["dxy"]`，会与 `dxy_proxy` 重复 | — | `asset_gold.go` |
| **P1** | A4 | HS300 读盘不显示 PMI/M2/LPR + cny_usd 也是 "待接入" | `asset_hs300.go.FetchSnapshot` (行 32-47) **完全没** populate `CrossAssetPrices`，所以 `in.CNYUSD = 0` → dashboard 写占位符 "待接入"；中国宏观数据 PMI/M2/LPR/Northbound/CNY 已全部存在 PriceStore (`hs300_pmi.json`、`hs300_cny.json` 等) | **字段方案（不引 PriceStore 句柄进 dashboard）**：(1) `asset_hs300.go.BuildPanel` 用 `a.store.Latest("hs300_pmi"/"hs300_m2"/"hs300_lpr"/"hs300_northbound"/"hs300_cny")` 预解析；(2) 在 `HS300DashboardInput` 加 `PMI/M2/LPR/Northbound/CNYUSD float64` 字段（5 个，含 cny_usd）；(3) `BuildHS300Dashboard` 保持纯函数，按字段写 `panel.Macro` | — | `asset_hs300.go`, `hs300_dashboard.go` |
| **P1** | Ax | HS300 panel 输出严重稀薄——只有 3 个 technical (macd/rsi_14/volatility_30d) + 1 个 valuation (sma_200_dev)，对比 Gold 的 9 个 technical | 实测发现：HS300 history=5899 (>200) 应走 `BuildHS300Dashboard` 路径，但实际输出像走了 BuildEquityPanel fallback；或 dashboard 本身就只产出这些 | **调查项（不直接落代码）**：(1) 跑 `guanfu hs300 --json` 看 `panel.Asset` 字段是 hs300 还是空；(2) 看 panel.Technical/Valuation map 完整 keys；(3) 决定是补 BuildHS300Dashboard 输出还是改 panel printer。**调查后再开 ticket**，不在 Wave 0 强行落地 | A4 验证副产物 | `asset_hs300.go`, `hs300_dashboard.go` |

---

## Track B — 预测特征调优（6 项）

| 优先级 | # | 改动 | 当前 | 目标 | 回测预期 | 依赖 |
|---|---|---|---|---|---|---|
| **P1** | B1 | Gold: DXY weight 0.45 → ≥0.7 | DXY 与 RSI 同权重 | DXY 是 gold 最主导变量 | 180d PIT 0.68 → 0.60（**注**：PIT 目标 ≈ 0.5，"下降" = 更校准，不是变差） | 权重预算（见 § 权重总和约束） |
| **P1** | B2 | QQQ/SPY: CAPE weight 0.55 → ≥0.8 | CAPE 与 drawdown 同权重 | 长端 CAPE 最重要 | 180d PIT 0.59 → 0.55（同上） | 权重预算 |
| **P1** | B3 | EquityExtractors 加 VIX | 只有 Gold 有 VIX | 美股也需要风险偏好区分 | 30d dir +2~5% | 权重预算 + FetchSnapshot 已写 VIXY |
| **P3** | B4 | HS300 日频特征调研 | PMI/M2/LPR 月频→kNN 近邻实质是"时间近" | 先验证 `hs300_volume` 方向命中率；可行再扩到北向/波动率/期货贴水 | 不确定，先验证 | — |
| **P2** | B5 | Per-asset horizon | 全部 30/90/180 | QQQ 加 63d(季度)/252d(年度)；Gold 加 60/120 | PIT 校准改善 | 见 B5 实现注 |
| **P3** | B6 | LearnWeights RFC | `Option.LearnWeights` 存在但 `Build()` 不检查 | 需 RFC 明确技术选型（线性回归系数? 互信息?）后再落地 | 不确定性大 | — |

### B5 实现注

`BuildForecast` 链路里有 `if len(opts.Horizons) == 0 { opts = forecast.DefaultOptions() }`，会把传入的 horizons 清空时回落到 30/90/180。Per-asset horizon 落地时需要在 asset 层显式构造 `forecast.Options{Horizons: ...}` 并 **不能** 走 DefaultOptions 路径，否则被覆盖。

### B4 说明

HS300 日频特征（volume、波动率等）需要先验证有效性再做——跑一个简化 backtest 只带 volume 特征看方向命中率是否优于随机。**如果 volume 无效则整个 B4 放弃**，接受 HS300 45% dir hit 为天花板。

### B6 RFC 待定项

- 学习方法：线性回归 coefficient / 随机森林 importance / 互信息
- 训练窗口：全局 vs 滚动
- 特征缺失处理
- 回测验证方法

---

## Track C — AI 通路（6 项）

| 优先级 | # | 改动 | 现状 | 目标 | 依赖 | 文件 |
|---|---|---|---|---|---|---|
| **P0** | C1 | MCP 工具加别名 + 重描述 + **alias 单测** | 工具名 `get_btc_panel` 误导 AI 认为仅支持 BTC；测试只覆盖 `get_btc_*` | dispatch 加别名 `get_panel`/`get_btc_panel`（向后兼容）；描述明确标注支持 `asset=qqq/spy/gold/hs300/btc`；`main_test.go` 加 `get_panel/get_verdict/get_forecast` 等价 case | — | `cmd/guanfu-mcp/main.go` + `main_test.go` |
| **P1** | C2 | MCP 新增 get_stock_forecast | 没有任意 stock 入口 | `get_stock_forecast(ticker, horizons)` → Yahoo fetch + kNN forecast；**+ 单测**（mock fetch + assert forecast shape） | D1+D2+D3 | `cmd/guanfu-mcp/main.go` + `main_test.go` |
| **P1** | C3 | resource/latest 支持资产参数 + list resources 列举 | `panel/latest` 硬编码 btc | 加 `panel/latest/qqq`/`spy`/`gold`/`hs300` URI；`resources/list` 返回所有可用 asset 资源；**+ 单测**（per-asset URI 命中 + list 输出 assert） | C1 后 | `cmd/guanfu-mcp/main.go` + `main_test.go` |
| **P1** | C4 | SKILL.md 重写（**与 C1 同 commit**） | 只有 CLI 用法，无 MCP 工具节 | 拆 3 节：CLI 命令、MCP 工具表（输入/输出/示例）、kNN 原理 | C1 | `skill/SKILL.md` |
| **P2** | C5 | 新建 CLAUDE.md | 不存在 | 项目架构、文件映射、AI 助手决策参考；面向 **维护者/AI coder**（与 C4 面向 skill 消费方不同） | D 系列落地后写更有信息量 | `CLAUDE.md` |
| **P3** | C6 | JSON output 标准化 | keys 大小写不统一 (`technical` vs `Technical`) | **先列消费方清单**（MCP 客户端、SKILL skill、外部脚本、`reports/` 输出），然后才能评估破坏性范围；不列清单不动手 | 消费方清单 | `model/panel.go` + CLI |

### C1 实现方案

```go
// dispatch — 新旧名字共存
case "get_panel", "get_btc_panel":
    // ... 同一 handler
case "get_verdict", "get_btc_verdict":
    // ... 同一 handler
case "get_forecast", "get_btc_forecast":
    // ... 同一 handler
```

**不删除旧名**，至少保留一个版本。AI 可以任选一个名字调用。

### C1 单测要求

`main_test.go` 现仅测 `get_btc_panel/verdict/forecast`。alias 加完后必须加：

```go
for _, name := range []string{"get_panel", "get_verdict", "get_forecast"} {
    out, rpcErr := handleToolCall(name, json.RawMessage(`{}`))
    if rpcErr != nil { t.Fatalf(...) }
    // 至少 assert 输出非空、含 panel/verdict/forecast key
}
```

否则 future refactor 误删 alias 不会被捕获。

---

## Track D — 任意美股推广（4 项）

| 优先级 | # | 改动 | 说明 | 依赖 | 文件 |
|---|---|---|---|---|---|
| **P2** | D1 | Yahoo auto-fetch + TTL 缓存 + **namespace 防撞** | `FetchAndCacheStock(ticker, days)` → 复用 `fetchYahooChart` → PriceStore Save + TTL 检查；ticker 不能与现有核心资产 key 冲突 | 见 § D1 namespace 选项 + `s.ListAssets()` 动态校验 | `pkg/client/fetch_stock.go` (新建) |
| **P2** | D2 | USStockExtractors | `GenericTechnical + DGS10 + DXY + HY spread + yield curve + VIX` (13 feat, 无 CAPE) | — | `bundles.go` |
| **P2** | D3 | readStock 赋能 | 先查 PriceStore, 无→auto fetch→forecast；切到 `USStockExtractors`；**同时实现 A6**（用 `BuildEquityPanel` 出 panel） | D1+D2 | `main.go` |
| **P2** | D4 | import-stock CLI（**Wave 1**，与 D1 同 wave） | `guanfu import-stock AAPL` → 手动 trigger Yahoo 全量拉取 | D1 | `main.go` |

### D1 namespace 选项

核心资产当前存为 `btc.json/qqq.json/spy.json/hs300.json/gold.json` —— **无前缀**。任意 stock 加进来必须 namespace 隔离，否则 `import-stock BTC` 会覆盖核心数据。两个方案：

| 方案 | 实现 | 利 | 弊 |
|---|---|---|---|
| **A. 平铺前缀** | `stock_AAPL.json` 与 `btc.json` 同目录 | 改动小，PriceStore 不动 | 命名混乱，核心/stock 风格不一致 |
| **B. 子目录** | `~/.guanfu/prices/stocks/AAPL.json`；PriceStore 增 namespace 概念，多用一层 dir | 干净，长期可扩（option/futures 也能加目录） | PriceStore 改动大，影响 ListAssets / Load / Save 接口 |

**推荐方案 A**（最小代价 + 立即可用）；如果未来 D 系列扩到 option/futures，再走 RFC 升级到方案 B。

### D1 TTL 逻辑

```text
FetchAndCacheStock(ticker, days):
  1. 校验 ticker 不冲突：existing := s.ListAssets()；若 ticker（大小写无关）∈ existing → reject
     —— 用动态查询不用硬编码列表，新增核心资产时不会脱节
  2. namespacedKey := "stock_" + strings.ToLower(ticker)   # 方案 A
  3. PriceStore.Load(namespacedKey)
  4. 有数据且最新日期 ≤1d ago → 返回
  5. 有数据但过期 → 增量 fetch 缺失天数 → PriceStore.Append
  6. 无数据 → Yahoo chart 全量 fetch (days) → PriceStore.Save
```

D1 的 namespace 设计要求 D3/D4 的 `readStock`/`import-stock` 入口在传 ticker 给 PriceStore 时统一通过 `FetchAndCacheStock` 包装，不允许 bypass。

---

## 执行路径

### 第 0 波 — AI 通路解锁 + 低成本修复

每个 weight 改动单独跑回测，便于定位回归源；alias 改动同时落 C4 否则 AI 消费方看不到。

```
A0   ▸ SnapshotData.HS300Price + IndicatorPanel.Asset 字段  [先于 A1；不动 SchemaVersion]
C1   ▸ MCP 工具加别名 + 重描述 + alias 单测                   [~5 行 dispatch + ~10 行 test]
C4   ▸ SKILL.md 重写                                         [文档；与 C1 同 commit]
A1   ▸ Gold/HS300 价格显示 + panel 标题参数化                  [+~10 行 main.go + 各 BuildPanel 填 Asset]
A3   ▸ Gold FetchSnapshot 注入 fred_dxy → CrossAssetPrices["uup"]  [复用 dxy_proxy 路径，不新建 CrossAsset["dxy"]]
A4   ▸ HS300 panel 补 PMI/M2/LPR/Northbound/CNYUSD（字段方案）  [~25 行；asset_hs300.go 预解析 5 个 + dashboard input 加 5 字段]
Ax   ▸ HS300 panel 稀薄度调查（不必落代码）                    [跑 --json 看 panel 完整 keys，决定下一步]
B1   ▸ Gold DXY weight 0.45→0.7                              [1 行] ↳ 单独跑回测
B2   ▸ CAPE weight 0.55→0.8                                  [1 行] ↳ 单独跑回测
B3   ▸ EquityExtractors 加 VIX                                [+1 行 bundles.go] ↳ 单独跑回测
```

A2 已在 `eb69e98` 落地，不在本 wave。
A5 已实测确认无 gap（QQQ panel.Macro 已有 `vix_level`），v5 移除。

### 第 1 波 — 任意美股 + 文档

```
D1   ▸ Yahoo auto-fetch + TTL + namespace      [新建 fetch_stock.go, ~80 行]
D2   ▸ USStockExtractors                       [+10 行 bundles.go]
D3   ▸ readStock 升级 + auto-fetch + panel(A6) [~30 行 main.go；A6 合入]
D4   ▸ import-stock CLI                        [+~10 行 main.go]
C2   ▸ get_stock_forecast MCP                  [+~30 行 mcp/main.go；含单测]
C3   ▸ resource/latest 参数化 + list           [+~20 行 mcp/main.go；含单测]
```

### 第 2 波 — 精度 + 文档

```
B5   ▸ Per-asset horizon                       [~10 行；注意不要走 DefaultOptions 路径]
C5   ▸ CLAUDE.md                                [文档；D 系列落地后写]
```

### 待定（需额外调研后决定是否做）

```
B4   ▸ HS300 日频特征                   [先验证 volume 有效性]
B6   ▸ LearnWeights                     [需 RFC 明确技术选型]
C6   ▸ JSON 标准化                      [先列消费方清单，再评破坏性]
```

A6（stock panel）已合入 D3，不再独立 wave。

---

## 验证方法

每次改动后运行基准 backtest 验证回归：

```bash
go test ./pkg/engine -run TestBacktestBundles -v
```

预期基准线（v6 更新于 2026-05-09，refresh 拉全量历史后）：

| 资产 | n_tests | 30d dir | 90d dir | 180d dir | 数据起始 |
|---|---|---|---|---|---|
| BTC | 46 | 61% | 65% | 63% | 2010-07-18 |
| QQQ | 20 | 70% | 75% | 80% | 2015-05-26 |
| SPY | 20 | 60% | 75% | 85% | 2015-05-26 |
| Gold | 51 | 51% | 55% | 49% | 2000-08-30 |
| HS300 | 47 | 47% | 45% | 49% | 2002-01-04 |

> **Gold 历史扩展说明 (2026-05-09)**：Yahoo XAUUSD=X 退市后切到 GC=F 期货，gold history 从 2752d (2015+) 扩到 6446d (2000+)，backtest 测试数从 20 增至 51。原 75% / 65% / 55% baseline 是单一 2020-2025 regime 的样本（n=20）；新数字覆盖 2013-2025 的多 regime（含 2013-2015 黄金熊市），是更诚实的 out-of-sample 估计。SPY 30d 从 70%→60% 是 n=20 下 1 个测试翻转，统计噪声范围内。

### 回归预算（regression budget）

| 指标 | 容忍下降 | 触发动作 |
|---|---|---|
| 任何 horizon dir hit（5 baseline 资产） | ≥3 个百分点 | 回滚该改动，单独再 review |
| PIT 偏离 0.5 加大 | ≥0.05 | 回滚或调权重 |
| 任意资产 backtest 失败 / panic | 任何 | 立即回滚 |
| **D2 任意 stock 路径 dir hit** | 低于 GenericTechnicalExtractors-only baseline | 回滚 D2，重审特征组合 |

每个 Wave 0 weight 改动**独立提交**，便于 `git revert` 单点回滚。

### D2 验收测试（baseline 之外）

`TestBacktestBundles` 覆盖 BTC/QQQ/SPY/Gold/HS300 五个固定资产，**不覆盖** D2 引入的任意 stock 路径。Wave 1 落 D2 时必须新增：

```go
// pkg/forecast/features/bundles_stock_test.go
func TestUSStockExtractors_AcceptanceAAPL(t *testing.T) {
    // 1. 加载 stock_aapl.json（Wave 1 由 D1 注入）
    // 2. 跑 USStockExtractors vs GenericTechnicalExtractors-only baseline
    // 3. assert: USStockExtractors dir hit (30d/90d/180d) ≥ baseline
}
```

代表 ticker 选 AAPL + MSFT（5y+ 历史、低 idiosyncratic 风险）。**未通过此测试不允许 D2 落地**。

### 权重总和约束

EquityExtractors 当前权重：CAPE 0.55, DGS10 0.45, DXY 0.45, HY 0.55, Curve 0.45（合 2.45）。Wave 0 同时执行 B2（CAPE→0.8）+ B3（VIX 0.55）后，合 ≈ 3.25，分布严重失衡。

约束方案（任选其一，按 RFC 决定）：

1. **绝对权重无所谓**——kNN 距离归一化已隐式做 normalization，weight 只影响 feature 间的相对权。直接调，回测验证。
2. **再加规一化层**——`forecast.Build` 内部把 weight 总和 rescale 到固定值（例如 2.5）。需要修 `pkg/forecast/distance.go` 的距离计算。

Wave 0 走方案 1（最小代价）；如果回测掉超回归预算，回退到方案 2 走 RFC。

---

## 文件索引

| 文件 | 改动 | 波次 |
|---|---|---|
| `cmd/guanfu/main.go` | printEquityPanel price 显示 + 标题参数化 (A1, 用 panel.Asset switch), readStock 升级 (D3), import-stock CLI (D4)；A2 已在 `eb69e98` 落地 | 0, 1 |
| `cmd/guanfu/akshare_import.go` | 工作树未追踪的 CSI300 一次性导入脚本（`// +build ignore`），是早期 hs300.json 的来源；建议入 git 但加 README 说明它是 ad hoc 工具 | 0 |
| `cmd/guanfu-mcp/main.go` | 工具别名 + 重描述 (C1), resource 参数化 (C3), get_stock_forecast (C2) | 0, 1 |
| `cmd/guanfu-mcp/main_test.go` | alias 单测 (C1) | 0 |
| `pkg/model/types.go` | SnapshotData.HS300Price + IndicatorPanel.Asset (A0)；**不动** MarketSnapshot/SchemaVersion | 0 |
| `pkg/engine/asset_gold.go` | FetchSnapshot 注入 fred_dxy → CrossAssetPrices["uup"] (A3)；BuildPanel 填 panel.Asset="gold" (A0/A1) | 0 |
| `pkg/engine/asset_hs300.go` | BuildPanel 预解析 hs300_pmi/m2/lpr/northbound/cny + 填 panel.Asset="hs300" (A0/A1/A4) | 0 |
| `pkg/engine/hs300_dashboard.go` | HS300DashboardInput 加 PMI/M2/LPR/Northbound/CNYUSD 5 字段（字段方案，不引 PriceStore 句柄）(A4) | 0 |
| `pkg/engine/asset_qqq.go` / `asset_spy.go` | BuildPanel 填 panel.Asset (A0/A1)；A5 已 v5 移除（实测 vix_level 已显示） | 0 |
| `pkg/forecast/features/enhanced.go` | DXY weight (B1), CAPE weight (B2)；(已有 VIX/hy_spread) | 0 |
| `pkg/forecast/features/bundles.go` | EquityExtractors 加 VIX (B3), USStockExtractors (D2) | 0, 1 |
| `pkg/client/fetch_stock.go` | Yahoo auto-fetch + TTL + namespace (D1) (新建) | 1 |
| `skill/SKILL.md` | MCP 工具节 + CLI 更新 (C4)；与 C1 同 commit | 0 |
| `CLAUDE.md` | 项目上下文 (C5) (新建) | 2 |
| `scripts/import_cape.py` | CAPE ingestion | — |

---

## 变更记录

| 版本 | 日期 | 变更 |
|---|---|---|
| v1 | 2026-05-08 | 初版，4 Track x 22 项 |
| v2 | 2026-05-08 | 加 Dependency 列；C1 加别名不改名；B4 降 P3 + 加验证说明；B6 摘出需 RFC；D1 加 TTL；执行顺序重排为 0/1/2/待定 四波 |
| v3 | 2026-05-08 | Review 落地修复：(1) **A0 新增** SnapshotData schema 改动作为 A1 前置；(2) **A2 修订**——working tree 改动需独立 commit；(3) **A3/A4 修订**——纠正"需要数据 ingestion"的误读，数据已在 PriceStore，只缺 panel 显示；(4) **A6 合入 D3**，不再独立；(5) **C1 加 alias 单测**要求；(6) **C4 与 C1 同 commit**；(7) **D1 加 namespace 防撞**；(8) **D4 显式 Wave 1**；(9) **新增"回归预算"** + **"权重总和约束"** 两节；(10) **B5 加实现注**（DefaultOptions 覆盖陷阱）；(11) **B1/B2 PIT 方向澄清**；(12) **C5 audience 注**；(13) **C6 要求消费方清单先行**；(14) **akshare_import.go 入文件索引**；(15) Wave 0 weight 改动**单独提交单独回测**。 |
| v4 | 2026-05-08 | 二次 review 落地的 10 项修复，主要是**追到调用链**后发现 v3 修法路径错：(1) **A3 修法重写**——通过 `CrossAssetPrices["uup"]` 注入复用 `panel.Macro["dxy_proxy"]`，**不**新建 `panel.CrossAsset["dxy"]`（重复 + 错域）；(2) **A5 改"先实测验证"**——`equity_panel.go:103-108` 已写 `vix_level`，A5 premise 可能根本不存在；新增 A5* 验证步骤前置；(3) **A0 同时加 `IndicatorPanel.Asset` 字段**——A1 必须依赖此字段才能选对 Snapshot 字段；(4) **A0 去掉 SchemaVersion bump**——`SnapshotData ≠ MarketSnapshot`，仅改前者无需 bump 也不影响落盘；(5) **A4 选定字段方案**——`asset_hs300.go.BuildPanel` 预解析后填 dashboard input 字段，dashboard 保持纯函数；(6) **A2 标 ✅ Done eb69e98**，从 active list 移除；(7) **A5 删 B3 依赖**——A5 (panel) 与 B3 (forecast) 正交；(8) **D1 加子目录 namespace 选项 trade-off**——保留 key 改用 `s.ListAssets()` 动态查；(9) **C2/C3 row 加 "+ unit test"** 字样对齐 C1；(10) **回归预算补 D2 验收 test**——`TestBacktestBundles` 不覆盖任意 stock 路径，新增 `bundles_stock_test.go`。 |
| v5 | 2026-05-09 | 最终 review 实测验证后的 5 项收尾：(1) **A5 整行删除**——实测 `guanfu qqq` 输出 `vix_level 17.4000`，A5 premise 完全 obsolete，不再保留 verify-then-delete 兜底；(2) **A4 范围扩 cny_usd**——实测 HS300 显示 `cny_usd 待接入`，根因是 `asset_hs300.go.FetchSnapshot` 完全没 populate `CrossAssetPrices`；A4 字段方案从 4 字段（PMI/M2/LPR/Northbound）扩到 5 字段（+ CNYUSD），对应 `hs300_cny.json` 数据已存在；(3) **新增 Ax (P1)**——HS300 panel 输出严重稀薄（仅 3 个 technical vs Gold 9 个），定为调查项不直接落代码；(4) **A1 顺手做 panel 标题参数化**——`printEquityPanel` 标题硬编码 "权益 ETF 盘面"，对 HS300（指数）/Gold/BTC 错标，A1 用 `panel.Asset` switch 时一并修；(5) **Track A header 数字调整**——剩 4 项 active（A0/A1/A3/A4 + Ax 调查项），A5 删除、A2 已 done、A6 已合 D3；(6) Wave 0 删 A5/A5\* 行，加 Ax。 |
