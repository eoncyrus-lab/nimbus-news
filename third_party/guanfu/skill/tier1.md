---
name: guanfu-tier1
description: guanfu 数据契约 + 关键指标阈值。默认必载。读其他 tier 前先读这个。
tier: 1
---

# guanfu · Tier 1 — 数据契约 + 关键指标阈值

> 这是 Claude / AI 读盘时**必载**的最小上下文。~200 行。覆盖字段定义、指标阈值、可靠性标注、source_health、必出规则。tier 2 / 3 按需补充。

## 1. 资产范围

- **BTC**:8 域 40+ 指标(Cycle / Valuation / Network / Positioning / Macro / Flow / Technical / CrossAsset)
- **QQQ / SPY**:6 域(Technical / Macro / Valuation / Positioning / 其余 BTC 专域不适用)
- **Gold**:同 QQQ/SPY 但 Valuation 侧重实际利率 / DXY / COT
- **任意美股**(`stock TICKER`):Technical + Macro(无 CAPE,无 per-name 基本面)

读盘必须同时加载对应 `skill/profiles/*.md`:BTC 用 `btc.md`,QQQ/SPY 用 `equity_index.md`,Gold 用 `gold.md`,任意美股用 `us_stock.md`。不要把 BTC 的 halving/AHR/network 语义套到非 BTC 资产。

Panel JSON 会带 profile metadata:`profile_key`、`profile_version`、
`asset_class`、`skill_profile_uri`、`domain_meta`。`domain_meta` 是当前资产
的读盘 lens；AI 应按它决定哪些 domain 有语义,不要假设所有资产都有 BTC 的
cycle/network/flow/cross_asset。

Forecast JSON 会带 profile metadata:`profile_key`、`profile_version`、
`asset_class`、`feature_bundle`、`skill_profile_uri`。这些字段来自
`pkg/assetprofile/profile.go`;AI 消费方应优先按 `skill_profile_uri` 选择 profile。

## 2. 盘面字段结构

每个指标 `Indicator`:
- `value` — 原始数值(**无 sigmoid 无 scaling**)
- `q` — 历史分位 [0, 1]:q05 = 历史最低 5%,q95 = 历史最高 5%
- `label` — 简短解读(**Claude 不应直接信赖 label 决策**,应自己读 value + q)
- `source` / `updated_at` / `note`

盘面顶层:
- `asset` — "btc"/"qqq"/"spy"/"gold"/"stock_aapl"/...
- `stale_warnings` — 非致命数据缺失 / 过期
- **`source_health`** — 每个数据源的状态表:
  - `status` ∈ ok / partial / stale / missing / warning
  - `as_of`、`fallback_used`、`impact`、`note`、`warnings`
  - **读盘前必须先检查**,避免用 stale / fallback 做强结论

## 3. 可靠性标注(必读)

`HorizonForecast` 上的两个字段:
- `reliability_note`(字符串,非空表示需要 caveat):
  - "样本不足" — n < 10
  - "接近随机" — 0.50 ≤ dir_hit < 0.55
  - "信号强度低于随机阈值" — dir_hit < 0.50
- `hard_blocked`(bool):`dir_hit < 0.50` 时 true
  - **true 时不要输出 median / p10 / p90 的具体数值**
  - 只说 "该 horizon 在历史回测中方向命中率低于随机,不给数值预测" + 附 reliability_note
  - ⚠ **契约提示**:即使 `hard_blocked=true`,`median_return_pct` / `p10_return_pct` / `p90_return_pct` / `expected_price` 等字段**仍在 JSON 中保持原值**(保留给 claim ledger 做事后回归校准)。Claude / MCP 消费方**必须先判 `hard_blocked`**,再决定是否渲染数值,不能直接取 median 字段。

### 当前 (asset, horizon) 可靠性表(截至 2026-05-11)

| 资产 | 30d | 90d | 180d | 特殊 |
|---|---|---|---|---|
| BTC | 61% ✓ | 61% ✓ | 63% ✓ | — |
| QQQ | 70% ✓ | 75% ✓ | **80%** ✓ | 也有 63d / 252d |
| SPY | 60% ✓ | 75% ✓ | **85%** ✓ | 也有 63d / 252d |
| Gold | **45% ✗** | 63% ✓ | 53% ⚠ | 30d hard-block；默认只用 30/60/90/120 时仍必须尊重 hard_block |
✓ = 可用 | ⚠ = 接近随机需附 caveat | ✗ = hard-block,不输出数值

真源:`pkg/assetprofile/profile.go`。`pkg/forecast/reliability.go` 只负责把
profile row 转成 `reliability_note` / `hard_blocked`。

## 4. 关键指标阈值(决策密度最高的 15 个)

### BTC Valuation

| 指标 | 极低估 | 低估 | 合理 | 偏高 | 高估 | 泡沫 |
|---|---|---|---|---|---|---|
| **ahr999_compressed**(主) | <0.549 | 0.549-0.846 | 0.846-1.147 | 1.147-1.682 | 1.682-3.344 | >3.344 |
| mayer_multiple | <0.6 | 0.6-1.0 | 1.0-2.4 | — | >2.4 | — |
| sma_200w_dev | <0 | 0-1.0 | 1.0-1.5 | — | >1.5 | — |
| pi_cycle_top_ratio | — | <1 | — | — | ≥1 触发 | — |

### BTC Positioning

| 指标 | 恐慌 | 正常 | 过热 | 极端 |
|---|---|---|---|---|
| funding_rate_pct (% /8h) | <-0.01 | -0.01~0.005 | >0.05 | >0.1 |
| oi_to_mc | <0.015 | 0.015-0.025 | 0.025-0.035 | >0.04 |
| fear_greed | <20 | 20-80 | — | >80 |

### Equity Options(QQQ / SPY)

| 指标 | Call chase / 自满 | 中性 | Hedging / 恐慌 |
|---|---|---|---|
| put_call_ratio | <0.7 | 0.7-1.2 | >1.2 |
| put_call_252d_percentile | <10% | 10-90% | >90% |

`put_call_ratio` 存储 key 仍叫 `stooq_putcall`,但默认来源是 CBOE official total put/call,无需 `STOOQ_APIKEY`。如果 `source_health.forecast_bundle_stooq_putcall` stale/missing,QQQ/SPY 期权情绪证据必须降级。

### Macro(BTC / Equity 通用)

| 指标 | BTC 顺风 | BTC 逆风 |
|---|---|---|
| m2_yoy (%) | >5 | <0 |
| real_yield_10y_pct | <1 | >2.5 |
| dxy_60d_trend_pct | <-1 | >+1 |
| spx_correlation_30d | <0.2 (独立) | >0.7 (宏观主导) |
| usd_cny_60d_trend_pct | CNY 投资者 USD 资产本币顺风 | CNY 投资者 USD 资产本币逆风 |
| global_rate_* | 全球央行约束背景 | 只作 context,不单独触发交易结论 |

### Flow

| 指标 | 强流入 | 正常 | 流出 |
|---|---|---|---|
| etf_net_flow_30d_usd | >$5B | $0-5B | <0 |

### Technical

- RSI(14):<20 极度超卖 / 20-30 超卖 / 30-45 偏弱 / 45-55 中性 / 55-70 偏强 / 70-80 超买 / >80 极度超买
- MACD hist:>0 多头,<0 空头,**收窄 = 反转前兆**
- MA50 vs MA200:金叉 / 死叉
- BB 位置 + volatility_20d:同时极端低 = 变盘前兆

## 5. 读盘必出 5 件(简化的决策模板)

每次读盘必须包含:

1. **通俗总结段**(3-5 句人话):"目前 X 大致处在 Y 阶段,支持 Z,但 W 仍需警惕,短期关注 A,中期关注 B"
2. **结论 + 概率 + 反证 + 失效条件**
   - 结论用概率化表达:"倾向积累,概率约 60%"(禁用 "一定 / 必然")
   - 列至少 2 条反证
   - 必须声明 "若 X 指标变化到 Y,结论失效"
3. **风险雷达**(3-5 条):每条 【触发条件 + 概率估计 + 影响路径 + 观察指标】
4. **source_health 检查**:若有 stale / fallback_used / missing,明确说明
5. **可靠性标注尊重**:hard_blocked horizon 不输出数值预测

## 6. 禁用行为(硬规则)

- ❌ 输出 0-100 总分
- ❌ 输出具体交易指令("涨到 $120k 减仓" / "仓位 30%")
- ❌ 使用绝对语气("一定" / "必然" / "绝对")
- ❌ 忽略 stale / fallback 做强结论
- ❌ hard_blocked horizon 还输出 median / p10 / p90 数值
- ❌ 单指标决策(至少 3 个域一致才有倾向性结论)
- ❌ 只列支持证据不列反证

## 7. 何时读 tier 2 / tier 3

- **tier 2**(决策框架 + 行为护栏)— 用户明确要"读盘 / 判断 / 建议"时必读
- **tier 3**(术语 + 机制库 + 历史类比)— 用户问"为什么 X 指标在这个位置" / "历史上类似情况是什么" / "这个指标是什么意思"时按需读
- 两者都不必读的场景:用户只问单指标当前值 / 单数据字段
