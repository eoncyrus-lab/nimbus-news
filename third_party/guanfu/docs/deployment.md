# guanfu 部署指南

覆盖 3 种使用方式:CLI / MCP 集成 / 定时任务。选任何一种,guanfu 都能跑起来。

---

## 一、前置条件

- **Go 1.26+**(如果走 `go install` 路径)
- **Python 3.9+**(可选;Futu / CAPE bridge 需要)
- **~/.guanfu/**(自动创建,用于价格历史 / claim ledger / portfolio 配置)

### API Key(按需)

| 变量 | 何时需要 |
|---|---|
| `FRED_API_KEY` | 想要 DXY / real yield / TGA / RRP / 利差 / T-bill / Fed-ECB-BOJ-PBoC rates 等宏观数据。免费 <https://fred.stlouisfed.org> 注册。**缺失时 baseline 对比会 fallback 到 flat 4.5%,forecast 仍可跑** |
| `COINMETRICS_API_KEY` | 想要 MVRV Z / NUPL 等付费端点。缺失 = 社区 tier,仅价格 + 少量链上 |
| `CMC_API_KEY` | 想要 CoinMarketCap global metrics / BTC quote 独立市场上下文。缺失只影响 `cmc_market_context`,不影响核心价格或 forecast |

默认 QQQ/SPY put/call 已改用 CBOE 官方免费无 key 数据源；本项目默认不需要 `STOOQ_APIKEY`。

---

## 二、CLI 安装

### 方式 1:go install(推荐)

```bash
go install github.com/Ricaardo/guanfu/cmd/guanfu@latest
go install github.com/Ricaardo/guanfu/cmd/guanfu-mcp@latest       # MCP server
go install github.com/Ricaardo/guanfu/cmd/guanfu-similar@latest   # 历史相似度
```

二进制落在 `$GOBIN`(默认 `$HOME/go/bin`),确保已加入 `PATH`。

### 方式 2:源码构建

```bash
git clone https://github.com/Ricaardo/guanfu.git
cd guanfu
make all   # vet + test + build 3 binary 到 bin/
```

### 方式 3:预编译(Releases)

从 [Releases](https://github.com/Ricaardo/guanfu/releases) 下载 linux/darwin/windows × amd64/arm64,解压即用。

### 首次验证

```bash
guanfu --version                    # 确认版本
guanfu refresh --dry-run            # 列 28 个数据源(不拉数据)
guanfu status --frank               # 按可靠性分类 (asset, horizon)
```

---

## 三、首次数据拉取

**第一次全量**大概 5-15 分钟(取决于网络 + FRED/Yahoo 限流):

```bash
export FRED_API_KEY=your_key
export CMC_API_KEY=your_cmc_key       # 可选:市场观察层
guanfu --timeout 600s refresh         # 28 个 source 串行拉
```

- 数据落在 `~/.guanfu/prices/<asset>.json`
- 失败的 source 不影响其他;看结束表格里 `fail` 状态
- 后续 `refresh` 走增量,只拉 lastDate+1 → today,通常 < 30 秒
- 可选:`--only=btc,fred_dxy` 指定源,`--skip=defillama_stablecoin_supply` 跳过
- CBOE put/call 首次会抓官方历史 CSV + 最近 daily pages；网络慢时可单独跑 `guanfu --timeout 180s refresh --only=stooq_putcall`

```bash
guanfu                               # 默认 brief 摘要(10 行)
guanfu btc --full                    # 完整 8 域 40+ 指标
guanfu btc --verdict                 # 结构化读盘
guanfu btc --forecast                # kNN 历史相似推演
guanfu qqq --verdict --forecast      # QQQ 同上
guanfu stock AAPL                    # 任意美股(首次自动拉 10 年)
```

---

## 四、可选:Futu OpenD 实时行情(macOS/Windows)

**不配也能跑** — 默认走 Yahoo Finance 降级。配置后 QQQ/SPY/GLD/UUP/VIXY 实时报价。

```bash
pip install futu-api
mkdir -p ~/.guanfu
curl -sL https://raw.githubusercontent.com/Ricaardo/guanfu/main/pkg/client/futu_bridge.py \
  -o ~/.guanfu/futu_bridge.py

# OpenD 本地运行后:
export FUTU_GATEWAY=127.0.0.1:11111
export FUTU_ENABLED=1   # 可选,default on
```

**禁用**:`export FUTU_ENABLED=0`,guanfu 自动回落 Yahoo。

**bridge 探测顺序**:`$FUTU_BRIDGE` → 二进制同目录 → `~/.guanfu/` → `~/.config/guanfu/`

---

## 五、MCP 集成(AI 客户端)

### Claude Desktop

编辑 `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "guanfu": {
      "command": "/Users/x/go/bin/guanfu-mcp",
      "env": {
        "FRED_API_KEY": "your_fred_key",
        "CMC_API_KEY": "your_cmc_key",
        "GUANFU_HISTORY_DB": "/Users/x/.guanfu/history.db",
        "GUANFU_SKILL_PATH": "/Users/x/guanfu/skill/SKILL.md"
      }
    }
  }
}
```

重启 Claude Desktop,直接问 "BTC 现在怎么样" 即可。

### Claude Code

两种方式互补:
- **Skill 方式**(默认):`skill/SKILL.md` 通过 CLI JSON 调用
- **MCP 方式**:`.claude/mcp.json` 加 `mcpServers.guanfu` 同上配置

### Cursor / Windsurf / Cline / 任意 MCP 客户端

同上,`command` 指向 `guanfu-mcp` 二进制绝对路径。

### MCP 提供的 Tools

| Tool | 别名(deprecated,仍工作) | 用途 |
|---|---|---|
| `get_panel` | `get_btc_panel` | 完整盘面;`asset` 参数切换 btc/qqq/spy/gold |
| `get_verdict` | `get_btc_verdict` | 结构化读盘(附 portfolio_context 当 `~/.guanfu/portfolio.json` 存在) |
| `get_forecast` | `get_btc_forecast` | kNN 推演 + 基线对比 + conformal 区间 + ensemble disagreement + reliability 标注 |
| `get_stock_forecast` | — | 任意美股 kNN(首次自动 Yahoo fetch + kNN) |
| `get_domain` | — | 单域 |
| `get_indicator` | — | 单指标 |

### MCP 提供的 Resources

| URI | 内容 | 大小 |
|---|---|---|
| `guanfu://skill/tier1` | 数据契约 + 关键阈值 + 可靠性规则 | **~200 行,必载** |
| `guanfu://skill/tier2` | 决策框架 + 行为护栏 + 输出模板 | 做判断时读 |
| `guanfu://skill/tier3` | 完整 SKILL.md(术语 + 机制 + 类比) | 追问原理时读 |
| `guanfu://knowledge/skill.md` | SKILL.md 完整版(兼容 alias) | 900 行 |
| `guanfu://panel/latest/{asset}` | 每资产缓存盘面 | JSON |
| `guanfu://verdict/latest/{asset}` | 每资产结构化读盘 | JSON |
| `guanfu://forecast/latest/{asset}` | 每资产 kNN 推演 | JSON |
| `guanfu://ledger/summary` | 最近 90d claim 汇总 + intent 列表 | JSON |

**Token 节省建议**:让 AI 按需加载 tier1 → tier2 → tier3,而不是一次性读完 SKILL.md。

---

## 六、可选:Portfolio 上下文

opt-in 功能。创建 `~/.guanfu/portfolio.json`:

```json
{
  "schema_version": 1,
  "holdings": {
    "btc": {"amount": 0.35, "cost_basis_usd": 42000, "acquired": "2023-06"},
    "qqq": {"shares": 50, "cost_basis_usd": 380},
    "cash": {"usd": 30000, "cny": 100000}
  },
  "preferences": {
    "horizon_years": 5,
    "risk_budget": "moderate",
    "home_currency": "CNY",
    "ceiling_pct": {"btc": 25, "equity": 60}
  },
  "behavior": {
    "cooldown_hours": 4,
    "fomo_threshold_pct": 20,
    "panic_threshold_pct": 20
  }
}
```

配置后:
- `guanfu btc --verdict` 附加 `portfolio_context`:当前权重 / 上限 / 剩余空间 / 是否超限
- MCP `get_verdict` 同样返回该字段
- AI 读盘会按 portfolio 给**个性化**结论(见 SKILL.md J9 节),而不是通用判断
- **不配置** = 回落到无上下文路径,行为等同 v2

---

## 七、日常使用:CLI 常用命令

```bash
# 默认 brief 摘要(10 行)
guanfu                               # 裸跑 = BTC
guanfu qqq                           # 其他资产同理

# 完整输出
guanfu --full                        # 40 指标 panel
guanfu btc --verdict --forecast      # 读盘 + 推演

# 子命令
guanfu market                        # 多资产一览 + 共识
guanfu dca                           # DCA 定投对比 + 成本提示
guanfu allocate                      # 懒人组合偏离
guanfu stress --series fred_dfii10 --shift +1.5 --asset btc
                                     # 条件化历史 analog 搜索
guanfu joint --assets btc,qqq,gold   # 跨资产 forecast 共识

# 用户向
guanfu intent log --asset btc --horizon 5y_hold --thesis "..."
guanfu intent list [--asset btc] [--since 2026-01-01]
guanfu intent review                 # drift 检查

# 监控 + 通知
guanfu watch btc --when 'mayer_multiple < 0.8' [--dispatch osascript]
guanfu digest                        # 每日 30 秒摘要(alerts + claims + prices + events)

# 回测与校准
guanfu backtest btc                  # 单资产 kNN 回测
guanfu backtest all                  # 全资产
guanfu backtest all --ablate-putcall # QQQ/SPY put/call 特征消融
guanfu calibrate [--asset btc]       # 读 ledger 对比实际价格,算 dir_hit / 区间覆盖率
guanfu status --frank                # 按可靠性三桶分类

# 统一数据刷新
guanfu refresh                       # 全量 / 增量
guanfu refresh --dry-run             # 仅列
guanfu refresh --only=btc,fred_dxy   # 指定源
```

---

## 八、定时任务(推荐)

### macOS / Linux (cron)

```bash
crontab -e
```

添加:

```cron
# 每日刷新 + archive 盘面(cron UTC 时区,按需调整)
0 9 * * * /usr/bin/env -S bash -lc 'guanfu refresh >> ~/.guanfu/cron.log 2>&1'

# 盘面 archive(可供未来 guanfu-similar 使用)
5 9 * * * /usr/bin/env -S bash -lc 'mkdir -p ~/.guanfu/panels && guanfu --json > ~/.guanfu/panels/$(date -u +%F).json 2>> ~/.guanfu/cron.log'

# 条件监控 + macOS 原生通知
10 9 * * 1-5 /usr/bin/env -S bash -lc 'guanfu watch btc --when "mayer_multiple < 0.8" --dispatch osascript --quiet >> ~/.guanfu/cron.log 2>&1'

# 每周五回顾 ledger
0 18 * * 5 /usr/bin/env -S bash -lc 'guanfu calibrate --json > ~/.guanfu/calibration-$(date -u +%F).json 2>> ~/.guanfu/cron.log'
```

### macOS launchd(备选)

如果 cron 被系统限制,使用 `~/Library/LaunchAgents/com.guanfu.refresh.plist`;略。

---

## 九、环境变量速查

| 变量 | 默认 | 作用 |
|---|---|---|
| `FRED_API_KEY` | — | FRED 宏观数据 key |
| `CMC_API_KEY` | — | CoinMarketCap market context key |
| `COINMETRICS_API_KEY` | — | CoinMetrics 付费端点 key |
| `GUANFU_NO_HISTORY=1` | off | 禁用 history.db 写入 |
| `GUANFU_HISTORY_DB` | `~/.guanfu/history.db` | SQLite 路径 |
| `GUANFU_SKILL_PATH` | `skill/SKILL.md`(相对) | MCP 读 SKILL 路径 |
| `GUANFU_SKILL_DIR` | 同 SKILL 目录 | MCP 读 tier*.md 目录 |
| `GUANFU_NO_CLAIMS=1` | off | 禁用 claim ledger 写入 |
| `GUANFU_CLAIMS_DIR` | `~/.guanfu/claims` | Claim + Intent ledger 路径 |
| `GUANFU_ALERTS_DIR` | `~/.guanfu/alerts` | Alert store 路径 |
| `GUANFU_PORTFOLIO` | `~/.guanfu/portfolio.json` | Portfolio 配置路径 |
| `CACHE_DIR` | 系统用户缓存 | BTC K 线缓存 + market snapshot |
| `GUANFU_BTC_KLINE_CACHE` | `$CACHE_DIR/btc_daily_history.json` | 覆盖 BTC 日线缓存路径 |
| `FUTU_GATEWAY` | `127.0.0.1:11111` | Futu OpenD 地址 |
| `FUTU_ENABLED=0` | on | 禁用 Futu |
| `FUTU_BRIDGE` | auto | 自定义 `futu_bridge.py` 路径 |

---

## 十、故障排查

| 症状 | 可能原因 | 解决 |
|---|---|---|
| `guanfu refresh` 报 FRED 4xx | `FRED_API_KEY` 未设置 / 无效 | 检查 env;所有 fred_* 失败时其他源仍正常 |
| `cmc_market_context` 为 config skip | `CMC_API_KEY` 未设置 | 配置 key 或接受 CMC 市场观察层缺失 |
| `stooq_putcall` stale / no_new_data | CBOE 官方页面暂未更新、周末/假日或网络失败 | 跑 `guanfu --timeout 180s refresh --only=stooq_putcall`;默认不需要 Stooq key |
| Futu 连不上 | OpenD 未启动 / 端口不对 | `export FUTU_ENABLED=0` 先回落 Yahoo |
| `source_health` 有 `stale` / `fallback_used` | 某 source 多天未更新 | 跑 `guanfu refresh --only=<key>` 重拉 |
| `guanfu calibrate` 说 no matured claims | ledger 为空或 claim 都未到期 | 每日自动跑 forecast 积累 30d 后再看 |
| MCP 里 Claude 读不到 guanfu | 二进制路径错 / env 变量缺失 | `which guanfu-mcp`;日志在 Claude Desktop 开发者控制台 |

### 诊断命令

```bash
guanfu status                        # PriceStore 每 asset 行数 + last_date
guanfu status --frank                # 按可靠性分类
guanfu status --json | jq            # 程序化消费
guanfu refresh --dry-run             # 不拉数据,只列计划
```

---

## 十一、数据目录结构

```
~/.guanfu/
├── prices/                 # JSON 日频存档(refresh 输出)
│   ├── btc.json / qqq.json / spy.json / gold.json
│   ├── fred_*.json / spx_cape.json
│   ├── defillama_stablecoin_supply.json / stooq_putcall.json / coinbase_btc.json
│   ├── deribit_dvol.json / deribit_skew_25d_pct.json / deribit_skew_expiry_days.json
│   └── stock_*.json         # 任意美股
├── history.db               # SQLite:15 非价格指标分位(730d 滚动)
├── panels/                  # (可选)每日盘面 archive
│   └── YYYY-MM-DD.json
├── claims/                  # Claim + Intent ledger
│   ├── claims/YYYY-MM/YYYY-MM-DD-{asset}-{horizon}-{id}.json
│   └── intents/YYYY-MM/YYYY-MM-DD-{asset}-{id}.json
├── alerts/                  # watch 触发记录
│   └── YYYY-MM/YYYY-MM-DD-{asset}-{id}.json
└── portfolio.json           # (opt-in)组合上下文
```

**备份策略**:`~/.guanfu/` 建议每月 tar + 外部备份 — 价格数据重拉成本低但 history.db / claims / intents 无法恢复。

---

## 十二、更新 / 卸载

```bash
# 更新
go install github.com/Ricaardo/guanfu/cmd/guanfu@latest   # 或 git pull + make all

# 卸载
rm "$(which guanfu)" "$(which guanfu-mcp)" "$(which guanfu-similar)"
rm -rf ~/.guanfu/                    # 删数据(claim/intent/portfolio 不可逆)

# 保留 ledger 但重置价格
rm -rf ~/.guanfu/prices/
```

---

## 十三、相关文档

- [`docs/DATA-SOURCES.md`](DATA-SOURCES.md) — 30+ 数据源完整列表
- [`docs/audience.md`](audience.md) — 三类用户画像及设计优先级
- [`docs/backtest-methodology.md`](backtest-methodology.md) — 回测口径与 walk-forward 矩阵
- [`docs/internals.md`](internals.md) — 内部约定(权重 / 源 / schema)
- [`skill/SKILL.md`](../skill/SKILL.md) — AI skill 消费方文档(含 tier1/2/3 分层)
- [`README.md`](../README.md) — 项目总览
