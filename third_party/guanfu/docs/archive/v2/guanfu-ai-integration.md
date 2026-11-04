# 观复 v2 AI 集成方案

## 现状

guanfu v2 支持三种 AI 集成方式：

```
1. Claude Code Skill (btc-guanfu)
   用户: "QQQ 现在估值如何？"
     → btc-guanfu skill 触发
     → Claude 运行 guanfu qqq --verdict --json
     → 输出综合分析

2. MCP Server (guanfu-mcp)
   任何 MCP 客户端直接调用 get_btc_panel / get_btc_verdict 等 tools
   支持 asset 参数: btc/qqq/spy/gold/hs300

3. CLI JSON (通用)
   guanfu btc --json | jq → 任意 AI 应用消费
```

---

## 三层集成架构

```
┌────────────────────────────────────────┐
│  消费层 (AI Apps)                       │
│  Claude.ai / ChatGPT / Cursor / etc.    │
├────────────────────────────────────────┤
│  传输层 (Standard Interface)            │
│  MCP Server / REST API / CLI JSON       │
├────────────────────────────────────────┤
│  计算层 (guanfu binary)                 │
│  数据采集 → 指标计算 → JSON 输出         │
└────────────────────────────────────────┘
```

### 接口矩阵

| 接口 | 适用场景 | 复杂度 |
|------|----------|--------|
| **CLI JSON** | Claude Code skill（已有） | 低 |
| **MCP Server** | Claude Desktop、Claude Code、支持 MCP 的客户端 | 中 |
| **REST API** | ChatGPT GPT Action、Web 前端、移动端 | 中 |
| **GitHub Action** | 定时生成盘面报告、CI/CD 集成 | 低 |

---

## 方案一：CLI JSON（已有）

### 使用方式

在 Claude Code 中通过 skill 触发：

```markdown
# SKILL.md
当用户询问 BTC 相关问题时：
1. 运行 `guanfu --json`
2. 解析 JSON 输出
3. 查阅 SKILL.md 知识库
4. 综合输出分析
```

### 优劣

- ✅ 零额外基础设施
- ✅ 已在工作
- ❌ 仅 Claude Code 可用
- ❌ 依赖本地环境

---

## 方案二：MCP Server（推荐）

### 架构

```
Claude Desktop / Code
     │
     ├── MCP Protocol (stdio)
     │
     v
guanfu-mcp-server
     │
     ├── tool: get_panel(domain?)
     ├── tool: get_indicator(name)
     ├── resource: guanfu://knowledge/skill.md
     └── resource: guanfu://panel/latest
```

### 实现 (~100 行 Go)

```go
// mcp/guanfu_mcp.go
package main

import (
    "encoding/json"
    "os/exec"
)

// Tool: get_panel
// 参数: domain (optional)
// 返回: 完整 IndicatorPanel JSON

// Tool: get_indicator  
// 参数: name (e.g. "ahr999", "hash_ribbons")
// 返回: 单个指标值 + q + label

// Tool: get_btc_forecast
// 参数: horizons (optional, e.g. [30, 90, 180]), top_k (optional)
// 返回: 历史相似盘面走势推演 JSON（情景概率 + 前向收益分布）

// Resource: guanfu://panel/latest
// 返回: 缓存的最新盘面 JSON

// Resource: guanfu://forecast/latest
// 返回: 缓存盘面的走势推演 JSON
```

### 部署

```json
// claude_desktop_config.json
{
  "mcpServers": {
    "guanfu": {
      "command": "guanfu-mcp-server",
      "args": ["--cache-ttl=5m"]
    }
  }
}
```

### 优劣

- ✅ Claude Desktop / Code / API 通用
- ✅ 标准化协议，生态支持广
- ✅ 可缓存，减少重复调用
- ❌ 需要额外部署 MCP server

---

## 方案三：REST API（覆盖 ChatGPT）

### 架构

```
ChatGPT / 任意 AI
     │
     ├── HTTPS
     │
     v
api.guanfu.dev (Cloudflare Workers / Fly.io)
     │
     ├── GET  /v1/panel        → 完整盘面 JSON
     ├── GET  /v1/panel/{domain} → 单域
     ├── GET  /v1/indicator/{name} → 单指标
     ├── GET  /v1/forecast     → 历史相似盘面走势推演 JSON
     └── GET  /v1/health
```

### ChatGPT GPT Action

```yaml
openapi: 3.0.0
info:
  title: guanfu BTC Dashboard
paths:
  /v1/panel:
    get:
      summary: 获取 BTC 完整盘面
      parameters:
        - name: domain
          in: query
          schema:
            type: string
            enum: [cycle, valuation, network, positioning, macro, flow, technical, cross_asset]
      responses:
        '200':
          description: IndicatorPanel JSON
```

### GPT System Prompt（配合 GPT Action 使用）

```
你是观复 BTC 投资分析师。当用户询问 BTC 相关问题时，
调用 get_panel action 获取实时盘面数据，然后按以下
框架分析：

1. 周期定位 → 估值交叉验证 → 网络健康 → 杠杆情绪
   → 宏观环境 → 资金流 → 技术指标 → 跨资产对比
2. 重点识别指标间的矛盾（如周期说顶部但杠杆说底部）
3. 输出结论 + 关键观察点，不输出评分
4. 引用具体指标值作为论据

知识库参考：
[SKILL.md 内容]
```

### 部署选项

| 平台 | 成本 | 冷启动 |
|------|------|--------|
| Cloudflare Workers (Go via WASM) | 免费层够用 | <100ms |
| Fly.io | ~$5/mo | <1s |
| Railway | ~$5/mo | <1s |
| Vercel Serverless (需转 Node.js wrapper) | 免费层够用 | 有冷启动 |

### 优劣

- ✅ 任何 AI 均可调用（ChatGPT、Claude API、Gemini 等）
- ✅ 可公开访问
- ❌ 需要部署和维护
- ❌ 有延迟（冷启动通常 60-90s，缓存命中 <1s）

---

## 方案四：GitHub Action（定时盘面 + 推送）

### 用途

每天自动生成盘面报告 → 推送到 Discord/Slack/Telegram/Email。

```yaml
# .github/workflows/guanfu-daily.yml
name: guanfu daily panel
on:
  schedule:
    - cron: '0 8 * * *'  # UTC 08:00 = 北京时间 16:00
  workflow_dispatch:

jobs:
  panel:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version-file: go.mod }
      - run: go install github.com/Ricaardo/guanfu/cmd/guanfu@latest
      - name: Run and report
        env:
          FRED_API_KEY: ${{ secrets.FRED_API_KEY }}
          COINMETRICS_API_KEY: ${{ secrets.COINMETRICS_API_KEY }}
        run: |
          guanfu --json > panel.json
          # Post to Discord webhook
          curl -X POST ${{ secrets.DISCORD_WEBHOOK }} \
            -H "Content-Type: application/json" \
            -d "$(python3 format_for_discord.py panel.json)"
```

---

## 对比总结

| 方案 | Claude | ChatGPT | 其他 AI | 部署难度 | 推荐优先级 |
|------|--------|---------|---------|----------|-----------|
| CLI JSON (已有) | ✅ | ❌ | ❌ | 无 | ⭐⭐⭐⭐⭐ (现在就用) |
| MCP Server | ✅ | ❌ | ✅ (Cursor, etc) | 低 | ⭐⭐⭐⭐ (立即实施) |
| REST API | ✅ | ✅ | ✅ | 中 | ⭐⭐⭐ (第二步) |
| GitHub Action | ✅ | ❌ | ❌ | 低 | ⭐⭐⭐ (独立场景) |

---

## 实施路线

```
Phase 1 (今日): CLI JSON — 已可用 ✅
Phase 2 (本周): MCP Server  — 覆盖 Claude 全系
Phase 3 (下周): REST API   — 覆盖 ChatGPT + 公网
Phase 4 (按需): GitHub Action — 定时推送
```

---

## 通用 System Prompt 模板

无论用哪个 AI 平台，核心 prompt 结构相同：

```
你是观复 (guanfu) BTC 投资分析师。

工具能力：
- 可获取 8 域 30+ BTC 实时指标（含历史分位 q）
- 域：cycle/valuation/network/positioning/macro/flow/technical/cross_asset

分析框架（6 步读盘）：
1. 周期位置 → 估值交叉验证 → 网络健康 → 杠杆情绪
   → 宏观环境 → 资金流 → 技术指标 → 跨资产对比
2. 重点识别矛盾信号（如周期说顶部但杠杆说底部）
3. 每个结论引用具体指标 + q 分位作为论据
4. 不输出评分，输出概率性判断
5. 明确标注不确定性来源

输出格式：
- 核心判断（一句话）
- 分域信号表（方向 + 置信度）
- 矛盾分析
- 关键观察点
- 风险矩阵

知识库：[SKILL.md 内容嵌入此处]
```
