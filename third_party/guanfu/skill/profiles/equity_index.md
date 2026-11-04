# Equity Index Profile

Profile keys: `qqq`, `spy`

Asset class: `equity_index`

Primary use: broad US equity index or index ETF reading and forecast.

## Reading Lens

Domains:

| Domain | Purpose |
|---|---|
| `valuation` | CAPE, PE/PB when available, earnings-yield context |
| `earnings_macro` | Growth/rate backdrop and broad earnings sensitivity |
| `liquidity_rates` | Treasury rates, real-rate pressure, liquidity context |
| `credit_risk` | HY spread / BAA spread, curve stress |
| `breadth_sentiment` | Fear/greed, volatility regime, breadth proxies when available |
| `options_positioning` | CBOE total put/call ratio, changes, percentile |
| `technical` | RSI, volatility, momentum, moving averages, drawdown |

Do not expose BTC-only domains such as `network`, `halving`, `hash_ribbons`, or
`AHR999`.

## Forecast Profile

Default horizons:

- QQQ/SPY: `30,63,90,180,252`

Feature family:

- generic technicals: 30d/90d/180d return, drawdown, Mayer-style 200d trend,
  realized volatility, RSI
- equity macro: CAPE, 10Y rate change, DXY, HY/BAA spread, 10Y-2Y curve, VIXY
- options: CBOE total put/call ratio, 30d change, trailing percentile

Put/call features are conservative until ablation proves durable value. The
current command for validation is:

```bash
guanfu backtest all --ablate-putcall --plain
```

## Source Policy

Futu valuation snapshots are reading context unless historical valuation
series are available. Latest-only PE/PB must not enter forecast.

CAPE can enter forecast because it has a historical monthly series, but it must
respect stale gates and ablation budgets.

## Skill Output Rules

When answering QQQ/SPY questions:

- Speak in index-risk language: earnings, rates, liquidity, credit, volatility.
- Do not imply BTC cycle mechanics.
- If options or valuation data is stale/missing, downgrade confidence instead
  of filling the gap with narrative.
- Use 63d and 252d horizons as quarter/year context when requested.
