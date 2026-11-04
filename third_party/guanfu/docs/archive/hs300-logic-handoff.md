# HS300 Logic Handoff

> Scope: this document is for implementing HS300 / CSI300 logic in another project.
> `guanfu` no longer owns A-share or Hong Kong market logic. Do not wire this back
> into `guanfu` CLI, MCP, refresh, forecast, docs, or skills.

## 1. Goal

Build a standalone HS300 module that can:

- ingest CSI300 price, valuation, macro, flow, and positioning data;
- produce a multi-domain indicator panel;
- produce an evidence-based verdict;
- optionally run kNN-style historical analogue forecasts, but only behind a strict reliability gate.

The old `guanfu` implementation treated HS300 as `hs300`, a China A-share large-cap index module built around:

- daily CSI300 close price;
- PE / PB valuation;
- China macro: PMI, M2 YoY, LPR, USD/CNY;
- flow: northbound net buy and volume;
- technical / momentum / drawdown indicators.

## 2. Data Model

Use a simple daily point format for all series:

```go
type PricePoint struct {
    Date   string  // YYYY-MM-DD
    Close  float64
    Source string
}
```

Store each series oldest-first. For monthly macro series, normalize the date to the first day of the month and forward-fill during feature extraction.

Recommended keys:

| Key | Meaning | Frequency | Preferred source |
| --- | --- | --- | --- |
| `hs300` | CSI300 close | daily | AkShare `stock_zh_index_daily("sh000300")` or Yahoo `000300.SS` |
| `hs300_pe` | CSI300 PE | daily / irregular | AkShare `index_value_name_funddb("000300", "市盈率")` |
| `hs300_pb` | CSI300 PB | daily / irregular | AkShare `index_value_name_funddb("000300", "市净率")` |
| `usd_cny` | USD/CNY spot | daily | Yahoo `CNY=X` |
| `hs300_pmi` | China manufacturing PMI | monthly | AkShare `macro_china_pmi()` |
| `hs300_m2` | China M2 YoY | monthly | AkShare `macro_china_money_supply()` |
| `hs300_lpr` | China 1Y LPR | daily / event | AkShare `macro_china_lpr()` |
| `hs300_northbound` | Northbound daily net buy | daily | AkShare `stock_hsgt_hist_em("北向资金")` |
| `hs300_volume` | CSI300 volume | daily | AkShare index daily volume |
| `hs300_cpi` | China CPI YoY | monthly | AkShare `macro_china_cpi_yearly()` |
| `hs300_margin` | SSE + SZSE margin balance | daily | AkShare `stock_margin_sse()` + `stock_margin_szse()` |

Avoid AkShare USD/CNY endpoints if possible; the old implementation treated them as unstable and used Yahoo `CNY=X`.

## 3. Refresh Pipeline

Use source-specific adapters that return full oldest-first point slices. Merge with local storage using append + dedupe by `Date`.

Recommended behavior:

1. If local last date is fresh, skip refresh.
2. If stale, fetch the source.
3. Normalize dates to `YYYY-MM-DD`.
4. Drop rows with missing numeric values.
5. Append and dedupe by date.
6. Persist `Source` per point for auditability.

For AkShare bridge style implementations, stdin/stdout JSON is enough:

```json
{"mode":"hs300_macro","series":"pmi"}
```

Return:

```json
{
  "pmi": {
    "points": [
      {"date":"2026-04-01","close":50.4}
    ],
    "count": 1
  }
}
```

## 4. Indicator Panel

Minimum price history: 200 daily closes for the full dashboard. With less than 200 but at least 14 daily closes, only a reduced technical panel is possible.

### Valuation

Indicators:

- `sma_200_dev`: `(price - SMA200) / SMA200 * 100`
- `pe`: CSI300 PE
- `pb`: CSI300 PB

PE zones:

| PE | Label |
| --- | --- |
| `< 10` | low valuation |
| `10-13` | neutral-low |
| `13-18` | neutral |
| `18-25` | neutral-high |
| `> 25` | high valuation |

### Technical

Indicators:

- `rsi_14`
- `macd`: MACD, signal, histogram
- `volatility_30d`
- optional: Bollinger band width if your target project already has it

Use the same definitions as broad equity index technicals.

### Flow

Indicators:

- `volume_trend`: old implementation used a price-range proxy if real volume was missing.
- `flow_sentiment`: 20-day price momentum.
- `northbound_30d`: 30-day cumulative northbound net buy.

Labels:

| Condition | Label |
| --- | --- |
| `volume_proxy > 3` and price above SMA200 | volume-backed rally / inflow |
| `volume_proxy > 3` and price below SMA200 | volume-backed selloff / outflow |
| `volume_proxy < 1` | low-volume wait-and-see |
| 20d momentum `> 5%` | short-term strong |
| 20d momentum `< -5%` | short-term weak |

Northbound labels, unit: CNY 100 million:

| 30d flow | Label |
| --- | --- |
| `>= 500` | strong inflow |
| `100 to 500` | inflow |
| `-100 to 100` | neutral |
| `-500 to -100` | outflow |
| `< -500` | strong outflow |

### Macro

Indicators:

- `usd_cny`: higher means CNY weakness and A-share headwind.
- `pmi`: manufacturing PMI.
- `m2_yoy`: M2 YoY growth.
- `lpr_1y`: 1-year LPR.

USD/CNY labels:

```text
dev = (usd_cny - 7.0) / 7.0 * 100
dev > 3%   => CNY depreciation, A-share headwind
dev < -1%  => CNY appreciation, A-share tailwind
otherwise => stable
```

PMI labels:

| PMI | Label |
| --- | --- |
| `>= 52` | strong expansion |
| `50-52` | weak expansion |
| `48-50` | weak contraction |
| `< 48` | strong contraction |

M2 YoY labels:

| M2 YoY | Label |
| --- | --- |
| `>= 12%` | strong easing |
| `9-12%` | neutral-easy |
| `7-9%` | neutral |
| `< 7%` | tight |

LPR labels:

| 1Y LPR | Label |
| --- | --- |
| `<= 3.2%` | very easy |
| `3.2-3.6%` | easy |
| `3.6-4.0%` | neutral |
| `> 4.0%` | tight |

### Cycle / Momentum

Indicators:

- `momentum_90d`
- `momentum_180d`
- `drawdown_200d`: current price versus 200-day high.

## 5. Top / Bottom Proximity

These are heuristic diagnostics, not trade signals.

Top proximity:

```text
components:
  RSI contribution = max(0, (RSI14 - 70) / 30)
  PE contribution  = min(1, (PE - 18) / 12), only when PE > 18

top_proximity = min(1, average(available components))
```

Bottom proximity:

```text
components:
  RSI contribution      = max(0, (30 - RSI14) / 30)
  PE contribution       = min(1, (13 - PE) / 5), only when PE < 13
  drawdown contribution = max(0, (-drawdown_200d - 15) / 20)

bottom_proximity = min(1, average(available components))
```

## 6. Verdict Logic

The old implementation scored five domains:

| Domain | Bullish | Bearish |
| --- | --- | --- |
| valuation | PE `< 10` | PE `> 25` |
| technical | RSI `< 30` | RSI `> 70` |
| momentum | 90d momentum `> 10%` | 90d momentum `< -10%` |
| macro | USD/CNY `< 6.8` | USD/CNY `> 7.3` |
| flow | volume proxy `> 3` | volume proxy `< 1` |

Each bullish condition adds `+1`; each bearish condition adds `-1`.

Regime:

| Net score | Regime | Stance |
| --- | --- | --- |
| `>= 2` | accumulation zone | valuation reasonable/low, technicals improving, currency supportive |
| `<= -2` | caution zone | valuation high or technical breakdown, currency headwind |
| otherwise | neutral | wait for confirmation |

Coverage:

```text
coverage = available_indicator_count / total_indicator_count
```

Confidence should be derived from coverage. Missing macro data should lower confidence rather than produce false precision.

## 7. Forecast Feature Bundle

The old kNN forecast bundle was:

- generic technical extractors;
- PMI;
- M2 YoY;
- 1Y LPR;
- 30d cumulative northbound flow.

Feature normalization:

| Feature | Normalization | Weight |
| --- | --- | --- |
| `pmi` | `clip((PMI - 50) / 3, -3, 3)` | `0.60` |
| `m2_yoy` | `clip((M2 - 9) / 4, -3, 3)` | `0.50` |
| `lpr_1y` | `clip(-(LPR - 3.7) / 1.0, -3, 3)` | `0.40` |
| `northbound_30d` | `clip(sum_30d / 1500, -3, 3)` | `0.45` |

Data requirements:

- PMI / M2 / LPR: at least 12 points.
- northbound: at least 60 daily points.
- price: at least 200 daily points, preferably much more.

Default horizons were `30d`, `90d`, and `180d`.

## 8. Reliability Gate

This is the most important lesson from the old implementation.

Backtest snapshot from 2026-05-09:

| Horizon | Direction hit rate | Tests | Decision |
| --- | ---: | ---: | --- |
| 30d | 46.8% | 47 | hard-block |
| 90d | 44.7% | 47 | hard-block |
| 180d | 48.9% | 47 | hard-block |

Because all tested horizons were below random, the target project should not present HS300 kNN output as a predictive forecast until a new backtest clears the gate.

Recommended gate:

```text
if dir_hit_rate < 50%:
  do not output numeric forecast
  show raw panel + caveat:
  "historical analogue signal is below random threshold; use indicators only"
```

Prefer an even stricter production gate:

```text
minimum tests >= 40
direction hit rate >= 52%
PIT mean near 0.50
no single regime dominates the test window
```

## 9. External Project Implementation Checklist

1. Build storage with date-deduped daily series.
2. Implement AkShare/Yahoo adapters.
3. Add source-health metadata: missing, stale, partial, fallback-used.
4. Build panel domains first; do not start with forecast.
5. Add top/bottom proximity only as diagnostics.
6. Add verdict after panel coverage is measurable.
7. Add forecast only after walk-forward backtest passes the reliability gate.
8. Keep A-share and Hong Kong market code out of `guanfu`.

## 10. Old Guanfu Files This Was Derived From

These files were removed from `guanfu` and should not be restored there:

- `pkg/engine/asset_hs300.go`
- `pkg/engine/hs300_dashboard.go`
- `pkg/client/hs300.go`
- `pkg/client/akshare_history.go`
- `pkg/forecast/features/china_macro.go`
- `scripts/akshare_bridge.py`

