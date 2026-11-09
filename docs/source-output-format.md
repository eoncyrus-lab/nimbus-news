# 数据源输出格式（2026-11-09 实测）

本文记录当前全部启用源各拉取一次的真实输出：状态、条数、耗时、消息结构、
正文形态与元数据。样本取自 `cmd/source-dump`，已截断长正文，保留原始格式。

## 1. 采集方式

```bash
# 1) 生成 dump 配置（从 config.platform.yaml.example 派生，勿手改）
make dump-config

# 2) 先把 venv 前置到 PATH（脚本源用 python3 调 akshare）
export PATH="$PWD/.venv/bin:$PATH"
go run ./cmd/source-dump -config config.dump.yaml \
  -out /tmp/nimbus-dump/out -timeout 240s

# 单源
... -name bwe-tradfi
```

- `config.dump.yaml` 由 `make dump-config` 生成（已 gitignore）：session 源加
  `force: true` 并去掉 `schedule`/`sinks`，追加 `brief-*` 源把时段成员拆开逐段核对。
  模板改了必须重新生成，否则会拿旧参数跑出假结果（金十评分参数就这样漂移过一次）。
- `max_age_hours` 沿用模板值；低频源（Fed 等）窗口内没有新条目时输出 0 条，不是故障。
- 必须用 `.venv/bin` 前置 PATH：脚本源调用 `python3`，系统 Python 没有 akshare/numpy。
- 结果信封：

```json
{
  "name": "bwe-tradfi",
  "type": "rss",
  "status": "ok",              // ok | fetch_error | timeout | create_error
  "error": "...",             // 仅失败时
  "elapsed_ms": 3690,
  "count": 20,
  "messages": [ ... ]
}
```

- `status=ok && count=0` 表示拉取成功但窗口内没有新条目，不是故障。

### 本次实测汇总

| 源 | 类型 | 状态 | 条数 | 耗时 | 说明 |
|---|---|---|---:|---:|---|
| bwe-tradfi | rss | ok | 20 | 3.7s | 20 条均带图 |
| kobeissi-letter | rss | ok | 20 | 4.4s | 16 条带图 |
| fed-speeches | rss | ok | 0 | 1.4s | feed 最新条目 10-15，超出窗口 |
| fed-press | rss | ok | 0 | 1.8s | 同上，低频官方源 |
| bloomberg-markets | rss | ok | 17 | 2.4s | 纯英文摘要 |
| bloomberg-economics | rss | ok | 8 | 1.6s | 纯英文摘要 |
| forexlive-breaking | rss | ok | 25 | 1.4s | 长文已按句/列表切成分点 |
| jin10 | jin10 | ok | 5 | 0.2s | 评分过滤：实体+事件+数据发布；非中美欧日央行降权 |
| wallstcn-hk | wallstcn | ok | 7 | 0.7s | 自带评分 |
| guanfu-score | guanfu | ok | 1 | 36.4s | 50 币种历史，最慢 |
| session-morning | session | ok | 1 | 7.5s | 行情 + 日历 |
| session-close | session | ok | 1 | 73s | 4 段脚本 + 恐慌温度 |
| session-us | session | ok | 1 | 4.1s | 缺 FRED key 时降级 |

## 2. 统一消息契约

`messages[]` 是 `model.Message` 的裁剪视图：

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | 源内稳定 ID；RSS 为 GUID→Link→Title+时间的哈希 |
| `type` | string | `news` / `analysis` |
| `title` | string | 推送标题；RSS 直接使用条目标题 |
| `content` | string | 正文（微信优先用这个） |
| `content_runes` | int | 原始正文的字符数（截断前） |
| `content_truncated` | bool | dump 是否截断（默认 4000 字符） |
| `short_content` | string | 摘要版，微信/短渠道优先 |
| `link` / `image_url` / `video_url` | string | 媒体字段 |
| `source` / `source_type` | string | 配置名 / 源类型 |
| `tags` | []string | 频道标签，用于路由与展示 |
| `metadata` | object | 源特有字段，见各源 |
| `create_time` | RFC3339 | 发布时间（RSS 为 pubDate） |
| `fetch_time` | RFC3339 | 本次拉取时间 |

所有 RSS 都会写 `metadata.display_source`（推送署名）；Bloomberg、ForexLive、
Fed 等按 URL 映射，未知域名回落 `RSS`。

## 3. RSS 类

### 3.1 bwe-tradfi

```json
{
  "id": "27fb98b43ba395dc",
  "type": "news",
  "title": "*特朗普谈英伟达CEO黄仁勋：我尚未与他谈及相关指控 *TRUMP ON NVIDIA CEO HUANG: I HAVE NOT SPOKEN TO HIM ABOUT ALLEGATIONS $NVDA 🕒 07:24:52 · 🌐 eqn.news/tradfi/12902",
  "content": "同上（标题即正文）",
  "content_runes": 140,
  "link": "https://t.me/BWEtradfi/7740",
  "image_url": "https://cdn5.telesco.pe/file/....jpg",
  "source": "bwe-tradfi",
  "source_type": "rss",
  "tags": ["机构", "对冲基金", "TradFi"],
  "metadata": {"display_source": "BWE TradFi"},
  "create_time": "2026-11-08T23:24:55Z"
}
```

格式要点：

- 标题 = 中文段 + 英文段 + `🕒 HH:MM:SS · 🌐 eqn.news/tradfi/<id>`。
- 正文与标题相同；部分条目中文段为空，只剩英文。
- 每条都带 Telegram CDN 图片；链接指向 `t.me/BWEtradfi/<msg_id>`。
- 时间尾巴会污染标题指纹，做跨源合并时应剥掉 `🕒` 之后内容与行首 `*`。

### 3.2 kobeissi-letter

```json
{
  "id": "4f663126111d8804",
  "title": "BREAKING: 221 US trucking companies have filed for bankruptcy so far in 2026, ...",
  "content": "完整英文长文（约 770 字符），结尾带 ( @TheKobeissiLetter )",
  "content_runes": 777,
  "link": "https://t.me/thekobeissiletter/8595",
  "image_url": "https://cdn4.telesco.pe/file/....jpg",
  "tags": ["宏观", "市场", "分析"],
  "metadata": {"display_source": "The Kobeissi Letter"},
  "create_time": "2026-11-09T03:41:06Z"
}
```

- 纯英文；标题是正文首句截断（`...`），正文完整。
- 图片是信息图，20 条里 16 条带图，适合单独推送。

### 3.3 fed-speeches / fed-press

本次均为 0 条：feed 最新条目为 2026-10-15，超出 dump 的 240 小时窗口。
命中时结构为：

```json
{
  "title": "Bowman, Supervisory Modernization After Silicon Valley Bank",
  "content": "Speech At the Bangkok Economic Festival, ...",
  "link": "https://www.federalreserve.gov/newsevents/speech/bowman20261014a.htm",
  "metadata": {"display_source": "Federal Reserve"},
  "create_time": "2026-10-15T02:55:00Z"
}
```

- 标题 `官员, 主题`；正文只有场合/地点，没有讲稿全文。
- 低频源，正常运行时多数轮次就是 0 条。

### 3.4 bloomberg-markets / bloomberg-economics

```json
{
  "id": "5a2f252abcbd4d0f",
  "title": "ICE Joins Wind Derivatives Push as Renewables Reshape Power",
  "content": "Intercontinental Exchange Inc. is launching new wind derivatives, ...",
  "content_runes": 225,
  "link": "https://www.bloomberg.com/news/articles/2026-11-09/ice-joins-wind-derivatives-push-as-renewables-reshape-power",
  "tags": ["市场", "并购", "Bloomberg"],
  "metadata": {"display_source": "Bloomberg 市场"},
  "create_time": "2026-11-09T08:00:00Z"
}
```

- 标题 + 1–2 句摘要，无图；正文不抓取原文。
- economics 的 tags 为 `["宏观","经济","Bloomberg"]`，display 为 `Bloomberg 经济`。
- 发布时间是整点/半点，时间精度低于通讯社快讯。

### 3.5 forexlive-breaking

```json
{
  "id": "6b4fb069aaf5f5cc",
  "title": "investingLive Asia-Pacific market news: Asia shrugs off Nvidia scandal, oil steadies",
  "content": "Qatar says US-Iran talks alive ... (多条要点拼接，最长 10858 字符)",
  "content_runes": 10858,
  "link": "https://investinglive.com/news/investinglive-asia-pacific-market-news-...",
  "tags": ["外汇", "宏观", "快讯"],
  "metadata": {"display_source": "ForexLive"}
}
```

- 两种形态：单条快讯（1–3 句）与“每日要闻汇总”（几十条要点拼接，可超 1 万字符）。
- 配置 `format: "bullets"` 后，正文长度 ≥ `bullet_min_runes`（默认 400）时：
  先保留 `<li>`/`<p>` 边界，再按中英文句末标点切分，输出 `- ` 分点；
  单条短快讯保持原文。实测 11017 字符的亚太汇总切成 40+ 条要点。

## 4. 专用型

### 4.1 wallstcn-hk（华尔街见闻）

```json
{
  "id": "b05e365ed03309d7",
  "type": "news",
  "title": "中国10月PPI同比 5.7%，前值 5%。",
  "content": "中国10月PPI同比 5.7%，前值 5%。",
  "content_runes": 22,
  "link": "https://wallstreetcn.com/livenews/3196575",
  "tags": ["中国", "港股", "宏观"],
  "metadata": {
    "display_source": "华尔街见闻",
    "wscn_channel": "hk-stock-channel",
    "wscn_score": 3
  },
  "create_time": "2026-11-09T09:30:13+08:00"
}
```

- 快讯体：标题=正文，常见 20–70 字符。
- `wscn_score` 是上游重要度（本次 2–3）；`wscn_channel` 记录抓取频道。
- 标签已按配置去重（旧版会输出两遍 `中国/港股/宏观`）。

### 4.2 guanfu-score（观复）

```json
{
  "id": "guanfu_2026-11-09_3",
  "type": "analysis",
  "title": "🔭 观复读盘 · 偏积累倾向 | BTC $73823",
  "content": "当前读盘：偏积累倾向。市场情绪偏悲观。\n主要支撑：valuation: ahr999_compressed=0.5125；...\n\n── 数据摘要 ──\nBTC $73823 | 恐贪 29 | 信号 +3/8 | 置信 中 | MVRV-Z 2.24 | AHR999 0.51\n⚠️ 失效: Pi Cycle 触发 (≥1.0) → 顶部信号，立即重新评估",
  "content_runes": 305,
  "short_content": "当前读盘：...（去掉数据摘要段）",
  "tags": ["crypto", "btc", "guanfu", "观复"],
  "metadata": {
    "ai_interpreted": false,
    "bottom_proximity": 0.1875,
    "btc_price": 73822.67,
    "confidence": "中",
    "coverage": 0.875,
    "fear_greed": 29,
    "net_direction": 3,
    "regime": "偏多过渡",
    "stance": "偏积累倾向",
    "top_proximity": 0
  }
}
```

- 每次拉取遍历 50 个币种历史，耗时约 36 秒；单条输出。
- `metadata` 是结构化读数，正文是给人看的摘要；`ai_interpreted=false` 表示规则生成。
- 无 `display_source`，推送标题直接用 `title`。

### 4.3 jin10（金十数据）

```json
{
  "id": "a3f1…",
  "type": "news",
  "title": "央行开展5000亿元逆回购操作",
  "content": "金十数据11月9日讯，中标利率1.50%。",
  "content_runes": 28,
  "link": "https://flash.jin10.com/detail/20261109163834903800",
  "tags": ["中国", "宏观", "金十"],
  "metadata": {"display_source": "金十数据", "important": 1, "jin10_score": 70, "jin10_reasons": "核心央行:央行操作,利率/货币操作:逆回购,important"},
  "create_time": "2026-11-09T16:38:34+08:00"
}
```

- 走公开 JSON API，每次约 20 条，按 `max_time` 翻页；默认 3 小时内最多 40 条。
- 过滤改为评分制（`score_threshold`，默认 45）：实体（中美欧日央行/中国政策机构/巨头）
  + 事件（利率操作/数据发布/政策动作/地缘升级/市场异动）+ `important`，再对边缘市场、
  人事、预告、机构观点、官方表态降权。低于阈值不进路由。
- 只认中美欧日（+英国）央行；“埃及/墨西哥/俄罗斯/土耳其”等央行按边缘市场 -40，
  基本不会过阈值。
- 促销/导流（点击查看、一文看懂、VIP、金十整理…）直接丢弃；`section-news` 多条目合集
  会拆成单条分别打分。
- 相同正文在 `dedupe_hours`（默认 2h）内只发一次；金十同内容会换 ID 重发。
- 个股公告类（公告/减持/解禁/业绩预告…）不进实时推送；商务部/交易所公告不算噪音，
  交给评分判断。
- 标题优先取 `【】` 内的内容，没有则取第一句（≤60 字符）。
- 公告噪音在源内按天攒着，每天 `noise_summary_time`（默认 08:30，30 分钟窗口）汇总成一条
  `type=analysis` 的消息：标题 `📋 金十公告汇总 · 近24小时（N 条）`，正文按类计数 + 列出前
  `noise_summary_limit` 条。它不进事件引擎，也不占实时判定；`noise_daily_summary: false` 可关。
- 评分原因写在 `metadata.jin10_reasons`，便于核对某条为什么被推/被丢。

## 5. 时段简报（session）

一个时段只输出一条（超长时按 `max_chars`，默认 1800 分多条）。
ID 形如 `session_<session>_<YYYYMMDD>_<HHMM>_<i>`，标题 `<title> · MM-DD`，
多段时追加 `（i/N）`；`display_source` 为时段标题。

```json
{
  "id": "session_morning_20261109_1608_1",
  "type": "analysis",
  "title": "🌅 盘前全景 · 11-09",
  "content": "── 实时行情 ──\n🟢 State Street: 735.29 (+0.31%)\n...\n\n📊 金银比: 78.6 (正常 70-85)\n  USDCNY: 6.6800\n...\n\n── 🌏 经济事件 11-10 ──\n🇺🇸 美国 — 共 4 条\n  21:30 | Consumer Price Index (MoM) | ⭐⭐⭐",
  "tags": ["简报", "morning"],
  "metadata": {"display_source": "🌅 盘前全景"}
}
```

### 5.1 morning（09:20 / 09:40 / 11:35 / 15:05）

| 成员 | 段落 | 数据 |
|---|---|---|
| 隔夜与盘前（market-briefing/pre_market） | `── 实时行情 ──`、`金银比`、汇率、`隔夜异动触发` | Go 行情适配器 |
| 今日数据（script） | `── 🌏 经济事件 MM-DD ──` | `scripts/econ_calendar.py` |
| 资讯摘要（market-briefing/aggregate） | `## 资讯摘要` | digest 租约桶 |

实测输出：行情 12 个标的 + 金银比 + 3 个汇率 + 美国 4 条/英国 1 条经济事件。
dump 没有 digest store，所以“资讯摘要”段为空；线上会带该段和 digest 租约。

盘中相位由 `preMarketSession()` 按北京时间判定：集合竞价（09:15–09:30）、开盘
（09:30–10:00）、盘中（10:00–11:30）、午盘（11:30–13:00）、午后开盘（13:00–14:00）、
尾盘（14:00–15:00）、收盘（15:00–15:30）、盘后（15:30 后）。`title_from_member: true`
让合并消息标题跟随相位（⏰ 集合竞价 / 🔔 开盘 / 🌆 午盘 / 🌇 收盘行情），
避免四个槽都叫“盘前全景”。每个槽都会重新租 digest，只带新到资讯。

### 5.2 close（16:10）

| 成员 | 段落 | 数据 |
|---|---|---|
| 收盘行情（market-briefing/closing） | `── 市场总览 ──`、`── 板块轮动 ──`、`── 资金面 ──`、`── 龙虎榜 ──`、`── A/H 恐慌温度 ──` | 4 个 Python 脚本 + Go 自算恐慌分 |
| 资讯摘要（market-briefing/aggregate） | `## 资讯摘要` | digest 租约桶 |

实测正文（本次 922 字符，单条推送）：

```text
📊 A股收盘 · 11-09
── 市场总览 ──
指数: 上证指数 +0.32% / 科创50 +0.30%
涨跌比: 2242 / 3148 | 涨停 54 / 跌停 1 | 炸板率 45%
成交额: 14946亿 (+0.0%)
情绪: 中性 (60.0)

── 板块轮动 ──
🔥 东方财富热股 (+2.59%)
   · [002086] 东方海洋 +10.08%
   · [603366] 日出东方 +10.01%
...（最多 5 个板块，每板块 2 只龙头）

── 资金面 ──
两融余额: 18766亿 (-0.63%)

── 龙虎榜 ──
• [002639] 雪人集团 +10.00% 净+2.7亿 · 日涨幅偏离值达到7%的前5只证券
...（最多 5 只，可含同一只的多条上榜原因）
机构买入: 凡拓数创, 皇台酒业, 东方海洋

── A/H 恐慌温度 ──
A股 恐慌分 20/100（平静）| RV20 16.1% | 日涨跌 +0.32% | 距20日高 -3.7%
港股 恐慌分 39/100（正常）| RV20 21.6% | 日涨跌 -0.13% | 距20日高 -1.4%
```

失败段不阻塞整条简报，末尾追加：

```
⚠️ 数据源异常：sentiment, hot_sector
```

### 5.3 us（21:00）

| 成员 | 段落 | 数据 |
|---|---|---|
| 美盘行情（market-briefing/us_preview） | `── 关键标的 ──`、`── VIX ──`、`── 利率路径 ──` | Go 行情；FedWatch 可选 |
| 美盘收盘（market-briefing/us_close） | `── 收盘指数 ──`、`── 利率与美元 ──`、`── 板块轮动 ──`（含板块宽度/等权vs市值）、`── 盘后/盘前 ──`、`── 夜盘期货与汇率 ──`、`── 中国与半导体 ──`、`── 今日盘后财报 ──`、`── 对 A 股映射 ──`（含产业链个股） | 美东 20:05；盘后用 Nasdaq ETF 报价；财报用 Nasdaq 日历；均为非官方接口，失败静默降级 |
| 港股收盘（market-briefing/hk_close） | `── 港股收盘 ──` + AH 溢价指数 | 新浪公开行情；恒指/国企/恒生科技 |
| 美股夜盘（market-briefing/us_overnight） | `── 夜盘期货与汇率 ──`、`── ETF 夜盘/盘前 ──`、`── 对今日 A 股映射 ──` | 作 session-close 成员，北京时间 16:10 左右 |
| 美国宏观（us-macro-report） | FRED 核心指标/流动性 | 需要 `FRED_API_KEY` |
| 明日数据（script） | `── 🌏 经济事件 MM-DD ──` | `scripts/econ_calendar.py` |
| 资讯摘要（market-briefing/aggregate） | `## 资讯摘要` | digest 租约桶 |

缺 FRED key 时不再让整条 session 构造失败：该段降级，消息末尾标注
`⚠️ 数据源异常：美国宏观`，其余段落照常推送。

## 6. 格式层面的已知限制

1. **RSS 摘要不抓原文**：Bloomberg/央行等只有标题 + 摘要；需要全文的源用 `fetch_linked_body`。
2. **bwe 标题含时间与短链**：跨源指纹必须先剥离 `🕒` 尾巴，否则同一事件无法合并。
3. **forexlive 汇总帖超长**：单条可达 1 万字符，推送前必须摘要/分段。
4. **fed 两个源多数轮次为 0 条**：低频官方源，不能按“条数少”判为健康异常。
5. **时段简报依赖 Python 环境**：`python3` 必须是 `.venv` 里的解释器，否则
   akshare/numpy 缺失，收盘段全部降级。
6. **脚本 stdout 可能带 banner**：baostock 会打印 `login/logout success!`；
   Go 侧已做“严格解析失败→按括号配对截取 JSON”兜底，并把 Python 的
   `NaN/Infinity` 归一化为 `null`。
7. **脚本网络无超时会造成整段挂死**：已在 `scripts/utils.py` 注入默认
   `(5s connect, 15s read)` 超时，脚本自身的显式 timeout 不受影响。
8. **收盘脚本并发会抢上游带宽**：sentiment 的全市场快照已改成进程内复用
   （原先涨跌家数与成交额各翻 59 页），单次约 35–70 秒；超时放宽到 3 分钟。
9. **dump 的 `content_truncated=true`** 只是展示截断，不改变源输出。
