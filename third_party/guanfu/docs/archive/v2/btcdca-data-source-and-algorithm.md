# btcdca.me 数据源与算法逆向分析

日期：2026-05-04  
目标站点：<https://btcdca.me/>

## 结论摘要

btcdca.me 是一个多资产 DCA 评分站，不只是 BTC。页面导航显示至少覆盖：

- BTC: <https://btcdca.me/btc/>
- 黄金: <https://btcdca.me/gold/>
- 纳斯达克 100: <https://btcdca.me/nasdaq/>
- 标普 500: <https://btcdca.me/sp500/>
- 沪深 300: <https://btcdca.me/hs300/>
- 美股基金持仓: <https://btcdca.me/us-fund-holdings/>
- 懒人组合: <https://btcdca.me/lazy/>

站点架构上，页面是大量内联 HTML/CSS/JS，核心资产通过同域 API 拉取实时评分。可公开访问的 API 包括：

| 资产 | 主要 API | 备注 |
|---|---|---|
| 首页聚合 | `/api/v2/markets` | 聚合 BTC、黄金、纳指、标普，`hs300` 当前可为空 |
| BTC | `/btc/api/score`, `/btc/api/history?days=N` | API 返回实时价格、7 个指标分、总分、倍数 |
| 黄金 | `/gold/api/score`, `/gold/api/history?days=N` | API 返回 `priceSource: yahoo_gc_f` |
| 纳指 | `/nasdaq/api/score`, `/nasdaq/api/score-12d`, `/nasdaq/api/history?days=N` | 4 维主评分 + 12 维展示评分并存 |
| 标普 | `/sp500/api/score`, `/sp500/api/history?days=N` | 10 维详细结果 |
| 沪深300 | `/hs300/api/hs300/spot`, `/hs300/api/hs300/dimensions` | `spot` 和 `dimensions` 均可访问，API 标记 cached |
| 美股基金 | 无独立 API，页面内联 JSON | 页面声明 East Money / AKShare |
| 懒人组合 | 无独立 API，页面内联 ETF 年度收益数组 | 页面声明 Portfolio Visualizer / PV 回测 |

重要限制：没有后端源码，只能基于公开页面源码、前端 JS、页面文案和 API 响应逆向。纳指、标普、沪深 300 存在“页面说明公式”和“API 返回倍数”不完全一致的问题，不能把页面文案当作完整真实后端算法。

进一步搜索结论：

- 未发现公开 GitHub 仓库、后端源码或 source map。
- 站点 HTTP header 显示由 `nginx/1.20.1` 直接服务静态 HTML，页面 JS 多数内联。
- 首页公开了“六大统一架构”：数据采集、指标计算、归一化、加权计算、倍数映射、输出决策。
- 作者/转发帖公开描述了核心资产范围、BTC/黄金/纳指/标普/基金/懒人组合的因子框架和风控大纲，可作为“作者说法”，但仍需以页面/API 为准。

## 通用产品模式

所有资产基本都遵循同一套产品结构：

1. 后端或前端计算若干维度分数，范围 0-100。
2. 高分表示“低估 / 更适合提高定投倍数”，低分表示“高估 / 降低或暂停定投”。
3. 将总分映射为 DCA multiplier。
4. 页面展示回测摘要、年度收益、风险收益指标和压力测试。
5. 回测数字大多硬编码在 HTML/JS 中，不能从页面独立复算。

前端 ticker 的开发环境端口也暴露了服务拆分痕迹：

- BTC local: `localhost:5002`
- Gold local: `localhost:5001`
- HS300 local: `localhost:3001`
- 生产环境走同域路径，如 `/btc/api/score`

首页工作流还给出一层更高层的数据源抽象：

- 交易所 API：实时价格、成交量。
- 链上节点 / Glassnode 数据。
- 宏观数据库：央行 / 美联储。
- 情绪指标：VIX / 恐惧贪婪。
- 归一化：原始值转历史百分位，再映射到 0-100。

这部分是站点产品说明，不等于每个后端接口都实际使用这些源。比如 BTC 页回测明确写 CryptoCompare，但并未公开说明 MVRV、Puell、Reserve Risk 的实时源具体来自 Glassnode、LookIntoBitcoin、CoinGlass 或自算。

## BTC

### 数据源

公开页面声明：

- 回测数据源：CryptoCompare API，BTC/USD。
- 回测区间：2015-01-01 到 2026-04-18。
- 回测引擎：`v3 Precision Scoring`。
- 作者公开帖称 BTC 看 7 个东西：AHR999、MVRV、200 日/周均线偏离、恐惧贪婪、Puell、Reserve Risk、Hash Ribbon。页面实际卡片写的是 200 周均线。

API 观测：

- `GET https://btcdca.me/btc/api/score`
- 返回字段包括 `btcPrice`, `change24h`, `totalScore`, `multiplier`, `eventType`, `timestamp`, `indicators`。
- 当前响应样例（2026-05-04）显示：BTC 价格约 79,736，`totalScore=76`，`multiplier=1.52`。

### 指标与权重

页面声明 7 维评分：

| 指标 | 权重 | 页面阈值说明 |
|---|---:|---|
| AHR999 | 20% | `<0.45` 抄底，`0.45-1.2` 定投，`1.2-5` 观望，`>5` 高风险 |
| 200 周均线 | 15% | 价格 / 200W MA，`<1.0` 低于长期趋势，`>3.0` 显著高估 |
| MVRV Z-Score | 15% | `<-1` 极度低估，`-1~1` 合理，`1~3` 高估，`>3` 泡沫 |
| 恐惧贪婪指数 | 15% | `0-24` 极度恐惧，`25-49` 恐惧，`50-74` 贪婪，`75-100` 极度贪婪 |
| Puell Multiple | 15% | `<0.5` 低估，`0.5-1` 正常，`1-2` 高估，`>2` 极度高估 |
| Reserve Risk | 10% | `<0.001` 低风险，`0.001-0.01` 正常，`>0.01` 高风险 |
| Hash Ribbon | 10% | API 返回 `buy` 等状态，页面雷达图把它作为第 7 维 |

用 2026-05-04 API 返回的指标分验证，加权和约为 76，和 `totalScore=76` 一致。

### 倍数算法

页面声明是线性分段插值：

| 分数 | 倍数 |
|---:|---:|
| 0 | 0.10x |
| 30 | 0.30x |
| 50 | 0.80x |
| 70 | 1.20x |
| 85 | 1.95x |
| 100 | 3.00x |

但前端历史图的 JS 里出现另一套分段：`30-50` 段插到 `0.60x`，`50-70` 段再从 `0.60x` 到 `1.20x`。因此 BTC 页面存在一个小的不一致。高分段对当前结果影响不大：`score=76` 映射到约 `1.5x`，与 API `1.52x` 接近。

### 风控规则

页面声明：

- 单周最大 3 倍基础金额。
- 连续 7 日高评分后自动降档。
- 回撤 30% 暂停一天新增定投。
- BTC 仓位不超过总资产 20%。

## 黄金

### 数据源

公开页面声明：

- 回测数据源：Yahoo Finance `GC=F`。
- 页面也写有 `Gold API | XAU/USD`。
- 回测生成时间：2026-01-30。

API 观测：

- `GET https://btcdca.me/gold/api/score`
- 当前响应包含 `priceSource: "yahoo_gc_f"`、`historySource: "database"`。
- 当前字段包括 `goldPrice`, `priceChange24h`, `totalScore`, `multiplier`, `rawData`, `indicators`。

### 指标与权重

页面声明 7 维：

| 指标 | 权重 | 方向 |
|---|---:|---|
| DXY | 25% | 美元指数，与黄金负相关 |
| TIPS 实际利率 | 20% | 实际利率，与黄金负相关 |
| MA200 | 18% | 价格相对 200 日均线偏离 |
| RSI | 12% | 超买超卖 |
| VIX | 10% | 恐慌指数，避险需求 |
| MACD | 8% | 趋势动量 |
| 季节性 | 7% | 页面称 1/9/12 月偏强 |

页面给出的关键阈值：

- DXY `<96` 利好，`>108` 利空。
- TIPS `<0%` 极度利好。
- MA200 偏离 `<-10%` 强买。
- RSI `<30` 超卖，`>70` 超买。
- VIX `>30` 恐慌。

### 倍数算法

页面声明为阶梯倍数：

| 分数 | 倍数 |
|---:|---:|
| `<30` | 0.5x |
| `30-44` | 0.7x |
| `45-54` | 1.0x |
| `55-69` | 1.5x |
| `70-84` | 2.0x |
| `85+` | 3.0x |

API 当前返回 `totalScore=44`、`multiplier=0.7`，与该阶梯规则一致。

### 风控规则

页面声明：

- 单周最大 3 倍基础金额。
- 连续 5 日评分高于 85，降档到 2.5 倍。
- 金价从近期高点回撤超过 12%，暂停倍数放大，回到 1 倍基础定投。
- 黄金仓位不超过流动资产 20%。

## 纳斯达克 100

### 数据源

公开页面声明：

- 回测数据源：Yahoo Finance `QQQ`。
- 回测区间：2015-2026 年 4 月。
- 回测引擎：`v3 Precision Scoring`。
- 移动页搜索结果显示另一处写法为 `v3 MA200 Deviation`，并明确称策略在价格低于 MA200 时大幅增加投入，最高 50 倍权重。这更像回测引擎或旧版算法，不是当前实时 API 的 DCA multiplier。

API 观测：

- `GET https://btcdca.me/nasdaq/api/score`
- `GET https://btcdca.me/nasdaq/api/score-12d`
- `GET https://btcdca.me/nasdaq/api/history?days=N`

注意：页面回测用 QQQ ETF；实时 API 字段名是 `qqqPrice`，但当前值约 27,710，更像纳斯达克 100 指数点位而不是 QQQ ETF 价格。这是一个数据口径不一致风险。

### 主评分算法

`/nasdaq/api/score` 返回 4 个主维度：

| 维度 | 权重 | API 字段 |
|---|---:|---|
| 估值 | 45% | `valuation`: PE, PB, PEG, ROE |
| 技术 | 25% | `technical`: RSI, MA50, MA200 等 |
| 宏观 | 20% | `macro`: VIX, DXY, 10Y |
| 情绪 | 10% | `sentiment`: Fear & Greed |

当前 API 响应示例：`totalScore=46`，`multiplier=0.72`。

注意：页面 `renderDimCards()` 展示权重写成估值 35%、技术 25%、宏观 20%、情绪 20%，但 API 返回 `weights` 是估值 45%、技术 25%、宏观 20%、情绪 10%。这里应以后端 API 为准，并把页面展示视为过期或未同步。

### 12 维展示评分

`/nasdaq/api/score-12d` 返回另一套 12 维：

| 维度 | 权重 |
|---|---:|
| PE | 12% |
| PB | 8% |
| MACD | 10% |
| RSI | 8% |
| Bollinger | 7% |
| MA50 | 10% |
| MA200 | 8% |
| VIX | 8% |
| 10Y | 7% |
| DXY | 6% |
| Fear & Greed | 8% |
| AAII | 8% |

页面文案称“12 维度综合评分”，但主 dashboard 用的是 4 维 API；12 维更像补充雷达图。

### 倍数算法与不一致

页面声明 `M = exp(Score/40 - 1)`，并给出示例区间。但当前 API 返回值不符合这个公式：

- 若 `score=46`，直接套公式应约为 `1.16x`。
- API 实际返回 `0.72x`。

页面又在评分区间写了：

- `0-25`: 0.0-0.5x
- `25-50`: 0.5-0.9x
- `50-75`: 0.9-1.5x
- `75-100`: 1.5-2.5x

当前 `score=46 -> 0.72x` 更接近区间映射，而不是指数公式。结论：纳指后端真实倍数算法可能是分段/缩放后的指数公式，公开页面不能完整还原。

### 风控规则

页面声明：

- 单周最大 5 倍基础金额。
- 连续 5 个交易日评分高于 90，降档到 4 倍。
- 纳指 100 从近期高点回撤超过 15%，暂停倍数放大，回归 1 倍基础定投。
- 纳指仓位不超过流动资产 25%。

## 标普 500

### 数据源

公开页面声明：

- 回测标的：SPY（S&P 500 ETF）。
- 回测区间：2015-01 到 2025-01。
- 作者公开帖称标普 500 加了席勒 CAPE 和行业轮动等数据因子。

API 观测：

- `GET https://btcdca.me/sp500/api/score`
- 当前响应包含 `sp500Price`, `spyPrice`, `totalScore`, `multiplier`, `detailed_results`。

### 指标与权重

API 与前端卡片显示 10 维：

| 维度 | 权重 | 主要指标 |
|---|---:|---|
| 传统估值 | 15% | PE, PB, PEG, PS, PCF |
| 周期估值 | 15% | CAPE, Tobin Q |
| 市值比率 | 10% | Buffett indicator |
| 技术动量 | 10% | RSI, MACD, MA trend, Bollinger, ATR |
| 市场情绪 | 10% | Fear & Greed, VIX, Put/Call, AAII |
| 盈利质量 | 10% | EPS growth, revenue growth, ROE, margin |
| 宏观环境 | 10% | 10Y yield, DXY, credit spread, yield curve |
| 资金流向 | 10% | ETF flow, institutional flow, buyback |
| 经济领先 | 5% | PMI, LEI, jobless claims |
| 地缘风险 | 5% | GPR, EPU, conflict, election |

当前 API 示例：`totalScore=40`，`multiplier=0.6`。其中 `detailed_results` 中很多值看起来像后端固定模型值，例如 CAPE、Tobin Q、GPR、EPU、PMI 等，但 API 未披露原始数据源。

### 倍数算法与不一致

页面声明 `M = exp(Score/40 - 1)`，同时页面区间又写：

- `0-19`: 0x
- `20-34`: 0.2-0.4x
- `35-49`: 0.5-0.8x
- `50-64`: 1.0-1.4x
- `65-79`: 1.5-1.9x
- `80-100`: 2.0-2.25x

API 当前 `score=40 -> multiplier=0.6`，符合区间规则，不符合直接指数公式。因此标普也存在“公式展示”和“后端实际倍数”不一致。

### 风控规则

页面声明：

- 单周最大 5 倍基础金额。
- 连续 5 日高评分后降档保护。
- 回撤 15% 触发熔断机制。
- 标普 500 仓位不超过流动资产 35%。

## 沪深 300

### 数据源

公开页面和注释显示多个来源：

- 页面注释：中证指数公司、Wind、东方财富。
- 前端注释：Node.js 数据服务，新浪财经。
- 前端注释：API 服务使用 AkShare。
- fallback：Yahoo Finance chart API，代码里使用 `^HS300`。
- 页面 footer 声明算法版本：`ALGO-v4.0`，基于 10 年真实月度历史数据、10 维度桥水风格评分系统。

API 观测：

- `GET https://btcdca.me/hs300/api/hs300/spot`
  - 返回价格、涨跌、成交量、PE、PB 等。
  - 当前响应标记 `cached: true`。
- `GET https://btcdca.me/hs300/api/hs300/dimensions`
  - 返回 10 维分数、权重、指标值、综合分和倍数。

当前 API 示例：`compositeScore=57`，`multiplier=1.08`，PE `11.99`，PB `1.40`。

### 10 维权重

前端 `appState.settings.weights` 和 API 返回基本一致：

| 维度 | 权重 |
|---|---:|
| 估值 valuation | 15% |
| 盈利 earnings | 12% |
| 资金流 capitalFlow | 12% |
| 波动率 volatility | 10% |
| 趋势 trend | 12% |
| 宏观 macro | 10% |
| 行业 industry | 8% |
| 拥挤度 crowding | 8% |
| 股息 dividend | 8% |
| 牛熊周期 bullBear | 5% |

### 前端 fallback 评分函数

页面内有完整 fallback 函数。核心结构：

- 估值：PE 50%、PB 30%、PEG 15%、ROE 5%。低 PE/PB/PEG、高 ROE 得高分。
- 盈利：EPS 增长 40%、营收增长 35%、利润率 25%。使用逆周期逻辑，盈利越低迷分数越高。
- 资金流：北向资金 40%、主力资金 35%、散户情绪 25%。资金流出/恐慌时分数更高。
- 波动率：20 日历史波动率 50%、ATR 30%、振幅 20%。高波动给高分。
- 趋势：均线排列 30%、MACD 25%、RSI 25%、`100-RSI` 20%。弱趋势/超卖给高分。
- 宏观：LPR 30%、汇率 25%、CPI 25%、GDP 20%。紧缩后期/通缩/增速放缓给高分。
- 行业：板块轮动 40%、龙头强度 35%、市场广度 25%。
- 拥挤度：换手率 35%、融资余额 35%、基金仓位 30%。越不拥挤分越高。
- 股息：股息率 50%、派息率 30%、稳定性 20%。
- 牛熊周期：周期位置 50%、持续时间 30%、极端程度 20%。

典型阈值可直接从前端函数还原：

- PE：`<10` 给 95 分，`10-12` 给 85，`12-14` 给 75，逐级下降，`>=26` 仅 5 分。
- PB：`<1.1` 给 95，`1.1-1.3` 给 85，`1.3-1.5` 给 75，`>=3.3` 仅 5 分。
- RSI：`<20` 给 95，`20-30` 给 85，`30-40` 给 70，`>=70` 给 15。
- 北向资金：大幅流出 `<-100` 给 95，大幅流入 `>=100` 给 15。
- 换手率：`<1.0` 给 95，`>=3.5` 给 20。
- 股息率：`>4%` 给 95，`1.5%-2%` 给 30，`<=1.5%` 给 20。

这说明沪深 300 是明确的逆向/均值回归模型：低估、恐慌、流出、低拥挤、高股息、弱趋势给高分。

### 倍数算法与不一致

前端 fallback 里有一套阶梯算法：

| 分数 | 倍数 |
|---:|---:|
| `>=80` | 3.0x |
| `65-79` | 2.0x |
| `50-64` | 1.0x |
| `35-49` | 0.5x |
| `20-34` | 0.3x |
| `<20` | 0.2x |

页面策略区又写了线性公式：`M = 0.2 + (Score-25) * 0.024`。

API 当前 `score=57 -> multiplier=1.08`，既不完全等于前端阶梯 `1.0x`，也不完全等于线性公式约 `0.97x`。说明线上后端可能有第三套插值或修正逻辑。

### 回测

页面声明：

- 回测区间：2015-2024。
- 数据来源：沪深300真实历史数据。
- 回测引擎：10 维度评分策略。
- 页面称策略在评分较低/市场恐慌时加大投入，在评分较高/市场过热时减少投入。

## 美股基金持仓页

这是一个静态数据展示页，页面内联了大量基金和持仓 JSON。

### 数据形态

- 持仓数据直接嵌入 HTML/JS。
- 部分基金 `reportDate` 为 2025-12-31，`quarter` 为 2025 年 Q4。
- 页面未暴露独立 API 来源，但 footer 写明 `Data sourced from East Money (AKShare) · Updated in real-time`。

### 市场评分

页面还有一个“美股市场估值 / 定投节奏”评分函数：

| 维度 | 权重 |
|---|---:|
| 估值水平 | 50% |
| 收益质量 | 15% |
| 恐慌/机会指数 | 20% |
| 宏观利率 | 15% |

其输入是页面内常量：PE、PB、股息率、CAPE、ERP、12 个月动量、VIX、Fed Rate、信用利差。倍数是 0-3x 分段映射。

可还原的市场评分公式：

```text
valuation = PE_score * 0.45 + PB_score * 0.25 + CAPE_score * 0.30
quality   = dividend_yield_score * 0.50 + ERP_score * 0.50
panic     = momentum_score * 0.40 + VIX_score * 0.35 + credit_spread_score * 0.25
rate      = fed_rate_score

total = valuation * 0.50 + quality * 0.15 + panic * 0.20 + rate * 0.15
```

前端常量当前为 PE `24.0`、PB `4.20`、股息率 `1.25`、CAPE、ERP、动量、VIX、Fed Rate、信用利差等。由于这些输入是静态常量，页面“实时”更多是基金数据刷新口径，市场评分本身未必实时。

倍数分段：

| 分数 | 倍数 |
|---:|---:|
| `<20` | 0.0x |
| `20-29` | 0.3x |
| `30-39` | 0.5x |
| `40-49` | 0.8x |
| `50-59` | 1.0x |
| `60-69` | 1.3x |
| `70-79` | 1.8x |
| `80-89` | 2.5x |
| `>=90` | 3.0x |

## 懒人组合页

这是静态组合回测页，不是实时评分页。

### 数据源与算法

- 页面内联 ETF 年度收益数组，覆盖 2017-2025。
- ETF 包括 VTI、TLT、BND、GLD、VNQ、VXUS、EFA、VWO、BIL、SHY、GSG、IVE、SPY、VBR、VB、TIP、EFV 等。
- 组合收益通过前端函数按 ETF 权重加权计算。
- 页面表格里写“数据来源：PV 回测”，这里的 PV 大概率指 Portfolio Visualizer，但页面没有外链或可验证 API。

核心前端算法：

```text
portfolio_return[year] = Σ(ETF_return[year] * allocation_weight / 100)
portfolio_value starts at 10000
portfolio_value *= 1 + portfolio_return / 100
```

因此懒人组合不是从服务端实时拉行情，而是用内联年度收益表复合出 CAGR、回撤、夏普等展示指标。

示例组合包括：

- 达里奥全天候组合
- 永久组合
- 经典 60/40 股债
- 耶鲁大学捐赠基金
- Ivy Portfolio
- Coffee House
- Bernstein No Brainer
- Core Four
- Buffett 90/10
- Swensen Lazy Portfolio

## 对 guanfu 的启发

btcdca.me 的强项是产品化表达：首屏给总分、倍数和一句话建议，资产覆盖广，用户理解成本低。弱项是可审计性：多个页面存在公式和 API 倍数不一致，回测数据多为硬编码摘要，部分数据源未披露到可复现级别。

guanfu 不应该直接复制“单一分数 + 今日倍数”的核心范式，因为这与当前三层分离设计冲突。但可以吸收三点：

1. 增加 `--summary` 或 `--brief`：用非交易指令方式输出首屏摘要，例如“偏积累 / 反证 / 数据健康 / 失效条件”。
2. 增加跨资产观察域：黄金、QQQ/SPY、沪深300不一定要变成 DCA 倍数，但可以成为 BTC 读盘的宏观/跨资产上下文。
3. 强化可复现回测文档：btcdca 页面有很强的营销转化，但公式口径不稳。guanfu 如果公开任何历史收益数字，必须保留样本、窗口、源码命令和反例。

## 证据链接

- 站点首页：<https://btcdca.me/>
- 首页聚合 API：<https://btcdca.me/api/v2/markets>
- BTC 页面：<https://btcdca.me/btc/>
- BTC API：<https://btcdca.me/btc/api/score>
- 黄金页面：<https://btcdca.me/gold/>
- 黄金 API：<https://btcdca.me/gold/api/score>
- 纳指页面：<https://btcdca.me/nasdaq/>
- 纳指 API：<https://btcdca.me/nasdaq/api/score>
- 纳指 12 维 API：<https://btcdca.me/nasdaq/api/score-12d>
- 标普页面：<https://btcdca.me/sp500/>
- 标普 API：<https://btcdca.me/sp500/api/score>
- 沪深300 页面：<https://btcdca.me/hs300/>
- 沪深300 spot API：<https://btcdca.me/hs300/api/hs300/spot>
- 沪深300 dimensions API：<https://btcdca.me/hs300/api/hs300/dimensions>
- 美股基金页面：<https://btcdca.me/us-fund-holdings/>
- 懒人组合页面：<https://btcdca.me/lazy/>
- 纳指移动页：<https://www.btcdca.me/nasdaq/index-mobile.html>
- 作者/转发帖镜像（TwStalker）：<https://twstalker.com/sm_rzc>
