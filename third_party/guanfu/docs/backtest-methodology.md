# 观复历史相似度与回测方法

推广材料里的历史收益或“相似时刻”必须从可复现流程生成，不能手写挑样本。

## 两套回测各回答什么（2026-07 定界，勿再长第三套）

| 实现 | 回答的问题 | 入口 |
|---|---|---|
| `pkg/engine/backtest.go` + `pkg/forecast/backtest`（walk-forward） | **forecast 准不准**：kNN 推演按 year×horizon 的 dir_hit / 覆盖率，产出 reliability 表；`RunWithOptions` 另供 `guanfu backtest` 的 A/B 消融 | `go test ./pkg/engine -run TestBacktestBundles`（回归门）· `guanfu backtest [asset|all]` |
| `cmd/guanfu-backtest`（verdict 回放） | **读盘有没有预测力**：把 verdict 引擎放回历史日期，按 stance / 顶底 proximity 分桶验证 hit rate 与前向收益 | `guanfu-backtest --start … --end …` |

新的回测需求先归入上面其一；两者都不合适才允许新实现（需在本文登记）。

## 面板归档

每天保存一次完整 JSON 面板（`~/.guanfu/panels/` 是 `guanfu-similar` 的默认 `--history-dir`）：

```bash
mkdir -p ~/.guanfu/panels
guanfu --json > "$HOME/.guanfu/panels/$(date -u +%F).json"
```

挂在 cron / launchd 上每天跑一次：

```cron
0 9 * * * /usr/bin/env -S bash -lc 'guanfu --json > ~/.guanfu/panels/$(date -u +%F).json 2>> ~/.guanfu/cron.log'
```

如果要让 ETF、funding、mempool、宏观等指标带历史分位，**不要**设置 `GUANFU_NO_HISTORY=1`，并长期保留同一个 `~/.guanfu/history.db`。

## 相似度计算

`guanfu-similar` 只比较双方都有 `q` 分位的指标。每个共享指标用 q 分位差计算欧氏距离：

```text
distance = sqrt(mean((current_q - history_q)^2))
similarity = max(0, 1 - distance) * 100
```

这种做法避免把美元、市值、利率、相关系数等不同量纲强行混在一起。缺点是早期样本如果没有足够 history.db，会因为共享 q 指标太少而被跳过。

## 命令

```bash
# 默认从 ~/.guanfu/panels 读 archive
guanfu --json | guanfu-similar --top 8

# 显式路径
guanfu --json > current.json
guanfu-similar --current current.json --history-dir /path/to/panels --top 8
```

输出示例：

```markdown
| rank | date | similarity | matched_q | distance | file |
|---:|---|---:|---:|---:|---|
| 1 | 2024-08-05 | 87.2% | 28 | 0.1280 | /Users/x/.guanfu/panels/2024-08-05.json |
```

## 收益统计口径

任何 `+30%`、`+100%` 这类宣传数字都必须同时标注：

- 样本日期来自 `guanfu-similar` 输出，不人工挑选。
- 价格源与面板价格源一致，默认使用 Binance BTCUSDT 日线收盘价。
- forward return 窗口固定，例如 30d / 90d / 180d / 365d，不能事后改窗口。
- 样本数量、`matched_q` 下限、相似度下限需要写清楚。
- 同时披露反例和最差 forward return。

在自动 forward-return 报告补齐前，公开推广材料只展示相似度方法，不展示历史收益数字。

---

## Walk-forward 矩阵(v3 新增,pkg/engine/backtest_bundles_test.go)

单一汇总命中率会**掩盖 regime 依赖**。`TestBacktestBundles` 按 (year, horizon) 切分,输出可以直接看出哪些年份的样本在拖/拉高平均。

### 典型输出格式

```
btc 180d walk-forward:
  2015:  dir_hit 0.71  n=4
  2016:  dir_hit 0.67  n=6
  2017:  dir_hit 0.83  n=6
  2018:  dir_hit 0.25  n=4       ← regime 不匹配:熊市 kNN 在牛市训练集找不到邻居
  2019:  dir_hit 0.60  n=5
  2020:  dir_hit 1.00  n=3
  ...
```

### 如何读这个矩阵

| 模式 | 含义 | 行动 |
|---|---|---|
| 各年均衡 50-70% | 信号在全 regime 可用 | 正常使用 |
| 少数年份极端低 (<30%) | 某个 regime(通常是 2018/2022 风险去杠杆)失效 | 在类似 regime 到来时降级,或加 regime gating (Track G2) |
| 多数年份都低 (<50%) | 全 regime 弱信号 | **hard-block** |
| 前 N 年好、后 N 年差 | 结构性失效,特征老化 | 可能需 recency-weighted kNN (Track G5) 或删特征 |

### 当前观察(2026-05-11 stale-gated + horizon-weighted baseline)

纯 (asset, horizon) 命中率表见 [`skill/tier1.md`](../skill/tier1.md) § 3。本表聚焦 walk-forward **诊断**：

| 资产 | 全局 dir_hit | walk-forward 诊断 |
|---|---|---|
| BTC 90d | 61% | 2018/2022 走弱,2020/2024 强;仍高于随机 |
| QQQ 180d | 80% | 2023-2025 样本强,2021/2022 中性;CBOE put/call stale gate 后历史缺口不再被 forward-fill |
| SPY 180d | 85% | 2023-2025 强,2021/2022 中性;SPY 90d 恢复到 75% |
| Gold 90d | 63% | horizon-specific re-rank 后显著改善；仍有 regime 依赖 |
| Gold 30d | 45% | 低于随机 → hard-block,不输出数值预测 |

`go run ./cmd/guanfu backtest all --plain` 的当前全资产汇总(口径与 `TestBacktestBundles` 不同,样本更多、用于运维巡检):

| Asset | Days | Tests | Hit30d | Hit90d | Hit180d | PIT | CRPS | Coverage |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| qqq | 2757 | 28 | 64.3% | 85.7% | 78.6% | 0.56 | 0.1724 | 100% |
| spy | 2757 | 28 | 67.9% | 78.6% | 82.1% | 0.56 | 0.1224 | 100% |
| btc | 5777 | 62 | 59.7% | 66.1% | 69.3% | 0.47 | 0.5030 | 100% |
| gold | 6447 | 69 | 53.6% | 62.3% | 59.4% | 0.50 | 0.1154 | 100% |

Conformal realized coverage after asset+horizon calibration:

| Asset | 30d | 90d | 180d |
|---|---:|---:|---:|
| qqq | 89% | 79% | 86% |
| spy | 89% | 75% | 89% |
| btc | 89% | 89% | 84% |
| gold | 83% | 78% | 80% |

### 如何生成最新矩阵

```bash
go test ./pkg/engine/ -run TestBacktestBundles -v
```

需要 `~/.guanfu/prices/` 有完整数据(`guanfu refresh` 先跑);CI 下无数据时 skip。

### 更新 reliability 表

如果 walk-forward 数字变化超出回归预算(任一 horizon dir_hit 下降 ≥ 3pp),更新 `pkg/assetprofile/profile.go` 里的 profile reliability row + `AsOf` 字段。`pkg/forecast/reliability.go` 只负责把 profile row 渲染成 caveat / hard-block。具体规则见 `docs/archive/v3/guanfu-v3-todo.md` 回归预算节。

### 为什么不在 BuildForecast 里实时跑 walk-forward

- IO 重:需要 PriceStore 所有历史 + 重算每个特征
- 慢:5 资产 × 多 horizon × 多年 = 数百次 kNN,一次 panel 请求延迟飙到分钟级
- 不必要:walk-forward 变化是月/季度尺度,没必要每次请求重算

所以是**离线跑、写回 reliability 表、Build 时 lookup**。
