# data-access SDK

The single entrypoint every Tier-2 app uses (decoupling plan §4.8). Apps call the
SDK (`import data_access as data`) instead of importing yfinance/futu/akshare/
openbb — enforced by docs/tools/check-tier-boundary.sh.

## Server side

The :8800 facade surface is served by the Go **dataplane** since 2026-07 R6 —
the datagw process's second listener (source: `datasources/cmd/datagw/dataplane.go`,
launchd `com.nimbus.datagw`, env `DATAPLANE_ADDR`). The Python facade process
that used to live here is retired; only the SDK (`data_access/`) and its smoke
test remain. Routing table: docs/ARCHITECTURE.md §4.

## Routing
| call | -> backend |
|---|---|
| quote | market-data-svc (realtime Futu) |
| history/fundamentals/technicals/universe/screening | warehouse-svc |
| news | news platform /api/news |

## SDK
```python
import data_access as data            # PYTHONPATH=services/data-access, or pip install -e
data.quote("US:AAPL","HK:00700"); data.history("US:AAPL", limit=30)
data.fundamentals("US:AAPL"); data.technicals("US:NVDA")
data.universe("US","etf"); data.screening("US"); data.news(limit=20)
```
Weak dependency: backend errors degrade to empty, never crash the app.
