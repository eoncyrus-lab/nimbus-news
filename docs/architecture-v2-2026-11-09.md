# nimbus-news 架构 v2（2026-11-09）

> 依据：`nimbus-news-sources-review-2026-11-09.md` + 全仓代码走查 + 10 个 feed 实抓。
> 本版取代 v1 的"两层架构"：不再按源分 L1/L2，改为**事件为中心**。
> 三个硬约束：**VPS 与 Mac 双实例并存**、**已识别缺陷全部修复**、**不接告警通道**。

---

## 0. 决策摘要

| 项 | v1（评估文档） | v2（本方案） |
|---|---|---|
| 运行形态 | 删 VPS，单机 Mac launchd | VPS + Mac 双实例，角色化：唯一 active，其余 standby/admin |
| 路由单位 | 源（per-source routing/配额） | 事件（Story），源只提供类别先验 |
| 即时控制 | bwe 无条件直推 + 其余关键词门禁 | 全局即时预算 6/日（硬 8）+ 影响分 + 静默时段 |
| 聚合控制 | 每源 digest 配额（先到先得） | 版次生成时按影响分择优，无 per-source 配额 |
| 去重 | 后到者丢弃 | 合并为 Story，保留多来源；Thread 只影响排版 |
| 定时简报 | 9 源合并成 2 条 | 2026-11-09 先按市场时段合并成 3 条（早/收盘/美盘）；后续再升级 Section 注册表 |
| 告警 | health 接企微 | 不推送；只在 `/api/status`、metrics、日志可见 |
| 预期量 | 18–48/日 | 4–8/日（即时 2–6 + 早晚报 2） |

---

## 1. 运行形态：VPS + Mac

### 1.1 事实

- `deploy/news-platform.service`、`deploy/launch-vps.sh`、`deploy/vps-build-deploy.sh` 构成 VPS systemd + 每 5 分钟 push 部署的现网路径。
- `deploy/com.news.platform.plist` 是损坏的遗留模板：硬编码 `/Users/x/...`，执行的 `bin/launch-platform.sh` 不在仓库，未设 `TZ`。
- Docker 路径不可用（只读配置挂载、只绑 loopback、镜像无 Python）。
- 存储是本地 Bolt，去重/配额/投递状态都在本机；**没有跨机共享状态**。

### 1.2 结论：不能双活

两台同时 active 会对同一 feed 各自摄入、各自去重、各自推送——同一事件推两遍，且两边的 digest 池互不可见。Bolt 也不适合放共享盘（单写锁 + 网络 FS 不可靠）。

采用**角色化**：

| 角色 | 行为 | 建议部署 |
|---|---|---|
| `active` | 唯一摄入、决策、投递、生成版次 | VPS（公网稳定） |
| `standby` | 进程常驻；不启动源、不投递、不生成版次；只提供 `/api/health` 与只读 API | Mac |
| `admin` | 只读 API + 手动 `FetchOnly`（只抓不发）；禁止 trigger/push | Mac（可与 standby 合并） |

- 配置新增 `runtime.role`（`active|standby|admin`）；两台共用一份 `config.yaml`，role 由 host overlay 或环境变量注入。
- 切换：改 role 重启；或 `POST /api/runtime/promote`（仅 standby 可用，需管理 token）。
- **不做**自动选主：没有共享存储，自动选主会引入脑裂；人工切换 + runbook 更可靠。

### 1.3 部署清理

- 保留并修正 VPS systemd；新增可用的 launchd 模板（路径变量化、设 `TZ=Asia/Shanghai`）。
- 删除 Docker 路径与失效 plist 引用，`readme.md` 只保留两条部署路径。
- 部署脚本的健康回滚只看**进程存活 + `/api/health` 的进程级字段**；digest 积压/过期不应触发回滚（现网会把正常新版本回滚）。
- 备份：VPS 每日 Bolt 快照保留 7 天；Mac 每日拉一份离线副本（只读，不参与运行）。

---

## 2. P0 可靠性修复（全部必修）

| # | 缺陷 | 证据 | 修复与验收 |
|---|---|---|---|
| R1 | 企业微信只判 HTTP 200，不解析 `errcode`；图片失败仍返回成功 | `internal/channel/wechat/wechat.go:92-94,283-304` | 解析 `errcode`，非 0 视为失败并进入渠道重试；图片失败至少标记 partial。验收：构造 errcode≠0 的响应，投递状态为 failed |
| R2 | digest 幂等键无生产实现，重试即重发 | `internal/channel/channel.go:43-47`、`internal/core/digest_delivery_worker.go:284-285` | 实现 `SendToIdempotent`：Bolt 中记录 `delivery:<id>:<sink>`（24h），发送前检查、发送后落账；明确残留窗口（发送成功但落账前崩溃仍会重发），文档写清 at-least-once |
| R3 | 跨车道准入不改 Lane：同日先入 digest 的记录会让 direct 准入"成功"但永远 claim 不到 | `internal/store/digest_routing.go:355-405,156`、`internal/core/routing.go:74-82` | 返回既有记录的真实 Lane；调用方按 Lane 处理，禁止"admit=true 但 claim=nil"的静默丢失。验收：先 digest 后 direct 的用例必须要么留在 digest，要么显式改道 |
| R4 | RSS 每轮最多 10 条，且在去重之前 | `internal/source/rss.go:466-468,600-601` | 先去重/过滤，再按 `published_at` 取前 N（N=50）。验收：构造 20 条新条目，第 11 条起不丢 |
| R5 | 简报死段：pre_market 的"今日日历"、closing 的"板块轮动"实际永远为空（脚本 CLI/JSON 契约不匹配，错误被 `_ =` 吞） | `internal/source/market_briefing.go:305-323,563` | v2 直接删除这些段落与对应脚本；不再"修契约" |
| R6 | 配置 API 对 filters/alert/health 返回 updated，但运行时不重建 | `internal/platformapp/app.go:131-146`、`internal/api/server.go:735-738` | 要么实现运行时重建，要么统一返回 `restart_required`。验收：改 filters 后 API 不得声称已生效 |
| R7 | GET 配置把密钥脱敏成 `***`，PUT 原样落盘；LLM 热切换会用 `***` 覆盖真 key | `internal/api/server.go:1450-1494`、`internal/config/fingerprint.go:142-147` | 写入时拒绝 `***`（全字段，不只 source options）；provider 热切换前校验非脱敏值 |
| R8 | 管理 API 无 token，仅靠 loopback + Origin | `internal/api/server.go:166-210` | 双实例/隧道场景下加 `NIMBUS_ADMIN_TOKEN`（env），管理路径必须携带；loopback 不再单独放行 |
| R9 | legacy direct 分发错误只打日志，不进补推 | `internal/core/news_engine.go:1024-1026` | 所有直推统一走 outbox（与事件模型合并后自然消失） |
| R10 | `mirror_channels` 只作用于 direct，digest/版次不镜像 | `internal/core/news_engine.go:987` | 镜像下沉到 Publication 投递层，对所有 kind 生效 |

> 不接告警：R1/R2/R3 的失败只写日志、metrics 与 `/api/status`，不推送任何消息。

---

## 3. 事件模型

```
RawItem(不可变) → Story(身份/去重/即时) → Thread(排版) → Decision(可解释)
                                                   ↓
                                          Edition(早/晚/周) → Publication → 渠道
```

| 实体 | 关键字段 |
|---|---|
| `RawItem` | source、source_item_id、published_at、fetched_at、title、body、url、media、content_hash |
| `Story` | story_id、canonical_url、title_core、entities、class、impact、first_seen、last_seen、item_ids、state |
| `Thread` | thread_id、story_ids（12h 窗口、同主体同类别） |
| `Decision` | story_id、lane、score、matched_rules、budget_state、decided_at |
| `Publication` | publication_id（kind+slot+date）、story_ids、rendered、created_at |
| `Delivery` | publication_id、channel、state、attempts、checkpoint |

**身份 vs 主题**：

- `story_id` 高精度，只用于"是否重复推送"：canonical URL 相同；或 `title_core` token Jaccard ≥0.85 且同 24h；或数字签名（如 `CPI +0.7%`）+ 2 个共有实体。
- `thread_id` 低精度，只用于版次排版：同主体 + 同类别 + 12h。误合并只影响排版，不影响推送去重。

---

## 4. 决策政策

| 类别 | 基础分 | 即时触发 |
|---|---|---|
| 央行 | 90 | FOMC、利率决议、QT/QE、紧急流动性、纪要政策信号 |
| 数据意外 | 75 + 幅度 | CPI/PCE/NFP/GDP/PMI 对预期偏离 ≥0.1pp 或 ≥10% |
| 系统性地缘 | 70 + 严重度 | 战争升级、制裁、关税/休战、霍尔木兹、台海 |
| 系统性公司 | 65 | 巨头并购/IPO/调查/破产/会计问题 |
| 市场体制 | 70 + 幅度 | 指数 ±3%、VIX ≥25 或 +20%、信用利差、干预 |
| 分析/背景 | 35 | Kobeissi、Markets Wrap |
| 国内政治 | 20 | 选举、党派、人事 |
| 未分类 | 40（默认 defer） | 进版次让人看到，不静默丢 |

加分：源等级（wire/官方 +10，分析 +0）；多源佐证（每个独立来源 +5，上限 +10）；首发 +5。

- ≥75 即时；40–74 defer；<40 archive。
- 即时预算 6/日（硬 8），分类预留：央行 2、数据 2、地缘 2、体制 2，未用回流。
- 静默时段 23:30–07:00，仅 ≥85 可穿透。
- 同一 thread 每日最多 1 条即时；重大升级需再 +15 分才允许第二条。
- 规则全部放 YAML；每条 Decision 记录命中规则，支持回放与调参。
- **LLM 不参与路由与选择**；只在版次生成时写 1 次叙事。

---

## 5. 版次

| 版次 | 时间 | 段落 |
|---|---|---|
| `value-morning` | 08:45 Asia/Shanghai，A 股工作日 | 隔夜体制（SPY/QQQ/VIX/金油/美元/AH 一行）→ 隔夜重大事件（4–6 行）→ 观复 stance → 今日 HIGH 事件 → 阅读清单 |
| `value-evening` | 21:15 Asia/Shanghai，美东工作日 | A 股收盘一行 → 美盘 quotes + VIX 期限 → FRED 核心（只报有变动）→ FedWatch → 今日重大事件 → 明日 HIGH + 发布日历 → 阅读清单 |
| `value-weekly` | 周日 20:00（可选） | 全量 FRED → 观复深读 → 一周 thread 回顾 |

Section 契约：`Render(ctx, window) (Block, error)`，独立超时、可 optional；失败只降级该段，文末标注"数据源异常"。每个版次最多 1 次 LLM。

**版次窗口**：morning 覆盖前一晚报 21:15 至 08:45（含美股时段）；evening 覆盖 08:45 至 21:15。即时推送过的 Story 仍可进入版次（"夜间推送 + 早晨复读"），这是当前系统缺失的能力。

---

## 6. 内容源处理（重点）

### 6.1 评估口径

不看"条目数"，看四个量：

1. **边际覆盖**：该源首发、或唯一覆盖的 Story 数。
2. **冗余率**：进入已有 Story 的比例（越高越可删）。
3. **信号率**：进入即时或版次的比例（archive 不算）。
4. **新鲜度**：`fetched_at - published_at` 的中位数，以及"最新条目年龄"。

每月用 `source_contribution` 报表重算一次，替代拍脑袋。

### 6.2 逐源处理卡

#### bwe-tradfi（保留，wire 骨干）

- 实测 ~6/日（20 条覆盖 78.8h，11-05 至 11-09）。
- 样本价值：伊朗核提议、S&P -4%、Silver Lake/PIF 收购 Unity、OpenAI 110 亿融资、Anthropic IPO。
- 噪声模式：Schumer 炒股禁令、Hegseth 弹劾、Rubio 人事、机构八卦。
- **格式坑**：标题是"中文段 + 英文段 + `🕒 HH:MM:SS · 🌐 eqn.news/tradfi`"；现有指纹会把时间/链接算进 token，跨源几乎不可能匹配。处理：剥掉 `🕒` 之后全部尾巴与行首 `*`，中文首段作 `title_core`，英文段只用于实体抽取。
- 处理：120s；类别由规则判定；score≥75 才即时，其余 defer。

#### forexlive-breaking（保留，数据/央行）

- 实测 ~10/日（25 条/61h；无 UA 会 403，仓库已带 Mozilla UA）。
- 价值：`China CPI +0.7% vs +0.8%`、`NFP beat`、`UMich 51.0 vs 48.0`。
- 噪声：`Equity close`、`Asia-Pacific market news`、`Americas wrap`、Trump 讲话。
- 处理：300s；标题匹配 `value vs expected` 计算 surprise；DATA/CENTRAL_BANK 即时；wrap/close 归 ANALYSIS，只 defer。

#### fed-press（保留，官方）

- 实测 0.4/日；`press_all.xml` 混执法/人事。
- 处理：900s；只放行 monetary/FOMC/rate decision/minutes/projections/testimony；其余 archive（保留可查，不推）。

#### fed-speeches（保留，低频权威）

- 实测 0.6/日；1800s；只 defer；标题+摘要入库，不抓正文。

#### bloomberg-markets（保留，版次素材）

- 实测 ~58/日，是当前最大的素材池。
- 价值：并购、关税、资金流、Asia/China 宏观。
- 噪声：枪击案、CEO 私生活、社会新闻、纯政治。
- 处理：600s；只 defer；版次按影响分取 top 3；非市场类 archive。URL 去 tracking 参数后做 canonical。

#### bloomberg-economics（保留）

- 实测 ~6/日，干净；600s；只 defer。

#### kobeissi-letter（保留，分析）

- 实测 ~16/日；Fed 资产负债表、M2、CapEx、CMBS、历史统计。
- **策略**：源级类别上限 `ANALYSIS`——标题即使带 `BREAKING` 也不即时；600s；每期阅读清单取 2–3 条。

#### eia-energy（可选，建议降级）

- 实测 0.4/日；Today in Energy 是博客不是事件；3600s；只进周报/版次。若一个月没人读，删除。

#### walter-bloomberg（删除）

- 实测 ~10/日；与 bwe 同一条快讯线；跨源重复高。删除后由 bwe + 事件合并覆盖。

#### bloomberg-politics（删除）

- 实测 ~32/日；政治口水为主。关税/制裁由 bwe 与 bloomberg-markets 覆盖。

#### earnings-calendar（删除）

- 免费档壳股噪音；连同 `scripts/earnings_calendar.py`、pre-market 的财报段、us-preview 的 Nasdaq 业绩段、`FINNHUB_API_KEY` 一并删除。

#### 旧 9 个定时源（合并为版次）

- `guanfu-score`、`pre-market-briefing`、`closing-briefing`、`us-preview`、`us-macro-report`、`earnings-calendar`、`econ-calendar`、`news-aggregate-morning/evening`。
- 保留的 provider：quotes、FRED、econ calendar、fedwatch、vix term、guanfu、story pool。
- 删除的段落：财报/IPO/解禁、情绪/两融/龙虎/打板、BlockBeats、板块轮动、盘中午间槽。
- 删除后 Python 只剩 guanfu（vendored Go）与可选的经济日历；`akshare/playwright/pywencai/pandas/longport` 全部移除。

### 6.3 去重与合并规则

1. canonical URL 相同 → 同一 Story。
2. `title_core` Jaccard ≥0.85 且同 24h → 同一 Story。
3. 数字签名相同（如 `CPI +0.7%`）+ 2 个共有实体 → 同一 Story。
4. 其余新 Story。
5. 跨类别不合并（DATA 与 ANALYSIS 永不合并）。
6. Thread 仅用于排版：同主体 + 同类别 + 12h；一个 thread 在版次里只占一行，附来源列表。
7. 合并发生在入库时，**后到者不丢弃**：补全来源、数字、正文，更新 `last_seen`。

### 6.4 价值投资口径评估（2026-11-09 执行）

口径：**只看宏观大事和非常重要的事件**；财报不进 news。四个量按 6.1 定义，实测数据来自 11-05 至 11-09 的抓取样本。

| 源 | 实测/日 | 边际价值 | 决策 | 理由 |
|---|---|---|---|---|
| bwe-tradfi | ~6 | 高 | 保留 | 唯一稳定的中文宏观快讯线；伊朗核提议、S&P -4%、Silver Lake/PIF 收购 Unity 均为首发 |
| forexlive-breaking | ~10 | 高 | 保留 | 数据 vs 预期、央行、地缘；直推预算收到 3/日 |
| fed-press | ~0.4 | 高 | 保留 | 官方，critical 词表直推；其余 archive |
| fed-speeches | ~0.6 | 中 | 保留 | 低频权威，只进聚合 |
| bloomberg-markets | ~58 | 中高 | 保留 | 版次素材池，按影响分取 top 3；不进即时 |
| bloomberg-economics | ~6 | 中 | 保留 | 干净宏观，进聚合 |
| kobeissi-letter | ~16 | 中 | 降级 | 分析/背景，不是事件；改为只进聚合（3/日上限） |
| walter-bloomberg | ~10 | 低 | **删除** | 与 bwe 同一条快讯线，跨源重复高 |
| bloomberg-politics | ~32 | 低 | **删除** | 政治口水为主；关税/制裁由 bwe + markets 覆盖 |
| eia-energy | ~0.4 | 低 | **删除** | Today in Energy 是博客不是事件，与油价交易无关 |
| earnings-calendar | 0–1 | 负 | **删除** | 免费档壳股噪音；用户明确财报不进 news |
| pre-market-briefing | 3 | 中 | 保留 1 档 | 09:15 盘前有隔夜信息；11:35/15:00 只是盘中指数涨跌，删除 |
| closing-briefing | 1 | 中 | 保留 | A 股收盘 + 港股，AH 覆盖主入口 |
| us-preview | 1 | 中 | 保留 | 删除 Nasdaq 业绩/除息/拆股段 |
| us-macro-report | 1 | 高 | 保留 | FRED/指数/流动性/FedWatch |
| econ-calendar | 0–1 | 中 | 保留 | 明日高重要性事件预告，宏观必需 |
| news-aggregate-morning | 1 | 高 | 保留 | 聚合池出口 |
| news-aggregate-evening | 1 | 高 | 保留 | 聚合池出口 |
| guanfu-score | 1 | 中 | 保留 | 低频日更，不占新闻预算 |

### 6.5 中国 / AH 覆盖缺口与新增源

现状：中文覆盖只有简报里的指数涨跌；**中国宏观数据、部委政策、港股监管没有任何中文新闻推送**——CPI/PPI 只能从 forexlive 的英文标题看到。

候选源实测（2026-11-09）：

| 候选 | 结果 | 结论 |
|---|---|---|
| 华尔街见闻 `hk-stock-channel` | 200，21 条/日，score 1/2/3 = 68%/20%/12% | **采用**：中国 CPI/PPI、交通运输部、港股监管都在，噪声低 |
| 华尔街见闻 `a-stock-channel` | 200，178 条/日，盘中个股异动为主 | 不接（需要时可加 channels） |
| 华尔街见闻 `global-channel` | 200，275 条/日 | 不接（全量，噪声最高） |
| 财联社 API | 404（签名接口） | 不可用 |
| 金十数据 flash API | 200，2h 40 条 → 宏观过滤后 17 条 | **采用**：`type: jin10`，中文一手宏观/政策/商品，重复度低于华尔街见闻 |
| 新浪财经 7x24 | 200，可用 | 备选：A 股个股快讯占比高，暂不接 |
| 央行 / 统计局 / gov.cn / 证监会 | 200（需跟随 301/302） | P1 备选：内容是权威原文，但比快讯慢，边际故事≈0 |
| 巨潮公告 / 港交所披露 | 200 | P1 备选：属于持仓公告监控，不是宏观新闻，不进 news |

新增 `wallstcn-hk`：`type: wallstcn`，180s，`min_score: 2`，直推预算 2/日 + critical 词表（央行/降准/LPR/CPI/PPI/PMI/证监会…），溢出进 `news_aggregate`。实测：11-09 15:11 近 6h 共 4 条、近 24h 共 6 条，内容为 CPI/PPI、交通运输部暂停对美船舶港务费、恒指开盘、伊朗谈判，全部在目标范围内。

高噪声频道需要时用 `include_keywords` 白名单接入（如 `a-stock-channel` + 证监会/国务院/回购/分红），默认不接。

### 6.6 目标源组合

| 源 | 节奏 | 类别先验 | 车道 | 估量/日 |
|---|---|---|---|---|
| bwe-tradfi | 120s | wire | 直推 ≤3 + critical，溢出 digest | ~6 |
| forexlive-breaking | 120s | 数据、央行 | 直推 ≤3 + critical，溢出 digest | ~10 |
| wallstcn-hk | 180s | 中国宏观、AH | 直推 ≤2 + critical，digest ≤6 | ~6（score≥2） |
| jin10 | 120s | 中国宏观、政策、商品 | 直推 ≤3 + critical，digest ≤6 | ~50（过滤后） |
| fed-press | 900s | 央行 | critical 直推 / archive | ~0.4 |
| fed-speeches | 900s | 央行/分析 | digest ≤2 | ~0.6 |
| bloomberg-markets | 300s | 公司、体制 | digest ≤3 | ~58（取 3） |
| bloomberg-economics | 300s | 宏观 | digest ≤2 | ~6（取 2） |
| kobeissi-letter | 300s | 分析 | 直推（不限量） | ~16 |

结果：8 个新闻源。kobeissi 带图（利率/资产负债表/ETF flow），2026-11-09 用户决定单独直推，不再埋进摘要。

### 6.7 当前在跑清单（12 个源 + 3 条时段简报）

以 `config.platform.yaml.example`（2026-11-09 版本）为准；真实 `config.platform.yaml` 未入库。

| # | 源 | 类型 | 触发 | 车道 | 实测/估量 |
|---|---|---|---|---|---|
| 1 | bwe-tradfi | rss | 120s | direct ≤3 / digest | ~6/日 |
| 2 | kobeissi-letter | rss | 300s | direct 不限 | ~16/日 |
| 3 | fed-speeches | rss | 900s | digest ≤2 | ~0.6/日 |
| 4 | fed-press | rss | 900s | critical direct，默认 silent | ~0.4/日 |
| 5 | bloomberg-markets | rss | 300s | digest ≤3 | ~58/日 |
| 6 | bloomberg-economics | rss | 300s | digest ≤2 | ~6/日 |
| 7 | forexlive-breaking | rss | 120s | direct ≤3 / digest | ~10/日 |
| 8 | jin10 | jin10 | 120s | direct ≤3 / digest ≤6 | ~50/日（宏观过滤后） |
| 9 | wallstcn-hk | wallstcn | 180s | direct ≤2 / digest ≤6 | ~6/日 |
| 10 | guanfu-score | guanfu | 10:00 | direct | 1/日 |
| 11 | session-morning | session | 09:20/09:40/11:35/15:05 | direct | 4/日 |
| 12 | session-close | session | 16:10 | direct | 1/日 |
| 13 | session-us | session | 21:00 | direct | 1/日 |

渠道：`wechat-main`、`discord-webhook`（3 个 webhook 轮询）；`mirror_channels: ["wechat-main"]`。

### 6.7.1 时段简报（2026-11-09 合并）

原来 8 个定时源各自成条、互相挨着弹（09:15 盘前 / 09:25 早报 / 16:10 收盘 / 19:30 晚报 / 21:00 日历 / 21:10 美盘 / 21:40 宏观），现按市场时间合并为 3 条，每条的成员由现有源类型组装（`type: session`，`options.members`）：

| 时段 | 时间 | 成员 | 内容 |
|---|---|---|---|
| session-morning | 09:20 / 09:40 / 11:35 / 15:05 | pre_market（`calendar: false`）+ econ_calendar + aggregate（`include_blockbeats: false`） | 隔夜/实时行情 + 今日数据 + 新增资讯摘要 + AI 相位研判；标题随竞价/开盘/午盘/收盘变化 |
| session-close | 16:10 | closing（`sections: [sentiment, hot_sector, capital_flow, lhb]`）+ events + hk_close + us_overnight + aggregate | 指数/涨跌家数/成交额/情绪 + 热门板块 + 两融/主力资金 + 龙虎榜 + **A/H 恐慌温度** + 事件版次 + 港股收盘/AH溢价 + 美股夜盘（恒指夜期/A50夜盘）+ 资讯摘要 |
| session-us | 美东 09:00（北京 21:00 夏 / 22:00 冬） | us_preview（`include_fedwatch: false`）+ us_macro_report + econ_calendar + aggregate | 美股行情/VIX 期限 + FRED/流动性/FedWatch/未来发布 + 明日数据 + 资讯摘要 + AI 前瞻 |
| session-us-close | 美东 20:05（北京 08:05 夏 / 09:05 冬） | us_close + aggregate | 收盘指数 + 利率/美元 + 板块轮动 + 盘后 ETF + 夜盘期货/汇率 + A股映射 + AI 复盘 |

- 单成员失败只降级该段，不影响整条；聚合成员租到的 `digest_lease_id` 会透传到合并消息，保证 ack 正常。
- 交易日判断：morning/close 用 A 股交易日；us 用美东星期（北京周一 21:00 = 美东周日，跳过）。
- 手动/测试可用 `force: true` 跳过判断。

### 6.7.2 A/H 恐慌温度（自算）

官方波指缺位：中国波指 2018 年起停发，恒指波指没有公开 K 线接口（实测 `^VHSI` 无适配器）。所以用 20 日已实现波动率自算：

- `RV20 = 日对数收益率标准差 × √252 × 100`（年化 %），数据来自上证指数 `000001.SS` 与恒指 `^HSI` 日线（`GetHistoricalQuotes`，30 根）。
- `恐慌分 = clamp((RV20 - 10) / 30 × 100, 0, 100)`；<25 平静、<50 正常、<75 紧张、≥75 恐慌。
- 输出示例（11-09 实测）：`A股 恐慌分 20/100（平静）| RV20 16.1% | 日涨跌 +0.32% | 距20日高 -3.7%`；`港股 恐慌分 39/100（正常）| RV20 21.6%`。
- 纯 Go 计算，不增加脚本；口径写死在 `internal/source/panic.go`。

### 6.8 已执行的删除（本轮）

代码与配置（均已验证零调用）：

| 对象 | 依据 |
|---|---|
| `internal/source/shared.go` | 7 个函数全部 0 外部调用 |
| `internal/runtime/`（整包） | 全仓 0 引用（进程 supervisor 遗留） |
| `InvestorConfig` / `PlatformConfig.Investor` | 0 消费者 |
| `scripts/treasury_press.py`、`scripts/bls_snapshot.py` | 0 引用 |
| `scripts/earnings_calendar.py` + `earnings-calendar` 源 | 用户要求财报不进 news |
| pre-market 财报段、AI prompt 财报输入 | 同上 |
| us-preview Nasdaq 日历（业绩/除息/拆股）整段 + `nasdaqCalendarClient` | 同上；`third_party/datasources/nasdaq` 不再被引用 |
| `config.yaml.archived`、`config/system.yaml`、`config/system.schema.json` | 无代码引用 |
| `walter-bloomberg`、`bloomberg-politics`、`eia-energy` 源配置 | 见 6.4 |
| `closing_scan_bootstrap.go`、`scripts/closing_scan.py`、`scripts/closing_scan_backfill.py`、`Options.BootstrapClosing` | 打板预测下线；启动不再触发 backfill |
| `pre-market-briefing`、`closing-briefing`、`us-preview`、`us-macro-report`、`econ-calendar`、`news-aggregate-*` 源配置 | 合并为 3 条时段简报（6.7.1），旧调度不再存在 |

仍待（依赖后续阶段）：

- `ai_filter.go` / `async_evaluator.go`：typed 源已短路，等事件模型上线后删。
- legacy `delivery_mode` / digest lease/ack 旧路径：等 Publication/Delivery 迁移。
- ~~`closing_scan_bootstrap.go` 与打板脚本~~ → 已删（打板预测下线）。
- `config.yaml.example`：Makefile/readme 仍引用，P4 与 Docker 路径一起删。

---

## 7. 配置 v2

一份 `config.yaml`：

```yaml
runtime: { role: active|standby|admin }
sources: [...]        # 8 个
policy:               # 分类词表、类别权重、即时预算、静默时段、版次选择上限
providers:            # quotes / fred / calendar / fedwatch / guanfu
editions:             # morning / evening / weekly + 段落清单
channels: [...]
observability: { metrics: true }   # 无 notify_channels
```

删除：`config.yaml.example`、`config.yaml.archived`、`config/system.yaml`、legacy `delivery_mode/briefing_target`、`filters.ai_filter`、`filters.ratelimit` 的源级特例。

---

## 8. 迁移路线

| 阶段 | 内容 | 工作量 |
|---|---|---|
| P0 | R1–R10 修复；删 3 源；forexlive 收紧；kobeissi 进 defer；删除死段与财报链 | 2–3 天 |
| P1 | Story/Thread 持久化 + 规则打分，影子模式（只写 Decision，不改变推送） | 2–3 天 |
| P2 | 事件级决策上线：即时预算、静默时段、跨源合并；旧 digest 保留 | 2–3 天 |
| P3 | 版次改造：Section 注册表、早/晚/周；退 9 个定时源；删 Python 链 | 3–5 天 |
| P4 | 配置 v2、双实例角色、部署清理、备份、贡献报表 | 2 天 |

验证：

- P0：`go test ./...`；逐源 fetch；构造 errcode≠0、跨车道、RSS 超额三类回归。
- P1：用 NewsStore 最近 7 天回放，人工抽检 50 对合并/拆分。
- P2：连续 3 天对比推送量与误判。
- P3：版次固定 fixture 的 golden 输出；DST（America/New_York）；provider 故障注入。
- P4：Mac standby 接管演练（promote → 收一条 → 降回）。

---

## 9. 指标（不推送，只在 `/api/status` 与 `/metrics`）

`stories_created{class}`、`story_merge_total`、`route_decisions{source,class,lane,reason}`、`instant_budget_used`、`edition_section_failures{section}`、`source_staleness_seconds{source}`、`source_dup_ratio{source}`、`publication_delivery_total{channel,state}`、`wechat_errcode_total{code}`。

---

## 10. 开放问题

1. ~~中国政策/监管一手源仍缺~~ → 已由 `wallstcn-hk` 覆盖（6.5）。央行/统计局/证监会原文属于 P1 可选，边际故事≈0；巨潮/港交所披露属于持仓监控，另做产品。
2. eia-energy 是否保留，按一个月阅读率决定。
3. 双实例的通道凭据：两台是否共用同一 webhook（推荐共用，切换时不会换群）。
4. 版次是否保留观复日更：目前按保留早报一段设计。

---

## 11. 实施状态（2026-11-09）

### 已完成

| 项 | 结果 |
|---|---|
| R1 | 企微解析 `errcode`，非 0 视为失败；图片失败只记日志 |
| R2 | `SendToIdempotent` 落 Bolt（`channel_idempotency`，7 天），`Claim` 原子占位，失败归还键 |
| R3 | 跨车道准入按既有记录真实 Lane 处理，不再"admit=true 但 claim=nil" |
| R4 | RSS 先去重/过滤再按 `published_at` 取前 50（原为入口 10 条截断） |
| R5 | 删除盘前 IPO/解禁日历段与 closing 死段脚本；财报链已移除 |
| R6 | filters/alert/health 写入返回 `restart_required`；整配置更新按段落列出需重启项 |
| R7 | 所有配置写入拒绝 `***`；LLM 热切换拒绝脱敏占位符 |
| R8 | `NIMBUS_ADMIN_TOKEN`：管理路径需 Bearer/X-Admin-Token；本机可信 Origin 浏览器 UI 除外 |
| R9 | 直推失败按渠道入补推队列（`BroadcastDetailed` 只报失败渠道） |
| R10 | `mirror_channels` 同时作用于定时简报投递 |
| 分段推送 | 时段简报按 `max_chars`（默认 1800）切分，标题带 `(i/N)`，digest 租约挂在段末 |
| 恐慌温度 | A 股/港股 RV20 恐慌分（`internal/source/panic.go`），收盘简报输出 |
| 双实例角色 | `runtime.role` / `NIMBUS_RUNTIME_ROLE`：active 全功能，standby 只读，admin 只读 + FetchOnly；`POST /api/runtime/promote` 人工提升 |
| 部署清理 | 删除 Docker 文件、`config.yaml.example`、坏 plist；新增 `launch-mac.sh`、plist 模板、`install-launchd.sh` |
| 探活/回滚 | 新增 `/api/health/live`；部署回滚与外部探活只看进程级存活 |
| 备份 | `POST /api/store/snapshot` 进程内快照；VPS cron 与 `mac-backup.sh` 改用快照，保留 7 份 |
| 死代码 | 删除 `internal/scheduler`、BlockBeats/IPO/解禁脚本、过期文档 |
| 逐源格式核对 | `cmd/source-dump` 逐源拉取；输出格式见 `docs/source-output-format.md` |
| 成员降级 | session 成员构造失败（如缺 FRED key）不再拖垮整条简报，只在文末标注异常 |
| 脚本超时 | `scripts/utils.py` 给 requests 注入默认 (5s/15s) 超时，避免上游挂起拖死收盘段 |
| 输出容错 | 脚本 stdout 带 banner 时按括号配对截取 JSON，NaN/Infinity 归一化；wallstcn 标签去重；Bloomberg/ForexLive 署名修正 |
| 收盘段修复 | 全市场快照进程内复用（59 页只翻一次）、脚本网络超时、sentiment 超时 3 分钟；炸板率/成交额单位修正（此前 4490%、0 亿） |
| 快讯可读性 | forexlive 长文保留 `<li>/<p>` 边界并按句切分为 `- ` 分点（11017 字符 → 40+ 条）；短快讯不变 |
| 中文源补充 | 新增 `jin10`（金十数据公开 JSON API）：important/宏观关键词过滤，个股公告噪音源内丢弃，实测 2h 40 条 → 17 条 |
| 盘中多槽 | session-morning 改为 09:20/09:40/11:35/15:05 四槽；`preMarketSession` 细分竞价/开盘/盘中/午盘/午后/尾盘/盘后；标题随相位变化，每槽重新租 digest |
| P1 影子 | `internal/event`：Story 身份（URL / Jaccard / 数字+实体）、Thread、可解释 Decision；shadow 观察全部 typed-routing 消息，不改推送 |
| P2 车道接管 | `events.mode=active` 时 instant→直推、defer→`news_aggregate`、archive/silent 丢弃；预算 6/8、类别保留 2、静默 23:30–07:00、Thread 每日 1 条；未校准前默认 shadow |
| P3 版次 | session 支持 `events` 成员（读事件引擎渲染 instant/defer）；新增 `session-week` 周日 20:00 周度版次；instant 事件可在版次中回看 |
| source_contribution | 按日按源统计 items/new/duplicates/instant/defer/archive/freshness；API `/api/events/contributions` 输出冗余率、信号率、新鲜度中位数 |

### 切换前校准（实现已完成，未改线上模式）

- 模板默认 `events.mode: "shadow"`；`active` 会改变推送，切之前用 `/api/events/decisions`
  与 `/api/events/contributions` 跑 1–2 周，核对预算消耗、跨源归并和误判。
- 身份归并目前是 URL + 标题 token + 数字/实体，没有语义向量；实体表为手工维护。
- 预算（6/8）、阈值（75/40）、静默穿透（85）来自先验值，尚未按真实流量校准。
- 设计与指标口径见 [event-model.md](event-model.md)。
