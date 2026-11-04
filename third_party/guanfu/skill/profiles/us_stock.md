# US Stock Profile

Profile key pattern: `stock_<ticker>`

Asset class: `us_stock`

Primary use: arbitrary US listed stock reading and conservative forecast.

## Reading Lens

Domains:

| Domain | Purpose |
|---|---|
| `price_action` | Returns, trend, drawdown, volatility, gap behavior |
| `valuation` | PE/PB/PS/forward PE when historical or snapshot data exists |
| `fundamentals` | Revenue, margins, earnings revisions, balance sheet when available |
| `macro` | Rates, dollar, credit, broad liquidity backdrop |
| `sector_relative` | Stock versus sector ETF, SPY, QQQ, and peers |
| `flow_sentiment` | Volume, options, short interest, sentiment when available |
| `event_risk` | Earnings, guidance, product/regulatory/legal events |
| `technical` | RSI, moving averages, realized volatility, support/resistance context |

Single-name stock outputs must be more conservative than BTC or broad index
outputs because company-specific events can dominate historical analogues.

## Forecast Profile

Initial feature family:

- generic technicals
- broad macro context
- sector/index relative strength when a sector map exists

Default reliability:

- If no ticker-level backtest exists, label the forecast `untested`.
- Class-level US stock reliability can be shown as background only.
- Do not promote a ticker to reliable until enough matured claims or
  walk-forward samples exist.

## Source Policy

Yahoo/Futu price history is acceptable for price-action features.

Latest-only fundamentals, analyst revisions, short interest, or options data
are reading context only until historical replay exists.

Earnings dates and event windows must be represented as caveats. Forecasts
inside an earnings window should be downgraded.

## Skill Output Rules

When answering arbitrary ticker questions:

- State whether the symbol is using a generic stock profile or a named profile.
- Emphasize event risk and missing fundamentals before forecast numbers.
- Never reuse QQQ/SPY index conclusions as a stock-specific conclusion.
- Compare against SPY/QQQ and sector ETF when data exists.
