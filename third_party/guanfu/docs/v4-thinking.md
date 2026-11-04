# v4 thinking — 不是 roadmap,是 decision log

> 此文档故意**不定任务 / 不列 P0-P3**。v3 刚完,很多方向依赖 30-90 天实测数据才能定论。先把**思考过程**写下,避免未来拍脑袋。

---

## v3 交付状态(2026-05-09)

- 72 / 73 roadmap 项完成,唯一 pending 的 F5(WGC 央行购金)被明确判为"等稳定 endpoint 再做"
- 8 commits 推送 origin/main
- 24 package 全绿 + vet clean + 无假阳性测试
- 冒烟验证:所有无 API key 路径跑通(见 `wave-7-smoke.md` 或本文件底部)

**核心 v3 产出**:
- Claim + Intent ledger 基础设施(K 系列)
- Portfolio 上下文(L 系列)
- Conformal / regime / recency / ensemble / hard-block 五种 forecast 升级(G 系列)
- 行为护栏 + 分层 SKILL + tier URIs(M + J 系列)
- 7 个新子命令(`intent / watch / digest / calibrate / stress / joint / status --frank`)
- 10 个新数据源(TGA / RRP / DGS3MO / Margin / DefiLlama / Put-Call / Coinbase / 3 个延伸)

---

## 关键未知变量(必须先测才能定 v4)

v3 非常完整,但**没有一次真实数据的完整链路**。v4 方向之前,这些必须有答案:

### U1. `guanfu refresh` 全量拉取多慢?

第一次全量 28 个 source 预估 5-30 分钟,方差取决于 FRED/Yahoo/Deribit 限流。
→ **行动**:用户首次使用前跑一次,记录实际耗时。如果 > 15 分钟,需要 UX 优化(进度条 / 并行 / chunking)

### U2. Claim ledger 积累 30 天后,`calibrate` 数字长什么样?

我们写了完整的 K4 校准,但**从未见过它输出真实数字**。
- BTC 90d dir_hit 会贴近 baseline 65% 吗?
- 80% conformal 区间 coverage 实际是 72% 还是 88%?
- Brier score 会稳定在 0.2-0.25 范围?
- Gold 90d (标 55% approaching random) 实际校准会不会反而正常?
→ **行动**:每天跑一次 `guanfu btc`(自动写 claim),90 天后跑 `guanfu calibrate --json` 看第一份真实数字

### U3. Ensemble disagreement 在 live 数据上分布如何?

G4 设 >5% 触发 caveat。如果真实数据里每次跑都 > 5% → 阈值太松,全是噪声;如果从来触不到 → 没用。
→ **行动**:30 天内记录每次 forecast 的 `ensemble_disagreement_pct`,画一个分布直方图。阈值可能需要重调

### U4. Portfolio 真的有人配置吗?

L 系列假设用户愿意写 `~/.guanfu/portfolio.json`。现实可能:
- 用户不知道有这个功能
- 用户嫌麻烦
- 用户担心隐私
→ **行动**:给 `guanfu portfolio init` 交互式创建,降低门槛

### U6. MCP 在 Claude Desktop / Cursor 里用起来怎么样?

tier1/2/3 是我设计的分层,但**没在真实 MCP 客户端里测过**。可能发现:
- Claude 根本不懂什么时候该加载 tier2 vs tier3
- resources/list 输出太长导致 Claude 忽略
- 工具输出 JSON 太大挤占 context(尽管我们已经做了 `include_panel` 开关)
→ **行动**:配置一次真实 MCP,走 5 个常见问法,记录 Claude 实际读了什么

---

## v4 三个可能方向,分别何时启动

### 方向 A:AI-agent 编排深化(MCP-native)

**假设场景**:Claude Desktop / Cursor 用户每天用 guanfu,v3 已经稳用

**核心工作**:
- `suggest_workflow(intent)` MCP 工具 — 给 AI 一个推荐工具链
- MCP 工具输出带 `reasoning_trace` — AI 能自己验证每个字段
- AI 解读回写 ledger — calibrate 也评估 AI 解读质量,不仅 forecast 数字
- 跨会话上下文(用户上次聊了什么,下次 guanfu 能接上)

**启动前置**:U6 跑通 — 确认当前 MCP 体验瓶颈是什么

**工程量**:2-3 周

**最大风险**:Claude 的 agent 框架还在快速变(2026 Q1 出的新 agent spec,规范还不稳)— 做早了容易白做

### 方向 B:短线模块(独立新产品)

**假设场景**:有真实短线用户 feedback,或你自己想用 guanfu 做 1-7 天决策

**核心工作**:
- DTW 路径匹配(不是 point-in-time kNN)
- 分钟级 / 小时级 orderbook + funding 边际特征
- 更强的行为护栏(短线是 FOMO 重灾区)
- `guanfu short <asset>` 独立子命令

**启动前置**:**必须先有用户需求证据**。不做假想需求

**工程量**:3-4 周 + 3-5 个新分钟级数据源

**最大风险**:做成了也容易和 guanfu "不给交易指令" 哲学冲突 — 短线本质就是频繁决策

### 方向 C:反馈环自动化(Self-improving forecast)

**假设场景**:已有 90 天 claim ledger,发现某 asset / horizon 的 calibrate 数字持续下滑

**核心工作**:
- Calibrate 定期跑(cron-friendly,`guanfu calibrate --auto-caveat`)
- 当 live dir_hit 连续 30 天低于 reliability 表 ≥5pp → **自动在 SKILL 输出里降级该 horizon 的置信度**
- 反过来,稳定高于预期的 → 提升该 horizon 权重
- 形成"guanfu 自己校准自己"的闭环

**启动前置**:**必须先有 90 天 ledger 数据**

**工程量**:1-2 周(纯逻辑,无新数据源)

**最大风险**:反馈环容易不稳定 — 如果实时数字被一次黑天鹅打坏,自动降级可能过度反应

---

## 我的当前建议

**未来 90 天不写 v4 代码**。做这些:

| 时间 | 动作 |
|---|---|
| 立即 | 把 v3 部署到日常使用(`guanfu` + cron `guanfu digest` + 每日 claim 自动 emit) |
| 第 7 天 | 第一次 `guanfu calibrate --json`,看能不能跑通(应该会说 "no matured claims") |
| 第 30 天 | 跑 `calibrate` 看第一份 30d horizon 真实数据 |
| 第 60 天 | 跑所有 horizon + 用 U3 / U4 观察点记录 |
| **第 90 天** | **基于真实数据重新看 v4 方向**;这时 U1-U6 都有答案 |

**90 天后三条路任选一条的决策树**:

```
[90 天实测完成]
  │
  ├─ MCP 用得频繁 + U6 显示 agent 编排是瓶颈 → 做方向 A
  │
  ├─ 短线需求明确 + 有多个真实用户反馈 → 做方向 B
  │   (否则永远不做;这是"假想需求"最高概率的陷阱)
  │
  └─ 90 天 calibrate 数据显示 reliability 表不稳 → 做方向 C
      (最无需假想,最有证据驱动)
```

**如果 90 天后三个条件都不明确**,说明 v3 已经够用,不需要 v4,继续积累数据直到条件明确。

---

## 即使不做 v4,仍可能需要的小维护

| 项 | 频率 | 工作量 |
|---|
| `pkg/assetprofile/profile.go` reliability row 更新 | 每 3 个月跑一次 `TestBacktestBundles`,数字变化 > 3pp 更新表 + AsOf；`pkg/forecast/reliability.go` 只保留渲染规则 | 1 小时 |
| `pkg/calendar/calendar.go` FOMC 表 | 每年 1 月添加下一年 FOMC 日程(Fed 官方发布时) | 30 分钟 |
| 数据源 endpoint 漂移(CBOE / CoinGecko / DefiLlama)| 通过 calibrate + source_health 监控;发现 stale/fail 率升高时修 | 按需 |

---

## 最后:哲学反思

v3 是"给工具的工具" — 给 maintainer 配置 / 给 AI 消费 context / 给用户 opt-in portfolio。非常完整,但也可能**过度工程化**:

- Claim ledger:真的每个用户都会读校准吗?还是大部分人只看当前读盘?
- Portfolio 上下文:真的会写那个 JSON 吗?
- Intent + drift:真的会记录自己的投资意图吗?

90 天数据会告诉我们:**哪些 feature 真的被用,哪些是 scratch-built 的理论正确功能**。不被用的可以在 v4 考虑降级为 internal-only。

**最诚实的可能**:v3 已经 > 用户真实需求,v4 主要工作不是加东西,而是**简化使用路径**(让 90% 的用户 3 分钟就能把最有价值的 20% 功能用起来)。

---

## 冒烟测试记录(2026-05-09)

不依赖外部 API key 的路径全部通过:

- `make all` → 3 binary 生成,全 test 绿,vet clean
- `guanfu --version` → v0.1.2-77-g9ee2424
- `guanfu refresh --dry-run` → 列出 28 个 source
- `guanfu status --frank` → 三桶分类输出符合预期
- `guanfu intent log/list` → 持久化到 `~/.guanfu/claims/intents/YYYY-MM/` + 读取
- `guanfu digest` → F7 事件日历命中下一个 CPI release
- `guanfu calibrate` → 空 ledger 干净响应 ("no matured claims")
- `guanfu-mcp` (stdio) → `resources/list` 返回完整 tier1/2/3 + K9 ledger summary + per-asset URI

**未测(需 API key)**:
- `guanfu refresh`(需 FRED_API_KEY / Yahoo 网络)
- `guanfu btc` / `guanfu qqq` 等 panel 构建(需外部数据)

这些走到真实环境再测。

## 维护记录(2026-05-11)

- Stooq put/call 默认路径替换为 CBOE official no-key 数据源；storage key 保持 `stooq_putcall` 以兼容历史文件和 forecast bundle。
- CBOE 单源刷新验证:`guanfu --timeout 180s refresh --only=stooq_putcall` 写入 4307 行,last_date=2026-05-08。
- 全资产巡检:`go run ./cmd/guanfu backtest all --plain` 通过,BTC/QQQ/SPY/Gold feature coverage 均为 100%。
- Reliability 表已按 `go test ./pkg/engine -run TestBacktestBundles -v` 同步到 2026-05-11；Gold 180d 继续 hard-block,Gold 90d 需 caveat。
