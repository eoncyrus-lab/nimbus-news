# Gold Profile

Profile key: `gold`

Asset class: `gold`

Primary use: USD gold macro, real-yield, dollar, positioning, and risk-off
reading.

## Reading Lens

Domains:

| Domain | Purpose |
|---|---|
| `real_yield` | TIPS real yield level/change and bond proxy pressure |
| `usd` | DXY/UUP direction and dollar pressure |
| `inflation` | Breakeven inflation and inflation-hedge context |
| `positioning` | COT managed-money net positioning and crowdedness |
| `risk_off` | VIX/risk-off demand and crisis premium |
| `technical` | RSI, moving averages, volatility, drawdown |
| `cross_asset` | Gold versus BTC, equities, dollar, and rates |

Gold must not be treated as an equity ETF. Shared technical calculations are
allowed through `BuildMarketPanel`, but the reading lens and verdict rules are
gold-specific.

Current JSON compatibility note: until `IndicatorPanel` supports fully dynamic
domain maps, the code maps Gold reading evidence into `valuation`, `macro`, and
`technical` maps and exposes the gold-specific lens through `domain_meta`.

## Forecast Profile

Default horizons: `30,60,90,120`.

Gold 180d may remain opt-in for explicit queries, but it should carry a
reliability caveat unless new backtests prove otherwise. Gold 30d is currently
hard-blocked in the static reliability table and must not be rendered as a
numeric actionable forecast.

Feature family:

- generic technicals: return, drawdown, Mayer-style trend extension,
  realized volatility, RSI
- macro: real yield, breakeven inflation, DXY, VIXY
- positioning: COT managed-money net

## Source Policy

FRED and COT data need stale gates. If real yield or COT is stale, show the
feature as unavailable instead of carrying old values forward.

## Skill Output Rules

When answering Gold questions:

- Lead with real yield and dollar evidence before technical evidence.
- Separate inflation-hedge demand from crisis/risk-off demand.
- Treat COT extreme positioning as crowdedness, not automatically bullish.
- Surface Gold 30d hard-blocks clearly.
