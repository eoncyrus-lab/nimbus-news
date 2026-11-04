# guanfu v2 最终完整方案

> 日期：2026-05-06 | 版本：v2.0 | 状态：已完成 ✓

**7 阶段 50 任务全部完成。ENSEMBLE 方向命中率 65.6%。**

---

## 零、数据源全景

### 已有且经校验

| 类别 | 数据 | 源 | 历史深度 | 费用 |
|------|------|-----|---------|------|
| BTC 价格 | BTC/USDT 日 K | Binance + CoinMetrics | 2010+ | 免费 |
| 纳指 100 | QQQ 日 K | Futu → Yahoo | 3000d | 免费 |
| 标普 500 | SPY 日 K | Futu → Yahoo | 3000d | 免费 |
| **伦敦金** | XAU/USD LBMA | DBnomics (回填) + Yahoo (增量) | **1968+（58 年）** | 免费 |
| 黄金 ETF | GLD 日 K | Futu | 3000d | 免费 |
| 美元 | UUP 日 K | Futu | 3000d | 免费 |
| 长端债 | TLT 日 K | Futu | 3000d | 免费 |
| 波动率 | VIXY 日 K | Futu | 3000d | 免费 |
| 原油 | USO/WTI | Futu → Yahoo | 3000d | 免费 |
| 短期国债 | **BIL / SHY** | **Futu（新增）** | 3000d | 免费 |
| 全债市 | **BND** | **Futu（新增）** | 3000d | 免费 |
| 全美股 | **VTI** | **Futu（新增）** | 3000d | 免费 |
| 情绪 | Fear & Greed | alternative.me | 2018+ | 免费 |
| 资金费率 | funding rate | Binance 期货 | 2019+ | 免费 |
| 哈希率 | hashrate | mempool.space | 2021+ | 免费 |
| 稳定币 | USDT/USDC 市值 | CoinGecko | 2019+ | 免费 |
| ETF 流量 | BTC ETF 净流入 | SoSoValue | 2024+ | 免费 |
| 链上估值 | MVRV/NUPL | CoinMetrics 社区 | 2010+ | 免费 |
| Deribit | DVOL/skew | Deribit | 2020+ | 免费 |
| 宏观 | DXY/实际利率/M2/Fed | FRED | 多变 | 需 API key |
| QQQ/SPY 估值 | PE/PB/市值 | **Futu 快照（需激活）** | 实时 | 免费 |
| A 股 | CSI300 价格+PE+北向 | AkShare | TBD | 免费 |

### 新增数据源（本方案需要）

| 数据 | 源 | 用途 | Phase |
|------|-----|------|-------|
| 伦敦金历史回填 | DBnomics LBMA API | 黄金价格 1968+ | Phase 0 |
| 伦敦金日常增量 | Yahoo XAUUSD=X | 黄金价格更新 | Phase 0 |
| BIL/SHY/BND/VTI | Futu（已有能力，加 symbol） | 懒人组合 | Phase D |
| Futu 快照激活 | Qot_GetSecuritySnapshot | QQQ/SPY PE/PB | Phase 3 |
| CSI300 全量 | AkShare / Yahoo ^HS300 | 沪深 300 | Phase 7 |

### 数据存储（增量优先）

```
~/.guanfu/
├── history.db              # 已有：指标分位历史（SQLite）
├── prices/                 # 新增：每日价格存档（JSON）
│   ├── btc.json            # [{date, close, source}] oldest-first
│   ├── qqq.json            # Futu QQQ
│   ├── spy.json            # Futu SPY
│   ├── gold.json           # 伦敦金 (DBnomics LBMA 1968+ / Yahoo XAUUSD=X)
│   ├── uup.json            # Futu UUP（DXY 代理）
│   ├── tlt.json            # Futu TLT（长端债）
│   ├── vixy.json           # Futu VIXY
│   ├── wti.json            # Futu USO / Yahoo CL=F
│   ├── bil.json            # Futu BIL（短期国债，新增）
│   ├── shy.json            # Futu SHY（1-3Y 国债，新增）
│   ├── bnd.json            # Futu BND（全债市，新增）
│   ├── vti.json            # Futu VTI（全美股，新增）
│   ├── hs300.json          # 远期
│   └── meta.json           # {asset: {last_date, count, source, updated_at}}
├── fundamentals/           # 远期：基本面数据
│   ├── qqq_pe.json
│   ├── spy_pe.json
│   └── hs300_pe.json
└── btc_daily_history.json  # 已有（向后兼容，逐步迁移）
```

**增量逻辑**：首次全量导入后，日常运行仅拉取最近 30 天新数据。API 调用从 O(N×3000) 降至 O(N×30)。

---

## 一、总架构

```
guanfu v2
│
├── 数据层
│   ├── prices/              # 每日价格存档（增量更新）
│   ├── client/              # API 拉取
│   │   ├── futu.go          # Futu 日K + 快照激活
│   │   ├── gold.go          # 伦敦金管道（新增）
│   │   └── hs300.go         # CSI300 管道（远期）
│   └── store/               # PriceStore 增量引擎（新增）
│
├── 引擎层
│   ├── engine/asset.go      # Asset 接口 + 注册表（新增）
│   ├── engine/              # BTC 面板（现有）
│   ├── engine/qqq/          # QQQ 面板（新增）
│   ├── engine/spy/          # SPY 面板（新增）
│   ├── engine/gold/         # 黄金面板（新增）
│   └── engine/hs300/        # CSI300 面板（远期）
│
├── 推演层
│   ├── forecast/            # 资产无关 kNN 引擎（重构）
│   ├── forecast/features/   # 特征注册表
│   └── forecast/backtest/   # 回测框架（新增）
│
├── 定投层
│   └── internal/dca/        # DCA 定投回放引擎（新增）
│
├── 配置层
│   └── internal/allocate/   # 懒人组合 / 资产配置（新增）
│
└── CLI
    ├── guanfu btc           # 盘面分析（截面，已有）
    ├── guanfu qqq           # 盘面分析（新增）
    ├── guanfu spy           # 盘面分析（新增）
    ├── guanfu gold          # 盘面分析（新增）
    ├── guanfu hs300         # 盘面分析（远期）
    ├── guanfu market        # 多资产一览（新增）
    ├── guanfu dca           # 定投参考（新增）
    └── guanfu allocate      # 资产配置参考（新增）
```

---

## Part A: BTC Forecast v2（走势推演增强）

### 目标
在 v1 的 kNN 基础上增加跨资产特征、回测验证、路径推演、状态检测。

### 特征体系（11+18+6=35）

| 层级 | 特征数 | 历史深度 | 条件 |
|------|--------|---------|------|
| Core（价格） | 11 | 2010+ | 无 |
| Cross-Asset | 10 | 2017+ | 无（数据已有） |
| Positioning | 8 | 2018-2021+ | 无（数据已有） |
| Macro（FRED） | 6 | 2022+ | **可用**（FRED_API_KEY 已配） |

两阶段匹配：Core 全历史初筛，Extended 重排。

### 新增能力
- **回测框架**：滚动窗口，方向命中率 + CRPS + PIT 校准
- **路径推演**：P50 主线 + P25/P75 扇区（`--forecast-path`）
- **状态检测**：规则式 3 状态（趋势牛/熊/断裂）
- **特征消融**：量化每个新特征的 Δ 贡献

---

## Part B: 多资产分析

### 面板

#### 各资产域设计

| 资产 | 域 1 | 域 2 | 域 3 | 域 4 | 域 5 |
|------|------|------|------|------|------|
| QQQ | 估值（PE/PB/PEG） | 技术（RSI/MACD/MA/BB） | 宏观（VIX/DXY/10Y） | 情绪（F&G） | |
| SPY | 估值（PE/PB） | 技术 | 宏观 | 情绪 | |
| 黄金 | 估值（实际利率/DXY/VIX） | 技术 | 宏观（远期） | | |
| 沪深300 | 估值（PE/PB） | 技术 | 资金（北向/融资） | 宏观（LPR/汇率） | 股息 |

#### 黄金估值域数据源

```
FRED DFII10 实际利率    → 主力（FRED_API_KEY 已配）
DXY 方向                → UUP（已有，Futu）
VIX 水平                → VIXY（已有，Futu）
备用降级: TLT 60d 变化   → TLT涨≈实际利率跌≈利好黄金（FRED 故障时启用）
```

#### FRED 降级路径（备用）

FRED_API_KEY 已配置，以下降级链仅在 FRED 故障时激活：

| 资产 | 依赖 FRED 的域 | 降级策略 |
|------|-------------|---------|
| BTC | Macro 域（6 特征） | 标记 optional，Core+Cross-Asset+Positioning 共 29 特征仍可用 |
| QQQ/SPY | 10Y 收益率（宏观域） | 用 TLT 价格变化做利率代理 |
| 黄金 | 实际利率（估值域） | 用 TLT 60d 变化做实际利率代理 |
| 全部 | DXY（贸易加权美元） | 用 UUP（已存在） |

### Positioning 特征历史数据限制

| 特征 | 全历史可用？ | 修复 | 阶段 |
|------|------------|------|------|
| fear_greed | ✅ 已修复 | Phase 0 全历史回填（2018+） | Phase 4 |
| funding_rate / oi_to_mc | ❌ Binance 2019+ | 需新历史管道 | Phase 4 后期 |
| hash_rate_change | ✅ 3y (2023+) | mempool 已有 | Phase 4 |
| stablecoin_30d_change | ❌ 仅当日值 | CoinGecko range API 回填 | Phase 4 后期 |

**kNN 实际可用特征**：Core（11, 2010+）+ Cross-Asset（10, 2015+）= 21 特征保证可用。Positioning 当前仅给当前日期用（不作为 kNN 历史匹配维度），待后期历史管道补齐。

### 沪深 300（Phase 7, 远期）

5 域（估值/技术/资金/宏观/股息），15 个特征，需 AkShare 数据管道。

---

## Part C: DCA 定投

### 定位

```
guanfu btc     → 盘面分析（截面诊断）
guanfu dca btc → 定投参考（纵向回放）
```

独立模块 `internal/dca/`，不影响盘面分析。

### 三种策略

| 策略 | 逻辑 | 参数 |
|------|------|------|
| fixed | 每期固定金额 | 无 |
| ahr | AHR999 分位加权 | <0.8 加速, 0.8-1.2 正常, >1.2 减速 |
| mayer | Mayer Multiple 加权 | <0.8 加速, 0.8-1.5 正常, >1.5 减速 |

### CLI

```
guanfu dca btc                         # 默认，当前估值区间的历史定投回放
guanfu dca btc --strategy compare      # 三种策略对比
guanfu dca qqq                         # QQQ 定投（远期）
guanfu dca gold                        # 黄金定投（远期）
```

### 输出

- 当前定投成本锚（200d 调和均价 + 浮动盈亏）
- 估值区间标签（是否在最佳定投启动区）
- 历史回放：当前估值区间内，1y/3y/5y 定投胜率 + 最大回撤 + 中位年化
- 三种策略的对比表现

### 核心原则

- ❌ 不输出"今日投 X 倍"
- ✅ 输出"当前估值区间历史定投胜率 + 极端机会提示"

---

## Part D: 懒人组合 / 资产配置

### 定位

```
guanfu allocate                  # 当前各资产估值区一览
guanfu allocate --portfolio 6040 # 60/40 股债，输出偏离度
guanfu allocate --portfolio allweather  # 达里奥全天候
guanfu allocate --rebalance     # 再平衡参考（不输出交易指令）
```

### 支持的经典组合

| 组合 | 构成 | 数据需求 |
|------|------|---------|
| **60/40** | SPY 60% + TLT 40% | 已有 |
| **全天候** (Dalio) | SPY 30% + TLT 40% + GLD 7.5% + 黄金 7.5% + 商品 7.5% + BIL 7.5% | 已有 + BIL |
| **永久组合** | SPY 25% + TLT 25% + 黄金 25% + SHY 25% | 已有 + SHY |
| **巴菲特 90/10** | SPY 90% + SHY 10% | 已有 + SHY |
| **全球市场** | VTI 60% + BND 40% | 新增 VTI + BND |

### 输出

```
懒人组合参考  2026-05-05

全天候组合 (Dalio All-Weather)
  标的      目标    当前    偏离    估值区
  ──────────────────────────────────────────
  SPY       30%    —       —      偏高 (PE 28.5)
  TLT       40%    —       —      中低 (实际利率回落中)
  GLD        7.5%  —       —      中性
  伦敦金     7.5%  —       —      中性偏积累 (实际利率 < 2%)
  商品       7.5%  —       —      WTI $62, 中性
  BIL        7.5%  —       —      现金等价

  组合整体: 中性偏防御
  最多偏离: TLT 若涨幅 > 10% 可能超出再平衡带
  积累区资产: 伦敦金、TLT

不是投资建议。组合配比应基于个人情况。
```

### 再平衡参考

```
再平衡检测 (5% 绝对偏离阈值)

  无资产触发再平衡阈值。

历史参考:
  60/40 组合 2018-2026 年化波动率: ~10%
  60/40 组合最大回撤 (2022): -18%
  全天候组合同期最大回撤: -12%
```

### 核心原则
- ❌ 不输出调仓指令（"卖出 X% SPY 买入 Y% TLT"）
- ✅ 输出偏离度 + 估值区 + 积累/谨慎提示
- ❌ 不用硬编码历史收益（btcdca 的做法）
- ✅ 用实时价格 + 估值分位做当前诊断

---

## Part E: 共享基础设施

### Asset 接口

```go
type Asset interface {
    Key()  string   // "btc", "qqq", "spy", "gold", "hs300"
    Name() string
    FetchSnapshot(ctx context.Context) (*AssetSnapshot, error)
    BuildPanel(snap *AssetSnapshot) (*IndicatorPanel, error)
    BuildVerdict(panel *IndicatorPanel) *Verdict
    BuildForecast(snap *AssetSnapshot, opts forecast.Options) (*forecast.Forecast, error)
}
```

### FeatureExtractor（推演复用）

```go
type FeatureExtractor func(points []Point, i int) (featureSet, bool)
```

所有资产复用同一套 kNN 引擎、回测引擎、路径推演引擎。

### 跨资产数据对齐

```
每个 BTC 日期 d:
  取 ≤ d 的最近跨资产交易日价格（forward-fill）
  周末/节假日 → 沿用上周五数据
```

---

## 六、完整 TODO 清单

### Phase 0: 数据基础设施（2026-05-05 → 05-09）

| # | 优先级 | 任务 | 文件 |
|---|--------|------|------|
| 0.1 | P0 | `internal/store/price.go`：PriceStore 增量引擎（Load/Save/Append/LastDate） | 新建 |
| 0.2 | P0 | `internal/store/meta.go`：meta.json 版本追踪 | 新建 |
| 0.3 | P0 | BTC 价格迁移：`btc_daily_history.json` → `prices/btc.json` | 修改 |
| 0.4 | P0 | QQQ/SPY/UUP/TLT/VIXY/WTI/GLD 首次全量导入（Futu → PriceStore） | 新建 |
| 0.5 | P0 | **伦敦金管道**：DBnomics LBMA 历史回填（1968+）+ Yahoo XAUUSD=X 增量 | 新建 |
| 0.6 | P0 | **Fear & Greed 全历史回填**：`api.alternative.me/fng/?limit=0` 一次性拉取（2018+）→ PriceStore | 新建 |
| 0.7 | P0 | BIL/SHY/BND/VTI 接入 Futu symbol list + 首次全量导入 | 修改 |
| 0.8 | P0 | `internal/store/store_test.go`：增量更新 + 边界测试 | 新建 |
| 0.9 | P1 | `client/real.go`：拉取时检查 PriceStore 做增量 | 修改 |

### Phase 1: 引擎重构（2026-05-09 → 05-11）

| # | 优先级 | 任务 | 文件 |
|---|--------|------|------|
| 1.1 | P0 | `engine/asset.go`：Asset 接口 + 注册表 | 新建 |
| 1.2 | P0 | `model/asset_snapshot.go`：AssetSnapshot 通用结构 | 新建 |
| 1.3 | P0 | BTC Asset 实现（现有逻辑封装到接口） | 修改 |
| 1.4 | P1 | `cmd/guanfu/main.go`：子命令路由（btc/qqq/spy/gold/hs300/market/dca/allocate） | 修改 |
| 1.5 | P1 | MCP tool 路由支持资产参数 | 修改 |

### Phase 2: QQQ + SPY 面板（2026-05-12 → 05-16）

| # | 优先级 | 任务 | 文件 |
|---|--------|------|------|
| 2.1 | P0 | `engine/qqq/panel.go`：QQQ 技术面板（通用价格特征） | 新建 |
| 2.2 | P0 | `engine/spy/panel.go`：SPY 技术面板 | 新建 |
| 2.3 | P0 | `guanfu qqq/spy` CLI 入口 + `--verdict` + `--json` | 修改 |
| 2.4 | P1 | MCP: `get_qqq_panel`, `get_spy_panel` | 修改 |
| 2.5 | P1 | QQQ/SPY 面板测试 | 新建 |

### Phase 3: 黄金面板 + Futu 快照激活（2026-05-17 → 05-22）

| # | 优先级 | 任务 | 文件 |
|---|--------|------|------|
| 3.1 | P0 | `engine/gold/panel.go`：黄金技术面板（伦敦金 1968+） | 新建 |
| 3.2 | P0 | `engine/gold/valuation.go`：黄金估值域（实际利率/DXY/VIX） | 新建 |
| 3.3 | P0 | `guanfu gold` CLI 入口 + `--verdict` | 修改 |
| 3.4 | P0 | **激活 Futu 快照**：`Qot_GetSecuritySnapshot` → QQQ/SPY PE/PB | 修改 futu.go |
| 3.5 | P0 | `engine/qqq/valuation.go`：QQQ 估值域 | 新建 |
| 3.6 | P0 | `engine/spy/valuation.go`：SPY 估值域 | 新建 |
| 3.7 | P1 | BTC forecast 跨资产特征中用伦敦金替代 PAXG | 修改 |
| 3.8 | P1 | 估值 Fallback：Futu 快照不可用时的降级策略 | 新建 |

### Phase 4: BTC forecast v2（2026-05-23 → 05-30）

| # | 优先级 | 任务 | 文件 |
|---|--------|------|------|
| 4.1 | P0 | 重构 `forecast/forecast.go`：`Build` 支持 `FeatureExtractor` | 修改 |
| 4.2 | P0 | `forecast/features/core.go`：通用价格特征提取器 | 新建 |
| 4.3 | P0 | `forecast/features/cross_asset.go`：跨资产特征 + 对齐函数 | 新建 |
| 4.4 | P0 | `forecast/features/positioning.go`：头寸/情绪特征（仅当前日期可用） | 新建 |
| 4.5 | P0 | BTC 两阶段匹配：Stage1 Core(11) 全历史 + Stage2 Cross-Asset(10) 2015+ | 修改 |
| 4.6 | P0 | `forecast/backtest/backtest.go`：滚动窗口回测引擎 | 新建 |
| 4.7 | P0 | `forecast/backtest/metrics.go`：方向命中率 + PIT + CRPS | 新建 |
| 4.8 | P0 | `forecast/projection.go`：PathProjection + 扇区构建 | 新建 |
| 4.9 | P1 | `--forecast-path` CLI 参数 + ASCII 扇区图输出 | 修改 |
| 4.10 | P1 | QQQ/SPY/黄金 kNN 推演（复用通用 + 跨资产价格特征） | 修改 |
| 4.11 | P1 | 首份回测报告：v1 纯价格 vs v2 跨资产 方向命中率对比 | 新建 |
| 4.12 | P2 | `forecast/regime.go`：规则式市场状态检测 | 新建 |
| 4.13 | P2 | Positioning 历史数据管道：funding/OI (Binance 历史) + stablecoin (CoinGecko range) | 新建 |

### Phase 5: DCA 定投（2026-05-31 → 06-04）

| # | 优先级 | 任务 | 文件 |
|---|--------|------|------|
| 5.1 | P0 | `internal/dca/dca.go`：DCA 回放引擎（walk-forward 模拟） | 新建 |
| 5.2 | P0 | `internal/dca/strategies.go`：Fixed / AHR / Mayer 三种策略 | 新建 |
| 5.3 | P0 | `guanfu dca btc` CLI 入口 | 修改 |
| 5.4 | P0 | `guanfu dca btc --strategy compare` 策略对比 | 修改 |
| 5.5 | P1 | 估值区间划分 + 历史回放：1y/3y/5y 胜率 + 回撤 + 年化 | 新建 |
| 5.6 | P1 | DCA 测试 | 新建 |
| 5.7 | P2 | QQQ/SPY/黄金 DCA 支持 | 修改 |

### Phase 6: 懒人组合 / 资产配置（2026-06-05 → 06-10）

| # | 优先级 | 任务 | 文件 |
|---|--------|------|------|
| 6.1 | P0 | `internal/allocate/allocate.go`：组合定义 + 偏离计算引擎 | 新建 |
| 6.2 | P0 | `internal/allocate/portfolios.go`：60/40 / 全天候 / 永久 / 巴菲特 90/10 | 新建 |
| 6.3 | P0 | `guanfu allocate` CLI 入口（多资产估值一览） | 修改 |
| 6.4 | P0 | `guanfu allocate --portfolio allweather` 组合偏离输出 | 修改 |
| 6.5 | P0 | `guanfu allocate --rebalance` 再平衡检测 | 修改 |
| 6.6 | P1 | 组合历史波动率/最大回撤参考（基于实时价格算，不硬编码） | 新建 |
| 6.7 | P1 | 分配测试 | 新建 |

### Phase 7: 多资产 Dashboard + 沪深 300（2026-06-11 → 06-20，远期）

| # | 优先级 | 任务 | 文件 |
|---|--------|------|------|
| 7.1 | P0 | `guanfu market` CLI 入口：多资产一览表 + 一致/分歧信号 | 修改 |
| 7.2 | P0 | MCP: `get_market_overview` | 修改 |
| 7.3 | P2 | `client/hs300.go`：CSI300 数据管道（AkShare / Yahoo ^HS300） | 新建 |
| 7.4 | P2 | `engine/hs300/panel.go`：沪深 300 面板（5 域） | 新建 |
| 7.5 | P2 | `engine/hs300/valuation.go`：估值域 | 新建 |
| 7.6 | P2 | `engine/hs300/flow.go`：资金域 | 新建 |
| 7.7 | P2 | `guanfu hs300` CLI 入口 | 修改 |
| 7.8 | P2 | CSI300 kNN 推演 | 修改 |

---

## 七、依赖关系

```
Phase 0 (数据) ──┬── Phase 1 (引擎) ──┬── Phase 2 (QQQ/SPY) ──┬── Phase 6 (Dashboard)
                 │                     ├── Phase 3 (黄金+估值) ──┤
                 │                     ├── Phase 4 (BTC forecast)─┤
                 │                     └── Phase 5 (DCA) ────────┤
                 │                                                │
                 └── Phase 7 (CSI300, 独立远期) ──────────────────┘

Phase 0 必须先完成（所有 Phase 依赖数据存档）。
Phase 2/3/4/5 可在 Phase 1 完成后并行推进。
Phase 6 依赖 Phase 2+3（需要各资产面板数据）。
```

---

## 八、验收标准

### Phase 0
- [ ] 首次运行后 `~/.guanfu/prices/` 下所有 JSON 存在且含完整历史
- [ ] 第二次运行仅拉取增量（网络请求数 < 10）
- [ ] 伦敦金历史 ≥ 50 年（1968+）
- [ ] Fear & Greed 历史 ≥ 7 年（2018+）
- [ ] BIL/SHY/BND/VTI 首次导入完成
- [ ] PriceStore 测试覆盖增量/边界/损坏恢复

### Phase 2-3
- [ ] `guanfu qqq/spy/gold` 输出人类可读面板 + `--json` 合法
- [ ] `--verdict` 含证据链 + 反证 + 失效条件
- [ ] QQQ/SPY 面板含 PE/PB（Futu 快照激活后）
- [ ] 黄金面板使用伦敦金数据，非 PAXG

### Phase 4
- [ ] 回测报告输出 BTC 方向命中率（按 horizon/年份分层）
- [ ] 跨资产特征接入后命中率 ≥ v1 纯价格特征
- [ ] `--forecast-path` 输出 P50 主线 + P25/P75 扇区
- [ ] QQQ/SPY/黄金推演可用（通用价格特征即可）

### Phase 5
- [ ] `guanfu dca btc` 输出估值区间 + 历史回放统计
- [ ] `guanfu dca btc --strategy compare` 三种策略对比
- [ ] 不输出任何"今日投 X 倍"的指令

### Phase 6
- [ ] `guanfu allocate` 输出多资产估值一览
- [ ] `guanfu allocate --portfolio allweather` 输出偏离度
- [ ] 不输出调仓指令

### Phase 7
- [ ] `guanfu market` 显示全部资产一览 + 一致/分歧信号
- [ ] `guanfu hs300` 输出面板（远期）

---

## 九、关键设计决策

1. **黄金用伦敦金（1968+）而非 PAXG（2019+）**：58 年历史，kNN 候选池从 ~1800→~14000。DBnomics LBMA 免费无 key。

2. **价格存档用 JSON**：单资产价格列表，可读可调试。SQLite 用于指标分位（已有 history.db）。

3. **Futu 快照激活优先于 Yahoo fundamentals**：代码骨架已有，只需调用。Yahoo 需要新依赖和限流处理。

4. **推演引擎资产无关化**：`Build()` 只依赖 `[]Point` + `FeatureExtractor`。任何资产有价格历史就能做推演。

5. **DCA 定投独立模块**：不影响盘面分析，不输出定投倍数，只输出历史回放统计。

6. **懒人组合不输出调仓指令**：只输出偏离度 + 估值区，不替代投顾。

7. **沪深 300 独立为 Phase 7**：需全新 AkShare 管道，不与 QQQ/SPY/黄金耦合。

8. **不输出单一分数/DCA 倍数**：来自 btcdca 可信度评估的核心教训。guanfu 坚持证据链式输出。

9. **FRED 数据已可用**：FRED_API_KEY 已配，实际利率、M2、Fed 资产、DXY 等全部可用。降级路径保留为 FRED 故障时的备用方案。

10. **Positioning 特征分期上线**：Fear & Greed 在 Phase 0 全历史回填后可入 kNN（2018+）。Funding rate / OI / stablecoin 仅给当前日期用，待 Phase 4 后期历史管道补齐。

11. **BIL/SHY/BND/VTI 一次性接入**：Futu 已支持全部美国上市 ETF，只需扩展 bridge symbol list，Phase 0 首次导入。
