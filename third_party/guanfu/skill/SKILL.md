---
name: btc-guanfu
description: |
  观复 v3 / 观察万物之周期回归 — 多资产投资盘面 + 解读知识库。覆盖 BTC/QQQ/SPY/Gold + 任意美股。BTC 输出 8 域 40+ 指标，权益/黄金输出 6 域面板含顶底检测、估值区间和宏观上下文。支持 kNN 走势推演（含 reliability 标注 + conformal 区间 + ensemble disagreement）、claim ledger 校准、行为护栏、DCA 定投回放、多资产回测（2026-07 起 intent/portfolio 上下文归 nimbus 侧持仓与论点系统）。**不输出单一评分或交易指令**。
  当用户问「BTC/QQQ/SPY/黄金 该不该买/卖」、「估值如何」、「顶/底在哪」、「定投」、「AHR999/MVRV/哈希率/ETF」、「周期位置」、「观复」时触发。
  NOT for: 仅查实时价格 → cmc-mcp；altcoin/memecoin → cmc-mcp/okx-dex；K线形态 → technical-analysis。
license: MIT
user-invocable: true
required_tools:
  - guanfu        # https://github.com/Ricaardo/guanfu — `go install github.com/Ricaardo/guanfu/cmd/guanfu@latest`
optional_tools:
  - jq            # JSON 解析
  - python3       # Futu / CAPE 数据桥接
---

# 观复 v3 (btc-guanfu): 多资产投资盘面 + 解读手册

> 致虚极,守静笃。**万物并作,吾以观复。** ——《道德经》第十六章

## 分层加载(v3 新增,推荐给 MCP / AI 客户端)

**这份 SKILL.md 是完整版(~900 行)**。日常读盘不必全量加载,按需拉取分层 resource:

| URI | 内容 | 何时读 |
|---|---|---|
| `guanfu://skill/tier1` | ~200 行数据契约 + 关键阈值 + 可靠性标注 + 必出规则 | **每次读盘都先读** |
| `guanfu://skill/tier2` | 读盘流程 / 域级方向 / 输出模板 / 行为护栏 | 做判断 / 给建议时读 |
| `guanfu://skill/tier3`(同本文件) | 指标定义细节 / 因果机制 / 历史类比 | 用户追问原理 / 历史时读 |
| `guanfu://knowledge/skill.md`(本文件) | 完整版,含所有节 | 向后兼容别名,新客户端优先用 tier1/2/3 |

**推荐加载序列**:tier1(必载)→ 对应 asset profile → tier2(决策时)→ 本文件特定章节(追问细节时)→ `kb/XX-*.md`(深度因果 / 类比 / 危机)

### Asset Profile 路由

Guanfu 的长期架构是 profile-first:BTC、权益指数、黄金、任意美股各自有不同读盘域、forecast 特征尺度、可靠性和 caveat。不要把 BTC 的 cycle/network/halving/AHR 语义套到其他资产。

| 用户问题 | 必载 profile | 说明 |
|---|---|---|
| BTC / 比特币 | `profiles/btc.md` | 8 域 crypto-cycle lens |
| QQQ / SPY / 美股指数 ETF | `profiles/equity_index.md` | 权益指数 lens:估值、利率、信用、期权、技术 |
| 黄金 / Gold / XAU | `profiles/gold.md` | 实际利率、美元、通胀、COT、避险 |
| 任意美股 ticker,如 AAPL/MSFT/NVDA | `profiles/us_stock.md` | 单一个股 lens,默认更保守,强调事件风险 |

新增资产必须先满足 `contracts/asset-profile.md` 和 `contracts/adding_asset.md`。架构决策见 [`docs/architecture/asset-profile-refactor.md`](../docs/architecture/asset-profile-refactor.md)。

## CLI 命令

```bash
guanfu                    # 默认 10 行摘要(读盘口径 + TOP3 支持 + TOP2 反证 + 失效 + source_health)
guanfu --full             # 完整 8 域 40+ 指标盘面
guanfu btc --verdict      # BTC + 结构化读盘 + 顶底检测
guanfu btc --forecast     # BTC kNN 历史相似走势推演
guanfu qqq --verdict      # QQQ 6 域面板(技术/估值/宏观/情绪)
guanfu spy --verdict      # SPY 6 域面板
guanfu gold --verdict     # 黄金面板(估值:实际利率 / DXY / VIX)
guanfu stock AAPL         # 任意美股(Yahoo 自动拉取 + kNN)
guanfu import-stock MSFT 3650  # 手动导入美股全量历史
guanfu refresh            # 统一刷新数据源(首次全量 / 后续增量)
guanfu refresh --dry-run  # 列出将刷新的 source,不真正拉数据
guanfu --timeout 180s refresh --only=stooq_putcall  # 单独刷新 CBOE put/call(legacy key,无 Stooq key)
guanfu dca                # DCA 定投策略对比(Fixed/AHR/Mayer)
guanfu allocate           # 全天候懒人组合偏离检测
guanfu market             # 多资产一览 + 共识/分歧信号
guanfu backtest btc       # kNN 回测(单资产)
guanfu backtest all       # 全资产回测 + 对比报告
guanfu status             # PriceStore 数据状态诊断
```

**`--brief` vs `--full`**(v3 新默认):
- 裸跑 `guanfu` / `guanfu qqq` → 默认 10 行摘要(brief mode)
- `--full` / `--verdict` / `--forecast` / `--domain` 任一显式指定 → 走完整盘面
- `--json` / `--pretty` → 结构化输出,不走 brief

## MCP 工具 (`guanfu-mcp`)

通过 stdio 暴露 MCP 协议接口；Claude Desktop / Cursor / 任何 MCP 客户端可直调，无需 Bash 权限。所有工具支持 `asset` 参数切换 BTC/QQQ/SPY/Gold。

| 工具 | 说明 | 关键参数 | 输出 |
|---|---|---|---|
| `get_panel` | 完整盘面(多资产)。BTC = 8 域 40+ 指标;权益/黄金 = profile lens | `asset` (默认 btc), `timeout_seconds` (默认 90) | `IndicatorPanel` JSON:`asset` / `profile_key` / `domain_meta` / `date` / `snapshot` / domain maps / `source_health` |
| `get_verdict` | 结构化多域读盘 | `asset`, `timeout_seconds`, `include_panel` (默认 false) | `Verdict` JSON:`net_direction` / `regime` / `domains` / `top_proximity` / `bottom_proximity` |
| `get_forecast` | kNN 历史相似走势推演 | `asset`, `horizons` (默认 auto = 资产专属),  `top_k` (默认 21,最小 5),`include_panel` | `Forecast` JSON:每个 horizon 的 reliability_note / hard_blocked / quantiles + 邻居 |
| **`get_stock_forecast`** | **任意美股 kNN 推演**(Yahoo 自动拉取 + `USStockExtractors`) | `ticker` (必填,如 AAPL/MSFT), `horizons`, `top_k`, `days` (默认 3650) | 同 `get_forecast`,但 asset 为 `stock_<ticker>` |
| `get_domain` | 单个域盘面 | `domain` (cycle/valuation/network/positioning/macro/flow/technical/cross_asset), `asset` | 该 domain 的 `map[string]Indicator` |
| `get_indicator` | 单个指标 | `name` (如 ahr999, rsi_14, vix_level), `asset` | 单个 `Indicator` JSON |

### 旧名向后兼容

`get_btc_panel` / `get_btc_verdict` / `get_btc_forecast` 是 deprecated alias，dispatch 同新名相同 handler。新代码用 `get_panel` 等不带 `_btc_` 的名字；旧客户端不需迁移即可继续工作。

### Resources

| URI | 内容 |
|---|---|
| `guanfu://skill/tier1` | **必载**:数据契约 + 关键阈值 + 可靠性规则(~200 行) |
| `guanfu://skill/tier2` | 决策框架 + 行为护栏(做判断时读) |
| `guanfu://skill/tier3`(当前同下) | 深度细节(追问原理时读) |
| `guanfu://knowledge/skill.md` | 完整版 SKILL.md(向后兼容,新客户端优先 tier) |
| `guanfu://skill/profiles/btc` | BTC 资产画像与读盘契约 |
| `guanfu://skill/profiles/equity_index` | QQQ/SPY 权益指数契约 |
| `guanfu://skill/profiles/gold` | 黄金契约 |
| `guanfu://skill/profiles/us_stock` | 任意美股契约 |
| `guanfu://skill/contracts/asset-profile` | 新资产必须满足的 profile 合同 |
| `guanfu://skill/contracts/adding_asset` | 新增资产 checklist |
| `guanfu://panel/latest` | BTC 盘面(deprecated alias,= `/latest/btc`) |
| `guanfu://panel/latest/{btc,qqq,spy,gold}` | 每资产盘面 |
| `guanfu://verdict/latest/{asset}` | 每资产 verdict |
| `guanfu://forecast/latest/{asset}` | 每资产 forecast |

### 调用示例

```jsonc
// tools/call get_panel for QQQ
{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{
  "name":"get_panel",
  "arguments":{"asset":"qqq","timeout_seconds":60}
}}

// tools/call get_forecast for Gold, custom horizons
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{
  "name":"get_forecast",
  "arguments":{"asset":"gold","horizons":[30,90],"top_k":15}
}}
```

### Claude Desktop / Cursor 配置

```json
{
  "mcpServers": {
    "guanfu": {
      "command": "/path/to/guanfu-mcp",
      "env": {
        "GUANFU_HISTORY_DB": "/path/to/history.db",
        "GUANFU_SKILL_PATH": "/path/to/skill/SKILL.md"
      }
    }
  }
}
```

## 数据架构 (PriceStore)

数据集存于 `~/.guanfu/prices/` JSON 日频存档；统一 `guanfu refresh` 拉取。

完整源列表 + 增量协议 + 降级路径见 [`docs/DATA-SOURCES.md`](../docs/DATA-SOURCES.md)。

关键源边界:
- `stooq_putcall` 是 legacy storage key,默认来源为 CBOE official total put/call,无需 `STOOQ_APIKEY`;用于 QQQ/SPY put/call ratio、30d change、252-observation percentile。
- `deribit_options` 写入 BTC `deribit_dvol`、`deribit_skew_25d_pct`、`deribit_skew_expiry_days`;skew 为 signed series,正值表示 put 更贵,负值表示 call 更贵。
- `cmc_market_context` 需要 `CMC_API_KEY`,只进入 market_reading,不替代 BTC 核心价格历史,也不直接进入 forecast。

## kNN 预测引擎 + 可靠性标注

Pluggable feature extractors（per-asset bundle：Core / Equity / Gold / USStock），Mahalanobis 距离，状态匹配，动态 TopK。`pkg/assetprofile` 已是 profile 侧单一真源:profile 决定资产类、默认 horizon、feature bundle、horizon 权重、reliability、conformal calibration scale、reading domain metadata、verdict 外层 policy 和 skill profile URI。下一阶段还要把 raw feature normalization scale 与 indicator-level scoring 规则迁入 profile-backed contracts。

每个 `Forecast` 顶层附 `profile_key` / `profile_version` / `asset_class` / `feature_bundle` / `skill_profile_uri`；每个 `HorizonForecast` 附 `reliability_note` + `hard_blocked` 字段。三档规则、当前 (asset, horizon) 命中率表、`hard_blocked` 时的契约（数值字段保留但不渲染），全部见 [`tier1.md`](tier1.md) § 3 "可靠性标注"——**AI 读盘必载**。profile 真源 `pkg/assetprofile/profile.go`，渲染规则在 `pkg/forecast/reliability.go`。

---

## 用法

```bash
# 完整盘面（人类可读）
guanfu

# JSON（喂 Claude / 程序）
guanfu --json | jq

# BTC 走势推演（历史相似盘面 + 前向收益分布）
guanfu --forecast --plain
guanfu --forecast-only --pretty
guanfu --forecast --forecast-horizons 30,90,180 --forecast-top 21

# 仅看一个 domain
guanfu --domain cycle
guanfu --domain technical
guanfu --domain cross_asset
# ... valuation / network / positioning / macro / flow

# 纯文本输出（适合复制到无 emoji / box drawing 的环境）
guanfu --plain

# AHR999 拟合半衰期（默认 1460 = 4 年）
guanfu --halflife 730   # 2 年，对快牛快熊敏感
```

冷启动通常 60-90s（需要拉 Binance 历史 K 线、Top50、mempool、SoSoValue、FRED/Futu/Yahoo 等），缓存命中 <1s。

**富途 OpenD 集成**：
- 默认自动连接 `127.0.0.1:11111`
- `FUTU_GATEWAY` 环境变量覆盖地址
- `FUTU_ENABLED=0` 禁用富途，走 Yahoo 降级
- 数据：QQQ / SPY / GLD / UUP (DXY proxy) / TLT / VIXY / USO (oil proxy)

**Futu Bridge 部署**（OpenD 默认需要加密握手，Go 端未实现）：
1. `pip install futu-api`（安装官方 Python SDK）
2. 将 `futu_bridge.py` 放到 `bin/` 同目录
3. guanfu 自动检测：Go 直连失败 → 调 Python bridge → Yahoo 降级
4. 如 bridge 脚本在其他位置，设 `FUTU_BRIDGE=/path/to/futu_bridge.py`

**重建二进制**：`go build -o bin/guanfu ./cmd/guanfu`

---

## 历史分位 (history.db)

ETF / mempool / 资金费率 / 宏观等没有公开历史 API 的指标，guanfu 通过 SQLite 每日采集，攒够 30 天后开始显示 `q`（历史分位），730 天滚动窗口回看。详细（15 个采集指标 + 时效表 + 直接查询）见 [`docs/DATA-SOURCES.md`](../docs/DATA-SOURCES.md)。

**消费方须知**：`q` 是 [0,1] 归一化的历史分位，`q05` = 历史最低 5%。BTC 价格相关指标（sma_200w_dev / mayer / AHR / RSI 等）的 q 由 BTC 全历史日线缓存直接计算，不进 history.db。

## 走势推演 (`--forecast`)

`guanfu --forecast` 是 **historical analogue / kNN 情景推演**，不是确定性价格预测，也不是交易/仓位指令。

当前实现按 `pkg/assetprofile` 中的 profile 使用不同的可回放 feature bundle：
- BTC:30/90/180d 动量、90d drawdown、Mayer、200wSMA 偏离、30d 年化波动率、RSI、压缩版 AHR999、halving cycle sin/cos。
- QQQ/SPY:价格动量、drawdown、Mayer、波动率、RSI、CAPE、FRED rates/USD/credit/yield curve、VIXY、CBOE put/call ratio / 30d change / 252d percentile。
- Gold:价格动量、drawdown、Mayer、波动率、RSI、real yield、breakeven、DXY、COT、VIXY。
- 任意美股:价格技术特征 + 通用宏观背景,不含 per-name 基本面。

匹配时会按 horizon 重新加权：30d 偏短期技术/风险，90d 偏中期趋势与宏观边际变化，180d+ 偏估值/宏观/头寸。外部序列有最大 forward-fill 容忍（如 CBOE put/call 5 个交易日、FRED 7 天、CAPE 45 天），超过后该特征缺失，不参与距离计算。

输出：
- 30/90/180d 前向收益分布（avg/median/p10/p90）
- 情景概率：上行延续 / 区间震荡 / 下行压力
- 隐含价格分布（expected/median/p10/p90）
- 相似历史样本、特征覆盖率、平均相似度、置信度

解读原则：
- 把 `forecast` 当作概率分布和反证生成器，不要把 median/expected price 当作目标价。
- 若 coverage 或 confidence 低，只能用于提醒不确定性。
- 只有能在 PriceStore 中历史回放的序列进入 forecast；ETF 实时流、funding、mempool 等 latest-only 指标只做读盘,除非先补齐历史并通过回测。

---

## 盘面字段说明

每个指标返回：
- **`value`** 原始数值（无 sigmoid，无 scaling）
- **`q`** 历史分位 [0, 1]（当前值在历史分布的位置；q05 = 历史最低 5%，q95 = 历史最高 5%）
- **`label`** 简短解读标签（仅人类速览用，**Claude 不应直接信赖此 label 做决策**，应自己读 value + q）
- **`source`** 数据源
- **`updated_at`** 数据时间
- **`note`** 计算备注 / 数据时效性

盘面顶层还会返回：
- **`stale_warnings`** 非致命数据缺失 / 过期提示。
- **`source_health`** 数据源健康表：`source`、`status`（ok / partial / stale / missing / warning）、`as_of`、`fallback_used`、`impact`、`note`、`warnings`。读盘前先检查这里，避免用断源或 fallback 数据做强结论。

---

## 指标手册（每个含定义、历史阈值、失效情形）

### 🌊 Cycle — 周期定位

#### `days_since_halving` / `days_to_halving`
- **定义**：距上次/下次 BTC 减半的天数。减半固定每 ~1458 天发生。
- **历史阶段（粗略）**：
  - 0-180d 减半后早期 — 价格通常温和反弹
  - 180-540d 牛市主升期 — 历史顶部基本都在 18 个月内
  - 540-900d 顶部 / 分配期 — 警惕分布
  - 900-1260d 熊市 / 积累期 — 历史积累观察期
  - 1260-1458d halving 前期 — 反弹起步
- **失效**：2024 ETF 通过后周期可能被宏观流动性主导，时间相位仅作粗参考。

#### `sma_200w` / `sma_200w_dev`
- **定义**：200 周（1400 日）简单均线（"BTC 长期价格地板"）+ 当前价偏离。JSON raw `value` 是比例值（`1.5` = `+150%`），展示层可格式化成百分比。
- **历史阈值**：
  - dev < 0 → BTC 跌破 200 周线，**罕见** — 仅 2015、2018 末、2020 3 月、2022 LUNA 危机等阶段出现，通常代表深度压力区
  - dev 0~1.0（0~+100%）→ 正常区
  - dev > 1.5（+150%）→ 牛市末期（2017 12月、2021 4月、2021 11月）
- **可信度**：较高，是 BTC 常用长期估值锚，但仍需结合宏观和危机状态。
- **失效**：算法本身不会失效。但 200wSMA 会随每日推进上移，"低估"标准在变。

#### `mayer_multiple` (price / 200d SMA)
- **历史阈值**：
  - < 0.6 → 历史深度低估区（2015、2018、2020、2022）
  - 0.6-1.0 → 偏低估
  - 1.0-2.4 → 中性区间
  - > 2.4 → 高估 / 顶部风险区（2013、2017、2021 一波二波）
- **失效**：2024 后波动率压缩，2.4 顶可能不再触发（ETF 改变波动结构）。

#### `pi_cycle_top_ratio` (111dMA / 2×350dMA)
- **定义**：111 日均线 / (2 × 350 日均线)。≥1 触发 = 历史多次临近顶部风险区。
- **历史命中**：2013-12, 2017-12, 2021-04 附近曾给出高风险信号。
- **失效情形**：2024+ 减半周期开始压缩，可能给假信号或滞后。
- **关键**：触发 ≥1 时严肃看待，"接近触发"（0.85-1.0）要警惕但非执行信号。

#### `phase` (cycle phase 启发式分类)
- **取值**：`accumulation` / `early_post_halving` / `markup` / `late_markup_or_top` / `distribution_risk` / `transition`
- **逻辑**：基于 `days_since_halving` + `sma_200w_dev` 简单组合。
- **可信度**：低，仅作粗略地图。**真正决策看具体指标**。

---

### 💰 Valuation — 估值

#### `ahr999_compressed`（sqrt-AHR — 默认主用估值指标）

> **重要**: 主估值信号统一收敛在 `ahr999_compressed`（压缩版 sqrt-AHR），所有阈值/投票/排序都用它。`ahr999` key 保留为**自适应原始值（展示 + 分歧检测诊断量）**，不应直接驱动决策——回测显示极端牛市后 fair value 上移导致分桶失效。两者方向打架时触发 `ahr999_divergence`。

- **定义**：pow(固定公式 AHR999, 0.75)。使用调和 DCA（九神原始公式本意）+ 固定幂律公允值 + pow(raw,0.75) 凸性压缩。
- **目的**：降低 price²/(DCA×fair) 的凸性偏差。回测验证：5.0-20.0 桶 fwd180 从假阳性 +47% 翻转为 -15% 后续回报转弱信号；≥20.0 桶 0% 胜率保持。
- **阈值（经 pow(x,0.75) 映射保证与原始 AHR999 分档等价）**：
  - < 0.549 → 极端低估
  - 0.549-0.846 → 低估
  - 0.846-1.147 → 合理
  - 1.147-1.682 → 偏高
  - 1.682-3.344 → 高估
  - 3.344-9.457 → 泡沫
  - > 9.457 → 极端泡沫（历史样本后续回报显著转弱）
- **全历史回测（2017-2026, 3181 天）**：
  - <0.45(映射): n=420, fwd180 +86.5%, 胜率 88%
  - 0.45-0.8(映射): n=784, fwd180 +67.5%, 胜率 89%
  - 2.0-5.0(映射): n=275, fwd180 -15.4%, 胜率 20%
  - 5.0-20.0(映射): n=108, fwd180 -7.7%, 胜率 30%

#### `ahr999_divergence`（分歧检测器 — 转向预警）
- **定义**：当固定公式 AHR999（原始）与自适应百分位方向不一致时触发。
- **三种预警模式**：
  - 原始低估 (<0.8) + 自适应偏贵 (q>55%) → "下跌中继，降低单边结论置信度"
  - 原始贵 (>1.2) + 自适应偏低 (q<35%) → "熊市初期反弹陷阱"
  - 原始泡沫 (>2.0) + 自适应不极端 (q<50%) → "最危险分歧，历史 -53% fwd180, 0% 胜率"
- **无分歧时不出现此指标**。分歧 = 转向而非跟随的信号。

#### 三维估值分解（V/M/P — 观复估值域主框架）

> 盘面中通过 `d3_score` / `d3_val_ratio` / `d3_momentum` / `d3_panic` 四个指标输出。

- **核心理念**：AHR999 = V(price/fair) × M(price/DCA)。拆开后，V 是唯一必需的维度。再加 P(90d drawdown) 区分"便宜但还在跌"和"便宜且开始反转"。
- **回测发现（2017-2026，3181 天）**：
  - **V--（仅便宜）：n=632, +100.9% fwd180, 95% 胜率，最差 -11.8%** — 全历史最强信号
  - **-M-（仅跌不便宜）：n=131, -33.4% fwd180, 11% 胜率** — 下跌延续风险
  - VMP（三项全满）：n=549, +66.5% fwd180, 85% 胜率
  - -MP（偏贵+跌+恐慌）：n=443, -21.3% fwd180, 19% 胜率（熊市反弹陷阱）
- **读盘规则**：有 V = 积累倾向增强；无 V = 估值证据不足。M（价格在均线下）单独出现是风险信号。
- **阈值确认（网格搜索验证）**：V<0.8, M<1.0, P<-20% 为最优切分点。放宽 V 阈值会稀释胜率。

#### `mvrv_z_score` / `nupl` (Phase 2，**需付费源**)
- **状态**：CoinMetrics community API 已收紧，免费 tier 无 realized cap。需 Glassnode/CryptoQuant 付费 API key 才能填充。
- **若有数据**，关键阈值：
  - MVRV Z < 0 → 历史深度低估（2018、2020、2022）
  - MVRV Z > 7 → 历史顶部
  - NUPL < 0 capitulation；NUPL 0-0.25 hope；0.25-0.5 optimism；0.5-0.75 belief；>0.75 euphoria

---

### ⛏️ Network — 网络

#### `hash_rate_ehs` (EH/s)
- **定义**：BTC 全网哈希率，长期一直上行。绝对值用于和历史比较。
- **历史阈值**：
  - 2024-04 减半时 ~600 EH/s
  - 2024 末 ~700 EH/s
  - 2025 ~900 EH/s
- **注意**：哈希率绝对值会随硬件周期长期上移，实时值以 guanfu 面板为准，不在 SKILL 中固化。
- **意义**：长期上行 = 矿工对网络/价格的信心投票。

#### `hash_ribbons` (30d MA vs 60d MA)
- **取值**：`上行（矿工扩张）` / `交叉中` / `下行（矿工投降信号）`
- **可信度**：较高。下行（30 < 60）历史上多次出现在矿工压力阶段，常领先修复，但不是独立底部信号。
- **失效**：低概率但存在，机制需结合宏观和资金流确认。
- **失效情形**：监管事件（如 2021 中国矿工迁移）可能误触发。

#### `difficulty_change_pct`
- **定义**：上次难度调整百分比（每 2016 块调一次，约 14 天）。
- **阈值**：
  - < -7% 大幅下调 → 矿工投降，常发生在恐慌底部
  - -2% ~ +3% 正常
  - > +8% 大幅上调 → 算力 FOMO 入场，常见于牛市中段
- **联动**：与 hash_ribbons 同向。

#### `mempool_mb`
- **定义**：mempool 待打包字节数（MB）。
- **阈值**：
  - < 5 → 畅通（牛市后期、熊市常见）
  - 5-30 → 正常
  - 30-100 → 拥堵
  - > 100 → 极度拥堵（2017 末、2021 4月、铭文/Runes 热潮 2023-2024）
- **意义**：链上活跃度。极度拥堵常是顶部信号或风潮事件。

---

### 📊 Positioning — 杠杆 & 情绪

#### `funding_rate_pct` (永续合约资金费率，% / 8h)
- **阈值**：
  - < -0.01% 负值 → 多头不愿付费给空头 → **潜在反转信号**（极端恐慌 / 已大跌之后）
  - -0.01% ~ 0.005% 正常
  - > 0.05% 多头拥挤 → 清算风险
  - > 0.1% 极度过热（2021 4月、2024 3月顶都见过）
- **联动**：funding < 0 + F&G < 25 + price 跌破均线 = 短期修复概率上升。

#### `oi_to_mc` (OI / 市值比)
- **阈值**：
  - < 0.015 杠杆松弛 → 行情可能蓄力
  - 0.015-0.025 中性
  - 0.025-0.035 偏拥挤
  - > 0.04 过度拥挤 → 任何利空触发清算瀑布

#### `fear_greed`
- **阈值**：< 20 极度恐慌（历史深度恐慌区）；> 80 极度贪婪（顶部风险信号）
- **可信度**：中。极端值（<20 或 >80）较有意义，中间值噪音。
- **联动**：与 funding rate / 价格 三角验证。

#### `altcoin_season`
- **定义**：Top 50 代币中过去 90 天跑赢 BTC 的百分比（0-100）。自算 — 基于 Binance Top 50 kline，与 blockchaincenter.net 定义一致，无需外部 API。
- **阈值**：
  - ≥ 75 山寨季 — 资金溢出 BTC → Alt
  - 50-75 偏山寨季
  - 25-50 偏 BTC 季
  - < 25 BTC 季 — 资金集中 BTC
- **可信度**：中高。区块链中心实时计算，每日更新。
- **联动**：与 ETH/BTC 比率 + BTC Dominance 交叉验证。山寨季 > 75 + ETH/BTC 上行 + BTC Dom 下降 = Alt 风险偏好回归。

#### `put_call_ratio` / `put_call_252d_percentile` (QQQ / SPY)
- **定义**：CBOE total put/call ratio。存储 key 仍叫 `stooq_putcall`,但默认源是 CBOE 官方无 key 数据。
- **阈值**：
  - ratio < 0.7 或 percentile < 10% → call chase / 自满,追涨拥挤风险上升
  - ratio 0.7-1.2 → 正常区
  - ratio > 1.2 或 percentile > 90% → hedging / 恐慌,若价格企稳可作反向修复观察
- **联动**：不要单独用 put/call 下结论；至少和 VIXY、HY spread、RSI/MA 共同判断。
- **缺失处理**：若 `source_health.forecast_bundle_stooq_putcall` stale/missing,QQQ/SPY 期权情绪与 forecast confidence 降级。

---

### 🌍 Macro — 宏观（FRED）

需要 `FRED_API_KEY` 环境变量。无 key 时该 domain 全部为 placeholder。

#### `dxy_60d_trend_pct` (DTWEXBGS 60 日变化 %)
- **定义**：Trade-Weighted USD Index (Broad)，FRED 上 DXY 的最佳代理（真 ^DXY 不在 FRED）。
- **阈值**：
  - < -3% 美元大幅走弱（BTC 顺风）
  - -3% ~ -1% 走弱
  - -1% ~ +1% 横盘
  - +1% ~ +3% 走强
  - > +3% 大幅走强（BTC 逆风）
- **联动**：与 BTC 价格历史负相关明显（2022 美元强势 = BTC 大熊）。

#### `real_yield_10y_pct` (DFII10)
- **定义**：10 年期 TIPS 实际利率（已是百分比）。
- **阈值**：
  - < 0% 负实际利率（极度宽松，2020-2022 风险资产狂飙时期）
  - 0% ~ 1% 低位（BTC 顺风）
  - 1% ~ 2% 正常
  - 2% ~ 2.5% 高位（BTC 逆风，2023-2024 多数时间）
  - > 2.5% 历史性逆风
- **机制**：实际利率 = 持有现金的真实回报。它越高，无风险资产相对吸引力越强，BTC/股市估值压缩。

#### `m2_yoy` (M2SL 同比 %)
- **定义**：M2 货币供应量同比增速（季调，月度数据）。
- **阈值**：
  - < -1% 收缩（罕见，2008、2022 末出现过）
  - -1% ~ 2% 停滞（流动性紧）
  - 2% ~ 5% 温和扩张
  - 5% ~ 8% 扩张（BTC 顺风）
  - > 8% 强劲扩张（2020-21 印钞峰值，BTC 翻数倍背景）
- **数据时效**：M2SL 月度发布，通常滞后 30-45 天。as_of 日期看 note 字段。

#### `usd_cny` / `usd_cny_60d_trend_pct`
- **定义**：Yahoo `CNY=X` 的 USD/CNY spot 和 60 日变化。数值上行 = 人民币走弱 / 美元走强。
- **用途**：服务 CNY 本币投资者。BTC / QQQ / SPY / Gold 都以 USD 计价；USD 价格横盘时，人民币贬值也会抬高 CNY 计价收益，反之亦然。
- **解读**：它是本币风险和购买力指标，不是 A 股或港股逻辑，也不应单独作为买卖信号。

#### `global_rate_*`
- **定义**：FRED 里的美欧日中前端政策/同业利率背景：
  - `global_rate_us_fed_pct`: Fed effective rate (`DFF`)，缺失时用 3M T-bill (`DGS3MO`) proxy。
  - `global_rate_eu_ecb_pct`: ECB deposit facility rate (`ECBDFR`)。
  - `global_rate_jp_boj_pct`: Japan overnight call / interbank rate (`IRSTCI01JPM156N`)。
  - `global_rate_cn_pboc_pct`: China overnight call / interbank rate (`IRSTCI01CNM156N`)。
  - `global_rate_spread_us_cn_pct`: US - China 利差。
  - `global_dm_policy_rate_avg_pct`: Fed / ECB / BOJ 均值。
- **用途**：判断全球央行约束和 USD 利差背景。只作为 macro context；进入 forecast 前必须单独 walk-forward 验证。

#### `spx_correlation_30d` (BTC vs SPX 30 日 Pearson 相关)
- **定义**：BTC 与 S&P 500 过去 30 个 SPX 交易日对数收益率的 Pearson 相关系数 [-1, 1]。
- **阈值**：
  - < -0.3 负相关（BTC 走独立避险行情，罕见）
  - -0.3 ~ +0.2 弱相关（BTC 独立性较强，是好 diversifier）
  - +0.2 ~ +0.5 中等相关
  - +0.5 ~ +0.7 强相关（BTC 表现像高 beta 科技股）
  - > +0.7 极强相关（与股市同步度极高，组合风险集中）

**启发**：2020 后 BTC 越来越像高 beta 风险资产，spx_correlation_30d 中位水平在 0.3-0.5。当 corr 突然飙到 >0.7，说明 BTC 已经被纳入"风险资产篮"由宏观流动性主导；当 corr 跌到 <0，往往是 BTC 出现独立叙事（如 ETF 通过、监管利好/利空）的时期。读盘必须把这 4 个指标和价格行为结合看。

#### `oil_proxy_usd` / `oil_proxy_60d_trend_pct`
- **定义**：Futu `US.USO` 原油 ETF proxy。它跟踪油价相关资产，但不是 WTI 桶价；不能把 `BTC / oil_proxy_usd` 解读为“多少桶油”。
- **用途**：观察油价相关资产的 60 日方向，用于通胀压力 / 需求崩塌的宏观辅助判断。

#### `wti_crude_usd` / `wti_crude_60d_trend_pct`
- **定义**：Yahoo `CL=F` WTI 近月原油期货（$/桶），通常作为 USO 不可用时的 fallback。
- **用途**：当 `source_health` 中 `cross_asset` 的 `fallback_used=true` 且指标 source 为 `yahoo:CL=F` 时，可按 WTI 桶价解释；否则优先按 oil proxy 解释。

---

### 💸 Flow — 资金流

#### `etf_net_flow_7d_usd` / `etf_net_flow_30d_usd`
- **定义**：现货 BTC ETF（IBIT, FBTC 等 11 只）净流入。来源 SoSoValue。
- **意义**：2024+ **最重要的边际需求驱动**。日均 $200M+ 持续流入支撑价格。
- **阈值（30d cumulative）**：
  - > $5B 强劲流入（机构 FOMO）
  - $1-5B 持续流入（正常牛市节奏）
  - $0-1B 微弱
  - < 0 流出（机构减持，BTC 逆风）
  - < -$3B 持续流出（前期顶部典型）
- **数据时效**：T-1 或 T-2（看 `source_health` / `stale_warnings` 与指标 `note`）。

#### `etf_total_assets_usd`
- ETF 行业总持仓。$100B+ 表示机构覆盖深度。

#### `stablecoin_market_cap_usd` / `stablecoin_supply_30d_pct`
- **定义**：`stablecoin_market_cap_usd` = USDT+USDC+DAI+FDUSD+FRAX 总市值（CoinGecko 实时）。`stablecoin_supply_30d_pct` = 基于 history.db 采集的 30 日增速。
- **意义**：稳定币总市值 30 日增速 → 加密链上流动性。扩张 = 新钱进场。
- **注意**：`stablecoin_supply_30d_pct` 需要 history.db 攒够 ≥ 30 天稳定币市值数据才会出现。冷启动期间先看 `stablecoin_market_cap_usd` 绝对值。
- **阈值**（30d 增速）：
  - > +5% 强劲扩张
  - +1% ~ +5% 温和扩张
  - -3% ~ +1% 停滞
  - < -3% 收缩（流动性退潮）

#### `eth_btc_ratio`
- **定义**：ETH 价 / BTC 价。
- **解读**：
  - < 0.030 → ETH 极弱，资金避险偏 BTC
  - 0.030-0.045 → ETH 弱（典型熊市后期）
  - 0.045-0.060 → 中性
  - > 0.075 → ETH 强（风险偏好高 / Alt season 临近）

---

### 📈 Technical — 技术指标 (v3 新增)

#### `rsi_14`
- **定义**：14 日相对强弱指数 (0-100)。
- **阈值**：< 20 极度超卖；20-30 超卖；30-45 偏弱；45-55 中性；55-70 偏强；70-80 超买；> 80 极度超买。
- **联动**：RSI < 30 + MACD 空头收窄 + funding < 0 = 短期修复概率上升。

#### `macd_histogram`
- **定义**：MACD(12,26,9) 柱状图。>0 多头动能，<0 空头动能。
- **关键信号**：柱为负但收窄 = **底部反转信号**；柱为正但收窄 = 可能见顶。

#### `ema_cross` / `ma_alignment`
- `ema_cross` (EMA12 vs EMA26) → 短期趋势。>0 多头。
- `ma_alignment` (MA50 vs MA200) → 中期趋势。>0 金叉。
- **矛盾时**：EMA 多头 + MA 死叉 = 短多中空，横盘蓄势信号。

#### `bb_position` / `volatility_20d`
- BB(20,2) 位置 + 20 日波动率。两者同时极端低 = 变盘前兆（1-2 周内）。

---

### 🔗 CrossAsset — 跨资产对比 (v3 新增)

数据源：Yahoo Finance (GC=F / QQQ / SPY)。

#### `btc_gold_ratio` / `btc_qqq_ratio` / `btc_spy_ratio`
- **定义**：1 BTC = X oz 黄金 / X 股 QQQ / X 股 SPY。
- **btc_gold_ratio** 历史区间：~5（BTC 极弱）~ ~40（BTC 极强，2021 高点）。

#### `btc_gold_corr_30d` / `btc_qqq_corr_30d` / `btc_spy_corr_30d`
- **定义**：30 日对数收益率 Pearson 相关 [-1, 1]。
- **阈值**：< -0.3 负相关；-0.3~0.2 弱相关；0.2-0.5 中等；> 0.5 强相关；> 0.7 极强。
- **关键**：BTC 与 QQQ/SPY 相关骤升到 > 0.7 → BTC 被纳入"风险资产篮"，由宏观流动性主导。

#### `uup_price` / `btc_uup_corr_30d` (v3.1, Futu)
- **uup_price**：做多美元 ETF (Invesco DB USD Index Bullish Fund)。作为 DXY 的实时代理（FRED DTWEXBGS 延迟 1-3 天且需 API key）。来源 Futu US.UUP。
- **解读**：UUP 上涨 = DXY 走强 = 美元购买力上升 → BTC 以 USD 计价承压。历史相关性稳定为负。
- **btc_uup_corr_30d**：BTC vs UUP 30 日对数收益率 Pearson 相关。预期值应为 **负值**（-0.3 ~ -0.7）。
  - 若转正 → 反常信号，可能 BTC 与美元同涨（全球避险 + 美元避险同时发生），需警惕宏观极端事件。
- **联动**：UUP 走强 + 实际利率 > 2% + M2 收缩 → 三重美元逆风，BTC 压力最大。

#### `vixy_price` (v3.1, Futu)
- **定义**：VIX 短期期货 ETF，追踪 S&P 500 隐含波动率。传统市场"恐慌指数"。来源 Futu US.VIXY。
- **阈值**：< 15 极度平静（市场自满 → 警惕黑天鹅）；15-20 低波；20-25 正常；25-30 偏高（市场紧张）；> 30 极高恐慌 → 风险资产全面承压。
- **联动**：VIXY > 30 + F&G < 20 + funding < 0 = 全市场恐慌修复观察窗口（2020-03、2022-06 等阶段）。
- **失效**：VIXY 跟踪的是美股波动，BTC 有时走独立行情（如 2024 ETF 通过时 VIXY 平稳但 BTC 暴涨）。需结合 `spx_correlation_30d` 判断独立性。

#### `gld_etf_price` (v3.1, Futu)
- **定义**：SPDR Gold Trust (GLD)，全球最大实物黄金 ETF。每份 ≈ 1/10 oz 黄金。来源 Futu US.GLD。
- **与 PAXG 的关系**：GLD × 10 ≈ PAXG（允许 ±2% 偏差，因 ETF 管理费和流动性差异）。两者同时可用时优先信 PAXG（币安 klines 与 BTC 时间戳对齐更好）；GLD 提供更长的历史（2011+，PAXG 仅 2019+）。
- **失效**：若 PAXG 与 GLD 偏离 > 5%，检查是否其中一方数据源异常（如 PAXG 流动性不足脱锚，或 GLD 美股休市）。

#### `rel_strength_90d_gold`
- **定义**：BTC 90 日收益率 - 黄金 90 日收益率（百分点差）。>0 BTC 跑赢。QQQ/SPY 相对强弱已去重，避免和股指相关性重复计票。
- ⚠ 跨资产历史长度可能不对齐，极端值需结合比率交叉验证。

---

## 读盘框架

完整的 9 步工作流 + 域级方向规则 + 净方向→读盘口径映射 + 行为护栏菜单 + J1-J13 输出模板都在 **tier2.md**（`guanfu://skill/tier2`）。读盘前必读。

### 组合感知(J9) — 已移除（2026-07）

组合上下文职责已归 nimbus 侧（futu+IBKR 实盘持仓 + thesis-tracker）。guanfu verdict 不再输出 `portfolio_context`；需要组合视角时由 nimbus 的 portfolio-manager 结合本盘面自行叠加。

### Step 1: 周期位置（地图坐标）
> "现在 BTC 大致在 cycle 哪一段？"

读 `days_since_halving` + `sma_200w_dev` + `phase`：
- accumulation / early_post_halving + dev<0% → 极端低估，DCA 黄金窗口
- markup + dev 0-100% → 中段，趋势跟随
- late_markup_or_top + dev>100% → 警惕分配
- distribution_risk + `sma_200w_dev` > 1.5（+150%）→ 接近顶部

### Step 2: 估值一致性（4 项交叉验证）
> "估值信号是否清晰？"

读 4 个估值指标：`ahr999_compressed`, `mayer_multiple`, `sma_200w_dev`, `pi_cycle_top_ratio`：
- **4 项都说低估** → 强烈低估，置信度高
- **4 项分歧** → 估值信号不清晰，等待
- **Pi Cycle 触发** → 顶部信号，独立看，权重最高

### Step 3: 网络健康（矿工是否在投降/扩张）
读 `hash_ribbons` + `difficulty_change_pct` + `mempool_mb`：
- 哈希率下行 + 难度大幅下调 → 矿工投降 = **底部前奏**
- 哈希率上行 + mempool 拥堵 → 链上活跃 = 牛市中-末

### Step 4: 杠杆健康（避免高拥挤区追逐反弹）
读 `funding_rate_pct` + `oi_to_mc`：
- funding < 0 + OI 低 → 杠杆已洗，潜在反弹
- funding 高 + OI 高 → 杠杆拥挤，清算风险

### Step 5: 宏观（货币环境）
读 macro domain（4 个 FRED 指标）：
- `m2_yoy` 上行 + `dxy_60d_trend_pct` < 0 + `real_yield_10y_pct` 下行 → 流动性宽松，BTC 顺风
- 反之 → BTC 逆风
- `spx_correlation_30d` > 0.5 → BTC 主要受宏观流动性驱动（看股市判断方向）
- `spx_correlation_30d` < 0.2 → BTC 走独立行情（看链上 / ETF 流入判断）

### Step 6: 流入（边际新钱）
读 flow domain：
- ETF 持续正流入 + 稳定币扩张 + ETH/BTC 上行 → 风险偏好回归
- ETF 流出 + 稳定币收缩 → 流动性退潮

### Step 7: 技术指标（短期方向确认）
读 `rsi_14` + `macd_histogram` + `ma_alignment` + `bb_position` + `volatility_20d`：
- RSI < 30 + MACD 空头收窄 → 短期修复概率上升
- RSI > 70 + MACD 多头减弱 → 短期过热概率上升
- MA 死叉 + EMA 多头 = 短多中空 → 横盘/方向待选
- BB 收窄 + 低波动 → 1-2 周内变盘

### Step 8: 跨资产（BTC 相对位置）
读 cross_asset domain：
- BTC/Gold 比率偏离历史区间 → 跨资产估值极端
- BTC 与 QQQ/SPY 30d 相关性骤升 → 宏观流动性主导
- BTC 独立行情（< 0.2 相关）+ ETF 持续流入 → BTC 自身叙事驱动

### Step 9: 图表输出（增强读盘可读性）

所有读盘结论必须附带 3 张图表。使用 `guanfu --json` 输出 + Python matplotlib 生成，保存为 PNG 并用 Read 工具展示。

**图表 1：价格 + 移动均线 + 估值带**

从生产 BTC 日线缓存或 Binance API 获取约 400 天 BTC 日线收盘价（Oldest first），叠加 SMA50/SMA200，背景根据 Mayer Multiple 着色（<1.0 浅绿色低估、1.0-2.4 灰色中性、>2.4 浅红色高估）。x 轴用月格式 `YYYY-MM`，每 30 天一个刻度，旋转 45°。

```python
import matplotlib; matplotlib.use('Agg')
import matplotlib.pyplot as plt, matplotlib.ticker as mticker, json, urllib.request, numpy as np
from datetime import datetime, timezone
plt.rcParams['font.sans-serif'] = ['Arial Unicode MS', 'Heiti TC', 'WenQuanYi Micro Hei']

# Fetch 400 days BTC daily close from Binance
url = 'https://api.binance.com/api/v3/klines?symbol=BTCUSDT&interval=1d&limit=400'
req = urllib.request.Request(url, headers={'User-Agent': 'Mozilla/5.0'})
with urllib.request.urlopen(req, timeout=30) as resp:
    klines = json.loads(resp.read().decode())
prices = [float(k[4]) for k in klines]  # oldest first
dates = [datetime.fromtimestamp(k[0]/1000, tz=timezone.utc) for k in klines]

# Compute SMA50, SMA200
def sma(data, n): return [None]*n + [sum(data[i-n:i])/n for i in range(n, len(data))]
sma50, sma200 = sma(prices, 50), sma(prices, 200)

# Get Mayer Multiple from guanfu
import subprocess; gf = json.loads(subprocess.check_output(["guanfu", "--json"]))
mayer = gf.get('cycle', {}).get('mayer_multiple', {}).get('value', 1.0)
zone = '#2ecc71' if mayer < 1.0 else ('#e74c3c' if mayer > 2.4 else '#95a5a6')

fig, ax = plt.subplots(figsize=(10, 5.5))
ax.plot(prices, 'k-', lw=1.2, label='BTC Price', zorder=3)
ax.plot(sma50, 'b--', lw=0.8, label='SMA50', alpha=0.7)
ax.plot(sma200, 'r--', lw=0.8, label='SMA200', alpha=0.7)
ax.axhspan(min(prices)*0.8, max(prices)*1.2, alpha=0.05, color=zone)
ax.yaxis.set_major_formatter(mticker.FuncFormatter(lambda x, _: f'${x:,.0f}'))
step = max(1, len(dates)//15)
ax.set_xticks(range(0, len(dates), step))
ax.set_xticklabels([d.strftime('%Y-%m') for d in dates[::step]], rotation=45, ha='right', fontsize=7)
ax.set_title(f'BTC Price + MA + Valuation Band (Mayer={mayer:.2f})'); ax.legend()
plt.tight_layout(); plt.savefig('/tmp/guanfu_chart_1_price.png', dpi=120); plt.close()
```

**图表 2：8 域方向雷达图**

从 `guanfu --json` 的 verdict 中提取 `domains[].vote`（每个域的方向值 -1 到 +1）。用 matplotlib polar 绘制 8 边形雷达图。轴标签用中文缩写（周期/估值/网络/杠杆/宏观/资金流/技术/跨资产）。正值为凹面（蓝色填充），负值为凹面（红色填充）。

如果 `guanfu` 未返回 verdict（未加 `--verdict` 参数），则从各域的指标标签趋势计数估算方向：
- 包含"低估/负值/多头/扩张/松弛"等词的标签计为 bullish +1
- 包含"高估/空头/死叉/拥挤"等词的标签计为 bearish -1
- 除以该域指标总数后归一化到 [-1, +1]

```python
fig, ax = plt.subplots(figsize=(5.5, 5.5), subplot_kw=dict(polar=True))
domain_names = ['周期', '估值', '网络', '杠杆', '宏观', '资金流', '技术', '跨资产']
domain_votes = [0.2, -0.8, -0.3, 0.6, 0.4, 0.7, 0.1, 0.0]  # 示例值，实际从 verdict 提取
N = len(domain_names)
angles = np.linspace(0, 2*np.pi, N, endpoint=False).tolist() + [0]
vals = domain_votes + domain_votes[:1]
ax.fill(angles, vals, alpha=0.25, color='steelblue')
ax.plot(angles, vals, 'o-', color='steelblue', lw=1.5)
ax.set_xticks(angles[:-1]); ax.set_xticklabels(domain_names, fontsize=8)
ax.set_ylim(-1.2, 1.2); ax.set_yticks([-1, 0, 1]); ax.set_yticklabels(['熊', '中', '牛'], fontsize=7)
plt.savefig('/tmp/guanfu_chart_2_radar.png', dpi=120); plt.close()
```

**图表 3：V/M/P 三维打分仪表盘**

三个水平并排的柱状图（`subplots(1, 3)`），每个显示一个维度的原始值、阈值线和触发状态。使用中文文本（**不要用 emoji**，matplotlib 渲染可能缺失）：

| 维度 | 值来源 | 阈值 | 颜色 |
|---|---|---|---|
| V 估值分 | `guanfu --json` → `valuation.d3_val_ratio.value` 或 `ahr999_compressed.value` | < 0.5 触发 | #2ecc71 |
| M 动量分 | `cycle.mayer_multiple.value` | < 1.0 触发 | #3498db |
| P 恐慌分 | 90 日最大回撤（从最高价算起） | > 30% 触发 | #e74c3c |

每个仪表盘：水平柱表示当前值/阈值 × 100，红虚线标记 100%（阈值线），右侧标注原始值和"已触发"/"未触发"状态（纯文本）。

```python
fig, axes = plt.subplots(1, 3, figsize=(9, 3.5))
metrics = [
    ('V 估值分', 0.51, 0.5, '#2ecc71'),   # 示例值
    ('M 动量分', 0.94, 1.0, '#3498db'),
    ('P 恐慌分', 0.05, 0.3, '#e74c3c'),
]
for ax, (name, val, thresh, color) in zip(axes, metrics):
    pct = min(val / thresh * 100, 100) if thresh > 0 else 0
    ax.barh(0, pct, color=color if val < thresh else '#ccc', alpha=0.7, height=0.4)
    ax.axvline(100, color='red', ls='--', lw=1)
    ax.set_xlim(0, 130); ax.set_yticks([]); ax.set_title(name, fontsize=9)
    status = '已触发' if (val < thresh and name != 'P 恐慌分') or (val > thresh and name == 'P 恐慌分') else '未触发'
    ax.text(105, 0, f'{val:.2f}\n{status}', fontsize=7, va='center')
    for spine in ax.spines.values(): spine.set_visible(False)
plt.tight_layout(); plt.savefig('/tmp/guanfu_chart_3_vmp.png', dpi=120); plt.close()
```

**生成步骤**：
1. 调用 `guanfu --json` 获取结构化数据和指标
2. 编写 Python 脚本生成 3 张图（matplotlib，`Agg` backend）
3. 保存到 `/tmp/guanfu_chart_{1,2,3}_*.png`
4. 用 Read 工具读取并展示图片
5. 图片展示后可删除临时文件

**注意事项**：
- `import matplotlib; matplotlib.use('Agg')` 确保无 GUI 环境运行
- 如 matplotlib 未安装，`pip install matplotlib` 或降级为 Unicode 水平柱状图
- 中文需设定字体：`plt.rcParams['font.sans-serif'] = ['Arial Unicode MS', 'Heiti TC', 'WenQuanYi Micro Hei']`
- **禁止使用 emoji 作为 matplotlib 标注文字**（emoji 在 matplotlib 中渲染可能失败），改用纯中文
- x 轴标签用月格式 `YYYY-MM`，每 30 天一个刻度，避免 400 个标签重叠
- 每张图 800x500 像素以内
- 雷达图的域评分优先从 `guanfu --json` 的 verdict `domains[].vote` 提取，比 label keyword 匹配更准
- 估值带用 `axhspan` 或 `fill_between` 轻量填充，alpha 0.05 不遮挡价格线

**输出顺序**：先展示 3 张图表，再输出读盘结论文本。图表提供视觉速览，文本提供详细推理。

---

## 知识库 (kb/)

读盘时不仅看指标值，还要看**为什么是这个值**和**接下来通常怎么演变**。`kb/` 目录是机制库，不是行情快照库：实时数值、日期、source health、missing 状态一律以本次 `guanfu --json` 为准；KB 只提供解释框架、失效条件和风险边界。

**加载协议**：
1. 每次读盘先检查 `source_health` / `stale_warnings`，再读指标。
2. 若需要字段口径，先读 `kb/00-data-contract.md`。
3. 常规读盘最多加载 3 个 KB 文件：`06` + `07` 是默认上下文，第三个按异常域选择。
4. 若触发危机条件，跳过常规类比，直接读 `09-crisis-playbook.md`。
5. 不得把 KB 中的历史示例或样例数值当作实时事实。

| 文件 | 层级 | 内容 | 何时触发 |
|---|---|---|---|
| `00-data-contract.md` | L0 契约 | 指标单位、missing/fallback、口径优先级 | 字段解释不确定时 |
| `06-regime-taxonomy.md` | L2 测算 | 6 种宏观测算 + 转换信号 | 每次读盘必查 |
| `07-historical-analogues.md` | L2 类比 | 历史相似组合 + 类比库 | 每次读盘必查 |
| `01-macro-transmission.md` | L1 因果 | 利率/通胀/美元/财政传导链 | 宏观域出现显著变化时 |
| `02-liquidity-plumbing.md` | L1 因果 | 稳定币/ETF/杠杆的"钱怎么进出" | Flow/Positioning 域异常时 |
| `03-crypto-mechanics.md` | L1 因果 | 减半/矿工/LTH/MVRV 的结构性因子 | Cycle/Valuation 域分析时 |
| `04-cross-asset.md` | L1 因果 | BTC vs Gold/SPX/Bonds 联动规则 | CrossAsset 域分析时 |
| `05-geopolitical.md` | L1 因果 | 5 类地缘冲击 × BTC 反应时间线 | 出现地缘事件时 |
| `08-decision-matrix.md` | L3 读盘 | 不同测算下的权重 + 误读防护 | 输出读盘结论前 |
| `09-crisis-playbook.md` | L3 应急 | 30s 危机判别 → 风险降级优先级 → 恢复确认清单 | VIX>35 / HY+100bp / BTC-15% / 脱锚 任一触发时 |

**输出边界**：KB 可以说“积累倾向增强 / 防守倾向增强 / 风险暴露需审视”，不要输出无上下文执行动作或具体风险参数。执行层必须由用户的期限、资金性质、风险预算和税务约束决定。

---

## 历史 Pattern 提示（轻量）

历史组合只作为模式识别入口，完整类比必须按 `kb/07-historical-analogues.md` 输出相似点、差异点、反向案例和失效条件。

### 深度低估 + 矿工压力
常见组合：`sma_200w_dev < 0`、`mayer_multiple < 0.7`、`ahr999_compressed < 0.549`、`hash_ribbons` 下行或刚修复、`funding_rate_pct` 偏低、`fear_greed < 20`。

读盘含义：长期估值压力较低，但短期仍需确认宏观测算、资金流和杠杆是否同步修复。

### 分布风险 + 拥挤
常见组合：`pi_cycle_top_ratio >= 1.0`、`mayer_multiple > 2.4`、`sma_200w_dev > 1.5`、`funding_rate_pct > 0.1%`、`oi_to_mc > 0.04`、`fear_greed > 80`。

读盘含义：估值、情绪和杠杆共振时，风险暴露需要审视，但仍要检查 ETF 时代资金流是否改变周期节奏。

### 假信号提醒
- AHR 低位但 hash ribbons 仍恶化时，低估只能作为背景。
- funding 极高但 ETF / M2 同时支持时，拥挤信号可能提前。
- 链外事件会污染链上估值和交易所数据，必须进入事件框架。

---

## 反模式

- ❌ **看一个指标做决策** — 至少 3 个 domain 一致才有意义
- ❌ **从 4 个估值指标里挑利己的** — 4 个都看低估再说
- ❌ **忽略 stale 警告** — ETF 数据是 T-1，遇到周末/假期更滞后
- ❌ **盘面不写入交易日志就行动** — 用 trade-journal skill 留痕，事后才能复盘
- ❌ **黑天鹅时还看 观复** — 监管 / 交易所暴雷 / 协议漏洞超出指标范围，需立刻切换 news-dashboard
- ❌ **用 观复 决策 altcoin** — 仅覆盖 BTC + ETH/BTC 比率，山寨币用 cmc-mcp / okx-dex
- ❌ **看到 phase=accumulation 就给极端结论** — phase 是粗分类，必须配合具体指标 + 风险预算（执行层另行处理）
- ❌ **输出倾向但不列反证** — 每条读盘结论必须有 ≥ 2 个反证指标
- ❌ **用绝对语气** — "一定会涨"、"绝对是底" → 正确表述："概率约 X%，基于 A/B/C 三个域一致看涨"
- ❌ **不设失效条件** — 必须声明"如果 X 指标变为 Y，本结论失效"
- ❌ **读完盘不输出图表** — 必须生成 3 张图表（价格估值带/8 域雷达图/VMP 仪表盘），文本再详细也不如图一眼

---

## 联动其他 skill

| 协同 skill | 用法 |
|---|---|---|
| `cmc-mcp` | 实时 BTC/ETH/F&G 数据，观复 是历史 + 周期 |
| `market-pulse` | 跨资产宏观 MHS，与 BTC 局部信号交叉验证 |
| `okx-dex` | altcoin/memecoin（观复 不覆盖）|
| `news-dashboard` | 黑天鹅事件 / 监管 / 交易所新闻 |
| `polymarket` | 加密相关预测市场（btc 价 / ETF 通过率 / 监管） |
| `trade-execution` | 观复给风险读盘 → 执行层再处理风险预算和执行细节 |
| `trade-journal` | 决策时 log，事后复盘 |
| `valuation`（贵金属模式）| BTC 与黄金互补避险，跨资产估值 |

---

## 演进历史 / 设计决策

| 项目 | v1 (废弃) | v2 | v3 (当前) |
|---|---|---|---|
| 总分 0-100 | ❌ 设计性稀释，永远停在 50 附近 | 不输出 |
| Action / State | ❌ 硬编码阈值 = 假精度 | 不输出，由 Claude 综合 |
| 4 维 sigmoid 子分 | ❌ 隐式压缩 | → 8 域原始指标 + 历史分位 |
| 数据源 | BTC + mempool + ETF + F&G + FRED + 极简 ETH |
| 估值层 | **压缩版 sqrt-AHR（主用）** + Mayer + 200wSMA dev + Pi Cycle + **三维打分 V/M/P（v3.2 新增）** |
| 网络层 | hash rate + ribbons + difficulty + mempool |
| 资金流 | 仅稳定币 | ETF 7d/30d + 稳定币 + ETH/BTC |
| 宏观层 | 无 | DXY + 10Y real yield + M2 YoY + BTC/SPX 30d corr (FRED) |
| 历史分位 | 无 | SQLite 每日采集 15 个指标 → 730 天分位 |
| 技术域 | 无 | 无 | RSI/MACD/EMA/MA/BB/波动 (7 指标) |
| 跨资产 | 无 | 无 | Gold/QQQ/SPY/UUP(DXY)/VIXY/GLD + 相关性 + 相对强弱 (12 指标) |
| 三维打分 V/M/P | — | — | — | V(price/fair<0.8)+M(mayer<1.0)+P(90d dd<-20%) 三维独立计分，零污染
| 山寨季 | ❌ (1-btc_dom)×100 | blockchaincenter.net → 自算 | 自算 (基于 Top50 kline) |
| 黄金数据 | 无 | 无 | Binance PAXG + Futu GLD 双源交叉验证 |
| 美元指数 | 无 | FRED DTWEXBGS (需 key, 延迟) | Futu UUP 实时 + FRED 备用 |

| 决策依据 | 1 个 score / 1 个 action | 8 域指标盘 + 本手册 + Claude 综合 |
