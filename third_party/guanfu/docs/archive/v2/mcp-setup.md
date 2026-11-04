# guanfu MCP Server 部署

## Claude Desktop

编辑 `claude_desktop_config.json`：

```json
{
  "mcpServers": {
    "guanfu": {
      "command": "/Users/x/guanfu/bin/guanfu-mcp",
      "env": {
        "FRED_API_KEY": "your_fred_key",
        "GUANFU_HISTORY_DB": "/Users/x/.guanfu/history.db",
        "GUANFU_SKILL_PATH": "/Users/x/guanfu/skill/SKILL.md",
        "FUTU_GATEWAY": "127.0.0.1:11111"
      }
    }
  }
}
```

重启 Claude Desktop，对话中直接问 "BTC 现在怎么样？" 即可。所有 tools 支持 `asset` 参数（btc/qqq/spy/gold/hs300）。

## Claude Code

Claude Code 已有 btc-guanfu skill（CLI JSON 方式），MCP 可作为补充：在 `.claude/mcp.json` 中添加同上配置。

## Cursor / Windsurf / 任意 MCP 客户端

同上，配置 `mcpServers.guanfu` 指向二进制路径。

> 如需一次性诊断且不想写入本地历史库，可临时设置 `GUANFU_NO_HISTORY=1`。正常使用建议保留 history，以便 ETF / mempool / funding / macro 等指标积累本地分位样本。

## 提供的 Tools

| Tool | 用途 |
|------|------|
| `get_btc_panel` | 完整 8 域 40+ 指标 JSON |
| `get_btc_verdict` | 结构化多域读盘 JSON，不输出交易 / 仓位指令 |
| `get_btc_forecast` | 历史相似盘面走势推演 JSON，输出情景概率 / 前向收益分布 / 相似样本 |
| `get_domain` | 单个域（cycle/valuation/...） |
| `get_indicator` | 单个指标值 + 分位 |

## 提供的 Resources

| URI | 内容 |
|-----|------|
| `guanfu://knowledge/skill.md` | SKILL.md 知识库 |
| `guanfu://panel/latest` | 缓存的最新盘面 |
| `guanfu://verdict/latest` | 缓存盘面的结构化读盘 |
| `guanfu://forecast/latest` | 缓存盘面的走势推演 |
