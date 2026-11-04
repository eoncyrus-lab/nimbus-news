# 观复 v3.1 roadmap — 投资者视角重构

```text
P0 ▸ 核心(阻塞发布)         P2 ▸ 精度 / 体验
P1 ▸ 功能 / 数据缺失         P3 ▸ 调研 / 扩展
```

---

## 用户画像(首次明确声明)

**Primary(核心)**:有一定投资经验、愿意用 CLI/MCP、5+ 年期限、10k~1M USD 级别可投资资产、偏被动配置的个人投资者。

**Secondary(重要)**:AI 重度用户,通过 Claude Desktop / Cursor / Claude Code 用 MCP 接入 guanfu;CLI 是 fallback。

**Tertiary(触达)**:不懂 CLI 的普通人 —— **不是 guanfu 的直接用户**,通过上游 skill / AI 对话分发消费 guanfu 数据。guanfu 本身不迁就他们。

设计优先级:Secondary > Primary > Tertiary。**MCP 优先于 CLI,CLI 优先于 Web**(我们不做 Web)。

---

## Philosophy(v3.1 最终版)

1. **给建议,但措辞概率化 / 条件化 / 区间化**。"倾向积累,概率约 60%" ≠ "应该买"。
2. **所有收益预期必须和基线对比**。5% 预期收益没意义,"5% vs 3m T-bill 1.2% = 风险调整差 +3.8%"才有意义。
3. **所有建议必须落盘到 claim ledger**。不是约束工具,是建立可回归校准的历史。
4. **组合上下文驱动差异化输出**。同一 BTC 盘面,15% 仓位的投资者和 35% 仓位的投资者看到的结论应该不同。
5. **行为护栏是核心,不是边角**。投资失败 80% 是行为错误,SKILL.md 必须干预。
6. **诚实降级**。信号不足的资产 / horizon(如 HS300 全历史 dir_hit 45-49%)不给伪装成有效的 forecast,写明"信号强度低于随机阈值"。
7. **MCP-native 优先**。SKILL 分层加载,不挤占其他 skill 的 context。
8. **默认简,详情要钱**。`guanfu` 裸跑默认 brief 摘要,`--full` 才是现在这套 40 行盘面。

三层分离保持:盘面层出数据 → 解读层出概率化建议 + 组合感知 + 行为护栏 → 决策层 = 用户 + AI 最终拍板。

---

## Track 分布(10 个)

| Track | 主题 | 代码影响 | v3.1 定位 |
|---|---|---|---|
| E | 定位 / 文档 | 0 | 基础 |
| F | 数据源补强 | `pkg/client/*` | 基础 |
| G | 算法升级 | `pkg/forecast/*` | 精度 |
| H | 产品体验 | `cmd/guanfu/*` | 用户 |
| I | 技术债 | 分散 | 维护 |
| J | 用户向输出(SKILL) | SKILL.md | 用户 |
| K | Claim + Intent ledger | `pkg/claim/`(新) | **v3 骨头** |
| **L** | **投资者上下文层** | `pkg/portfolio/` + `pkg/alerts/`(新) | **v3.1 骨头** |
| **M** | **MCP skill 分层加载** | `cmd/guanfu-mcp/` | **MCP 差异化** |
| **N** | **诚实降级 / 范围声明** | 多处 | **可信度** |

---

## Track E — 定位与文档(10 项)

| # | 优先级 | 改动 | 文件 |
|---|---|---|---|
| E1 | **P0** | README 首屏重写 + **明确用户画像** | `README.md` |
| E2 | **P0** | 命中率表同步 v6 baseline | `README.md` |
| E3 | **P0** | SKILL.md CLI 命令节补完(`stock` / `import-stock` / `refresh`) | `skill/SKILL.md` |
| E4 | **P1** | SKILL.md MCP 工具表补 `get_stock_forecast` + `guanfu calibrate` | `skill/SKILL.md` |
| E5 | **P1** | Reliability 标注文档化(`HorizonCaveat` 机制) | `skill/SKILL.md` + `README.md` |
| E6 | **P1** | DATA-SOURCES.md 重写(30+ 数据集 / 23 refresh source / 任意 stock / CAPE) | `docs/DATA-SOURCES.md` |
| E7 | **P2** | v2 roadmap 归档 | `docs/guanfu-v2-todo.md` |
| E8 | **P2** | Walk-forward 矩阵写进文档 | `docs/backtest-methodology.md` |
| E9 | **P0** | **新定位句**(见下) | `README.md` + `skill/SKILL.md` |
| E10 | **P1** | `docs/audience.md` — 用户画像与不同画像下的功能映射 | `docs/audience.md`(新) |

### 新定位句(E9)

> guanfu 是**面向有经验的个人投资者的多资产决策辅助工具**。CLI + MCP 双入口。覆盖 BTC / QQQ / SPY / Gold / HS300 + 任意美股。
>
> 输出:**原始指标 + 历史分位 + 前向收益分布 + 可靠性标注 + 数据源健康 + 基线对比 + 组合感知建议 + 行为护栏**。
>
> 我们**给建议**,但用概率、区间、条件表达,全部落 claim ledger 定期回归校准。我们**知道自己不适用**的情景(如 A 股 kNN 弱信号)会明说,不伪装。
>
> 对不懂 CLI 的普通人,通过 Claude/ChatGPT skill 分发。

### 与同类项目的关键差异(E1 首屏点题)

1. **无单一 0-100 总分**(btcdca.me / F&G / Rainbow 都压缩,丢失条件性)
2. **Horizon 可靠性标注**(dir_hit < 0.55 或 n < 10 自动 caveat)
3. **source_health 面板**(显式 ok/partial/stale/missing)
4. **Walk-forward 按年矩阵**(Gold regime 依赖、HS300 弱信号不藏)
5. **组合上下文驱动**(读 `portfolio.yaml`,同一盘面不同用户不同结论)
6. **基线对比强制**(vs 3m T-bill / 60-40)
7. **行为护栏**(冷静期 / 对立论证 / 锚定检查)
8. **Claim + Intent ledger**(工具预测 + 用户纪律双轨回归)

---

## Track F — 数据源补强(8 项,按 ROI 排)

| # | 优先级 | 源 | 用途 | 依赖 | 文件 |
|---|---|---|---|---|---|
| F1 | **P1** | FRED `WTREGEN` + `RRPONTSYD` | TGA + ON RRP,2022-23 真流动性 | `FRED_API_KEY` | `pkg/client/fred_history.go` |
| F2 | **P1** | DefiLlama `/stablecoins` | 稳定币按链 netflow | — | `pkg/client/defillama_stablecoin.go`(新) |
| F3 | **P1** | AkShare 融资余额 | HS300 拥挤度(可能推 dir_hit 过 50%) | — | `akshare_bridge.py` + `akshare_history.go` |
| F4 | **P1** | **FRED `DGS3MO`(3m T-bill)** | **所有 forecast 基线对比必备** | `FRED_API_KEY` | `fred_history.go` |
| F5 | **P2** | SPDR GLD holdings + WGC 央行购金 | Gold 2022+ regime 解释 | — | 新 Source |
| F6 | **P2** | CBOE Put/Call + NAAIM | Equity sentiment | — | CBOE done; NAAIM still candidate |
| F7 | **P2** | 事件日历(FRED FOMC / BLS CPI / BTC halving 固定表 / SEC ETF deadlines) | 为 `guanfu digest` + 告警服务 | — | `pkg/client/calendar.go`(新) |
| F8 | **P3** | Coinbase premium(Coinglass 免费) | BTC 美国机构 vs 全球需求差 | 免费 key | 新 client |

**不补**:CryptoQuant 付费 / Bloomberg / 单股 Form 4 / 新闻 feed / 社媒情绪。

### F5 状态更新(2026-05-09,Wave 6 决策)

F5 真正稀缺的是**央行季度购金净额**(World Gold Council 2022+ regime 的解释变量),而非 GLD 价格(Futu/Yahoo 已覆盖 → PriceStore key `gld`)。免费 endpoint 现状:

- WGC 季度 CSV:URL 在 `gold.org/goldhub/` 下多次改路径,无稳定版
- IMF DOT:有 CSV 但需要注册 + token
- Stooq `xaucb.f`(央行储备变化)系列:历史覆盖不足 + 数据过旧
- SPDR `historical_mf_data.xml`(GLD 持仓,but not 央行):XML 格式偶尔异常

**结论**:暂缓实现,不硬上会频繁 fail 的 source。Gold 2022+ regime 识别已被 G2 `regimeBucket` + 现有 DXY / real yield / COT 覆盖,边际收益不值得 endpoint 不稳定的维护成本。等 WGC 公开稳定 CSV 或 Stooq 补齐 central-bank-reserves symbol 历史覆盖再启动 F5。

### F6 状态更新(2026-05-11,review pass)

CBOE put/call 已落地为默认 no-key source,storage key 保持 `stooq_putcall` 兼容旧 forecast bundle。QQQ/SPY panel 与 feature bundle 已输出 `put_call_ratio`、`put_call_30d_change`、`put_call_252d_percentile`,并通过 `source_health.forecast_bundle_stooq_putcall` 暴露 stale/missing。

剩余仅保留为候选:
- NAAIM/AAII:只有找到稳定免费历史源并通过 ablation 才进入 forecast。
- CBOE 2019-10-07 至最近窗口之间的完整 daily-page backfill:当前不是阻塞项,因为 recent 420d 已覆盖 252-observation percentile。

---

## Track G — 算法升级(6 项,不换 kNN 架构)

| # | 优先级 | 改动 | 目标 | 文件 |
|---|---|---|---|---|
| G1 | **P1** | Conformal prediction 给 quantile 覆盖率保证 | split/online conformal,给 80/90% 区间实际下限 | `pkg/forecast/conformal.go`(新) |
| G2 | **P2** | 轻量 regime gating | 滚动 90d vol + DXY 60d 方向 → 2-3 regime,kNN 距离降权跨 regime | `pkg/forecast/regime.go`(扩展) |
| G3 | **P3** | Feature coverage 公式修复 | `Σ(weight_found)/Σ(weight_total)` 替代 `expectedFeatureCount=11` | `pkg/forecast/forecast.go` |
| G4 | **P3** | kNN + 线性模型 ensemble | disagreement 作 reliability 信号 | — |
| G5 | **P1** | **Recency-weighted analog 选项**(回应质疑 2) | `--recency-weighted`:近 5 年 analog 加权 2 倍;并要求 reliability 首因子变为 "当前 regime 历史样本数" | `pkg/forecast/forecast.go` |
| G6 | **P0** | **HS300 forecast 硬门槛**(回应质疑 1) | dir_hit < 0.50 时不输出数值预测,只说 "信号强度低于随机阈值,建议仅看原始指标" | `pkg/forecast/reliability.go` |

**不做**:Deep transformer / KAN / RL allocator / DTW。

---

## Track H — 产品体验(6 项)

| # | 优先级 | 改动 | 设计要点 | 文件 |
|---|---|---|---|---|
| H1 | **P0** | **默认 `--brief`,详情要 `--full`** | `guanfu` 裸跑出 10 行摘要(当前判断 + TOP3 支持 + TOP2 反证 + 最大风险 + 最 stale source)。现 40 行改到 `--full` | `cmd/guanfu/main.go` |
| H2 | **P1** | `guanfu digest` 子命令 | 每日 30 秒摘要:"相对昨天 X 发生有意义变化;未来 7 天事件:Y / Z" | `cmd/guanfu/cli_digest.go`(新) |
| H3 | **P1** | **所有 forecast 输出强制带基线对比**(3m T-bill / 60-40) | 见 § 基线对比示意 | `pkg/forecast/forecast.go` + 显示层 |
| H4 | **P2** | `guanfu stress --scenario 'real_yield+150bp'` | 扰动单特征,kNN 检索扰动后状态的 analog | `cmd/guanfu/cli_stress.go`(新) |
| H5 | **P3** | 跨资产联合 forecast(`guanfu joint --assets btc,qqq,gold`) | kNN 特征向量联合,不是 5 个独立问题 | RFC |
| H6 | **P2** | `market` / `dca` / `allocate` 子命令审计 | 确认输出概率化,不残留 v1 action 痕迹 | `cli_commands.go` |

### 基线对比示意(H3)

```
90d forecast BTC (n=21 analogs, feature_coverage 0.92, reliability OK)

  预期收益:     +5.2%  [-3.1%, +13.8%]  (p10/p90, 80% conformal)
  vs 3m T-bill: +1.2%  (无风险基线)
  vs 60/40:     +2.8%  (被动参考)
  风险调整差:   +2.4%  (仅当 80% 区间正数主导时)

  意味着:       90d 后 68% 概率跑赢 T-bill;32% 概率持现金更优。
```

---

## Track I — 技术债(5 项,同 v3.0)

I1-I5 同 v3.0,不赘述。

---

## Track J — 用户向输出升级(14 项,SKILL.md 为主)

| # | 优先级 | 新增输出 | 文件 |
|---|---|---|---|
| J1 | **P0** | 通俗总结段(读盘首段,3-5 句人话) | `skill/SKILL.md` |
| J2 | **P0** | 投资者问答模板(按 profile 分层) | `skill/SKILL.md` |
| J3 | **P0** | 风险雷达(读盘倒数第二段) | `skill/SKILL.md` |
| J4 | **P1** | 叙事化情景推演(3-5 条叙事路径) | `skill/SKILL.md` |
| J5 | **P1** | 合理估值区间推演 | `skill/SKILL.md` |
| J6 | **P1** | 买卖触发条件清单(指标,非价位) | `skill/SKILL.md` |
| J7 | **P1** | 跨资产比较段 | `skill/SKILL.md` |
| J8 | **P2** | 时间分层关注点(短/中/长线) | `skill/SKILL.md` |
| J9 | **P0(升级)** | **用户 profile 映射 — 读 portfolio.yaml 自动适配**(不再是"问一下") | `skill/SKILL.md` |
| J10 | **P2** | 决策自查清单(checklist) | `skill/SKILL.md` |
| J11 | **P2** | 历史复盘对照(读 claim ledger) | `skill/SKILL.md` |
| J12 | **P3** | 通俗术语表 | `skill/SKILL.md` |
| J13 | **P0** | **行为护栏段**(必出) | `skill/SKILL.md` |
| J14 | **P1** | **基线对比段**(必出,配合 H3) | `skill/SKILL.md` |

### J13 行为护栏(必出,投资者视角最高杠杆)

每次读盘强制附带至少 3 条护栏,从以下菜单中按场景选:

1. **冷静期检查**:若同一资产 < 4h 内多次读盘,明确提醒 "行情波动驱动反复读盘是过度反应风险,建议 15 分钟后再看"
2. **对立论证**:bull 读后附 "最强反方论点";bear 读后附 "最强正方论点"
3. **锚定偏差检查**:"**忽略你当前成本基础**,如果你今天有和持仓等值的现金,你还会买吗?"
4. **期限一致性**:portfolio.yaml 声明 5 年但当前看 30d forecast 时,提醒 "30d 对你是噪声"
5. **确认偏差**:"读盘前,你**希望**答案是什么?读盘后对比是否相符,不符时请认真看反证"
6. **FOMO 检查**:当价格 30d 涨 > 20% 且用户不在持仓时,"追涨是统计上的负期望行为,请先读 J6 触发条件,未满足不追"
7. **恐慌检查**:当价格 30d 跌 > 20% 且用户满仓时,"恐慌卖是锁定亏损,请先读触发条件"

---

## Track K — Claim + Intent ledger(9 项,v3 骨头之一)

### K 系列原则

- **Claim** = 工具的预测(forecast 自动落盘)
- **Intent** = 用户的意图(计划 / 触发条件 / 期限声明)
- 两者独立记录,**共同**进入回归校准

### 具体项

| # | 优先级 | 改动 | 依赖 | 文件 |
|---|---|---|---|---|
| K1 | **P0** | `pkg/claim` 数据类型(Claim + Intent) | — | `pkg/claim/types.go`(新) |
| K2 | **P0** | Ledger 持久化(`~/.guanfu/claims/YYYY-MM/`) | K1 | `pkg/claim/ledger.go`(新) |
| K3 | **P0** | forecast 自动发 claim | K1, K2 | `pkg/engine/asset.go` + `pkg/forecast/*` |
| K4 | **P1** | `guanfu calibrate` — 校准 claim | K3 + 3 月数据 | `cmd/guanfu/cli_calibrate.go`(新) |
| K5 | **P1** | `TestClaimLedgerIntegrity` CI 回归 | K1-K4 | `pkg/claim/ledger_test.go`(新) |
| K6 | **P2** | 校准指标进 README + source_health | K4 | `README.md` + `cmd/guanfu-mcp/main.go` |
| K7 | **P1** | **`guanfu intent log/list/review`** — Intent ledger CLI | K1 | `cmd/guanfu/cli_intent.go`(新) |
| K8 | **P1** | **`guanfu review-intent`** — drift 检查 | K7 + 60d Intent | `cli_intent.go` |
| K9 | **P2** | Claim + Intent 联合 dashboard(MCP resource) | K3+K7 | `cmd/guanfu-mcp/main.go` |

### K7 Intent 结构

```go
type Intent struct {
    ID           string
    AsOf         time.Time
    Asset        string
    HorizonClass string         // "5y_hold" / "6m_rebalance" / "3m_trade"
    Thesis       string         // 自述逻辑
    TriggerBuy   []Condition    // 指标阈值触发
    TriggerSell  []Condition
    CurrentPos   string         // "15%, 上限 25%"
    SchemaVersion int
}
```

### K8 `guanfu review-intent` 示意

```
Intent[2026-03-01] BTC "5y_hold" thesis: M2 扩张期积累
  声明触发:    mayer<0.8 && ETF 4w 正流入 → 考虑加仓
  期间满足:    过去 60d 有 12d 满足条件
  你加仓了吗?  未记录执行(建议手动 log)
  纪律评分:    N/A(需执行 log 才能算)

Intent[2026-02-10] QQQ "6m_rebalance"
  声明:        若 QQQ 90d 涨 > 15% 且 CAPE q > 85%,部分减持
  期间满足:    满足 23d
  Horizon drift:  ⚠ 你在此期间交易 3 次,明显偏离 "6m_rebalance" 节奏
```

---

## Track L — 投资者上下文层(8 项,v3.1 新骨头)

这是整个 v3.1 最被低估、ROI 最高的 track。

### 设计原则

- **Opt-in**:不创建 `portfolio.yaml` 时,guanfu 退化为无上下文模式,行为和 v2 一致
- **本地**:纯本地文件,不触网,不同步
- **最小侵入**:已有 CLI / MCP 不动,加一层 context 预处理

### 具体项

| # | 优先级 | 改动 | 说明 | 文件 |
|---|---|---|---|---|
| L1 | **P0** | `portfolio.yaml` schema + loader | 位置 `~/.guanfu/portfolio.yaml`;字段见下 | `pkg/portfolio/types.go`(新) |
| L2 | **P0** | 读盘链路注入 portfolio context | `BuildPanel` / `BuildVerdict` / `BuildForecast` 可选接收 `*Portfolio` | `pkg/engine/*` |
| L3 | **P0** | **portfolio-aware verdict** | 同一盘面:15% 仓位 "倾向积累,权重空间充足" vs 35% 仓位 "估值偏低但已超自定上限" | `pkg/engine/verdict.go` |
| L4 | **P1** | `guanfu watch <asset> --when '<cond>'` — 条件监控 | 后台轮询,触发写 `~/.guanfu/alerts/` | `cmd/guanfu/cli_watch.go`(新) |
| L5 | **P1** | Alert dispatcher(osascript / 邮件 / Telegram / webhook) | 插件式,默认 osascript(macOS 通知) | `pkg/alerts/dispatcher.go`(新) |
| L6 | **P1** | `guanfu digest` — 每日摘要 | 见 H2;配合 L4/F7 事件日历 | `cmd/guanfu/cli_digest.go`(新) |
| L7 | **P2** | 多币种视图(`home_currency` 字段) | USD 资产自动附 CNY/JPY 等 6/12m 收益列 | `pkg/engine/currency.go`(新) |
| L8 | **P2** | 成本感知(TER / 托管 / 点差) | DCA 模拟末尾附 "若通过 GLD 持有,20y 复合 -6%" | `cmd/guanfu/cli_dca.go` |

### L1 `portfolio.yaml` schema

```yaml
# ~/.guanfu/portfolio.yaml(opt-in)
schema_version: 1
holdings:
  btc:
    amount: 0.35
    cost_basis_usd: 42000
    acquired: 2023-06
  qqq:
    shares: 50
    cost_basis_usd: 380
  cash:
    usd: 30000
    cny: 100000

preferences:
  horizon_years: 5
  risk_budget: moderate      # conservative / moderate / aggressive
  home_currency: CNY
  ceiling_pct:               # 自定单资产上限
    btc: 25
    equity: 60
    gold: 15

behavior:
  cooldown_hours: 4          # J13 冷静期
  fomo_threshold_pct: 20     # J13 FOMO 检查
  panic_threshold_pct: 20    # J13 恐慌检查
```

---

## Track M — MCP skill 分层加载(4 项,MCP 差异化)

### 问题

`skill/SKILL.md` 900+ 行 ≈ 12K token。Claude Sonnet 一次加载后,其他 skill 挤不进 context。真实 MCP 用户体验是 "问 BTC 怎么样 → 读 SKILL → 读 panel JSON → 读 verdict → 读 forecast → context 爆了"。

### 具体项

| # | 优先级 | 改动 | 说明 | 文件 |
|---|---|---|---|---|
| M1 | **P0** | SKILL 拆 3 层 | tier1 200 行(数据契约 + 关键阈值,必载)/ tier2 决策框架 + 行为护栏 / tier3 术语 + 机制库 + 类比 | `skill/SKILL.md` 拆 |
| M2 | **P0** | MCP resource 分层 URI | `guanfu://skill/tier1` / `tier2` / `tier3`;`resources/list` 明示用法 | `cmd/guanfu-mcp/main.go` |
| M3 | **P1** | 每个 MCP 工具 description 注明所需 tier | 如 `get_forecast` → "建议先载 tier1 + tier2" | `cmd/guanfu-mcp/main.go` |
| M4 | **P1** | `guanfu://knowledge/skill.md` 保留 alias(向后兼容) | 默认返回 tier1 + tier2 合并,加一行提示 "深入问题请载 tier3" | `cmd/guanfu-mcp/main.go` |

---

## Track N — 诚实降级 / 范围声明(5 项)

### 原则

guanfu 和同类项目的最大差异化 = **敢于说 "我不知道"**。Track N 把这一点系统化。

| # | 优先级 | 改动 | 说明 | 文件 |
|---|---|---|---|---|
| N1 | **P0** | HS300 forecast 硬门槛(同 G6) | dir_hit < 0.50 时不输出数值,只说 "信号强度低于随机" | `pkg/forecast/reliability.go` |
| N2 | **P1** | 当前 regime 历史样本数作为 reliability 首因子 | kNN 检索后,若 "当前特征向量 + 最近 2 年 analog n < 10",reliability 降级 | `pkg/forecast/reliability.go` |
| N3 | **P1** | source_health 升级为 "可信度分" | 每个 source 附 0-1 可信度(综合 stale / fallback_used / historical coverage) | `pkg/model/types.go` + display |
| N4 | **P2** | `guanfu status --frank` | 按资产输出 "以下 horizon 可靠:X / 可疑:Y / 不建议使用:Z" | `cmd/guanfu/cli_status.go` |
| N5 | **P2** | README 加 "已知失效情景" 列表 | 不藏 regime shift / 黑天鹅 / 监管事件下 forecast 不可用的事实 | `README.md` |

---

## 建议执行顺序(Wave 重排)

### Wave 0 — 定位与紧急修复(1 周,零风险)

```
E1 E2 E9 E10   ▸ README + 用户画像 + 新定位
E3 E4 E5       ▸ SKILL.md CLI / MCP / reliability
E6 E7          ▸ DATA-SOURCES / 归档 v2
M1 M2          ▸ SKILL 拆 3 层(P0,立即释放 MCP context)
N1(同 G6)     ▸ HS300 forecast 硬门槛
H1             ▸ 默认 --brief
G3             ▸ Feature coverage 公式修复(顺手)
```

### Wave 1 — Claim + Intent + Portfolio(3 骨头并行,~1.5-2 周)

```
K1 K2 K3 K5    ▸ Claim ledger
K7 K8          ▸ Intent ledger + review-intent
L1 L2 L3       ▸ portfolio.yaml + 注入 + portfolio-aware verdict
J9             ▸ SKILL 读 portfolio.yaml 自动适配
J13            ▸ 行为护栏段
```

### Wave 2 — 数据源 + 基线对比(~1-2 周)

```
F4             ▸ 3m T-bill(基线对比基础)
F1 F2 F3       ▸ TGA+RRP / 稳定币 netflow / A 股融资余额
F7             ▸ 事件日历
H3 J14         ▸ forecast 强制基线对比
L4 L5 L6       ▸ watch + alerts + digest
E8             ▸ Walk-forward 矩阵文档
```

### Wave 3 — 算法升级 + 校准(~2-3 周)

```
G1             ▸ Conformal intervals
G2             ▸ regime gating
G5             ▸ recency-weighted analog
N2 N3          ▸ reliability 首因子 + source_health 可信度分
K4 K6          ▸ calibrate 子命令 + 指标进 README
K9             ▸ Claim + Intent 联合 MCP resource
H2             ▸ guanfu digest
J1 J2 J3       ▸ SKILL 通俗总结 + 问答 + 风险雷达(依赖 K/L)
```

### Wave 4 — 剩余用户向 + 扩展(按需)

```
J4-J8 J10-J12  ▸ 情景推演 / 估值区间 / 触发清单 / 跨资产比较 / 分层关注 / 自查 / 复盘 / 术语
F5 F6 F8       ▸ GLD+WGC / Put-Call / Coinbase premium
G4             ▸ kNN + 线性 ensemble
H4 H5 H6       ▸ stress / joint forecast / 子命令审计
L7 L8          ▸ 多币种 / 成本感知
N4 N5          ▸ status --frank / 失效情景文档
I1-I5          ▸ 技术债
M3 M4          ▸ MCP tier 声明 / 向后兼容
F7             ▸ 扩展 calendar 源
```

---

## 回归预算(v3.1 最终)

| 指标 | 容忍下降 | 触发动作 |
|---|---|---|
| 任何 horizon dir hit(5 baseline + 任意 stock) | ≥ 3pp | 回滚改动,单独 review |
| PIT 偏离 0.5 加大 | ≥ 0.05 | 回滚或调权重 |
| 任意资产 backtest 失败 / panic | 任何 | 立即回滚 |
| K4 区间覆盖率与声称值偏差 | ≥ 5pp | 回滚最近特征 / 权重 / conformal 改动 |
| K4 dir_hit 连续 3 月趋势下降 | 任何 | 触发 RFC,review 整个特征 bundle |
| **K8 Intent drift 误报率** | **≥ 20%** | **调 drift 判定阈值** |
| **L3 portfolio-aware verdict 在无 portfolio.yaml 时输出变化** | **任何** | **回滚(必须保持无上下文路径不变)** |
| **M1 tier1 > 300 行** | **任何** | **拆不干净就回滚** |

---

## v3.1 关键观点 (与 v3.0 的区别)

1. **用户画像明确 Primary/Secondary/Tertiary**。Tertiary 不迁就,通过 skill 分发 → 所有 CLI/MCP 设计优先服务前两类。
2. **Track L(投资者上下文)升为 v3 第 2 骨头**。`portfolio.yaml` 驱动所有个性化输出。
3. **Intent ledger 和 Claim ledger 并重**。纪律回归比预测回归对最终回报更重要。
4. **行为护栏(J13)升 P0,必出**。投资失败 80% 是行为错误。
5. **基线对比(H3/J14)升 P0**。所有 forecast 必须 vs T-bill / 60-40。
6. **HS300 forecast 硬门槛(G6/N1)升 P0**。dir_hit<50% 时不伪装预测。
7. **MCP SKILL 分层(Track M)升 P0**。当前 900 行挤占 context,影响 MCP 用户体验。
8. **H1 默认 brief**。主次倒过来。
9. **Track N(诚实降级)独立成 track**。不再零散。

---

## 文件索引(v3.1 全量)

| 文件 | 改动 | Wave |
|---|---|---|
| `README.md` | E1/E2/E9/E10 + K6 + N5 | 0/2/3/4 |
| `skill/SKILL.md` → 拆 3 层 | M1 / E3-E5 / J1-J14 | 0/1/3/4 |
| `skill/tier1.md`(新) | M1 数据契约 + 阈值 | 0 |
| `skill/tier2.md`(新) | M1 决策框架 + 行为护栏 | 0 |
| `skill/tier3.md`(新) | M1 术语 + 机制 + 类比 | 0 |
| `docs/audience.md`(新) | E10 用户画像 | 0 |
| `docs/DATA-SOURCES.md` | E6 重写 | 0 |
| `docs/backtest-methodology.md` | E8 walk-forward 矩阵 | 2 |
| `docs/guanfu-v2-todo.md` | E7 freeze | 0 |
| `pkg/claim/types.go`(新) | K1 Claim + Intent | 1 |
| `pkg/claim/ledger.go`(新) | K2 | 1 |
| `pkg/claim/ledger_test.go`(新) | K5 | 1 |
| `pkg/portfolio/types.go`(新) | L1 | 1 |
| `pkg/portfolio/loader.go`(新) | L1 | 1 |
| `pkg/alerts/dispatcher.go`(新) | L5 | 2 |
| `pkg/engine/asset.go` + `pkg/forecast/*` | K3 forecast 自动落盘 + L2 context 注入 | 1 |
| `pkg/engine/verdict.go` | L3 portfolio-aware | 1 |
| `pkg/engine/currency.go`(新) | L7 多币种 | 4 |
| `cmd/guanfu/main.go` | H1 默认 brief | 0 |
| `cmd/guanfu/cli_calibrate.go`(新) | K4 | 3 |
| `cmd/guanfu/cli_intent.go`(新) | K7/K8 | 1 |
| `cmd/guanfu/cli_watch.go`(新) | L4 | 2 |
| `cmd/guanfu/cli_digest.go`(新) | H2/L6 | 3 |
| `cmd/guanfu/cli_stress.go`(新) | H4 | 4 |
| `cmd/guanfu-mcp/main.go` | M2/M3/M4 + K9 | 0/3 |
| `pkg/client/fred_history.go` | F1 TGA+RRP / F4 T-bill | 2 |
| `pkg/client/defillama_stablecoin.go`(新) | F2 | 2 |
| `pkg/client/akshare_history.go` + `scripts/akshare_bridge.py` | F3 融资余额 | 2 |
| `pkg/client/calendar.go`(新) | F7 事件日历 | 2 |
| `pkg/forecast/conformal.go`(新) | G1 | 3 |
| `pkg/forecast/regime.go`(扩展) | G2 | 3 |
| `pkg/forecast/forecast.go` | G3 coverage / G5 recency / H3 基线 | 0/3/2 |
| `pkg/forecast/reliability.go` | G6/N1/N2 | 0/3 |
| `pkg/model/types.go` | N3 source_health 可信度分 | 3 |

---

## 变更记录

| 版本 | 日期 | 变更 |
|---|---|---|
| v3.0 | 2026-05-09 | 首版。7 Track × 46 项 |
| **v3.1** | **2026-05-09** | **投资者视角重构**。(1) 明确用户画像 Primary/Secondary/Tertiary;(2) 新增 Track L(投资者上下文层)与 K 并列骨头,含 portfolio.yaml / watch / alerts / digest / 多币种 / 成本感知;(3) 新增 Track M(MCP SKILL 分层)解决 context 占用;(4) 新增 Track N(诚实降级)独立成 track;(5) K 扩展为 Claim + Intent 双 ledger;(6) 行为护栏 J13 升 P0 必出;(7) 基线对比 H3/J14 升 P0;(8) HS300 forecast 硬门槛 G6/N1 升 P0;(9) H1 默认 brief;(10) G5 recency-weighted 应对 2024+ 新 regime;(11) F4 3m T-bill 升 P1 作为基线基础;(12) F7 事件日历支撑 digest/watch;(13) 回归预算扩 Intent drift / portfolio 无上下文路径稳定 / SKILL 分层规模 |
