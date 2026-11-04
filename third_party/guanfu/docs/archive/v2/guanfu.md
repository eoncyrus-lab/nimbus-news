# 观复 v2 (guanfu): 多资产投资盘面系统

> 致虚极，守静笃。**万物并作，吾以观复。** ——《道德经》第十六章

## 概述

guanfu v2 是一个**多资产**投资盘面 CLI 工具，覆盖 **BTC / QQQ / SPY / Gold / CSI300**。

- BTC：8 域 40+ 指标（周期/估值/网络/杠杆/宏观/资金流/技术/跨资产）
- QQQ/SPY：6 域面板（估值/技术/动量/宏观/情绪/资金流）
- Gold：6 域面板 + COT 持仓 + 实际利率估值
- CSI300：6 域面板 + 中国宏观（PMI/CPI/M2/CNY/北向/LPR）

支持 kNN 历史相似走势推演、DCA 定投回放、懒人组合配置、多资产回测。

## CLI

```bash
guanfu                        # BTC 8域面板（默认）
guanfu btc --verdict          # BTC + 读盘结论
guanfu btc --forecast         # BTC kNN 走势推演
guanfu btc --forecast-path    # ASCII 扇区图
guanfu qqq --verdict          # QQQ 面板
guanfu spy --verdict          # SPY 面板
guanfu gold --verdict         # 黄金面板
guanfu hs300                  # 沪深300面板
guanfu dca                    # DCA定投对比 (Fixed/AHR/Mayer)
guanfu allocate               # 全天候组合配置
guanfu market                 # 多资产一览 + 共识信号
guanfu backtest btc           # 单资产 kNN 回测
guanfu backtest gold          # 黄金回测
guanfu backtest all           # 全资产回测报告
guanfu status                 # 数据诊断
```

## 架构

```
guanfu v2
├── 数据层 (pkg/store/)          PriceStore JSON 日频存档
│   ├── 26 数据集                ~/.guanfu/prices/
│   └── 增量同步                 仅拉取新数据
├── 引擎层 (pkg/engine/)         Asset 接口 + 注册表
│   ├── BTC: 8域40+指标          BuildPanel + BuildVerdict
│   ├── QQQ/SPY: 6域面板         equity_dashboard.go
│   ├── Gold: 6域+COT估值        asset_gold.go
│   └── CSI300: 6域+中国宏观     hs300_dashboard.go
├── 推演层 (pkg/forecast/)       kNN 预测 + 回测
│   ├── 11核心 + 6跨资产 + 3仓位特征
│   ├── 状态匹配 + 多周期集成 + 动态K
│   └── 滚动窗口回测 (方向命中率/PIT/CRPS)
├── 策略层
│   ├── pkg/dca/                 DCA 定投引擎
│   └── pkg/allocate/            懒人组合引擎
└── CLI (8子命令) + MCP Server
```

## 数据源 (26 数据集)

| 类别 | 数据集 | 来源 | 起点 |
|------|--------|------|------|
| 价格 | btc | CoinMetrics | 2010-07 |
| 价格 | qqq, spy, gold | Yahoo Finance | ~2015 |
| 价格 | hs300 | AkShare | 2002-01 |
| 链上 | btc_mvrv, txcnt, hashrate | CoinMetrics | 2010-07 |
| 宏观 | fred_dfii10, dgs10, dxy, yield_curve | FRED | 1976+ |
| 黄金 | gold_cot | CFTC | 1986-01 |
| 中国 | hs300_pmi, m2, cpi, retail, cny, northbound, volume, lpr | AkShare | 1991+ |
| 估值 | spx_cape | Shiller | 1871-01 |

## kNN 预测引擎

方向命中率（170+ 次回测验证）：

| 资产 | 命中率 | 最优方法 |
|------|--------|---------|
| QQQ | 73.6% | 状态匹配 |
| SPY | 71.4% | 状态匹配 |
| Gold | 68.8% | 多周期集成+COT |
| BTC | 66.2% | 集成+链上+动态K |
| CSI300 | 49.1% | 状态+动态K+宏观 |
| **ENSEMBLE** | **65.6%** | |

## MCP Server

`guanfu-mcp` 提供 MCP 协议接口。Tools: `get_btc_panel`, `get_btc_verdict`, `get_btc_forecast`, `get_domain`, `get_indicator`（均支持 `asset` 参数: btc/qqq/spy/gold/hs300）。

## 安装

```bash
go install github.com/Ricaardo/guanfu/cmd/guanfu@latest
go install github.com/Ricaardo/guanfu/cmd/guanfu-mcp@latest
```

**免责声明**：观复输出历史分位 + 模式参考，不是投资建议。
