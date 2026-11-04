# guanfu 深度分析与重构方案（2026-07-03）

> **执行记录（2026-07-03，同日执行完毕；主人拍板：1 接 / 2 拆 / 3 删）**
> - **G1 ✅** claim 闭环接通——根因比预想多两层：cron 缺 `--forecast` + `~/.local/bin/guanfu`
>   停在 5/5 旧版 + **gold 价格序列断更 35 天**（从无人跑 `guanfu refresh`，forecast 一直基于
>   5 月末数据）。现 `scripts/emit_claims.sh`（cron 09:15）先 refresh 再四资产 emit；
>   当日已写入 btc/qqq/spy/gold 共 20 条 claim。
> - **G2 ✅** deadcode 57→11；余 11 项全部有保留理由（history 旧 API / backtest Run/HitRate/
>   Summary / claim Ledger.Root 均被测试或回归门引用——**deadcode 对测试引用全盲，删前必须
>   grep _test**，本次靠这个躲过两次误删）。
> - **G3 ✅** intent + portfolio 全删（含 Verdict.PortfolioContext、MCP K9 intent 段）；
>   `parseKV` 迁至 cli_commands.go。
> - **G4 ✦ 修正**：`pkg/forecast/backtest` **半活**（`RunWithOptions` 是 `guanfu backtest`
>   消融引擎）——不删，改为在 backtest-methodology.md 定界"两套回测各答什么"。
> - **G5 ✅** web/ + 3 个喂数脚本 + 部署文档 → 独立仓 `~/nimbus-os/btcdca-web`（已 init+commit）；
>   `guanfu-web-export` 默认输出改指新仓；threshold-search → `cmd/experiments/`；data/ 快照出库。
> - **G6 ✅** README/CLAUDE.md/skill 文档对齐；顺带修复 `make mcp` 存量破损（cp 早已删除的
>   futu_bridge.py）。
> - **验收**：go build/vet 清洁、全测 0 FAIL、TestBacktestBundles 绿、`backtest all` 跑通、
>   CLI 冒烟正常。算法未动（按 §7 决策：等 90 天 calibrate 数据）。
> - **spx_cape ✅ 已修（2026-07-03）**：shillerdata.com 撤掉了稳定的 `/ie_data.xls` 路径（改为主页
>   背后轮换 UUID 的 wsimg blob 链接）→ `import_cape.py` 改为每次抓主页解析当前链接；**刻意不用**
>   仍返回 200 的 Yale 镜像做兜底（它冻结在 2023-10，静默陈旧比响亮失败更糟）。数据已补到
>   2026-07-01（CAPE 39.46）。连带修了第二个坑：脚本直写 JSON 绕过 meta，refresh 见文件新鲜即跳过、
>   永不回写 meta → status 长期显示假 "! 882d ago"；CAPESource skip 路径现做 meta 对账。

> **性质**：架构/产品审查 + 减法为主的重构方案（规划，待批准）。
> **方法**：逐包读码（22 pkg + 6 cmd 实测 LOC）+ `~/.guanfu` 使用痕迹取证 + 对照
> `docs/v4-thinking.md`（2026-05-09）自设的 90 天观察期（现已过 ~55 天）。
>
> **一句话诊断**：核心（读盘 + kNN 推演 + 数据底座）**不杂，而且质量很高**；杂的是
> **围绕核心长出的三圈外围**——①v3 造好但 55 天零使用的"理论正确功能"（intent/portfolio/
> stress/joint…），②同一问题的多套实现与停放实验（三套回测、双版特征 API、40+ 死函数），
> ③repo 内容混装（Go CLI + Next.js 网站 + 数据文件 + 研发脚本同仓）。
> v4-thinking 早已预言："**v4 的主要工作不是加东西，而是简化**"——本方案就是执行它。

---

## 1. 功能全景（as-built，六个面）

| 面 | 内容 | 体量 |
|---|---|---|
| A 核心读盘 | Asset 接口（Key/FetchSnapshot/BuildPanel/BuildVerdict/BuildForecast）：BTC 8 域 40+ 指标、QQQ/SPY 6 域、Gold、任意美股 dashboard；结构化 verdict（证据链+反证+失效条件） | engine 7.4k |
| B 推演引擎 | kNN 历史相似（point-in-time、profile 驱动特征、TopK=21）+ conformal 区间 + ensemble disagreement + regime/recency 加权 + reliability 三档 hard-block | forecast 3.9k |
| C 数据底座 | PriceStore（~/.guanfu/prices，60 keys，只增不减守则）+ history.db（自采指标日行→分位）+ refresh 28 源 + source_health 显式降级 | client 4.6k + store/history/cache 1.1k |
| D 周边决策 | dca / allocate / market / digest / watch / stress / joint / calibrate / intent | dca/allocate/portfolio/claim/calendar ~1.9k |
| E 分发面 | CLI、MCP（tier1/2/3 资源分层）、nimbus `btc-guanfu` skill（skill/ 目录）、**web/（Next.js，btcdca.me）** + guanfu-web-export → 小程序间接消费 | mcp 1.1k + web 整站 |
| F 研发工具 | `guanfu backtest`（kNN walk-forward，回归门）、cmd/guanfu-backtest（verdict 历史回放，1527 行单文件）、threshold-search、similar、ablation | ~2.4k |

## 2. 先说不动的：核心质量评价

**算法侧是同类项目里少见的诚实设计，全部保留**：不压缩总分、每 (asset,horizon) 独立
reliability、`dir_hit<0.50` 拒绝输出、conformal 报实际覆盖、walk-forward 按 year×horizon、
source_health 显式 fallback 标注。工程侧：Asset 接口 4 方法干净、profile 驱动特征已统一入口
（`ExtractorsForAsset`）、数据源已 Go-first 收敛（client 23 文件中 20 个走 datasources 包）、
回归门文化（TestBacktestBundles + 回归预算 3pp）成熟。

**这不是一个需要"重写"的项目。是一个需要"收摊子"的项目。**

## 3. "杂"的证据（逐项实测）

### D1（最大头）：功能面 ≫ 使用面 —— v4-thinking 的预言应验

`~/.guanfu` 取证（2026-07-03，观察期第 ~55 天）：

| 功能 | v3 投入 | 55 天真实使用 | 判定 |
|---|---|---|---|
| 每日盘面 cron（`guanfu --json`→panels/）| — | ✅ 每天 09:00 在跑（cron.log 至 07-02） | **活** |
| refresh / PriceStore | C 系列 | ✅ 60 keys，06-29 仍更新 | **活** |
| nimbus btc-guanfu skill 按需读盘 | M/J 系列 | ✅ | **活** |
| claim ledger + calibrate | K 系列（v3 核心卖点"问责"）| ❌ 23 条 claim 全部停在 05-08~10 冒烟周，之后 0；calibrate 无料可校 | **断档**（根因见下）|
| intent ledger | K8 | ❌ 0 条，从未用过 | 零使用 |
| portfolio 上下文 | L 系列 | ❌ portfolio.json 不存在（U4 答案：没人配）| 零使用 |
| watch / stress / joint / digest | v3 新子命令 | ❌ cron 无、状态文件无 | 零使用 |

**claim 断档的根因是机制而非态度**：`emitClaim` 只在产生 *forecast* 时写入
（`cmd/guanfu/main.go:141`），而 cron 跑的是裸 `guanfu --json`（默认盘面无 forecast）→
账本永远不积累 → calibrate 永远空转 → README 主打的"问责+校准"闭环实际是断的。

### D2：同一问题多套实现 / 新旧并存

| 重复点 | 实况 |
|---|---|
| **三套回测** | ①`pkg/engine/backtest.go`（kNN walk-forward，回归门在用）②`cmd/guanfu-backtest` 1527 行单文件（verdict/stance 历史回放，独立方法论）③`pkg/forecast/backtest`（walk-forward 评估框架，**零调用方，死**）|
| **特征 API 双版** | 活：`ExtractorsForAsset/ForProfile`（profile 驱动）；死：`EquityExtractors/GoldExtractors/USStockExtractors` 旧包装。**CLAUDE.md「核心抽象」章还在教旧 API** → 文档/代码漂移，会误导下一个维护者（本次差点因此误删活代码）|
| **停放实验 40+ 函数** | `BuildTwoStage`（两阶段匹配）、`DetectRegime`（7 子判定牛熊分类器）、`BuildEquityVerdictEnhanced`+3 个 scoreEquity*、assetprofile 4 函数、`allocate.ComputeRiskMetrics`——全部零接线。对应 v4 方向 B/C 的预研，但 v4-thinking 明确"启动前置未满足不做" |
| **孤儿桥** | `~/.guanfu/futu_bridge.py`：零 Go 调用方（已被 `services_market.go`→market-data:8810 替代）|
| **三处缓存位置** | repo 内 `cache/`（3 个 JSON **已入库**）+ `data/`（CSV/JSON 入库）+ `~/.guanfu/cache`。价格快照类数据进 git 是历史遗留 |

### D3：repo 内容混装

一个 Go module 仓里同时住着：Go CLI/MCP 核心、**完整 Next.js 网站**（`web/`，btcdca.me，
自带 node_modules/构建链）、`skill/`（给 AI 的知识库+contracts+profiles）、入库的数据快照
（cache/ + data/）、docs 里 v1-v3 全量 archive、以及 cmd 下研发脚本与产品入口平级混放
（threshold-search / ablation vs guanfu / guanfu-mcp）。CLAUDE.md 自己都写了
"ad-hoc 调参脚本不要放进 cmd/guanfu/"——但整个 web 站在仓里比这大得多。

### D4：文档面积 > 代码需要

docs/ 14+ 文件含 v1-v3 archive、5 处 plans/architecture 散布；README 功能表（intent/
calibrate/stress…）与真实使用面（3 个活场景）差距大——**对外承诺了没在兑现的东西**（问责闭环断档）。

## 4. 重构方案（减法为主；对齐 v4-thinking 决策树）

> 铁律：A/B/C 三面（读盘/推演/底座）不动逻辑；`TestBacktestBundles` 全绿；
> PriceStore 只增不减；每步独立可回滚。

### G1 接上唯一断掉的闭环（唯一的"加法"，改一行）
cron 改为每日 emit claim（`guanfu btc --forecast --json > panels/…` 或给 `--json` 加
`--emit-claim`）。**理由**：问责+校准是 README 的差异化卖点和 K 系列全部投入的意义；
接上后 90 天才有 calibrate 真实数字（U2），v4 方向 C 才有证据基础。若你决定放弃问责卖点，
则改为把 K 系列整体降级删除——二选一，不能保持现状（造了不用最杂）。

### G2 死代码清簇删除（T-6 落地，~40 函数）
删：`BuildTwoStage`、`DetectRegime`+7 子判定、`pkg/forecast/backtest` 整包、
`BuildEquityVerdictEnhanced`+scoreEquity*、bundles 旧 API 三函数、assetprofile 4 函数、
`allocate.ComputeRiskMetrics`、`client/mock.go` 的死项、`~/.guanfu/futu_bridge.py`。
**同一 PR 内改 CLAUDE.md**（bundles 章改教 `ExtractorsForProfile`）防止漂移复发。
git 历史可捞；回归门保绿。

### G3 零使用功能降级（v4-thinking："不被用的降级为 internal-only"）
| 功能 | 建议 |
|---|---|
| intent ledger | **删**（0 条/55 天；"记录自己投资意图"已被 nimbus 侧 thesis-tracker/decision ledger 覆盖，职责重复）|
| portfolio 上下文（L 系列）| **删或冻结**——真实组合上下文在 nimbus（futu+IBKR 实盘），guanfu 再维护一份手写 JSON 是平行世界 |
| stress / joint / watch | **从 SKILL/tier 文档与 README 撤下**（缩小路由面），代码暂留一个版本周期，90 天期满仍零使用则删 |
| digest / dca / allocate / market | 留（dca/market 是 web 与日常路径的一部分）|

### G4 回测归一（3 → 2 套，明确分工）
留 ①engine/backtest（"forecast 准不准"，回归门）与 ②cmd/guanfu-backtest（"verdict 有没有
预测力"，方法论不同，README 至 §backtest-methodology 已文档化）；删 ③forecast/backtest（死）。
在 docs/backtest-methodology.md 顶部加一段"两套回测各自回答什么问题"，杜绝再长第四套。

### G5 repo 归置
1. **web/ 拆出去**：Next.js 站独立 repo（如 `btcdca-web`），guanfu 只留 `guanfu-web-export`
   产出 JSON 契约。理由：构建链/依赖面/发布节奏完全不同，混在 Go module 里是最大的一块"杂"。
2. `cache/`、`data/*.json|csv` 出库（.gitignore + 移 ~/.guanfu 或删，快照可再生）。
3. `cmd/guanfu-threshold-search`、ablation 移 `cmd/experiments/` 或删（threshold 结论已沉淀
   进 profile 就删）。
4. docs：v1-v3 archive 压成一个 `docs/archive/`（已基本如此，补 banner）；plans 只留活的。

### G6 文档与承诺对齐
README 功能表按"活/内测/已移除"重排；CLAUDE.md 核心抽象章更新；skill/tier1 撤零使用工具。

### 执行顺序与体量
G1（10 分钟）→ G2（半天，含测试）→ G4（1 小时）→ G3（半天）→ G5（半天-1 天，web 拆迁为主）
→ G6（1-2 小时）。合计 **2-3 天**，预计净删 ~4-6k LOC + 一个 Next.js 站出仓。

## 5. 明确不做

- **不合并三套 panel**（BTC 8 域/equity 6 域/stock dashboard）：资产本就不同形，强行参数化
  = 06-22 模块评估里否决过的那类"伪去重"。
- **不做 v4 方向 A/B**（MCP 编排深化/短线模块）：前置条件（U6 实测/真实短线需求）仍未满足。
- **不改 kNN/conformal/reliability 任何算法**：等 G1 接上后的 90 天 calibrate 数据说话。
- **不把 guanfu 并进 nimbus services**：它是独立产品（自有 web/小程序消费方），只共享 datasources。

## 6. 验收

| 指标 | 现状 | 目标 |
|---|---|---|
| deadcode ./... 报告 | 40+ 函数 | ≤ 5（且全部有"为何保留"注释）|
| 回测实现 | 3 套 | 2 套，分工文档化 |
| cmd/ 产品入口 vs 研发脚本 | 混放 6 个 | 4 产品 + experiments/ 隔离 |
| repo 内非代码资产 | web 整站 + cache/data 入库 | 出仓/出库 |
| claim ledger | 断档（23 条冻结）| 每日自动积累，90 天后首份真实 calibrate |
| CLAUDE.md 与代码 | bundles 章漂移 | 同步 |
| 回归门 | 绿 | 绿（TestBacktestBundles + `backtest all` 覆盖率 100% 不降）|

## 7. 需要你拍板的三个点

1. **K 系列去留**（G1）：接上问责闭环（推荐，改一行 cron）还是放弃卖点整体删？
2. **web/ 拆仓**（G5.1）：拆独立 repo，还是接受混装现状？
3. **portfolio/intent 删除**（G3）：确认这两块的职责归 nimbus 侧，guanfu 不再维护？
