# guanfu AI 原生设计

## 现状

guanfu 有两种 AI 交付方式：

| 方式 | 适用场景 | 状态 |
|------|---------|------|
| CLI JSON + SKILL.md | Claude Code skill | ✅ |
| MCP Server (stdio) | Claude Desktop / Cursor / 任意 MCP 客户端 | ✅ 刚完成 |

但这只是"让 AI 能读到数据"。AI 时代的真正问题是：**怎么让 AI 用得更好？**

---

## AI 时代三个方向

### 1. Structured Output → AI-Ready Schema

目前 `--json` 输出的是人类可读的嵌套 map。AI 可以直接读，但不是最优。

**改进方向**：增加 `--json-schema` 输出模式，生成 OpenAPI/JSON Schema 格式，让 ChatGPT GPT Action、Claude API tool use、LangChain agent 可以直接注册为 function call。

```json
// 当前 --json
{"cycle": {"mayer_multiple": {"value": 0.93, "q": 0.20, "label": "偏低估"}}}

// 新增 --schema
// → 每个指标带 type/enum/description，AI 无需猜测字段含义
```

### 2. History → Time-Series Reasoning

当前 `q` 分位是一个快照——"现在在历史上的位置"。但 AI 擅长的是时间序列推理。

**改进方向**：输出每个指标的最近 N 天趋势方向（↑/↓/→）+ 变化速率。让 AI 可以做趋势判断而非单点判断。

```
// 当前
ahr999: {value: 0.71, q: 0.24}

// 改进后
ahr999: {value: 0.71, q: 0.24, trend_30d: "↓", velocity: -0.03/周}
```

### 3. Embedding → Semantic Search + Memory

当前每次对话 AI 都是"冷启动"——读完 JSON + SKILL.md 从零分析。但如果把每次盘面做 embedding 存入向量库：

- "2024-08 的信号组合"可以和当前盘面做语义匹配
- 历史分析报告可被检索和引用
- AI 可以说"这次和 2024 年 8 月相似度 92%，那次我的建议是..."

**改进方向**：`guanfu --json` 的输出做 embedding → 存入本地向量库 (Chroma/LanceDB)，AI 通过 tool call 检索历史相似盘面。

---

## 推荐路线

```
Phase 1 (done): CLI JSON + SKILL.md → Claude Code skill
Phase 2 (done): MCP Server → 跨客户端免 Bash
Phase 3 (本周): --json-schema 输出 → GPT Action / LangChain function call
Phase 4 (下周): 趋势字段 (trend_30d, velocity) → 时间序列推理
Phase 5 (远期): Embedding + 向量检索 → 历史盘面语义匹配
```
