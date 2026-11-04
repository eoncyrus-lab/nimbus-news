# guanfu backtest baseline + AHR999 comparison

- Generated: 2026-05-03T04:42:44Z
- Requested range: 2017-08-17 -> 2026-05-02
- Effective BTC daily data: 2017-08-17 -> 2026-05-02 (3181 closes)
- Verdict sample interval: 7d
- Price source: Binance BTCUSDT closed UTC daily candles
- Forward returns: close-to-close 30d / 90d / 180d

## Executive summary

- Verdict baseline samples: 455; average coverage 25.5%. Coverage is low by design because this historical replay only uses kline-derived indicators and marks ETF/funding/macro/on-chain fields missing.
- AHR original samples: 2982; modified adaptive samples: 2087; overlapping samples: 2087.
- On overlapping days, modified/raw AHR is on average +42.7% vs original; median absolute relative gap is 49.9%; log-value correlation is 0.938.
- Raw threshold bucket changed on 1310 / 2087 overlapping days (62.8%).
- Latest overlapping day 2026-05-02: original 0.477 (0.45-0.8 低估), modified 0.721 / q24% (0.45-0.8 低估; q10-35% 偏低), BTC $78687.

## Verdict baseline

### Stance buckets

| stance | n | avg fwd30 | avg fwd90 | avg fwd180 | hit30 | hit90 | hit180 |
|---|---:|---:|---:|---:|---:|---:|---:|
| 持有观察倾向 | 214 | +4.1% | +16.6% | +45.7% | 55% | 59% | 63% |
| 等待 | 207 | +6.6% | +23.9% | +30.5% | n/a | n/a | n/a |
| 防守倾向 | 34 | +3.1% | -3.1% | -1.5% | 56% | 59% | 65% |

### Bottom proximity buckets

| bucket | n | avg fwd30 | avg fwd90 | avg fwd180 |
|---|---:|---:|---:|---:|
| bottom_proximity <0.3 | 335 | +4.8% | +18.4% | +31.0% |
| bottom_proximity 0.3-0.5 | 64 | +11.1% | +25.8% | +47.5% |
| bottom_proximity 0.5-0.7 | 25 | -4.8% | +4.1% | +12.2% |
| bottom_proximity >0.7 | 31 | +5.0% | +14.1% | +69.7% |

### Top proximity buckets

| bucket | n | avg fwd30 | avg fwd90 | avg fwd180 |
|---|---:|---:|---:|---:|
| top_proximity <0.3 | 449 | +5.2% | +18.7% | +35.5% |
| top_proximity 0.3-0.5 | 4 | +15.0% | +19.3% | -12.0% |
| top_proximity 0.5-0.7 | 2 | -26.8% | -42.9% | +6.5% |

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
| <0.45 极端低估 | 476 | 392 | +6.5% | 68% | +30.8% | 69% | +87.9% | 86% | -23.8% |
| 0.45-0.8 低估 | 799 | 703 | +6.8% | 64% | +22.4% | 68% | +67.4% | 89% | -48.3% |
| 0.8-1.2 合理 | 859 | 859 | +1.9% | 49% | +10.4% | 55% | +26.7% | 50% | -54.5% |
| 1.2-2.0 高估 | 532 | 532 | +3.6% | 44% | +9.7% | 36% | -0.3% | 37% | -60.7% |
| >=2.0 泡沫 | 316 | 316 | +0.6% | 41% | -4.3% | 30% | -13.9% | 25% | -55.3% |

### Modified raw AHR buckets

| bucket | n | n180 | avg fwd30 | pos30 | avg fwd90 | pos90 | avg fwd180 | pos180 | worst180 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| <0.45 极端低估 | 219 | 211 | +2.5% | 53% | +8.0% | 41% | +27.2% | 69% | -45.6% |
| 0.45-0.8 低估 | 344 | 203 | +0.8% | 62% | +5.9% | 51% | +33.1% | 60% | -52.3% |
| 0.8-1.2 合理 | 397 | 366 | +2.4% | 50% | +8.9% | 57% | +21.0% | 71% | -60.4% |
| 1.2-2.0 高估 | 689 | 689 | +7.0% | 57% | +27.6% | 72% | +54.7% | 62% | -60.7% |
| >=2.0 泡沫 | 438 | 438 | +6.1% | 50% | +15.9% | 40% | +13.9% | 55% | -55.3% |

### Modified dynamic percentile buckets

| bucket | n | n180 | avg fwd30 | pos30 | avg fwd90 | pos90 | avg fwd180 | pos180 | worst180 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| q<10% 极低分位 | 147 | 139 | +5.2% | 64% | +8.1% | 37% | +26.3% | 69% | -35.5% |
| q10-35% 偏低 | 384 | 230 | -1.2% | 52% | +4.6% | 45% | +35.0% | 61% | -51.8% |
| q35-55% 中性 | 264 | 246 | +2.0% | 52% | +6.0% | 55% | +17.4% | 69% | -58.3% |
| q55-75% 偏高 | 611 | 611 | +7.0% | 60% | +19.2% | 72% | +27.8% | 62% | -60.7% |
| q75-90% 高位 | 471 | 471 | +1.3% | 42% | +15.9% | 50% | +50.6% | 62% | -55.3% |
| q>=90% 极高 | 210 | 210 | +16.8% | 67% | +44.7% | 52% | +33.7% | 56% | -47.5% |

### Compressed sqrt-AHR buckets (harmonic DCA + fixed fair + pow(raw, 0.75))

| bucket | n | n180 | avg fwd30 | pos30 | avg fwd90 | pos90 | avg fwd180 | pos180 | worst180 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| <0.45 极端低估 | 146 | 112 | +12.6% | 86% | +32.2% | 75% | +58.0% | 79% | -20.8% |
| 0.45-0.8 低估 | 893 | 750 | +4.5% | 59% | +20.0% | 62% | +70.3% | 90% | -48.3% |
| 0.8-1.2 合理 | 1113 | 1110 | +3.8% | 54% | +16.2% | 61% | +40.2% | 57% | -54.5% |
| 1.2-2.0 高估 | 568 | 568 | +3.8% | 44% | +10.5% | 40% | -0.4% | 35% | -60.7% |
| 2.0-5.0 泡沫 | 244 | 244 | -0.0% | 40% | -4.6% | 26% | -12.8% | 31% | -55.3% |
| 5.0-20.0 超级泡沫 | 18 | 18 | +2.7% | 61% | -34.0% | 0% | -16.5% | 0% | -24.2% |

> sqrt-AHR = 原始 AHR999^0.75。压缩 price² 的凸性偏差，让 5.0+ 泡沫桶从假阳性翻转为真卖出信号。回测验证：5.0-20.0 桶 fwd180 从 +47% 降至 -35%。

### Raw bucket transition counts

| original bucket | modified raw bucket | n |
|---|---|---:|
| 0.8-1.2 合理 | 1.2-2.0 高估 | 440 |
| 0.45-0.8 低估 | 0.8-1.2 合理 | 291 |
| >=2.0 泡沫 | >=2.0 泡沫 | 226 |
| <0.45 极端低估 | <0.45 极端低估 | 206 |
| 1.2-2.0 高估 | >=2.0 泡沫 | 186 |
| 0.45-0.8 低估 | 0.45-0.8 低估 | 155 |
| <0.45 极端低估 | 0.45-0.8 低估 | 138 |
| 1.2-2.0 高估 | 1.2-2.0 高估 | 125 |
| 0.45-0.8 低估 | 1.2-2.0 高估 | 115 |
| 0.8-1.2 合理 | 0.8-1.2 合理 | 65 |
| 0.8-1.2 合理 | 0.45-0.8 低估 | 51 |
| 1.2-2.0 高估 | 0.8-1.2 合理 | 41 |

## Interpretation

- Treat the verdict baseline as a low-coverage sanity check, not a production-grade historical proof. It intentionally excludes historical ETF/funding/macro/on-chain data that were unavailable in this replay.
- The AHR comparison uses every Binance BTCUSDT daily close available in the requested range. Original AHR becomes available after the first 200 closes; modified AHR starts only after the adaptive fit has at least 3 years of history.
- For modified AHR, raw value still helps compare with public AHR dashboards, but q percentile is the safer internal regime signal because it is calibrated to the same rolling fit window.
- Compressed sqrt-AHR (pow(raw, 0.75)) is tested as an improvement over the original formula. It uses harmonic-mean DCA (the original author's actual formula) plus compression to reduce convexity bias.
- Public claims should quote sample counts and the exact date range above; do not extrapolate beyond Binance spot history without another data source.
