#!/usr/bin/env python3
"""Export daily_prices from DuckDB to JSONL for guanfu backtest.

Usage:
  uv run python scripts/export_prices.py [--db path/to/db.duckdb] [--out /tmp/prices.jsonl]
  uv run python scripts/export_prices.py --market HK --start 2024-01-01

Output: newline-delimited JSON, one object per row:
  {"symbol":"0700.HK","market":"HK","date":"2024-01-02","close":295.0}
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

# datapipe/ is the package root
sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from db import get_store


def export(
    db_path: str | None = None,
    out_path: str | None = None,
    market: str | None = None,
    start: str | None = None,
    end: str | None = None,
) -> int:
    """Export daily_prices to JSONL. Returns row count."""
    import os
    if db_path:
        os.environ["DATAPIPE_DB"] = db_path

    store = get_store()
    store.init_db()

    sql = "SELECT market, symbol, trade_date AS date, close FROM daily_prices"
    conditions = []
    params = []
    if market:
        conditions.append("market = ?")
        params.append(market.upper())
    if start:
        conditions.append("trade_date >= ?")
        params.append(start)
    if end:
        conditions.append("trade_date <= ?")
        params.append(end)
    if conditions:
        sql += " WHERE " + " AND ".join(conditions)

    df = store.query_df(sql, params)
    if df.empty:
        print("No data found", file=sys.stderr)
        return 0

    df["date"] = df["date"].astype(str)

    out = open(out_path, "w") if out_path else sys.stdout
    count = 0
    with out:
        for _, row in df.iterrows():
            obj = {
                "symbol": f"{row['symbol']}.{row['market']}",
                "market": row["market"],
                "date": str(row["date"])[:10],
                "close": round(float(row["close"]), 4),
            }
            out.write(json.dumps(obj, ensure_ascii=False) + "\n")
            count += 1
    print(f"Exported {count} rows", file=sys.stderr)
    return count


def main():
    p = argparse.ArgumentParser(description="Export DuckDB daily_prices to JSONL")
    p.add_argument("--db", help="DuckDB path (default: DATAPIPE_DB env or datapipe.duckdb)")
    p.add_argument("--out", help="Output path (default: stdout)")
    p.add_argument("--market", help="Filter by market (A/HK/US)")
    p.add_argument("--start", help="Start date YYYY-MM-DD")
    p.add_argument("--end", help="End date YYYY-MM-DD")
    args = p.parse_args()
    export(args.db, args.out, args.market, args.start, args.end)


if __name__ == "__main__":
    main()
