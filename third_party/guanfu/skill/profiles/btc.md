# BTC Profile

Profile key: `btc`

Asset class: `btc`

Primary use: cycle-aware crypto market reading and BTC-specific historical
analogue forecasting.

## Reading Lens

Domains:

| Domain | Purpose |
|---|---|
| `cycle` | Halving phase, long moving-average distance, cycle-top/bottom structure |
| `valuation` | AHR999, MVRV/NUPL, realized-cap valuation signals |
| `network` | Hash rate, difficulty, mempool, miner stress |
| `positioning` | Funding, open interest, fear/greed, Deribit skew/DVOL |
| `macro` | Dollar, real yield, liquidity, broad risk backdrop |
| `flow` | Spot ETF flow, stablecoin liquidity, ETH/BTC risk appetite |
| `technical` | RSI, MACD, moving-average alignment, volatility |
| `cross_asset` | BTC versus gold, QQQ/SPY, DXY/UUP, VIXY |

BTC-only indicators such as halving phase, AHR999, hash ribbons, and miner
cost floor must never be routed into equity, gold, or arbitrary stock profiles.

## Forecast Profile

Default horizons: `30,90,180`.

Feature family:

- price/cycle: 30d/90d/180d return, drawdown, Mayer, 200w SMA distance
- valuation: compressed AHR999
- cycle clock: halving sine/cosine
- risk: realized volatility, RSI

Reliability is asset+horizon specific and must be read from the profile-backed
table before rendering a numerical forecast. If `hard_blocked=true`, do not
show numeric targets as user-facing guidance.

## Source Policy

CMC is market context only. It can cross-check global market cap or BTC quote,
but it must not replace canonical BTC price history.

Latest-only sources are reading context unless they have historical replay and
backtest evidence.

## Skill Output Rules

When answering BTC questions:

- Always include the strongest evidence chain and the strongest counter-chain.
- Treat forecast as a distribution and analogue search, not as a target price.
- Surface source-health issues before interpreting a missing or stale domain.
- Portfolio context lives on the nimbus side (2026-07); raw market facts here are portfolio-agnostic.
