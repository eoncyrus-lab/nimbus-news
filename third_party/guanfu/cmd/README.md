# cmd/ — 二进制入口导览

guanfu 的命令行工具分 5 个入口,按角色分为:

## 产品二进制(用户直接使用)

| 目录 | 用途 | 调用方式 |
|---|---|---|
| `guanfu/` | 主 CLI + MCP 可调的多资产盘面 | `guanfu [btc\|qqq\|spy\|gold\|stock TICKER] [--verdict\|--forecast\|--full]`;子命令 `refresh` / `market` / `dca` / `allocate` / `intent` / `watch` / `digest` / `calibrate` / `backtest` / `backtest all --ablate-putcall` / `status --frank` |
| `guanfu-mcp/` | MCP stdio server(Claude Desktop / Cursor / Claude Code) | 配置 `mcpServers.guanfu.command = /path/to/guanfu-mcp`,资源 `guanfu://panel/latest/{btc,qqq,spy,gold}` `guanfu://skill/tier1` `guanfu://skill/profiles/btc` 等 |

## 辅助工具(二进制级,但更偏运维/复盘)

| 目录 | 用途 |
|---|---|
| `guanfu-similar/` | 给一个当前盘面 JSON + 一个历史目录,找最相似的 N 条历史盘面。需要长期 archive `~/.guanfu/panels/` 才有统计意义。 |
| `guanfu-backtest/` | AHR999 全历史三版对比回测。输出 Markdown 报告 + per-bucket fwd180 表。生产和回测共用 BTC 日线缓存。 |

## 分析脚本(研究向,不发布为产品)

| 目录 | 用途 | 状态 |
|---|---|---|
| `guanfu-threshold-search/` | 对 V/M/P 三维阈值做网格搜索,帮助定 v2 估值分桶。结果已写入 SKILL.md / backtest baseline 文档,脚本保留作为复现。 | 稳定,不常跑 |

## ⚠ 运行 ad-hoc 分析的口径

guanfu-threshold-search 这类脚本**生命周期与正式 CLI 不同**:
- 不进入 `make build` 默认产物
- 可能在未来大改 v3/v4 特征后不再 compile(因为依赖内部 API)
- 输出**不是给消费方的**,是给 maintainer 调参/复现用的
- 每一次用它跑出新阈值,必须把最终常量写回代码(SKILL 阈值表 / reliability 表),而**不是让生产二进制运行时依赖脚本**

如果有更多 ad-hoc 分析需求(regime 调参 / feature 淘汰 / 权重优化),建议:
1. 在 `cmd/` 新开一个 `guanfu-<task>/` 子目录
2. 顶部注释写清用途 + 如何复现 + 输出落到哪个常量
3. 不追求测试覆盖,但**必须能无依赖 run**(不依赖 `~/.guanfu/` 以外的路径)

---

历史上曾有多个 `*_backtest.go`(`// +build ignore` tag)这种混在 `cmd/guanfu/` 里的 ad-hoc 脚本。它们已经归档到 `archive/` 或重写成独立入口。新 ad-hoc 脚本**不要再混进主 CLI 目录** — 保持 `cmd/guanfu/` 纯净。
