# guanfu 用户画像

> guanfu 不是给所有人用的。把画像写死能让每个 Track 的优先级有锚,避免"做给所有人结果没人满意"。

---

## Primary — 核心用户(工具必须直接服务)

**典型画像**:有一定投资经验 + 愿意用 CLI/MCP + 5 年以上期限 + 10k-1M USD 级别可投资资产 + 偏被动配置的个人投资者。

**他们的真实场景**:
- 组合里已有 BTC / QQQ / Gold 类持仓,每隔 1-4 周复盘一次
- 不信单一评分,也不想自己看 40 个指标
- 习惯用 Claude / Cursor / Claude Code,有 FRED / Yahoo API key
- 做决定前喜欢看反证 + 失效条件,不喜欢"一定会涨"

**他们不要什么**:
- 不要交易指令("涨到 $120k 减仓")
- 不要总分("当前评分 72")
- 不要营销话术("BTC 必将突破 $500k")
- 不要黑盒("相信模型就行")

**功能映射**:

| 功能 | Primary 受益 |
|---|---|
| `portfolio.yaml` + portfolio-aware verdict | ★★★★★ 同一盘面不同仓位不同结论 |
| 基线对比 (vs 3m T-bill / 60-40) | ★★★★★ 避免用绝对收益数字欺骗自己 |
| Claim + Intent ledger | ★★★★☆ 自己的纪律回归比工具预测还重要 |
| `guanfu watch` + alerts | ★★★★☆ 不用每天盯盘 |
| 行为护栏 (冷静期 / 对立论证) | ★★★★☆ 防止行情触发的冲动决策 |
| 可靠性标注 + 诚实降级 | ★★★★☆ 知道哪些 horizon 不可信 |
| Conformal 区间 | ★★★☆☆ 比经验分位数更可信的风险估计 |
| 事件日历 | ★★★☆☆ FOMC / CPI 前夕降低 position 冒险 |

---

## Secondary — 重要用户(MCP 侧)

**典型画像**:AI 重度用户,主要通过 Claude Desktop / Cursor / Claude Code 的 MCP 接入 guanfu。CLI 是 fallback,不是主入口。

**他们的真实场景**:
- 在 Claude 里问"BTC 现在值不值得买"
- 期望 AI 自己选工具、自己组织答案,不是给一堆原始数字
- 对 context 占用敏感:SKILL.md 太胖会影响 Claude 同时用其他 skill
- 更在意输出**读起来顺**,不太在意 JSON schema 细节

**他们不要什么**:
- 不要 900 行 SKILL 一次性加载
- 不要 AI 每次都得读 3 个 resource 才能回答问题
- 不要自己手动组合多个 CLI 命令

**功能映射**:

| 功能 | Secondary 受益 |
|---|---|
| SKILL 分层加载 (tier1/2/3) | ★★★★★ 省 context = 给别的 skill 留空间 |
| MCP 工具别名 (`get_panel` 不带 `_btc_`) | ★★★★☆ AI 更容易理解多资产支持 |
| `guanfu://knowledge/skill.md` resource | ★★★★☆ AI 自取上下文,不用用户帖 skill |
| 叙事化情景推演 (J4) | ★★★★☆ AI 把 quantile 翻译成人话 |
| 投资者问答模板 (J2) | ★★★★☆ AI 知道怎么按用户期限分层答 |
| 通俗总结段 (J1) | ★★★★☆ AI 答复的第一段有模板 |
| CLI subcommand 完整 (`stock` / `import-stock` / `refresh`) | ★★★☆☆ MCP 侧少走曲线 |

---

## Tertiary — 触达用户(不直接服务)

**典型画像**:不懂 CLI / 不会装 MCP / 可能连 API key 都没听过的普通投资者。通过社交媒体 / 朋友介绍 / 二次创作的内容接触 guanfu 的结论。

**guanfu 不直接服务他们**,而是:
- 把数据和解读给到上游 skill / AI 对话 / 公众号作者
- 这些上游再把信息翻译成更大众的形式(海绵宝宝讲通胀 / 短视频 / 图解)
- guanfu 本身保持精简,不为了迁就 Tertiary 降低对 Primary 的信息密度

**他们间接受益的东西**:
- 诚实降级 — 上游引用 guanfu 结论时,原始的 reliability caveat 会一起传过去
- Claim ledger — 上游可以把"guanfu 3 月前说过什么"作为信誉背书
- 概率化建议 — 比"一定"/"必然"的话术对普通人更负责

**不为他们做的事**:
- 不做 Web UI
- 不做 Telegram Bot(如果要做,应该是另一个项目,guanfu 只提供 API)
- 不简化 SKILL.md 到失去专业性(分层加载是为了 Secondary 的 context 效率,不是为了让 Tertiary 能读)

---

## 设计优先级

**Secondary > Primary > Tertiary**

- Secondary 之所以排第一位,是因为 guanfu 的真实杠杆来自"AI 原生多资产数据源"这个生态位,不是"又一个 CLI"
- Primary 排第二,是 Secondary 体验的根基 — CLI 行为稳定,MCP 才稳
- Tertiary 不优化,通过 Skill 分发

**所有冲突按此顺序解决。** 例:如果让 MCP context 高效和让 CLI 裸跑 `guanfu` 更漂亮冲突 → 前者优先。

---

## 怎么用这份画像

- 新增 feature 前先问:"这优先服务谁?他们的真实使用场景是什么?"
- 删除 feature 前先问:"有没有画像在依赖它?"
- 写文档 / SKILL 时,默认目标读者是 Primary,但 Claude 读的时候会自动触达 Secondary
- 不确定画像的 feature 往往是过度设计 — 删掉或暂缓

---

## 变更记录

| 版本 | 日期 | 变更 |
|---|---|---|
| v1 | 2026-05-09 | 首版。Primary/Secondary/Tertiary 三层画像明确 + 设计优先级 Secondary > Primary > Tertiary |
