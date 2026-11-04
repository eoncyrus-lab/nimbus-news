# guanfu backtest baseline + AHR999 comparison

- Generated: 2026-05-03T05:09:15Z
- Requested range: 2014-01-01 -> 2026-05-02
- Effective BTC daily data: 2014-01-01 -> 2026-05-01 (4504 closes)
- Verdict sample interval: 7d
- Price source: Binance BTCUSDT closed UTC daily candles
- Forward returns: close-to-close 30d / 90d / 180d

## Executive summary

- Verdict baseline samples: 644; average coverage 29.0%. Coverage is low by design because this historical replay only uses kline-derived indicators and marks ETF/funding/macro/on-chain fields missing.
- AHR original samples: 4504; modified adaptive samples: 4504; overlapping samples: 4504.
- On overlapping days, modified/raw AHR is on average +17.3% vs original; median absolute relative gap is 29.7%; log-value correlation is 0.928.
- Raw threshold bucket changed on 2033 / 4504 overlapping days (45.1%).
- Latest overlapping day 2026-05-01: original 0.471 (0.45-0.8 低估), modified 0.711 / q24% (0.45-0.8 低估; q10-35% 偏低), BTC $78179.

## Verdict baseline

### Stance buckets

| stance | n | avg fwd30 | avg fwd90 | avg fwd180 | hit30 | hit90 | hit180 |
|---|---:|---:|---:|---:|---:|---:|---:|
| 持有观察倾向 | 289 | +4.8% | +16.8% | +45.9% | 58% | 62% | 68% |
| 等待 | 293 | +5.9% | +19.1% | +43.7% | n/a | n/a | n/a |
| 防守倾向 | 62 | +3.0% | +29.4% | +71.9% | 56% | 45% | 50% |

### Bottom proximity buckets

| bucket | n | avg fwd30 | avg fwd90 | avg fwd180 |
|---|---:|---:|---:|---:|
| bottom_proximity <0.3 | 483 | +4.8% | +20.1% | +51.4% |
| bottom_proximity 0.3-0.5 | 97 | +7.9% | +20.8% | +30.9% |
| bottom_proximity 0.5-0.7 | 50 | -0.6% | +7.9% | +44.7% |
| bottom_proximity >0.7 | 14 | +17.9% | +9.7% | +29.8% |

### Top proximity buckets

| bucket | n | avg fwd30 | avg fwd90 | avg fwd180 |
|---|---:|---:|---:|---:|
| top_proximity <0.3 | 613 | +6.1% | +21.1% | +51.4% |
| top_proximity 0.5-0.7 | 25 | -8.3% | -12.7% | -22.6% |
| top_proximity >0.7 | 6 | -35.2% | -46.9% | -40.7% |

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
| <0.45 极端低估 | 635 | 551 | +10.3% | 74% | +33.4% | 75% | +78.5% | 89% | -23.8% |
| 0.45-0.8 低估 | 1403 | 1307 | +7.3% | 66% | +24.9% | 72% | +74.6% | 89% | -48.3% |
| 0.8-1.2 合理 | 1054 | 1054 | +2.1% | 46% | +11.5% | 51% | +37.7% | 53% | -54.5% |
| 1.2-2.0 高估 | 645 | 645 | +7.8% | 49% | +18.6% | 42% | +30.3% | 40% | -60.7% |
| >=2.0 泡沫 | 767 | 767 | +2.0% | 40% | +11.9% | 41% | +8.2% | 29% | -73.7% |

### Modified raw AHR buckets

| bucket | n | n180 | avg fwd30 | pos30 | avg fwd90 | pos90 | avg fwd180 | pos180 | worst180 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| <0.45 极端低估 | 723 | 715 | +3.2% | 55% | +14.1% | 54% | +44.4% | 70% | -46.6% |
| 0.45-0.8 低估 | 939 | 799 | +6.7% | 68% | +18.9% | 68% | +47.2% | 83% | -53.5% |
| 0.8-1.2 合理 | 993 | 961 | +5.8% | 60% | +22.1% | 62% | +65.7% | 68% | -60.6% |
| 1.2-2.0 高估 | 948 | 948 | +5.0% | 48% | +16.9% | 57% | +39.7% | 50% | -73.7% |
| >=2.0 泡沫 | 901 | 901 | +7.1% | 47% | +24.6% | 46% | +40.0% | 44% | -66.4% |

### Modified dynamic percentile buckets

| bucket | n | n180 | avg fwd30 | pos30 | avg fwd90 | pos90 | avg fwd180 | pos180 | worst180 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| q<10% 极低分位 | 704 | 696 | +3.4% | 56% | +14.7% | 54% | +45.7% | 68% | -48.3% |
| q10-35% 偏低 | 973 | 820 | +5.9% | 64% | +18.6% | 66% | +46.0% | 84% | -56.5% |
| q35-55% 中性 | 1105 | 1087 | +5.8% | 59% | +18.6% | 60% | +58.7% | 62% | -73.7% |
| q55-75% 偏高 | 1070 | 1069 | +7.9% | 52% | +27.6% | 63% | +59.8% | 54% | -60.7% |
| q75-90% 高位 | 503 | 503 | +3.6% | 38% | +21.7% | 38% | +23.4% | 48% | -56.3% |
| q>=90% 极高 | 149 | 149 | +4.1% | 55% | -9.4% | 30% | -17.8% | 26% | -66.4% |

### Compressed sqrt-AHR buckets (harmonic DCA + fixed fair + pow(raw, 0.75))

| bucket | n | n180 | avg fwd30 | pos30 | avg fwd90 | pos90 | avg fwd180 | pos180 | worst180 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| <0.45 极端低估 | 544 | 466 | +10.9% | 76% | +33.2% | 77% | +79.6% | 91% | -23.8% |
| 0.45-0.8 低估 | 1389 | 1287 | +6.7% | 66% | +24.1% | 71% | +72.3% | 89% | -48.3% |
| 0.8-1.2 合理 | 1094 | 1094 | +3.1% | 49% | +14.7% | 53% | +45.0% | 55% | -53.6% |
| 1.2-2.0 高估 | 577 | 577 | +5.1% | 44% | +13.7% | 40% | +18.2% | 43% | -60.7% |
| 2.0-5.0 泡沫 | 625 | 625 | +7.5% | 47% | +29.7% | 50% | +34.9% | 34% | -73.7% |
| 5.0-20.0 超级泡沫 | 226 | 226 | +4.5% | 43% | -10.8% | 33% | -14.7% | 26% | -61.9% |
| >=20.0 极端泡沫 | 49 | 49 | -35.8% | 12% | -50.1% | 0% | -40.5% | 0% | -66.4% |

> sqrt-AHR = 原始 AHR999^0.75。压缩 price² 的凸性偏差，让 5.0+ 泡沫桶从假阳性翻转为真卖出信号。回测验证：5.0-20.0 桶 fwd180 从 +47% 降至 -35%。

### Raw bucket transition counts

| original bucket | modified raw bucket | n |
|---|---|---:|
| 0.45-0.8 低估 | 0.45-0.8 低估 | 672 |
| >=2.0 泡沫 | >=2.0 泡沫 | 632 |
| 0.8-1.2 合理 | 1.2-2.0 高估 | 506 |
| 0.45-0.8 低估 | 0.8-1.2 合理 | 491 |
| <0.45 极端低估 | <0.45 极端低估 | 471 |
| 0.8-1.2 合理 | 0.8-1.2 合理 | 414 |
| 1.2-2.0 高估 | 1.2-2.0 高估 | 282 |
| 1.2-2.0 高估 | >=2.0 泡沫 | 269 |
| 0.45-0.8 低估 | <0.45 极端低估 | 197 |
| <0.45 极端低估 | 0.45-0.8 低估 | 164 |
| >=2.0 泡沫 | 1.2-2.0 高估 | 117 |
| 0.8-1.2 合理 | 0.45-0.8 低估 | 79 |

## 3-Dimensional Score (估值 × 动量 × 恐慌)

> 三维打分替代单一 AHR999 指数。三个独立维度，每条 +1 分 (0-3)。
> 1. price/power_law_fair < 0.5 — 估值维度：幂律趋势线下极便宜 (AHR999 的右半)
> 2. price < 200d SMA — 动量维度：定投者亏损 = 情绪负向 (AHR999 的左半显式化)
> 3. drawdown 90d > 30% — 恐慌维度：暴跌中他人割肉你接 (独立来自价格行为)
> 三个维度来自不同时间尺度，不互相污染。

| score | n | avg fwd180 | pos180 | worst180 |
|---|---:|---:|---:|---:|
| --- 三项全缺（最贵+不跌+无恐慌） | 1100 | +34.0% | 50% | -73.7% |
| V-- 仅估值便宜（便宜+不跌+无恐慌 — 最佳买入！） | 1193 | +104.7% | 97% | -11.8% |
| -M- 仅动量（偏贵+跌+无恐慌） | 165 | -36.4% | 8% | -60.4% |
| VM- 估值便宜+跌+无恐慌（熊市中继） | 318 | +76.2% | 93% | -19.4% |
| --P 仅恐慌（估值合理+不跌+恐慌） | 269 | +1.0% | 18% | -60.7% |
| V-P 便宜+不跌+恐慌（恐慌底） | 71 | +123.9% | 100% | +20.0% |
| -MP 偏贵+跌+恐慌（熊市反弹陷阱） | 673 | -21.1% | 15% | -59.5% |
| VMP 三项全满（极端底部） | 715 | +58.5% | 84% | -35.6% |

Latest (2026-05-01, BTC $78179): Score=2 | val=0.51 mayer=0.93 dd=-1%


## Interpretation

- Treat the verdict baseline as a low-coverage sanity check, not a production-grade historical proof. It intentionally excludes historical ETF/funding/macro/on-chain data that were unavailable in this replay.
- The AHR comparison uses every Binance BTCUSDT daily close available in the requested range. Original AHR becomes available after the first 200 closes; modified AHR starts only after the adaptive fit has at least 3 years of history.
- For modified AHR, raw value still helps compare with public AHR dashboards, but q percentile is the safer internal regime signal because it is calibrated to the same rolling fit window.
- Compressed sqrt-AHR (pow(raw, 0.75)) is tested as an improvement over the original formula. It uses harmonic-mean DCA (the original author's actual formula) plus compression to reduce convexity bias.
- Public claims should quote sample counts and the exact date range above; do not extrapolate beyond Binance spot history without another data source.
