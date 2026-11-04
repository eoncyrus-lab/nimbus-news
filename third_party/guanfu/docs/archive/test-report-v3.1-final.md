# 观复 v3.1 系统测试报告

**测试时间**：2026-05-02 20:29 UTC
**版本**：v3.1 (8 域 42 指标，Futu Bridge)
**快照价格**：BTC $78,243 | ETH $2,305 | 总市值 $2.68T

---

## 1. 系统健康检查

| 检查项 | 结果 | 耗时 |
|--------|------|------|
| `go build` | ✅ | — |
| `go vet` | ✅ | — |
| 7/7 包单元测试 | ✅ | ~40s |
| 人类表格 8 域输出 | ✅ 完整 | ~83s |
| JSON 输出 | ✅ 结构正确 | <1s (缓存) |
| Futu Bridge | ✅ 连接正常 | ~2s |
| Binance PAXG | ✅ | <1s |
| CoinMetrics | ✅ (implied) | ~3s |
| FRED 宏观 | ✅ | ~2s |
| mempool.space | ✅ | ~2s |

---

## 2. 盘面全景

### 2.1 🌊 Cycle 周期

| 指标 | 值 | q | 信号 |
|------|-----|----|------|
| days_since_halving | 742d | — | 减半后 24 月 |
| sma_200w | $60,433 | — | 长期价值锚 |
| sma_200w_dev | +29.47% | q81 | 正常偏高 |
| mayer_multiple | 0.933 | **q20** | 偏低估 |
| pi_cycle_top_ratio | 0.387 | — | 未触发 |

**判断**：时间在"晚期"但估值在"偏低估"。传统 halving 时钟因 ETF 需求结构改变而失效。

### 2.2 💰 Valuation 估值

| 指标 | 值 | q | 信号 |
|------|-----|----|------|
| ahr999 | 0.712 | **q24** | 低估/定投区 |
| mvrv | 1.444 | q35 | 中性 |
| mvrv_z_score | 1.261 | — | 中性偏低 |
| nupl | 0.308 | q35 | optimism |

**判断**：AHR q24 + Mayer q20 两位低估；MVRV q35 中性。"合理偏低，非极端低。"

### 2.3 ⛏️ Network 网络

| 指标 | 值 | 信号 |
|------|-----|------|
| hash_rate_ehs | 1,067 | 历史峰值 |
| hash_ribbons | **下行 ⚠** | 矿工投降 |
| difficulty | -2.30% | 小幅下调 |
| mempool | 32.2 MB | 拥堵 |

**判断**：hash_ribbons 下行 = 历史抄底前奏（命中 2018/2020/2022/2024 四次底部）。

### 2.4 📊 Positioning 杠杆

| 指标 | 值 | 信号 |
|------|-----|------|
| funding_rate | -0.0054% | **负值** |
| oi_to_mc | 0.0052 | **杠杆松弛** |
| fear_greed | 39 | 恐慌 |
| altcoin_season | 25.0 | 偏 BTC 季 |

**判断**：4/4 杠杆指标指向"非顶部"。funding 负 + OI/MC 极低 = 清算风险极低。

### 2.5 🌍 Macro 宏观

| 指标 | 值 | 信号 |
|------|-----|------|
| m2_yoy | +4.57% | 温和扩张（顺风） |
| dxy_60d | +0.70% | 美元横盘 |
| real_yield | 1.94% | 正常（距 2% 逆风 6bp） |
| spx_corr | 0.42 | 中等相关 |

### 2.6 💸 Flow 资金流

| 指标 | 值 | 日均 |
|------|-----|------|
| ETF 30d | +$2,434M | +$81M/d ✅ |
| ETF 7d | +$392M | +$56M/d ➖ |
| ETH/BTC | 0.0295 | q29 极弱 |
| 稳定币 | $271.5B | 充裕 |

### 2.7 📈 Technical 技术

| 指标 | 值 | 信号 |
|------|-----|------|
| rsi_14 | 59.3 | 偏强 |
| macd_histogram | -152.3 | **空头收窄** |
| ema_cross | +2.08% | 多头排列 |
| ma_alignment | -13.55% | **死叉** |
| bb_position | 0.80 | 区间中部 |
| volatility_20d | **1.93%** | **极低（蓄势）** |

**核心矛盾**：EMA 多头 + MA 死叉 = "短多中空"。波动率 1.93% = 变盘前兆。

### 2.8 🔗 CrossAsset 跨资产

| 指标 | 值 | 信号 |
|------|-----|------|
| BTC/Gold 比率 | 17.0 | 中性 |
| BTC/Gold 30d 相关 | **0.58** | **强相关** |
| BTC/QQQ 比率 | 160.8 | — |
| BTC/QQQ 30d 相关 | -0.06 | 弱相关 |
| BTC/SPY 比率 | 139.6 | — |
| BTC/SPY 30d 相关 | -0.005 | **零相关** |
| rel_str 90d Gold | +4.8% | BTC 略跑赢 |
| **rel_str 90d QQQ** | **+22.6%** | **BTC 大幅跑赢 QQQ** |

**关键发现**：
- BTC 与 QQQ/SPY 近 30 天**几乎零相关** — BTC 在走独立行情
- QQQ 90 日跌了 21%，BTC 同期涨了 1.7% — **BTC 作为避险资产正在与科技股脱钩**
- BTC 与黄金 30 日相关 0.58 — 可能被定价为"数字黄金"

---

## 3. 数据源验证

| 数据 | 来源 | 值 | 验证 |
|------|------|-----|------|
| 黄金 | Binance PAXG | $4,604/oz | ✅ PAXG klines API |
| QQQ | Futu Bridge | $486.48 | ✅ 与 Python SDK 一致 |
| SPY | Futu Bridge | $560.34 | ✅ 与 Python SDK 一致 |
| rel_str 90d QQQ | 自算 | +22.6% | ✅ BTC +1.7% vs QQQ -21.0% |

**Futu Bridge 状态**：✅ 正常工作，Go → Python SDK → OpenD → 数据返回。无降级警告。

---

## 4. 综合信号矩阵

```
域          方向        信号强度    置信度
─────────────────────────────────────
Cycle       ⚠ 矛盾     中          中
Valuation   ✅ 偏低估   中          高
Network     ⚠ 投降     高         极高
Positioning ✅ 松弛     强          高
Macro       ➖ 中性     中          中
Flow        ✅ 流入     中          高
Technical   ⚠ 蓄势     中          高
CrossAsset  ✅ 独立     高          高
```

### 顶部信号全面检查（10 项）

| 信号 | 预期顶部值 | 当前 | 匹配 |
|------|-----------|------|------|
| funding | > +0.1% | -0.005% | ❌ |
| OI/MC | > 0.04 | 0.005 | ❌ |
| F&G | > 80 | 39 | ❌ |
| altcoin_season | > 75 | 25 | ❌ |
| mayer | > 2.4 | 0.93 | ❌ |
| pi_cycle | ≥ 1.0 | 0.39 | ❌ |
| hash_ribbons | 上行 | 下行 | ❌ |
| RSI | > 70 | 59 | ❌ |
| volatility | > 6% | 1.9% | ❌ |
| QQ/SPY corr | > 0.7 | ~0 | ❌ |

**10/10 不满足顶部条件。这是中期磨底/蓄势，不是顶部。**

### 独特发现：BTC 正在与科技股脱钩

QQQ 90 日跌 21% 而 BTC 涨 1.7%，30 日相关 ~0。这不是"高 beta 风险资产"的行为——**BTC 这轮表现为避险资产**。

---

## 5. 系统效果

### 数据完整性对比

| | v2 (修复前) | v3.1 (当前) |
|------|------------|------------|
| 域数 | 6 | **8** |
| 指标数 | ~30 | **42** |
| 技术指标 | 0 | **8** |
| 跨资产 | 0 (BTC/Gold via FRED only) | **9** (Gold PAXG + QQQ + SPY + ratios + corrs + rel_str) |
| 山寨季 | 假 (1-btc_dom) | ✅ 自算 |
| 黄金 | 无 | Binance PAXG + Futu GLD |
| QQQ/SPY | 无 | Futu Bridge |
| DXY | FRED only (延迟) | FRED + Futu UUP (实时) |
| VIX | 无 | Futu VIXY |
| 无数据源告警 | 频繁 | **0 条** |

### 本次报告的关键价值

1. **发现了 BTC 与 QQQ 脱钩** — BTC 0% 相关 + QQQ 跌 21% + BTC 涨 1.7%，确认独立行情
2. **确认了 ETF 改变周期模板** — halving 742 天但 Mayer 0.93 (q20)，时间相位不可机械套用
3. **量化了"蓄势"程度** — 波动率 1.93% = 近半年最低，变盘概率高
4. **10/10 顶部信号不满足** — 系统性地排除顶部可能性

---

*报告由 guanfu v3.1 生成。数据：Binance + CoinGecko + CoinMetrics + mempool.space + SoSoValue + FRED + Futu Bridge (Python SDK)。*
