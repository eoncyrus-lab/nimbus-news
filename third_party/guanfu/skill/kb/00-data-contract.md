# 数据契约 (Data Contract)

> 本文件定义 guanfu 面板和 KB 的连接边界。读盘时，实时值永远来自本次 `guanfu --json`；本 KB 只解释字段语义、单位、fallback 和缺失处理。

---

## 读取顺序

1. 先看顶层 `source_health` 和 `stale_warnings`。
2. 再读各 domain 的指标值、`q`、`missing`、`source`、`updated_at`、`note`。
3. `missing=true` 的指标不得参与域级投票、历史类比或强结论。
4. `source_health.status` 为 `missing/stale/partial/warning` 时，相关域只能输出低置信结论。
5. KB 中任何示例值、历史样例和阈值都不得覆盖实时面板值。

---

## 指标字段

| 字段 | 含义 | 读盘规则 |
|---|---|---|
| `value` | 原始数值 | 按指标单位解释，不做二次缩放 |
| `q` | 历史分位 `[0,1]` | 有 `q` 时优先用于“相对历史位置”；没有 `q` 时只看静态阈值 |
| `label` | 人类速览标签 | 仅作提示，不直接当作结论 |
| `source` | 数据来源或计算来源 | 与 `source_health` 一起判断可信度 |
| `updated_at` | 数据时间 | 周末、假日、月度宏观数据可能滞后 |
| `note` | 计算备注 | fallback、估算、窗口说明优先看这里 |
| `missing` | 缺失/placeholder | true 时必须跳过 |

数值 `0` 不代表缺失；只有 `missing=true` 或 `source_health` 显式异常才代表该指标不可用。资金费率、相关性、难度调整、山寨季指数等都可能出现真实 0 值。

---

## Source Health 门控

| 状态 | 允许结论 |
|---|---|
| `ok` | 可正常使用，但仍需交叉验证 |
| `warning` | 可使用，必须说明 warning |
| `partial` | 只允许低/中置信结论 |
| `stale` | 不得用该域做主证据 |
| `missing` | 该域跳过 |

若 `source_health` 不存在，按旧版面板处理：只能基于各指标 `updated_at`、`note` 和 `missing` 降低置信度。

### Stale 容忍预算

`updated_at` 距当前的最大可接受滞后：

| 数据类 | 容忍上限 | 超过后处理 |
|---|---|---|
| BTC 价格、funding、OI、Top50 | 4 小时 | 标记 stale，不得做实时拥挤判断 |
| 哈希率、mempool | 12 小时 | 标记 stale，仅作 1-2 日趋势 |
| ETF 流入、稳定币市值 | 2 个交易日（周末/假日除外） | 标记 stale，不得做边际资金主证据 |
| 链上估值（mvrv / nupl） | 2 天 | 标记 stale，仅作背景 |
| 跨资产价格（GLD / QQQ / SPY / UUP / VIXY / TLT 等 ETF） | 1 个交易日 | 标记 stale，仅作背景 |
| CBOE put/call（`stooq_putcall` legacy key） | 5 个交易日 | QQQ/SPY 期权情绪和 forecast 特征降级 |
| Deribit DVOL/skew | 2 天 | BTC 期权读盘跳过，不影响 BTC 价格或 forecast |
| CMC market context | 1 天 | 市场观察层缺失，不影响核心价格或 forecast |
| 月频宏观（M2、CPI、real yield、HY spread、yield curve） | 45 天 | 标记 stale；M2 因发布周期可放宽到 60 天 |
| 减半距离、phase、SMA200W | 不会 stale | — |

超过上限即视同 `stale`；接近上限（≥ 容忍 70%）应在 note/warning 中提示。

---

## 关键口径

### AHR999

- `ahr999_compressed`：推荐主估值信号。固定幂律 AHR999 经 `raw^0.75` 压缩，阈值也按 `x^0.75` 映射。
- `ahr999`：自适应版，用于辅助确认和分歧检测，不单独驱动估值域。
- `ahr999_divergence`：只有固定公式与自适应分位方向冲突时出现，是“置信度下降/转向预警”，不是方向信号。

### MVRV / MVRV Z / NUPL

- `mvrv` 来自 CoinMetrics `CapMVRVCur`。
- 当 `CapRealUSD` 不可用时，guanfu 可用 `CapMrktCurUSD / CapMVRVCur` 反推 realized cap；此时应在 warning/note 中标注为 implied。
- 当前实现的 `mvrv_z_score` 是 guanfu 计算口径，使用滚动窗口，不应与第三方仪表盘的全历史 `CapMVRVZ` 直接数值比较。

### Hash Ribbons

- 面板标签叫 `30d MA vs 60d MA`，但当前实现为了降噪，使用最近 30 天哈希率均值对比最多 180 天的基线均值。
- 因此它是“矿工投降/扩张 proxy”，不是严格的传统 Hash Ribbons 复刻。

### Oil / WTI

- `oil_proxy_usd`：Futu `US.USO` ETF proxy，不是 WTI 桶价。
- `wti_crude_usd`：Yahoo `CL=F` fallback，可按 WTI 近月期货解释。
- `btc_oil_ratio` 只有 source 为 `yahoo:CL=F` 时才能解释成“1 BTC = 多少桶 WTI”；source 为 `futu:US.USO` 时只能解释成“多少份 USO ETF”。

### Legacy storage key

- `stooq_putcall` 是兼容旧本地文件和 feature bundle 的存储 key；默认实现已改为 CBOE 官方 no-key 数据源。
- 判断数据来源时优先读指标的 `source` 字段和 `source_health.note`,不要只看 key 名。

---

## 禁止事项

- 不用过期 KB 示例值替代实时面板。
- 不把小样本历史类比写成“必然”。
- 不在缺少 `source_health` 或数据 stale 时输出高置信结论。
- 不输出无上下文交易指令或具体执行参数。
