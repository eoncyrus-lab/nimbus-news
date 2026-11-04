# btc-guanfu skill (v3.1)

观复 — 多资产投资盘面 + 解读知识库的 Claude Code skill 包。

安装：

```bash
ln -s "$(pwd)/skill" ~/.claude/skills/btc-guanfu
```

内容：

| 文件 | 用途 |
|---|---|
| `SKILL.md` | 完整版 ~850 行：指标手册（定义 + 阈值 + 失效情形）、知识库指针、读盘工作流、图表代码 |
| `tier1.md` | **数据契约 + 可靠性表 + 关键阈值** — AI 每次读盘必载（~200 行）|
| `tier2.md` | 决策框架 + 域级方向规则 + 行为护栏 + 输出模板 — 做判断时读（~160 行） |
| `profiles/` | 资产画像：BTC / QQQ-SPY / Gold / 任意美股，各自定义读盘 lens 和 caveat |
| `contracts/` | 新资产接入合同：profile 必备项、adding asset checklist |
| `kb/` | 10 个因果推理文件（宏观传导 / 危机 playbook / 历史类比等）|

推荐加载顺序：`guanfu://skill/tier1`（必载）→ 对应 `profiles/*.md` → `guanfu://skill/tier2`（决策时）→ `SKILL.md` 对应节（追问细节时）。

当前数据源注意点：`stooq_putcall` 只是兼容旧 forecast bundle 的 key,默认来源为 CBOE 官方 no-key total put/call；Deribit DVOL/skew 也是 no-key；`cmc_market_context` 需要 `CMC_API_KEY` 且只作为 market reading context。

架构边界：新增资产前先看 [`docs/architecture/asset-profile-refactor.md`](../docs/architecture/asset-profile-refactor.md) 和 `contracts/adding_asset.md`。不要把 BTC 的 cycle/network/halving/AHR 语义套到其他资产。
