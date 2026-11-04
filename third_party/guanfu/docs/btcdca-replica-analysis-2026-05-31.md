# btcdca.me 深度拆解与 guanfu 前端复刻报告

日期：2026-05-31  
抓取时间：2026-05-31 22:13-22:15（Asia/Shanghai）  
参考来源：<https://btcdca.me/>、<https://btcdca.me/btc/>、<https://btcdca.me/gold/>、<https://btcdca.me/nasdaq/>、<https://btcdca.me/sp500/>、<https://btcdca.me/hs300/>、<https://btcdca.me/us-fund-holdings/>、<https://btcdca.me/lazy/>

## 结论

btcdca.me 的核心不是复杂页面，而是“复古计算机 + 多维度仪表盘 + 回测可信感”的信息架构。值得复刻的是模块层级、CRT/terminal 风格、五资产入口、评分/倍数/历史回测的组合表达；不应照搬的是它的若干数据口径问题：黄金单位混乱、NASDAQ 字段名与指数点位混用、SP500 历史为空、部分页面大量内嵌静态数据。

本项目应保持 guanfu 口径：价格、分数、倍数、区间回测都来自本地可复现导出；页面只学习参考站的信息层级和视觉语言。

## 页面模块拆解

| 页面 | 主要模块 | 数据来源 | 复刻建议 |
|---|---|---|---|
| 首页 `/` | 顶部导航、首屏标题、五台资产计算机、移动轮播、DCA 工作流、五项目实现对比 | `/api/v2/markets`，失败时 fallback 默认数据 | 已复刻 3+2 计算机矩阵；补充一张“Market Data Console”提高数据密度 |
| BTC `/btc/` | loading terminal、顶部评分/倍数/价格、7维度卡、雷达图、价格+评分历史、历史回测、0-3x 定投系统 | `/btc/api/score`、`/btc/api/history?days=2190` | 我们可继续接 guanfu BTC 链上指标；当前先用 Mayer/MA/收益/波动把详情页加厚 |
| Gold `/gold/` | terminal、黄金评分、7维度卡、雷达图、历史价格、策略说明 | `/gold/api/score`、`/gold/api/history?days=...` | 视觉可学；单位必须改为 USD/oz，避免参考站 `CNY/g` 与 `GC=F` 价格混用 |
| NASDAQ `/nasdaq/` | terminal、4维度总览、12维度量化系统、雷达图、历史回测、策略说明 | `/nasdaq/api/score`、`/nasdaq/api/score-12d`、`/nasdaq/api/history?days=...` | 可借鉴“4维 + 12维”布局；本项目若用 QQQ ETF 价，页面不要显示成 NDX 点位 |
| S&P 500 `/sp500/` | terminal、4维总览、10维 detailed results、历史回测、策略说明 | `/sp500/api/score`、`/sp500/api/history?days=...` | 参考站 SP500 详情数据丰富，但历史接口返回空；我们优先展示可复现区间回测 |
| HS300 `/hs300/` | 十维度详情、价格面板、历史数据、策略详情、交易模拟 | `/hs300/api/hs300/spot`、`/hs300/api/hs300/dimensions`、`/hs300/history?...` | 十维度页面值得学习；本项目当前 HS300 数据 stale，必须显式标记 |
| 美股基金 `/us-fund-holdings/` | QDII 主/被动基金 tab、排序表、基金详情、持仓权重、基金经理 | 页面内嵌 `FUND_DATA`、持仓 JSON | 可借鉴表格密度；应接入本项目 `qdii.json`，不要硬编码大块持仓 |
| 懒人组合 `/lazy/` | 十大组合网格、详情页、增长曲线、年度收益、资产配置、风险指标、压力测试 | 页面内嵌组合数组和回测数据 | 我们已有 `portfolios.json`，可复刻详情页结构 |

## 接口抓取摘要

| 端点 | 样本结果 | 字段结构 | 数据问题 |
|---|---:|---|---|
| `/api/v2/markets` | BTC 73767.65 / Gold 4593 / NDX 30333.18 / SPX 7587.49 / HS300 4892.12 | `data.{btc,gold,nasdaq,sp500,hs300}`，每项含 `price/score/multiplier/status/unit/indicators` | 首页 JS 使用 `json.generatedAt`，但接口实际返回 `timestamp` 和 `data.lastUpdate` |
| `/btc/api/score` | BTC 73774.34，score 77，multiplier 1.54 | `btcPrice/change24h/totalScore/multiplier/indicators` | 指标简洁稳定，7项为 AHR999、FGI、Hash Ribbon、MA200、MVRV、Puell、Reserve |
| `/btc/api/history?days=30` | 30条，2026-05-01 到 2026-05-31 | `data.history[]`：`date/btcPrice/totalScore/multiplier/fgi_value/eventType` | shape 与 Gold 不一致，history 包在 `data.history` |
| `/gold/api/score` | Gold 4593，score 40，multiplier 0.86 | `goldPrice/totalScore/multiplier/advice/rawData/indicators` | 首页写 `unit: CNY/g`，但价格和 `priceSource: yahoo_gc_f` 更像 USD/oz；单位错误风险高 |
| `/gold/api/history?days=30` | 仅5条，2026-05-08 与 2026-05-28~31 | `data[]`：`gold_price/total_score/multiplier/dxy/tips/vix/rsi/macd/ma200` | 30天请求只返回5条；score 端点 4593 与 history 最新 4560.5 不一致 |
| `/nasdaq/api/score` | `qqqPrice` 30333.18，score 44，multiplier 0.68 | 4维：valuation、technical、macro、sentiment | 字段名 `qqqPrice` 但数值是指数点位，不是 QQQ ETF 价格 |
| `/nasdaq/api/score-12d` | score 48，multiplier 0.76 | 12维：PE、PB、MACD、RSI、Bollinger、MA50、MA200、VIX、10Y、DXY、FearGreed、AAII | `macd.value` 与 RSI 数值相同，疑似映射错误 |
| `/nasdaq/api/history?days=30` | 30条，但从 2026-05-31 倒序到 2026-05-02 | `data.history[]` | 历史顺序与 BTC 相反；维度分数多为 null |
| `/sp500/api/score` | SPY 756.48 / SPX 7587.49，score 42，multiplier 0.64 | 4维 `indicators` + 10维 `detailed_results` | 详情数据比首页丰富，应优先消费 `detailed_results` |
| `/sp500/api/history?days=30` | `count: 0` | `data.history[]` 空 | 历史回测模块实际会 fallback mock 数据 |
| `/hs300/api/hs300/spot` | 4892.12，-0.45%，PE 11.96，PB 1.40 | `symbol/name/price/change/volume/amount/pe/pb/updateTime` | cached=true；适合做顶部行情卡 |
| `/hs300/api/hs300/dimensions` | compositeScore 59，multiplier 1.12 | 10维：估值、盈利、资金流、波动率、趋势、宏观、行业、拥挤度、股息、牛熊 | 多个维度 `isDefault: true`，说明部分指标是默认值 |
| `/hs300/api/hs300/history` | 请求失败 | 需要 `startDate/endDate` | 页面代码实际拼 `period=daily&startDate=YYYYMMDD&endDate=YYYYMMDD` |

## 可复刻的视觉/交互模式

1. 浅米色纸面背景、黑色硬边框、8px 内的圆角、轻量投影，形成复古工具感。
2. 首页五台 CRT 计算机是第一视觉锚点：每台机器承载一个资产的 price、score、multiplier 和状态。
3. 资产页以“结果先行”：顶部直接给分数、倍数、价格；细节放到维度卡、图表、回测表。
4. Terminal/loading 动画用于制造计算过程感，但不能阻塞真实数据显示。
5. 数据页要有表格密度：基金、组合、历史观测都应使用紧凑表格和排序/状态标签。

## 不应照搬的问题

1. 单位不一致：Gold 首页 `CNY/g` 与 API `yahoo_gc_f` 价格 4593 冲突。
2. 资产命名不一致：NASDAQ 使用 `qqqPrice` 字段却返回 NDX 点位。
3. API shape 不一致：BTC history 是 `data.history`，Gold history 是 `data[]`，NASDAQ 倒序。
4. 空历史 fallback：SP500 history 为 0 条，但页面仍有 mock 图表。
5. 大量静态内嵌数据：基金和懒人组合页面把大 JSON 直接放在 HTML/JS 内，维护成本高。

## 本项目前端改进方向

已落实：

1. 首页保留五台复古计算机，并增加 `Market Data Console`，展示五资产价格、分数、倍数、价格日、数据健康和关键指标。
2. 资产详情页增加 `Market Snapshot` 指标网格：Mayer、MA200偏离、MA50偏离、30/90/1年收益、30期波动、样本数。
3. 资产详情页增加 `数据健康` 面板：source、历史覆盖、样本数、stale days、note。
4. 资产详情页增加 `最近观测` 表：最近采样收盘价、MA200 和偏离度。
5. 导出 JSON 增加 `data_health` 与 `metrics`，让前端不再只依赖单一 score。
6. 懒人组合页扩展到 10 个静态组合，新增 Sortino、最好/最差年份、上涨年份占比、$10k 终值、最大回撤起止区间和近十年年度收益。
7. 名家持仓页扩展到 7 位 13F 经理，每位展示前 20 大持仓、Top1/Top5/Top10 集中度、完整申报行数、申报市值和环比变化摘要。
8. QDII、懒人组合、名家持仓三类旁路数据页统一到浅色纸面、硬边框、紧凑表格和同一字号体系；这些旁路数据仅做展示，不进入 guanfu 预测核心。

下一步建议：

1. BTC 详情页继续接入 guanfu 已有链上/周期指标，形成真正 8 域面板。
2. HS300 恢复数据刷新源；在刷新恢复前继续显式 stale。
3. 国会交易 watcher 当前返回 403，后续可替换为自有缓存源或官方 disclosure 数据抓取链路。
4. 给每个资产新增“指标解释抽屉”，把评分输入、口径、失效条件并列展示。
