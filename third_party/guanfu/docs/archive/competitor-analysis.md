# 同类项目对比分析

## 概览

市场上存在三类 BTC 分析工具，guanfu 属于其中独一档的 "多源多域 CLI + AI 原生" 定位。

---

## 一、竞品清单

### 1. AHR999 类（单指标）

| 项目 | 语言 | 指标数 | 特点 |
|------|------|--------|------|
| [ahr999-free](https://github.com/lovexw/ahr999-free) | HTML/JS | 1 | 纯静态页面，GitHub Actions 每 4h 更新 |
| [ahr999-dc](https://github.com/pluveto/ahr999-dc) | Go | 1 | CLI 采集 AHR999 → InfluxDB，单次/定时运行 |

**guanfu 差异**：AHR999 是 guanfu 42 个指标中的 1 个。且 guanfu 的 AHR 实现是改进版——调和均值 DCA + Huber IRLS 动态拟合 + 动态分位评分，非原版固定系数。

### 2. 综合分析类（多指标但浅）

| 项目 | 语言 | 指标数 | 域数 | AI 集成 | 数据源 |
|------|------|--------|------|---------|--------|
| [btc-ai-advisor](https://github.com/fanyilun0/btc-ai-advisor) | Python | ~5 | 2 | Deepseek R1 | Binance + flink API |
| [CryptoSentinel](https://github.com/fanyilun0/CryptoSentinel) | Python | ~5 | 2 | — | 同上 |

**guanfu 差异**：指标数 8×，数据源 4×，SKILL.md 知识库系统化（非 hardcode 建议），JSON 输出可直接喂任何 AI。

### 3. 链上数据类（深度但单维）

| 项目 | 语言 | 数据 | 域 |
|------|------|------|-----|
| [BRK](https://github.com/bitcoinresearchkit/brk) | Rust | 全链上 | 链上分析 |
| [Satonomics](https://codeberg.org/EthnTuttle/satonomics) | — | 全链上 | 宏观链上 |
| [UTXOscope](https://github.com/Drew72-ita/UTXOscope) | Python | UTXO | 价格热图 |
| [BlockSci](https://github.com/citp/BlockSci) | C++/Python | 全链上 | 交易图分析 |

**guanfu 差异**：链上只是 guanfu 8 域中的估值域 + 网络域。guanfu 整合了链上 + 交易数据 + 宏观 + 跨资产，而非纯链上。且不需要跑全节点。

### 4. 实时行情类（Web/短周期）

| 项目 | 栈 | 定位 |
|------|-----|------|
| [BTC Tooling](https://github.com/douvy/btc-tooling) | Next.js | 实时行情 + TradingView + orderbook |
| [isbtchot](https://pypi.org/project/isbtchot/) | Python | 终端热力指数 |
| [BTC-Tracker](https://github.com/wilqq-the/BTC-Tracker) | Next.js | 个人持仓跟踪 |

**guanfu 差异**：不覆盖分时/orderbook/技术图表。guanfu 定位长期投资而非日内交易。

---

## 二、定位对比

```
        指标深度
          ↑
    BRK   │   guanfu ← (多源多域 × 历史分位 × AI原生)
  Satonomics│
  BlockSci │   btc-ai-advisor
          │   CryptoSentinel
          │   ahr999-free / ahr999-dc
          │   BTC-Tracker
          │   isbtchot     BTC Tooling
          └──────────────────────────→ 覆盖广度
         链上       多源       实时
```

---

## 三、guanfu 的不可替代性

| 维度 | 所有竞品 | guanfu |
|------|---------|--------|
| 域数 | 1-2 | **8** (cycle/valuation/network/positioning/macro/flow/technical/cross_asset) |
| 指标数 | 1-5 | **42** |
| 历史分位(q) | 无 | ✅ 自建 SQLite 历史库 |
| AI 集成 | 硬编码建议或单模型 | ✅ SKILL.md 知识库 + JSON 输出，跨 Claude/GPT |
| 数据源 | 1-3 | **13** (Binance/CoinGecko/mempool/SoSoValue/CoinMetrics/FRED/Futu/Yahoo) |
| 部署 | 多数需 Web/数据库 | ✅ 单二进制 + SQLite |
| 语言 | 多数 Python/JS | ✅ Go (单文件部署) |
| 跨资产对比 | 无 | ✅ BTC vs Gold/QQQ/SPY/UUP/VIXY |
| 技术指标 | 无独立域 | ✅ 7 个 (RSI/MACD/EMA/MA/BB/Vol) |
| 矿工/网络 | BRK 有 | ✅ hash rate + ribbons + difficulty + mempool |
| 长期 vs 短线 | 多数偏短线 | ✅ 明确长期投资定位 |
| 加密握手 | 无 | ✅ 自写 Futu protobuf + Python bridge 降级 |

---

## 四、一句话定位

> **guanfu 是唯一一个同时做 8 域多源聚合 + 历史分位 + AI 原生 JSON 输出 + 本地单二进制部署的 BTC 长期投资数据底盘。**
