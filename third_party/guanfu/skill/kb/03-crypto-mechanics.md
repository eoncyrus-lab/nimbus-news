# 加密内生机制 (Crypto-Native Mechanics)

> 解释 BTC 自身结构性变量：减半、矿工、AHR/MVRV/NUPL、持有人行为。实时值以 guanfu 面板为准；本文不保存当前行情快照。

---

## 适用时机

当以下域给出强信号或相互矛盾时加载本文：

- `cycle`: `days_since_halving`, `mayer_multiple`, `sma_200w_dev`, `pi_cycle_top_ratio`
- `valuation`: `ahr999_compressed`, `ahr999`, `mvrv`, `mvrv_z_score`, `nupl`
- `network`: `hash_rate_ehs`, `hash_ribbons`, `difficulty_change_pct`, `mempool_mb`

---

## 减半周期

减半降低新增供给，但不直接决定价格。它改变的是矿工卖压和市场叙事。

| 减半后天数 | 常见阶段 | 读盘注意 |
|---|---|---|
| `0-180` | 早期再定价 | 供给叙事增强，但趋势常不稳定 |
| `180-540` | 历史主升窗口 | 仍需估值和杠杆确认 |
| `540-900` | 历史顶部/分配窗口 | 时间风险上升，但不能单独当顶部 |
| `900-1260` | 熊市/积累窗口 | 更关注低估和投降信号 |
| `>1260` | 下一轮减半前 | 预期交易可能提前 |

2024 之后 ETF 改变了边际需求结构，所以“减半后 X 天”只能作为背景，不应压过估值、ETF 流向和宏观测算。

---

## 矿工行为

矿工影响 BTC 的路径：

```
价格/手续费/难度 → 矿工利润率 → 关机或扩张 → 哈希率趋势 → 市场对网络健康和卖压的判断
```

### `hash_ribbons`

当前 guanfu 实现是“短期哈希率均值 vs 长基线”的矿工扩张/投降 proxy。详见 `00-data-contract.md`。

| 状态 | 含义 | 读盘 |
|---|---|---|
| 上行 | 矿工扩张或投降结束 | 网络域偏健康 |
| 交叉中 | 方向未定 | 中性 |
| 下行 | 矿工投降或利润压缩 | 底部接近度上升，但不等于立即见底 |

### `difficulty_change_pct`

- `<-7%`：较强投降信号。
- `-2% 到 +3%`：正常波动。
- `>+8%`：算力快速上线，常见于矿工乐观期。

`hash_ribbons 下行` 单独不是看跌票；若同时 `difficulty_change_pct < -5%`，说明矿工压力更真实。若投降结束转上行，才是更强确认。

---

## AHR999

guanfu 有两个 AHR 视角：

| 指标 | 角色 |
|---|---|
| `ahr999_compressed` | 主估值信号，固定公式压缩版 |
| `ahr999` | 自适应辅助信号 |
| `ahr999_divergence` | 分歧预警 |

### 读盘原则

- 域级估值优先看 `ahr999_compressed`。
- `ahr999` 的动态分位可辅助判断“市场是否相对近期过热/过冷”，但不单独定方向。
- `ahr999_divergence` 出现时，降低估值结论置信度，等待 Mayer、MVRV、ETF 或技术确认。

---

## MVRV / NUPL

### `mvrv`

MVRV = 市值 / realized cap，近似衡量全市场相对链上成本的盈利倍数。

历史阈值（基于 2013-2022 年数据）：
- `< 1`：整体低于链上成本，历史底部区常见（2015、2018、2020-03、2022-11）。
- `1-2`：中性到恢复区。
- `> 3.5`：过热风险上升（2017 顶 ~5.4，2021-04 顶 ~3.9）。

> **ETF 时代结构变化**：2024-03 BTC 创历史新高时 MVRV 仅 ~2.7，未触及 3.5。原因可能是 ETF 持续买盘抬升 realized cap 增速，让 market cap 相对 realized cap 的"领先幅度"压缩。`> 3.5` 的"过热"阈值在 ETF 时代可能整周期不会触发——**优先用 `q`（历史分位）而非绝对值定位高/低**。

### `mvrv_z_score`

MVRV Z 衡量市值相对 realized cap 的偏离程度（标准化）。

> **重要口径差异**：guanfu 实现使用 **rolling 1-year std** 作为标准化分母（README "关键算法"节确认），而第三方仪表盘（Glassnode `MVRVZScore`、CoinMetrics `CapMVRVZ`）使用 **cumulative-to-date std**。两者不可直接数值对比：
> - rolling 1y std 在低波动时段较小 → guanfu 的 Z 值会偏大；
> - cumulative std 包含早期高波动 → 第三方 Z 值在历史底/顶更"压扁"。
>
> **首选**：用 `q`（历史分位）判断相对位置，不要直接套下面的绝对阈值（这些阈值是按标准 cumulative std 校准的）。

下表是**标准 CapMVRVZ 口径**的参考阈值（guanfu 自算口径下需通过 backtest 重新校准）：

- `< 0`：极端低估。
- `0-2`：低估到中性。
- `2-5`：偏高。
- `> 5`：高位预警。
- `> 7`：历史顶部区风险。

### `nupl`

NUPL = 未实现利润 / 市值，反映持有人心理状态：

- `<0`：capitulation。
- `0-0.25`：hope/fear。
- `0.25-0.5`：optimism。
- `0.5-0.75`：belief。
- `>0.75`：euphoria。

---

## 加密内部资金轮动

判断"周期晚期 / 顶部接近度"时，BTC 估值/网络/杠杆指标之外，**资金从 BTC 向 alt 的扩散程度**是关键证据。guanfu 提供 3 个相关字段：

| 指标 | 域 | 用途 |
|---|---|---|
| `flow.eth_btc_ratio` | flow | ETH/BTC 价格比，反映资金对二号资产的偏好 |
| `positioning.altcoin_season` | positioning | Binance Top 50 中 90 日跑赢 BTC 的占比 × 100 |
| BTC dominance（外部） | — | guanfu 不直接输出 BTC 市占率，需外部数据 |

### 阶段判读

| 组合 | 含义 |
|---|---|
| `eth_btc_ratio` 上行 + `altcoin_season > 50` | 资金从 BTC 扩散到 alt，**周期晚期典型形态** |
| `eth_btc_ratio` 下行 + `altcoin_season < 25` | 资金回流 BTC 或退出加密，BTC dominance 上升 |
| `altcoin_season > 75` 且持续 | 极端 alt 行情，历史上常见于周期顶部前 4-12 周（2017-12, 2021-04, 2021-11） |
| BTC 价格新高 + `altcoin_season < 25` | "纯 BTC 行情"，多见于 ETF 驱动阶段（2024-Q1）或机构主导早期 |

### 读盘原则

- alt 季节性单独不是顶部信号；必须同时看 funding/OI 拥挤、NUPL > 0.75、Pi cycle 接近这些 confirm。
- ETF 时代的扩散节奏可能与 pre-2024 不同——2024-Q1 BTC 主升期间 alt 几乎纹丝不动，这是 ETF 资金通道**只接 BTC** 造成的结构性差异。
- `eth_btc_ratio` 长期下行（2022-2024）反映了**ETH 未享受到 ETF 那一波叙事红利**，不必然意味着 ETH 弱。需要结合 ETH 自身（坎昆升级、L2 TVL、ETH ETF 流向）。

---

## 持有人分层的边界

LTH/STH、STH realized price、交易所 BTC 余额等很有价值，但 guanfu 当前不直接输出。除非用户提供外部数据，否则只能作为“待核查变量”，不能在读盘中虚构当前值。

可写：

> “若外部 STH realized price 显示短期持有人深度亏损，则可解释为潜在投降压力。”

不可写：

> “当前 STH 成本是 X，所以一定存在抛压。”

---

## 组合模式

| 组合 | 解释 |
|---|---|
| `mayer <1` + `ahr999_compressed 低估` | 估值偏低 |
| `sma_200w_dev <0` + `mvrv_z <0` | 历史级低估，需检查宏观/危机 |
| `hash_ribbons 下行` + `difficulty 大幅下调` | 矿工投降，底部接近度上升 |
| `hash_ribbons 上行` + `估值低` + `杠杆低` | 结构更健康 |
| `pi_cycle >=1` + `mayer >2.4` + `NUPL >0.75` | 顶部风险显著 |

---

## 输出要求

引用本文时要区分：

- “底部接近度上升” vs “已经见底”。
- “时间窗口风险” vs “顶部信号”。
- “链上估值低” vs “宏观允许低估修复”。

不得用单个 cycle 或 miner 信号直接输出交易动作。
