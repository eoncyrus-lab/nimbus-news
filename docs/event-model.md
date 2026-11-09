# 事件模型（Story / Thread / Decision / Edition）

2026-11-09 实现。目标是把“每条消息各自决定推不推”改成“同一事件只推一次，按影响分和预算决定即时/版次”。

## 1. 三层身份

| 层 | 作用 | 判定 |
|---|---|---|
| Story | 是否同一事件、是否重复推送 | canonical URL 精确匹配；或标题 token Jaccard ≥ 0.85；或数字签名交集 ≥2 且共享实体 ≥2（24h 窗口） |
| Thread | 同一主体每日即时配额 | 共享实体 + 类别，12h 窗口；只影响预算与版次排版，不合并 Story |
| Decision | 可解释车道路由 | instant / defer / archive / silent，带命中规则、分数和预算状态 |

`silent` 只表示“本次重复观测不再投递”，不会覆盖此前已产生的 instant/defer 判定，避免版次漏掉已推事件。

## 2. 打分

```
score = 类别基础分 + 源等级加分 + 首发 5 + 多源佐证（每源 +5，上限 +10）
        + 数据/市场类“超预期”措辞 +5
critical 命中时抬到至少 85，并绕过预算
```

| 类别 | 基础分 | 例子 |
|---|---:|---|
| central_bank | 90 | Fed/ECB/BOJ/PBOC、利率决议、QE/QT |
| data_surprise | 75 | CPI/PPI/PCE/非农/GDP/PMI |
| geopolitics | 70 | 战争、制裁、关税、停火、霍尔木兹 |
| systemic_company | 65 | 并购、破产、调查、退市 |
| market_regime | 70 | 熔断、暴跌、信用利差、流动性危机 |
| analysis | 35 | 策略、展望、研报 |
| domestic_politics | 20 | 选举、弹劾、党派 |
| unclassified | 40 | 未命中任何类别 |

源等级：wire/official +10，analysis +0。

## 3. 车道与预算

| 分数 | 车道 | 行为 |
|---|---|---|
| ≥ 75 | instant | 直推；扣当日预算 |
| 40–74 | defer | 进 `news_aggregate`，由时段简报带出 |
| < 40 | archive | 只留档，不推送 |

预算默认值：

- 软预算 6/日，硬上限 8/日；每类保留 2 条（软预算用完后，本类已用满 2 条才拦）。
- 静默时段 23:30–07:00：分数 <85 的即时转 defer；≥85 或 critical 可穿透。
- 同一 Thread 每天第一条即时；第二条需比第一条高 15 分，且最多 2 条。
- 已推事件再次出现：分数未高出 15 分则 silent；高出则可按新预算再推一次。

## 4. 模式

```yaml
events:
  mode: "shadow"   # off | shadow | active
```

| 模式 | 行为 |
|---|---|
| off | 不构造引擎，零开销 |
| shadow | 观察全部 typed-routing 消息，写 Story/Thread/Decision 与来源贡献；**不改变推送** |
| active | 判定接管 typed routing：instant 直推、defer 进聚合、archive/silent 丢弃 |

`active` 会改变线上推送。切换前建议先让 `shadow` 跑 1–2 周，用下面的 API 核对预算消耗和误判。

## 5. 配置

```yaml
events:
  mode: "shadow"
  location: "Asia/Shanghai"
  quiet_start: "23:30"
  quiet_end: "07:00"
  daily_budget: 6
  daily_hard_limit: 8
  class_reservation: 2
  instant_threshold: 75
  defer_threshold: 40
  quiet_bypass_score: 85
  story_window_hours: 24
  thread_window_hours: 12
  upgrade_delta: 15
  retention_days: 30
```

状态存 BoltDB 独立 bucket（`event_story` / `event_thread` / `event_decision` / `event_source_stat`），带过期时间；进程重启不会重复消耗预算。

## 6. 版次（Edition）

时段简报（`type: session`）可以加 `events` 成员：

```yaml
members:
  - label: "事件"
    type: "events"
    options:
      window_hours: 12
      limit: 6
      min_score: 40
```

- 渲染本周/本时段进入 instant/defer 的 Story：`⚡ [92] 央行 · 标题（源 +N）`。
- 只在 `mode=active` 时输出；shadow/off 返回空，不改变推送。
- `session-week` 每天 20:00 触发，`session: "week"` 内部只在周日产出，形成周度版次。
- instant 事件已经在即时推送里出现过，版次会再次聚合，方便回看。

## 7. API

```bash
GET /api/events/overview              # 今日预算、类别用量、lane 计数、Story/Thread 数
GET /api/events/decisions?date=...    # 某天判定（默认今天）
GET /api/events/contributions?days=7  # 按源贡献报表
```

`contributions` 字段：

| 字段 | 含义 |
|---|---|
| items / new_stories / duplicates | 条目数、新事件数、重复数 |
| redundancy_rate | duplicates / items，跨源冗余率 |
| signal_rate | (instant+defer) / items，有效信号率 |
| freshness_median_s | 抓取时间 - 发布时间的中位数 |
| latest_published | 该源最近发布时间 |

## 8. 验证与限制

- 身份归并基于标题与 URL，没有语义向量；同一事件的不同措辞可能漏并，靠多源佐证分和多源 URL 缓解。
- 实体表是手工维护的高价值主体（约 60 个别名），新主体需要补 `entityAliases`。
- 预算与阈值来自架构文档的先验值，尚未用真实流量校准；`active` 前先用 shadow 数据看 `decisions` 与 `contributions`。
- 周度版次复用每日调度器（只支持 HH:MM），非周日会空跑一次，不产生消息。
