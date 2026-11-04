# guanfu backtest baseline + AHR999 comparison

- Generated: 2026-05-03T15:37:49Z
- Requested range: 2010-07-18 -> 2026-05-02
- Effective BTC daily data: 2010-07-18 -> 2026-05-02 (5768 closes)
- Verdict sample interval: 7d
- Price source: CoinMetrics PriceUSD full daily history + Binance BTCUSDT latest daily overlay
- Forward returns: close-to-close 30d / 90d / 180d

## Executive summary

- Verdict baseline samples: 824; average coverage 28.4%. Coverage is low by design because this historical replay only uses kline-derived indicators and marks ETF/funding/macro/on-chain fields missing.
- AHR original samples: 5569; modified adaptive samples: 4674; overlapping samples: 4674.
- On overlapping days, modified/raw AHR is on average +16.6% vs original; median absolute relative gap is 29.4%; log-value correlation is 0.932.
- Raw threshold bucket changed on 2088 / 4674 overlapping days (44.7%).
- Latest overlapping day 2026-05-02: original 0.477 (0.45-0.8 低估), modified 0.720 / q24% (0.45-0.8 低估; q10-35% 偏低), BTC $78687.

## Verdict baseline

### Stance buckets

| stance | n | avg fwd30 | avg fwd90 | avg fwd180 | hit30 | hit90 | hit180 |
|---|---:|---:|---:|---:|---:|---:|---:|
| 持有观察倾向 | 368 | +16.0% | +57.3% | +108.1% | 68% | 72% | 83% |
| 等待 | 330 | +18.1% | +74.8% | +301.0% | n/a | n/a | n/a |
| 防守倾向 | 124 | +2.3% | +49.8% | +85.2% | 64% | 58% | 61% |
| 高防守倾向 | 2 | -18.7% | +13.0% | +347.9% | 100% | 0% | 50% |

### Bottom proximity buckets

| bucket | n | avg fwd30 | avg fwd90 | avg fwd180 |
|---|---:|---:|---:|---:|
| bottom_proximity <0.3 | 696 | +15.9% | +69.2% | +204.3% |
| bottom_proximity 0.3-0.5 | 59 | +3.5% | +13.0% | +24.6% |
| bottom_proximity 0.5-0.7 | 49 | +6.4% | +42.7% | +102.1% |
| bottom_proximity >0.7 | 20 | +24.4% | +24.7% | +42.0% |

### Top proximity buckets

| bucket | n | avg fwd30 | avg fwd90 | avg fwd180 |
|---|---:|---:|---:|---:|
| top_proximity <0.3 | 734 | +14.7% | +62.6% | +188.5% |
| top_proximity 0.3-0.5 | 8 | +98.3% | +44.7% | +48.9% |
| top_proximity 0.5-0.7 | 48 | -11.0% | -9.7% | +145.7% |
| top_proximity >0.7 | 34 | +31.7% | +180.2% | +149.5% |

## AHR999 formula comparison

| dimension | original AHR999 | guanfu modified AHR999 |
|---|---|---|
| DCA cost | 200d arithmetic SMA | 200d harmonic fixed-amount DCA cost |
| fair value | fixed `10^(5.84*log10(days)-17.01)` curve | rolling log-log fit, 8y max window, 4y half-life, one-step Huber reweighting |
| classification | fixed raw thresholds 0.45 / 0.8 / 1.2 / 2.0 | raw value plus dynamic percentile q from same adaptive window |
| structural risk | fixed coefficients can stale after new market regimes | adapts to recent 8y data but has fewer early samples and can re-center after extreme cycles |
| compressed sqrt-AHR | — | raw = (price/harmonic_dca) × (price/fixed_fair), then pow(raw, 0.75). Same thresholds. Reduces convexity bias; makes 5.0+ a real sell signal |

### Original raw AHR buckets

| bucket | n | n180 | avg fwd30 | pos30 | avg fwd90 | pos90 | avg fwd180 | pos180 | worst180 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| <0.45 极端低估 | 708 | 624 | +12.5% | 73% | +37.8% | 79% | +78.4% | 91% | -23.6% |
| 0.45-0.8 低估 | 1684 | 1588 | +8.9% | 68% | +46.4% | 75% | +122.0% | 91% | -48.4% |
| 0.8-1.2 合理 | 1192 | 1192 | +3.9% | 45% | +24.6% | 52% | +67.5% | 57% | -54.2% |
| 1.2-2.0 高估 | 796 | 796 | +20.6% | 55% | +105.1% | 50% | +111.4% | 50% | -60.6% |
| >=2.0 泡沫 | 1189 | 1189 | +20.3% | 41% | +78.0% | 44% | +70.5% | 37% | -90.1% |

### Modified raw AHR buckets

| bucket | n | n180 | avg fwd30 | pos30 | avg fwd90 | pos90 | avg fwd180 | pos180 | worst180 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| <0.45 极端低估 | 730 | 722 | +1.6% | 53% | +11.8% | 52% | +42.0% | 69% | -46.7% |
| 0.45-0.8 低估 | 953 | 811 | +6.2% | 67% | +18.8% | 68% | +46.8% | 83% | -56.1% |
| 0.8-1.2 合理 | 996 | 966 | +6.0% | 59% | +22.2% | 61% | +64.4% | 67% | -63.2% |
| 1.2-2.0 高估 | 946 | 946 | +7.5% | 49% | +31.8% | 59% | +48.9% | 52% | -72.2% |
| >=2.0 泡沫 | 1049 | 1049 | +15.4% | 51% | +50.1% | 49% | +72.9% | 47% | -66.8% |

### Modified dynamic percentile buckets

| bucket | n | n180 | avg fwd30 | pos30 | avg fwd90 | pos90 | avg fwd180 | pos180 | worst180 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| q<10% 极低分位 | 725 | 717 | +2.0% | 55% | +12.6% | 53% | +43.0% | 67% | -48.4% |
| q10-35% 偏低 | 961 | 807 | +5.9% | 65% | +19.2% | 67% | +47.6% | 85% | -57.3% |
| q35-55% 中性 | 1146 | 1128 | +7.8% | 59% | +33.8% | 59% | +65.9% | 62% | -72.2% |
| q55-75% 偏高 | 1126 | 1126 | +13.7% | 54% | +48.2% | 65% | +89.3% | 56% | -60.6% |
| q75-90% 高位 | 555 | 555 | +8.0% | 41% | +22.2% | 39% | +22.1% | 47% | -56.7% |
| q>=90% 极高 | 161 | 161 | +2.1% | 50% | -11.4% | 27% | -20.1% | 21% | -66.8% |

### Compressed sqrt-AHR buckets (harmonic DCA + fixed fair + pow(raw, 0.75))

| bucket | n | n180 | avg fwd30 | pos30 | avg fwd90 | pos90 | avg fwd180 | pos180 | worst180 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| <0.45 极端低估 | 592 | 514 | +13.5% | 76% | +37.6% | 80% | +80.0% | 92% | -23.6% |
| 0.45-0.8 低估 | 1653 | 1551 | +8.9% | 69% | +46.1% | 75% | +117.2% | 91% | -48.4% |
| 0.8-1.2 合理 | 1213 | 1213 | +4.0% | 48% | +21.6% | 55% | +74.2% | 59% | -53.8% |
| 1.2-2.0 高估 | 681 | 681 | +8.6% | 44% | +33.7% | 42% | +38.1% | 50% | -60.6% |
| 2.0-5.0 泡沫 | 796 | 796 | +34.4% | 56% | +152.1% | 59% | +129.5% | 48% | -72.2% |
| 5.0-20.0 超级泡沫 | 408 | 408 | +12.3% | 38% | +119.1% | 45% | +154.5% | 49% | -61.8% |
| >=20.0 极端泡沫 | 226 | 226 | +14.6% | 27% | -29.4% | 15% | -41.9% | 5% | -90.1% |

> sqrt-AHR = 原始 AHR999^0.75。压缩 price² 的凸性偏差，让 5.0+ 泡沫桶从假阳性翻转为真卖出信号。回测验证：5.0-20.0 桶 fwd180 从 +47% 降至 -35%。

### Raw bucket transition counts

| original bucket | modified raw bucket | n |
|---|---|---:|
| >=2.0 泡沫 | >=2.0 泡沫 | 736 |
| 0.45-0.8 低估 | 0.45-0.8 低估 | 674 |
| 0.8-1.2 合理 | 1.2-2.0 高估 | 498 |
| 0.45-0.8 低估 | 0.8-1.2 合理 | 488 |
| <0.45 极端低估 | <0.45 极端低估 | 463 |
| 0.8-1.2 合理 | 0.8-1.2 合理 | 410 |
| 1.2-2.0 高估 | >=2.0 泡沫 | 312 |
| 1.2-2.0 高估 | 1.2-2.0 高估 | 303 |
| 0.45-0.8 低估 | <0.45 极端低估 | 202 |
| <0.45 极端低估 | 0.45-0.8 低估 | 170 |
| >=2.0 泡沫 | 1.2-2.0 高估 | 100 |
| 0.8-1.2 合理 | 0.45-0.8 低估 | 70 |

## 3-Dimensional Score (估值 × 动量 × 恐慌)

> 三维打分替代单一 AHR999 指数。三个独立维度，每条 +1 分 (0-3)。
> 1. price/power_law_fair < 0.5 — 估值维度：幂律趋势线下极便宜 (AHR999 的右半)
> 2. price < 200d SMA — 动量维度：定投者亏损 = 情绪负向 (AHR999 的左半显式化)
> 3. drawdown 90d > 30% — 恐慌维度：暴跌中他人割肉你接 (独立来自价格行为)
> 三个维度来自不同时间尺度，不互相污染。

| score | n | avg fwd180 | pos180 | worst180 |
|---|---:|---:|---:|---:|
| --- 三项全缺（最贵+不跌+无恐慌） | 1374 | +81.5% | 55% | -90.1% |
| V-- 仅估值便宜（便宜+不跌+无恐慌 — 最佳买入！） | 1597 | +265.0% | 98% | -11.9% |
| -M- 仅动量（偏贵+跌+无恐慌） | 193 | -30.8% | 16% | -64.8% |
| VM- 估值便宜+跌+无恐慌（熊市中继） | 315 | +77.2% | 92% | -19.2% |
| --P 仅恐慌（估值合理+不跌+恐慌） | 539 | +147.2% | 37% | -84.3% |
| V-P 便宜+不跌+恐慌（恐慌底） | 223 | +1615.0% | 100% | +20.2% |
| -MP 偏贵+跌+恐慌（熊市反弹陷阱） | 726 | -16.9% | 18% | -59.5% |
| VMP 三项全满（极端底部） | 801 | +63.7% | 86% | -36.1% |

Latest (2026-05-02, BTC $78687): Score=2 | val=0.51 mayer=0.94 dd=-0%


## Interpretation

- Treat the verdict baseline as a low-coverage sanity check, not a production-grade historical proof. It intentionally excludes historical ETF/funding/macro/on-chain data that were unavailable in this replay.
- The AHR comparison uses the same BTC daily history chain as production: CoinMetrics PriceUSD from 2010-07-18 plus Binance BTCUSDT latest daily overlay. Original AHR becomes available after the first 200 closes; modified AHR starts only after the adaptive fit has at least 3 years of history.
- For modified AHR, raw value still helps compare with public AHR dashboards, but q percentile is the safer internal regime signal because it is calibrated to the same rolling fit window.
- Compressed sqrt-AHR (pow(raw, 0.75)) is tested as an improvement over the original formula. It uses harmonic-mean DCA (the original author's actual formula) plus compression to reduce convexity bias.
- Public claims should quote sample counts and the exact date range above; do not extrapolate beyond Binance spot history without another data source.
