# btcdca.me 可信度评估与 guanfu 借鉴建议

日期：2026-05-04  
关联文档：[btcdca-data-source-and-algorithm.md](./btcdca-data-source-and-algorithm.md)

## 结论

btcdca.me 可以作为“产品表达、因子清单、跨资产视角”的参考，但不适合作为 guanfu 的算法依据直接复用。

它更像一个面向普通用户的定投建议产品：优点是表达清楚、资产覆盖广、决策入口简单；缺点是后端不可审计、回测不可复现、部分公式与 API 输出不一致。

guanfu 应该吸收它的可读性和跨资产组织方式，但继续坚持当前核心原则：

- 不输出单一 0-100 总分。
- 不输出无上下文的买卖/定投倍数指令。
- 每个结论必须附证据链、反证、数据健康和失效条件。

## 可信度分层

| 模块 | 可信度 | 判断 |
|---|---|---|
| 原始行情源 | 中等 | 页面/API 明确出现 CryptoCompare、Yahoo Finance、East Money / AKShare、Portfolio Visualizer 等线索，这些作为行情或基金数据源可参考。 |
| 因子框架 | 中等 | BTC、黄金、纳指、标普、沪深300的因子选择整体合理，符合常见市场分析框架。 |
| 评分算法 | 低到中等 | 部分权重和前端 fallback 可还原，但后端真实计算不可见，且页面、前端、API 有不一致。 |
| 回测结果 | 低 | 回测多为页面硬编码摘要，缺少脚本、数据快照、参数、手续费、滑点、调参过程和反例样本。 |
| 投资建议 | 低 | “今日定投 x 倍”把多维不确定性压缩成单动作，不适合作为高可信投资结论。 |

## 主要可信度问题

### 1. 后端不可审计

没有发现公开 GitHub 仓库、后端源码或 source map。页面大量逻辑是内联 JS，但核心实时评分由同域 API 返回，无法确认后端真实公式、数据清洗、异常处理和缓存策略。

这意味着页面公开的算法说明只能当作“产品文案 + 局部逆向”，不能当作完整实现。

### 2. 公式与 API 不一致

多个资产页面存在公开公式和 API 返回倍数不一致：

- Nasdaq 页面写 `M = exp(Score/40 - 1)`，但当前 API `score=46` 返回 `multiplier=0.72`，更像分段映射。
- S&P 500 页面同样写指数公式，但 API `score=40` 返回 `multiplier=0.6`，也更符合区间规则。
- HS300 页面策略区写线性公式，前端 fallback 又有阶梯倍数，API `score=57 -> multiplier=1.08` 与两者都不完全一致。

这类不一致会削弱模型可信度，因为用户无法确认“展示算法”和“实际执行算法”是否相同。

### 3. 数据口径混用

纳指页面回测写 Yahoo Finance `QQQ`，但实时 API 字段 `qqqPrice` 当前更像纳斯达克 100 指数点位而不是 QQQ ETF 价格。

指数、ETF、人民币计价、美元计价、现货、期货之间如果没有严格区分，会造成：

- 回测标的和实时标的不一致。
- 分数阈值适用对象不一致。
- 用户误以为同一套模型可直接迁移。

### 4. 回测不可复现

页面给了很多回测结论和风险指标，但没有公开：

- 原始历史数据文件或下载命令。
- 回测脚本。
- 再平衡规则。
- 手续费、滑点、税费、汇率假设。
- 调参前后结果。
- 失败样本和反例。

因此这些回测只能作为产品展示，不能作为研究证据。

### 5. 部分“实时”表达实际依赖静态数据

美股基金页的市场评分输入是前端常量，懒人组合页是内联 ETF 年度收益数组。它们可以展示，但不应被理解为完整实时模型。

## btcdca.me 值得借鉴的地方

### 1. 首屏摘要表达

btcdca.me 的强项是用户一眼能看到：

- 当前资产。
- 综合状态。
- 主要指标。
- 一句话建议。

guanfu 可以借鉴这种低摩擦入口，但表达形式应改成“读盘摘要”，而不是“动作指令”。

建议新增：

```bash
guanfu --brief
```

输出结构：

```text
结论倾向: 偏积累 / 中性 / 分配风险
置信度: 低 / 中 / 高
主要证据: valuation, flow, positioning ...
主要反证: technical, macro ...
数据健康: ok / stale / fallback
失效条件: BTC 跌破/突破某关键结构，ETF 流反转，宏观数据失效等
```

### 2. 跨资产观察域

btcdca.me 的多资产覆盖值得借鉴，但 guanfu 不应为黄金、SPY、QQQ、沪深300都生成定投倍数。

更合适的方式是把它们纳入 BTC 的 cross_asset / macro 背景：

- 黄金：避险资产相对强弱。
- SPY / QQQ：风险资产 beta 环境。
- DXY：美元流动性压力。
- VIX：风险偏好。
- 10Y real yield：实际利率压力。
- HS300：全球风险偏好和中国资产风险偏好侧面信号。

建议扩展 cross_asset 域：

```text
gold_vs_btc
btc_vs_spy_30d_corr
btc_vs_qqq_30d_corr
dxy_trend_60d
vix_level
real_yield_10y
hs300_trend_or_risk_proxy
```

### 3. 指标说明卡片

btcdca.me 每个资产页都把指标、权重、阈值写在页面上，这一点对普通用户友好。

guanfu 可以进一步标准化每个指标的元信息：

```json
{
  "key": "mvrv_z_score",
  "domain": "valuation",
  "value": 1.23,
  "q": 0.62,
  "label": "neutral",
  "source": "coinmetrics/glassnode/fallback",
  "updated_at": "...",
  "formula_version": "mvrv-z-v1",
  "source_version": "coinmetrics-community-vX",
  "staleness": "ok",
  "failure_modes": ["realized cap unavailable", "fallback implied data"]
}
```

### 4. 产品化文档

btcdca.me 很会把“为什么这么算”展示给用户。guanfu 可以借鉴文档组织方式，但必须更可审计。

建议为 guanfu 每个核心指标补齐：

- 数据源。
- 原始字段。
- 清洗规则。
- 计算公式。
- 历史窗口。
- 分位算法。
- 降级/fallback 逻辑。
- 失效情形。
- 最后更新时间。

### 5. Ticker / Dashboard 思路

btcdca.me 首页 ticker 能快速展示多个资产状态。guanfu 可以做一个只读式 dashboard：

```bash
guanfu --cross-asset
```

但输出应是环境观察：

```text
BTC: valuation 偏低, technical 偏弱
Gold: risk-off support
SPY/QQQ: risk appetite neutral
DXY: weakening / strengthening
VIX: elevated / calm
Real yield: headwind / tailwind
```

不输出统一分数，不输出仓位建议。

## 不建议借鉴的地方

### 1. 不要做单一总分

单一总分会掩盖冲突信号。例如 valuation 低估、technical 破位、macro 收紧、flow 转负，在一个 0-100 分里会被平均掉。

guanfu 的优势正是保留多维冲突，而不是把冲突压平成分数。

### 2. 不要输出“今日定投倍数”

倍数指令隐含了用户资产规模、现金流、风险承受能力、税务、交易渠道、投资期限等信息。工具无法知道这些上下文。

guanfu 应输出“读盘”和“条件”，不输出个人化动作。

### 3. 不要硬编码回测结果

如果 guanfu 发布回测，应必须能复现：

```bash
guanfu backtest --strategy valuation-flow-v1 --from 2017-01-01 --to 2026-05-04
```

并同时输出：

- 数据哈希。
- 参数版本。
- 样本数量。
- 交易假设。
- 最差阶段。
- 失败案例。
- 与基准对比。

### 4. 不要让文档、页面、API 三套口径并存

btcdca.me 最大的可信度损伤来自口径不一致。

guanfu 应确保：

- CLI 输出的公式版本和源码一致。
- JSON schema 和文档一致。
- 回测使用同一套算法版本。
- 任何参数变化都记录版本号。

## 建议优先级

### P0: 增加算法与数据可审计字段

为每个指标增加：

- `formula_version`
- `source_version`
- `raw_fields`
- `staleness`
- `fallback_used`
- `calculation_note`

这是 guanfu 与 btcdca.me 拉开可信度差距的关键。

### P1: 增加 `--brief`

目标是降低阅读成本，但不牺牲多维结构。

建议 brief 固定输出五段：

1. 当前倾向。
2. 支撑证据。
3. 反证。
4. 数据健康。
5. 失效条件。

### P1: 扩展 cross_asset 域

优先加入稳定且可解释的跨资产指标：

- Gold / BTC 相对趋势。
- SPY、QQQ 与 BTC 的 30d / 90d 相关性。
- DXY 60d trend。
- VIX level / percentile。
- 10Y real yield。

### P2: 建立可复现回测目录

建议新增：

```text
docs/backtests/
  README.md
  valuation-flow-v1.md
  cross-asset-v1.md
scripts/backtest/
  ...
```

每篇回测报告必须包含命令、数据窗口、参数、结果、反例和限制。

### P2: 做只读 dashboard

可以考虑：

```bash
guanfu dashboard
```

但定位必须是“观测台”，不是“交易台”。

## 结论给 guanfu 的一句话

btcdca.me 证明了普通用户需要更低摩擦的表达；也反面证明了，如果算法、数据源、回测和 API 口径不一致，评分产品很快会失去研究可信度。

guanfu 最值得做的是：把 btcdca.me 的表达效率，嫁接到 guanfu 现有的证据链、反证、数据健康和可审计结构上。

## 参考来源

- btcdca.me 首页：<https://btcdca.me/>
- BTC 页面：<https://btcdca.me/btc/>
- 黄金页面：<https://btcdca.me/gold/>
- 纳指页面：<https://btcdca.me/nasdaq/>
- 标普页面：<https://btcdca.me/sp500/>
- 沪深300 页面：<https://btcdca.me/hs300/>
- 美股基金页面：<https://btcdca.me/us-fund-holdings/>
- 懒人组合页面：<https://btcdca.me/lazy/>
- 作者/转发帖镜像：<https://twstalker.com/sm_rzc>
