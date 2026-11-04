#!/usr/bin/env bash
set -uo pipefail
export PYTHONPATH="$(cd "$(dirname "$0")/.." && pwd)"
python3 - <<'PY'
import data_access as data
assert data.health().get("ok"), "facade unhealthy"
assert data.quote("US:AAPL")[0]["last"] is not None
assert len(data.history("US:AAPL", limit=3)) == 3
# ETF history isn't in the warehouse — facade must fall back to market-data /kline.
assert len(data.history("US:SPY", from_="2026-06-10")) >= 1, "ETF history fallback broken"
assert data.fundamentals("US:AAPL")
assert data.universe("US","etf")
print("data-access smoke OK")
PY
