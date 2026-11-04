# 观复 v3 (guanfu) — 多资产投资决策辅助工具

> 致虚极,守静笃。**万物并作,吾以观复。** ——《道德经》第十六章

**guanfu 是面向有经验个人投资者的多资产决策辅助 CLI + MCP**。覆盖 **BTC / QQQ / SPY / Gold + 任意美股**。输出**原始指标 + 历史分位 + 前向收益分布 + 可靠性标注 + 数据源健康**,让人和 AI 在清楚每条证据可信度的前提下做综合判断。

**我们给建议** — 但用概率、区间、条件表达。所有建议落 claim ledger 定期回归校准,接受历史检验。**我们知道自己不适用的情景会明说** — 信号弱的 horizon 会直接标注可靠性 caveat,而不是伪装成预测。

[![Go Version](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-green)](LICENSE)

```bash
guanfu                    # 默认:10 行摘要(读盘 + TOP3 支持 + TOP2 反证 + 失效条件)
guanfu --full             # 完整 8 域 40+ 指标
guanfu btc --verdict      # BTC + 结构化读盘
guanfu qqq --forecast     # QQQ + kNN 历史相似推演
guanfu spy --verdict      # SPY 6 域面板
guanfu gold --verdict     # 黄金面板(实际利率 / DXY / COT)
guanfu stock AAPL         # 任意美股(Yahoo 自动拉取 + kNN)
guanfu import-stock MSFT  # 手动导入美股历史
guanfu refresh            # 统一刷新数据源(首次全量 / 后续增量)
guanfu dca                # DCA 定投策略对比
guanfu allocate           # 懒人组合偏离检测
guanfu market             # 多资产一览 + 共识/分歧信号
guanfu backtest all       # 全资产 kNN 回测
guanfu status             # 数据诊断
```

## 为什么用 guanfu(与同类项目的关键差异)

同类项目(btcdca.me / LookIntoBitcoin / Fear & Greed / Rainbow Chart)多数共用三个模式:单一 0-100 总分 × 线性 DCA 倍数 × 黑盒回测数字。guanfu 故意不做这些,原因和对应的差异化:

| 同类常见做法 | guanfu 的选择 | 为什么 |
|---|---|---|
| 把 40 指标压缩成 0-100 总分 | 输出原始值 + 历史分位 | 压缩必然丢失条件性;总分停在 50 附近无法区分 "杠杆过热上涨" 和 "机构买入上涨" |
| 所有 horizon 同等可信 | 每个 (资产, horizon) 独立打可靠性 caveat | QQQ 180d dir_hit 80% 和 Gold 180d 49% 不应该用同一套置信度 |
| 预测区间 = 经验分位 | Conformal 给统计覆盖下限,backtest 报实际覆盖 | p10/p90 在小样本下可能远不到 90% 覆盖率 |
| 数据源隐形降级 | `source_health` 显式标 ok/partial/stale/missing + fallback_used + impact | 用 fallback 做强结论容易自欺 |
| 回测只报全历史汇总 | Walk-forward 按 year × horizon 矩阵 | Gold 有强 regime 依赖,汇总数字会掩盖这些 |
| "BTC 必将突破 $X" | 概率化 / 条件化:"基于 A/B/C 三条证据,倾向积累,概率约 60%,若 X 指标突破 Y 则结论失效" | 过于肯定的话术是使用者的负担 |
| 无问责 | Claim ledger 每日自动积累 + `guanfu calibrate` 定期校准 | 给建议就要接受历史检验 |
| 没有"我不知道"按钮 | (已落地 v3.1) `dir_hit < 0.50` 时拒绝输出数值,只给 "信号低于随机" | 弱信号 horizon 不伪装成预测 |

## 可靠性标注 + 校准回归(v3)

### 静态 Reliability 表(load-time,零延迟)

每次 forecast 输出的 `HorizonForecast` 带 `reliability_note` + `hard_blocked` 字段。三档规则（`n < 10` / `dir_hit < 0.55` / `dir_hit < 0.50` hard-block）+ 当前 (asset, horizon) 命中率表见 [`skill/tier1.md`](skill/tier1.md) § 3。profile 真源 `pkg/assetprofile/profile.go`，渲染规则在 `pkg/forecast/reliability.go`。

### 动态 Calibration(run-time,校准实际历史)

`guanfu calibrate` 读 claim ledger,筛到期 claim,查 PriceStore 实际价,算四指标:

```
guanfu calibrate

ASSET           HRZN    N  DIR_HIT   INTERVAL% MED_ABS_E BRIER_UP
-----           ----    -  -------   --------- --------- --------
BTC               30   48    64.6%  77% (t=80%)     6.20   0.2100
BTC               90   32    65.6%  82% (t=80%)    11.50   0.1980
QQQ              180   11    81.8%  90% (t=80%)     7.10   0.1500
GOLD              90   20    55.0%  75% (t=80%)     5.40   0.2500
  ...

  DIR_HIT     方向命中率
  INTERVAL%   实际落入声明 [p10,p90] 区间的比例
  MED_ABS_E   median(|expected - actual|) 百分点
  BRIER_UP    P(up) vs 实际上行的 Brier score
```

如果连续 2-3 个月 `DIR_HIT` 掉超过回归预算(≥ 3pp),就是需要 RFC 的信号。详见 [`docs/archive/v3/guanfu-v3-todo.md`](docs/archive/v3/guanfu-v3-todo.md) 回归预算节。

`guanfu calibrate --json` 输出结构化,方便接入 CI / 自动监控。

## 已知失效情景(诚实的边界)

guanfu 明确不适用以下场景。不是它"可能"不适用,是它**结构上**不适用。

| 情景 | 为什么失效 | 建议行动 |
|---|---|---|
| **Regime shift / 结构断裂** | kNN 假设"历史分布可回归",2024+ BTC ETF / 2022+ Gold 央行购金改变主导变量,老样本的信号在新机制下不再成立。walk-forward 会率先看到(某年 dir_hit 剧降) | 用 `--forecast-recency-weighted` / `--forecast-regime-gate`;关注 `guanfu calibrate` 的趋势下降 |
| **黑天鹅 / 监管事件** | 脱锚 / 交易所暴雷 / 美国 SEC 行政令,事件本身超出指标覆盖范围;F&G / funding / mempool 等都会被污染 | 停用 guanfu,切到 news-dashboard;事件过去 30 天再回来重读 |
| **极端宏观 dislocation** | VIX > 35 / HY 利差 +100bp / 实际利率 > 3% 这种极端位置,历史样本稀疏,kNN 距离变得噪声 | 读 `skill/kb/09-crisis-playbook.md`,优先风险控制不是方向判断 |
| **Gold 180d** | 回测 49% dir_hit,强 regime 依赖 | 已从默认 horizon 移除;用 30/60/90 horizon |
| **小样本美股**(< 5 年历史) | kNN 需要至少 ~500 个候选样本;新 IPO 不够 | `guanfu import-stock` 前先看上市日期,< 3 年直接用 generic 技术指标 |
| **Altcoin / memecoin** | guanfu 只覆盖 BTC + ETH/BTC 比率,山寨币没有 ETF 流入 / 链上 / 宏观 proxy | 用 cmc-mcp / okx-dex,不用 guanfu |
| **单股基本面** | guanfu 用宏观 + 技术,无 per-name earnings / 管理层 / 行业周期 | 基本面研究用 octagon-mcp / SEC EDGAR |

**基本原则**:guanfu 是统计工具,不是新闻工具、不是基本面工具、不是风险管理工具。它知道自己不知道什么。

## 数据架构

数据集存于 `~/.guanfu/prices/` JSON 日频存档。来源:CoinMetrics / Binance / Yahoo / FRED / DefiLlama / CFTC / Shiller / CBOE / Deribit / CoinMarketCap / SoSoValue / CoinGecko / mempool.space / alternative.me。

统一 `guanfu refresh` 刷新框架(28 个 Source 实现):首次全量,后续增量(`last_date` ≤ 24h 跳过;月频 CAPE 28d TTL)。非 refresh 框架的 BTC 链上源独立拉取（CoinGecko / mempool / SoSoValue / alternative.me / Binance / CoinMetrics community）。

| 类别 | 数据集 | 来源 |
|---|---|---|
| 价格 | btc / qqq / spy / gold / stock_* | CoinMetrics+Binance / Yahoo |
| BTC 链上 | btc_mvrv / txcnt / hashrate | CoinMetrics |
| 宏观 | fred_dfii10 / dgs10 / dxy / yield_curve / breakeven / hy_spread / TGA / RRP / Fed-ECB-BOJ-PBoC rates | FRED |
| 黄金 | gold_cot | CFTC COT |
| 估值 | spx_cape | Shiller (1871+) |
| 期权/情绪 | deribit_dvol / deribit_skew_25d_pct / stooq_putcall | Deribit / CBOE |
| 加密市场上下文 | cmc_total_market_cap_usd / cmc_btc_dominance_pct / cmc_btc_price_usd | CoinMarketCap |

完整源列表 + 增量协议 + 降级路径: [`docs/DATA-SOURCES.md`](docs/DATA-SOURCES.md)。

## kNN 预测引擎 + 可靠性标注(stale-gated baseline,2026-05-11)

参见 [`skill/tier1.md`](skill/tier1.md) § 3 的 (asset, horizon) 可靠性表 + 三档规则。profile 真源 `pkg/assetprofile/profile.go`，渲染规则在 `pkg/forecast/reliability.go`。

## 设计哲学(v3.1)

1. **给建议,不给指令**。用概率 / 区间 / 条件表达,不用"一定 / 必然"。过于肯定的话术是使用者的负担。
2. **基线对比强制**。所有收益预期附 vs 3m T-bill、vs 60/40 组合。孤立的 "+5% 预期" 没意义。
3. **建议落盘回归校准**。给建议就要接受历史检验。
4. **盘面组合无关**。guanfu 输出 portfolio-agnostic 的市场事实；组合视角由上层（nimbus 持仓系统）叠加（2026-07 起 portfolio.json 已移除）。
5. **行为护栏**。投资失败 80% 是行为错误(FOMO / 恐慌 / 锚定),SKILL 会主动干预。
6. **诚实降级**。不适用的资产 / horizon 直接说,不伪装。
7. **MCP 原生**。SKILL 分层加载不挤占其他 skill 的 context。
8. **默认简,详情要 `--full`**。`guanfu` 裸跑只出摘要。

**三层分离保持不变**:盘面层(guanfu 二进制)→ 解读层(SKILL.md)→ 决策层(人 + AI)。

**用户画像**:见 [`docs/audience.md`](docs/audience.md)。Primary = 有经验 + 5y+ 期限 + 10k-1M USD 的个人投资者;Secondary = AI 重度用户(MCP 优先);Tertiary = 普通人通过 skill 分发触达。设计优先级 Secondary > Primary > Tertiary。

**演进路径**:v3 (Track K+M 已完成)。未来方向见 [`docs/v4-thinking.md`](docs/v4-thinking.md)。

## 安装

```bash
# 一行安装(需 Go 1.26+)
go install github.com/Ricaardo/guanfu/cmd/guanfu@latest
go install github.com/Ricaardo/guanfu/cmd/guanfu-mcp@latest       # MCP server
go install github.com/Ricaardo/guanfu/cmd/guanfu-similar@latest   # 可选:历史相似度

# 或源码构建
git clone https://github.com/Ricaardo/guanfu.git
cd guanfu && make all
```

**没有 Go?** 从 [Releases 页面](https://github.com/Ricaardo/guanfu/releases) 下载预编译二进制(linux/darwin/windows × amd64/arm64),解压即用。

**完整部署指南**(含 MCP 集成 / cron 定时任务 / API key / 故障排查):见 [`docs/deployment.md`](docs/deployment.md)。

**Futu Bridge(可选,获取 QQQ/SPY/DXY/VIX 实时数据)**:

```bash
pip install futu-api

# 推荐：放到 ~/.guanfu/ 下，guanfu 会自动找到（cron / launchd / 任意 PATH 都生效）
mkdir -p ~/.guanfu
curl -sL https://raw.githubusercontent.com/Ricaardo/guanfu/main/pkg/client/futu_bridge.py \
  -o ~/.guanfu/futu_bridge.py

# 或放到二进制同目录 / ~/.config/guanfu/ / 通过 FUTU_BRIDGE 环境变量自定义
```

guanfu 按以下顺序查找 bridge：`$FUTU_BRIDGE` → 二进制同目录 → `~/.guanfu/` → `~/.config/guanfu/`。

## 使用

```bash
# 第一次运行（不写 history.db，最轻量）
GUANFU_NO_HISTORY=1 guanfu

# 完整盘面（人类可读，冷启动通常 60-90s，缓存命中 < 1s；首次会创建 ~/.guanfu/history.db）
guanfu

# JSON 输出 → 喂 Claude / ChatGPT
guanfu --json | jq

# 历史相似盘面走势推演（概率分布，不是确定性价格目标）
guanfu --forecast --plain
guanfu --forecast-only --pretty
guanfu --forecast --forecast-horizons 30,90,180 --forecast-top 21

# 只看关心的域
guanfu --domain valuation    # 估值
guanfu --domain technical    # 技术指标
guanfu --domain cross_asset  # 跨资产对比

# 参数
guanfu --halflife 730        # AHR 半衰期（默认 1460=4年）
guanfu --timeout 180s        # 超时
guanfu --plain               # 纯文本输出（无 emoji / box drawing）
guanfu --version             # 打印版本（提 issue 时附上）
GUANFU_NO_HISTORY=1 guanfu   # 跳过 history.db
```

**配合 AI 使用**：

```bash
# Claude Code: 直接对话"BTC 现在怎么样？"（skill 自动调用）

# Claude API / ChatGPT: 将 JSON + SKILL.md 附加到 prompt
guanfu --json > panel.json
cat panel.json | your-ai-client --system "$(cat skill/SKILL.md)"
```

**历史相似度（推广/复盘用）**：

```bash
# 一次性比对
guanfu --json | guanfu-similar --top 8         # 默认 --history-dir ~/.guanfu/panels

# 每日 archive 一份盘面（cron / launchd）
mkdir -p ~/.guanfu/panels
guanfu --json > ~/.guanfu/panels/$(date -u +%F).json

# crontab 行（每天 09:00）
0 9 * * * /usr/bin/env -S bash -lc 'guanfu --json > ~/.guanfu/panels/$(date -u +%F).json 2>> ~/.guanfu/cron.log'
```

archive 攒满 30+ 天后，`guanfu-similar` 给出的"今天与历史最相似的盘面"才有统计意义。相似度只比较双方都有 `q` 的指标，方法见 [docs/backtest-methodology.md](docs/backtest-methodology.md)。公开文案里的历史收益数字必须由该流程生成，并披露样本数量、窗口和反例。

**走势推演**：

`guanfu --forecast` 使用 `pkg/assetprofile` 中每个资产自己的 profile 做 historical analogue / kNN：profile 决定默认 horizon、feature bundle、reliability、conformal scale、horizon 权重和 skill profile URI。BTC 用价格/周期/估值特征；QQQ/SPY 用技术、CAPE、FRED 宏观、VIXY 和 CBOE put/call；Gold 用技术、实际利率、通胀预期、DXY、COT 和 VIXY；任意美股用技术 + 通用宏观。引擎会把当前状态与历史日期做加权距离匹配，并按 horizon 重新加权候选样本：短周期偏技术/风险，长周期偏估值/宏观。

输出包括：情景概率（上行延续 / 区间震荡 / 下行压力）、收益分位数、隐含价格分布、相似历史样本、特征覆盖率、conformal 区间和 reliability caveat。只有进入 PriceStore 且能历史回放的序列会进 forecast；ETF 实时流、funding、mempool 这类 latest-only 指标仍只用于读盘，除非先补齐历史并通过回测。

读盘 JSON 的 `IndicatorPanel` 也带 profile metadata：`profile_key`、
`profile_version`、`asset_class`、`skill_profile_uri` 和 `domain_meta`。
`domain_meta` 是当前资产的读盘 lens；例如 Gold 的 VIX 高位会按避险需求解读，
不再复用权益 ETF 的 VIX 看空语义。

## Demo

```text
$ guanfu --plain
guanfu BTC panel (2026-05-02)   price: $78209.73
BTC dominance: 58.46%   F&G: 39   total cap: $2.7T

Cycle 周期定位
  days_since_halving             742  顶部 / 分配期 (18-30m)
  mayer_multiple              0.9325  q20  偏低估
  pi_cycle_top_ratio          0.3868  未触发
  sma_200w_dev                +29.42%  q81  正常区
  ...

Valuation 估值
  ahr999_compressed           0.5863  低估区
  ahr999                      0.7224  q25  低估区（自适应辅助）
  mvrv_z_score                1.2613  中性偏低
  ...

Network 网络
  hash_ribbons               下行（矿工投降信号 ⚠）
  ...
```

完整 8 域 40+ 指标见 [`skill/SKILL.md`](skill/SKILL.md)。

## 8 域指标体系

| 域 | 核心指标 |
|----|----------|
| 🌊 Cycle 周期 | halving 天数、200wSMA 偏离、Mayer Multiple、Pi Cycle Top |
| 💰 Valuation 估值 | **ahr999_compressed**（推荐）、ahr999（自适应）、ahr999_divergence、MVRV、NUPL |
| ⛏️ Network 网络 | 哈希率、Hash Ribbons、难度调整、Mempool 拥堵 |
| 📊 Positioning 杠杆 | 资金费率、OI/MC、恐慌贪婪、山寨季指数（自算） |
| 🌍 Macro 宏观 | DXY 60d、10Y TIPS、M2 YoY、SPX 相关、**USD/CNY**、**Fed/ECB/BOJ/PBoC 前端利率**、**USO 油价 proxy / WTI fallback**、**HY 信用利差**、**10Y-2Y 利差** |
| 💸 Flow 资金流 | ETF 7d/30d 净流入、稳定币市值、ETH/BTC 资金偏好 |
| 📈 Technical 技术 | RSI(14)、MACD 柱、EMA 交叉、MA50/200、Bollinger、波动率 |
| 🔗 CrossAsset 跨资产 | BTC/Gold·QQQ·SPY/UUP/VIXY/GLD 比率、相关性、相对强弱、**BTC/原油** |

## 环境变量

完整列表（~20 个变量，含默认值 + 说明）见 [`docs/deployment.md`](docs/deployment.md) § 环境变量速查。核心变量：

| 变量 | 说明 |
|------|------|
| `FRED_API_KEY` | FRED 宏观数据（无 key 则 Macro 域为 placeholder；免费注册） |
| `CMC_API_KEY` | CoinMarketCap global metrics / BTC quote 市场观察层；不影响核心价格或 forecast |
| `COINMETRICS_API_KEY` | CoinMetrics 付费端点 |
| `GUANFU_NO_HISTORY=1` | 禁用 history.db 写入/查询 |

CBOE total put/call（存储 key 仍叫 `stooq_putcall`）和 Deribit DVOL/skew 都走官方免费无 key 路径，默认不需要 `STOOQ_APIKEY`。

## 历史分位系统

ETF、mempool、资金费率等指标没有公开历史 API。guanfu 通过 SQLite (`~/.guanfu/history.db`) 每天采集一行，攒够 30 天后回填 `q`（历史分位）字段。

- 第 1 天：15 个指标入库，无 q 显示
- 第 30 天：开始显示 q
- 第 365 天：覆盖全年节奏
- 第 730 天：达到查询窗口上限

## 数据源

| 数据源 | 用途 | 免费 |
|--------|------|------|
| CoinMetrics + Binance | BTC 日线全历史（2010-07-18 至最新；CoinMetrics `PriceUSD` + Binance 最新日线覆盖） | ✅ |
| Binance | ETH K线 (3000d)、Top50、资金费率、OI | ✅ |
| CoinGecko | 总市值、BTC 市占率、稳定币市值 | ✅ |
| mempool.space | 哈希率 (3y)、难度、mempool | ✅ |
| SoSoValue | BTC ETF 净流入 | ✅ |
| alternative.me | 恐慌贪婪指数 | ✅ |
| CoinMetrics | BTC `PriceUSD` 全历史、MVRV/NUPL/MVRV Z | ✅ 社区端点 |
| Yahoo Finance | QQQ/SPY fallback、CL=F WTI futures fallback | ✅ |
| Futu OpenD | QQQ/SPY/GLD/UUP/VIXY、USO 油价 proxy (本地网关，需 Python bridge) | ✅ |
| FRED | DXY/10Y TIPS/M2/SPX/HY利差/10Y-2Y利差/TGA/RRP/美欧日中前端利率 | 需注册(免费) |
| Deribit | BTC DVOL + 25-delta skew | ✅ 无 key |
| CBOE | Total put/call ratio (`stooq_putcall` legacy storage key) | ✅ 无 key |
| CoinMarketCap | Global metrics / BTC quote 独立交叉检查;后续 CEX/DEX 候选 | 需 API key |

JSON 顶层包含 `source_health`，用于查看每个数据源的 ok/partial/stale/missing/warning 状态、`as_of`、fallback、`impact` 和 warning。

## AI 集成

guanfu 设计为 AI 原生工具，可接入多种 AI 平台：

| 平台 | 方式 | 状态 |
|------|------|------|
| **Claude Code** | `btc-guanfu` skill → 调用 CLI JSON | ✅ 已可用 |
| **Claude Desktop / Cursor** | MCP Server 封装 guanfu | ✅ 已可用 |
| **ChatGPT** | GPT Action → REST API | 📋 计划 |
| **任意 AI** | CLI JSON + System Prompt | ✅ 已可用 |

guanfu 设计为 AI 原生工具,可接入多种 AI 平台(见 [`docs/deployment.md`](docs/deployment.md) MCP 集成节)。

## 关键算法

### AHR999（三个版本）

| 版本 | 面板指标 | 公式 | 用途 |
|---|---|---|---|
| 自适应版 | `ahr999` | 调和DCA + Huber IRLS 动态拟合 + 动态分位数 | 辅助确认 |
| **压缩版** | **`ahr999_compressed`** | pow(固定公式 AHR, 0.75)，阈值 pow(x,0.75) 映射 | **推荐主用** |
| 分歧检测 | `ahr999_divergence` | 固定公式 vs 自适应百分位方向不一致 | 转向预警 |

压缩版回测验证（2010-2026, 5767天）：
- `<0.549`（映射 <0.45）：n=544, fwd180 +79.6%, 胜率 91%
- `0.549-0.846`（映射 0.45-0.8）：n=1389, fwd180 +72.3%, 胜率 89%  
- `3.344-9.457`（映射 5.0-20.0）：n=226, fwd180 **-14.7%** ← 高风险信号
- `>9.457`（映射 ≥20.0）：n=49, fwd180 **-40.5%**, 胜率 0%

详见 `docs/archive/v2/backtest-baseline-ahr999-*-*.md`。

### 山寨季指数（自算）

基于 Binance Top 50 kline：90 日跑赢 BTC 的占比 × 100。**零外部 API 依赖**。

### 历史相似盘面推演

`pkg/forecast` 将 BTC 全历史日线转成可回放特征向量，并用加权 kNN 输出前向收益分布：

- 默认周期：30d / 90d / 180d
- 默认样本：21 个相似历史状态，按 30 天窗口去重，减少同一阶段日频重复样本的权重
- 特征：30/90/180d 动量、90d 回撤、Mayer、200wSMA 偏离、30d 波动率、RSI、压缩 AHR999、halving cycle sin/cos
- 输出：收益分布、情景概率、相似样本、coverage/confidence

该模块只做情景推演，不输出交易或仓位指令。

### MVRV Z-Score

使用 rolling 1-year std(market_cap - realized_cap)，非全历史标准差。

### Hash Ribbons

30d MA vs 60d MA（180 天窗口），下行 = 矿工投降 = 历史底部前常见。

## 回测

```bash
# AHR999 全历史对比报告（默认复用生产 BTC 日线缓存）
guanfu-backtest --all-data --interval 7 --report-md report.md

# 自定义日期范围 + 外部指标注入
guanfu-backtest --start 2020-01-01 --end 2026-01-01 --indicators indicators.json --report-md report.md

# 导出逐日 AHR999 CSV
guanfu-backtest --all-data --ahr-csv ahr_daily.csv
```

生产与回测共用 BTC 日线缓存：默认 `$CACHE_DIR/btc_daily_history.json`（未设置 `CACHE_DIR` 时使用系统用户缓存目录），也可用 `GUANFU_BTC_KLINE_CACHE=/path/to/btc_daily_history.json` 指定固定路径。缓存从 CoinMetrics `PriceUSD` 建立 2010-07-18 起的全历史，并在每次未命中快照缓存的运行中用 Binance 最新日线增量覆盖。

`--kline-cache` 仍支持手动指定缓存文件（兼容生产缓存 envelope 和旧格式 `{"YYYY-MM-DD": close_price, ...}`）。`--start` 不传时默认从 4 年前开始；`--all-data` 覆盖 `--start`，从 2010-07-18 起算。

回测报告结构：Verdict 基线 → AHR999 三版对比（原始/改良/压缩）→ 3D 评分（V/M/P 8 组合）→ 分桶过渡矩阵。

## AI 知识库

`skill/` 是一个 self-contained 的 Claude Code skill 包：`SKILL.md`（数据契约 + 指标手册）+ `kb/`（10 个因果推理文件，~1500 行）。安装见 [`skill/README.md`](skill/README.md)。

| 文件 | 内容 |
|---|---|
| `00-data-contract.md` | 盘面 JSON schema + 域/指标语义 |
| `01-macro-transmission.md` | 利率/通胀/美元/财政 传导链 |
| `02-liquidity-plumbing.md` | 稳定币/ETF/杠杆 流动性管道 |
| `03-crypto-mechanics.md` | 减半/矿工/LTH/MVRV 结构性因子 |
| `04-cross-asset.md` | BTC vs Gold/SPX/Bonds 联动规则 |
| `05-geopolitical.md` | 5 类地缘冲击 × BTC 反应时间线 |
| `06-regime-taxonomy.md` | 6 种宏观测算 + 转换信号 |
| `07-historical-analogues.md` | 历史相似组合 + 类比库 |
| `08-decision-matrix.md` | 不同测算下的权重 + 不做什么 |
| `09-crisis-playbook.md` | 30s 危机判别 → 保命优先级 → 恢复确认清单 |

## 项目结构

```
guanfu/
├── cmd/
│   ├── guanfu/                  # CLI 入口
│   ├── guanfu-mcp/              # MCP stdio server
│   ├── guanfu-similar/          # 历史 JSON 盘面相似度
│   ├── guanfu-backtest/         # 回测 CLI（独立于 guanfu backtest 子命令）
│   └── guanfu-threshold-search/ # 阈值搜索辅助
├── pkg/
│   ├── client/      # refresh 框架 + 各数据源专用拉取
│   ├── assetprofile/# 资产 profile registry: horizons / bundles / reliability / calibration
│   ├── engine/      # Asset 接口 + 8 域盘面构建 + verdict 引擎
│   ├── forecast/    # kNN 推演 + reliability + conformal + ensemble
│   ├── store/       # PriceStore JSON 日频持久化
│   ├── history/     # SQLite 历史分位（15 个非价格指标，730d 滚动）
│   ├── model/       # IndicatorPanel / Indicator / SnapshotData 等类型
│   ├── mathutil/    # 技术指标（MA/EMA/MACD/RSI/BB）
│   ├── claim/       # Claim ledger（v3 K 系列；intent 已归 nimbus 侧）
│   ├── alerts/      # watch 告警 store（v3 L 系列）
│   ├── calendar/    # 事件日历（FOMC/CPI/halving）
│   ├── allocate/    # 懒人组合配置
│   ├── dca/         # DCA 定投策略对比
│   ├── cache/       # 行情快照磁盘缓存
│   └── version/     # 构建版本
├── scripts/         # import_cape.py（Shiller CAPE 导入）
├── docs/            # 项目文档（v3 内部 + archive/v2 v3）
├── skill/           # Claude Code skill 包（SKILL.md + tier1/2.md + kb/）
├── .github/workflows/  # CI / release
├── Makefile
└── README.md
```

详细子模块职责见 [`CLAUDE.md`](CLAUDE.md) "文件索引"节。

## License

MIT
