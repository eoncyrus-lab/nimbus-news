"""Exclusive A-share data loaders: 龙虎榜, capital flow, and northbound flow.

Each sync_* function is additive-only: it fetches, normalises, and upserts into the
three tables (lhb_detail, capital_flow, northbound_flow) added in this iteration.
Failures are isolated — a single symbol or channel failure never crashes the whole
run.
"""

from __future__ import annotations

import logging
from datetime import datetime, timedelta

import pandas as pd

from datapipe.db import get_store, latest_table as _latest_table
from datapipe.pipeline import _record_ingest_failure
from datapipe.sources.akshare_client import (
    _clean_a_symbol,
    _first_existing,
    _now,
    _number,
)

logger = logging.getLogger("ah_screener.exclusives")


# ---------------------------------------------------------------------------
# Helper: infer akshare market prefix from cleaned A-share symbol
# ---------------------------------------------------------------------------

def _a_market_prefix(symbol: str) -> str:
    """Return 'sh', 'sz', or 'bj' for an A-share symbol (6-digit, no prefix)."""
    if symbol.startswith(("60", "68", "90", "51", "56", "58", "50")):
        return "sh"
    if symbol.startswith(("00", "30", "20", "15", "16")):
        return "sz"
    if symbol.startswith(("43", "83", "87", "88", "92")):
        return "bj"
    return "sh"


# ---------------------------------------------------------------------------
# sync_lhb
# ---------------------------------------------------------------------------

def sync_lhb(days: int = 5) -> dict[str, int]:
    """Fetch 龙虎榜 detail from AKShare and upsert into lhb_detail."""
    import akshare as ak

    store = get_store()
    store.init_db()

    end_dt = datetime.now()
    start_dt = end_dt - timedelta(days=days)
    end_date = end_dt.strftime("%Y%m%d")
    start_date = start_dt.strftime("%Y%m%d")

    try:
        raw = ak.stock_lhb_detail_em(start_date=start_date, end_date=end_date)
    except Exception as exc:
        _record_ingest_failure("exclusives_lhb", str(exc)[:300])
        return {"lhb_detail": 0}

    if raw is None or raw.empty:
        return {"lhb_detail": 0}

    updated_at = _now()
    out = pd.DataFrame(
        {
            "trade_date": pd.to_datetime(raw["上榜日"], errors="coerce").dt.date,
            "symbol": _clean_a_symbol(raw["代码"]),
            "name": raw["名称"].astype(str),
            "reason": raw["上榜原因"].astype(str).fillna("unknown"),
            "interpretation": raw["解读"].astype(str),
            "close_price": _number(raw["收盘价"]),
            "pct_change": _number(raw["涨跌幅"]),
            "lhb_net_buy": _number(raw["龙虎榜净买额"]),
            "lhb_buy": _number(raw["龙虎榜买入额"]),
            "lhb_sell": _number(raw["龙虎榜卖出额"]),
            "lhb_turnover": _number(raw["龙虎榜成交额"]),
            "market_turnover": _number(raw["市场总成交额"]),
            "net_buy_ratio": _number(raw["净买额占总成交比"]),
            "turnover_ratio": _number(raw["成交额占总成交比"]),
            "turnover_rate": _number(raw["换手率"]),
            "free_float_mcap": _number(raw["流通市值"]),
            "d1_chg": _number(raw["上榜后1日"]),
            "d2_chg": _number(raw["上榜后2日"]),
            "d5_chg": _number(raw["上榜后5日"]),
            "d10_chg": _number(raw["上榜后10日"]),
            "source": "akshare.stock_lhb_detail_em",
            "updated_at": updated_at,
        }
    )
    out["reason"] = out["reason"].fillna("unknown").replace("", "unknown")
    out = out.dropna(subset=["trade_date", "symbol"])
    out = out.drop_duplicates(["trade_date", "symbol", "reason", "source"], keep="last")
    count = store.upsert_dataframe("lhb_detail", out)
    return {"lhb_detail": count}


# ---------------------------------------------------------------------------
# sync_capital_flow
# ---------------------------------------------------------------------------

def sync_capital_flow(top: int = 100, include_rank: bool = False) -> dict[str, int]:
    """Fetch market-level and top-N stock capital-flow data; upsert into capital_flow.

    include_rank is reserved for a future rank-table extension; ignored for now.
    """
    import akshare as ak

    store = get_store()
    store.init_db()

    updated_at = _now()
    market_count = 0
    stock_count = 0
    stock_failed = 0

    # (a) Market-level flow
    try:
        raw_m = ak.stock_market_fund_flow()
        if raw_m is not None and not raw_m.empty:
            out_m = pd.DataFrame(
                {
                    "trade_date": pd.to_datetime(raw_m["日期"], errors="coerce").dt.date,
                    "symbol": "MARKET",
                    "flow_scope": "market",
                    "name": pd.NA,
                    "close": _number(raw_m["上证-收盘价"]),
                    "pct_change": _number(raw_m["上证-涨跌幅"]),
                    "close2": _number(raw_m["深证-收盘价"]),
                    "pct_change2": _number(raw_m["深证-涨跌幅"]),
                    "main_net": _number(raw_m["主力净流入-净额"]),
                    "main_net_pct": _number(raw_m["主力净流入-净占比"]),
                    "super_net": _number(raw_m["超大单净流入-净额"]),
                    "super_net_pct": _number(raw_m["超大单净流入-净占比"]),
                    "large_net": _number(raw_m["大单净流入-净额"]),
                    "large_net_pct": _number(raw_m["大单净流入-净占比"]),
                    "medium_net": _number(raw_m["中单净流入-净额"]),
                    "medium_net_pct": _number(raw_m["中单净流入-净占比"]),
                    "small_net": _number(raw_m["小单净流入-净额"]),
                    "small_net_pct": _number(raw_m["小单净流入-净占比"]),
                    "indicator": pd.NA,
                    "source": "akshare.stock_market_fund_flow",
                    "updated_at": updated_at,
                }
            )
            out_m = out_m.dropna(subset=["trade_date"])
            out_m = out_m.drop_duplicates(
                ["trade_date", "symbol", "flow_scope", "source"], keep="last"
            )
            market_count = store.upsert_dataframe("capital_flow", out_m)
    except Exception as exc:
        _record_ingest_failure("exclusives_capital_flow_market", str(exc)[:300])

    # (b) Stock-level flow — universe from market_snapshots latest snapshot, A-market only
    try:
        snapshots = _latest_table(store, "market_snapshots", "trade_date")
        if snapshots.empty:
            _record_ingest_failure(
                "exclusives_capital_flow_stock",
                "market_snapshots is empty; skipping stock capital flow",
            )
        else:
            pool = (
                snapshots[snapshots["market"].astype(str).str.upper().eq("A")]
                .copy()
                .assign(
                    amount_num=lambda df: pd.to_numeric(
                        df.get("amount", pd.Series(0, index=df.index)), errors="coerce"
                    ).fillna(0)
                )
                .sort_values("amount_num", ascending=False)
                .head(top)
            )
            for row in pool.itertuples(index=False):
                sym = str(row.symbol)
                mkt_prefix = _a_market_prefix(sym)
                try:
                    raw_s = ak.stock_individual_fund_flow(stock=sym, market=mkt_prefix)
                    if raw_s is None or raw_s.empty:
                        continue
                    out_s = pd.DataFrame(
                        {
                            "trade_date": pd.to_datetime(
                                raw_s["日期"], errors="coerce"
                            ).dt.date,
                            "symbol": sym,
                            "flow_scope": "stock",
                            "name": pd.NA,
                            "close": _number(raw_s["收盘价"]),
                            "pct_change": _number(raw_s["涨跌幅"]),
                            "close2": pd.NA,
                            "pct_change2": pd.NA,
                            "main_net": _number(raw_s["主力净流入-净额"]),
                            "main_net_pct": _number(raw_s["主力净流入-净占比"]),
                            "super_net": _number(raw_s["超大单净流入-净额"]),
                            "super_net_pct": _number(raw_s["超大单净流入-净占比"]),
                            "large_net": _number(raw_s["大单净流入-净额"]),
                            "large_net_pct": _number(raw_s["大单净流入-净占比"]),
                            "medium_net": _number(raw_s["中单净流入-净额"]),
                            "medium_net_pct": _number(raw_s["中单净流入-净占比"]),
                            "small_net": _number(raw_s["小单净流入-净额"]),
                            "small_net_pct": _number(raw_s["小单净流入-净占比"]),
                            "indicator": pd.NA,
                            "source": "akshare.stock_individual_fund_flow",
                            "updated_at": updated_at,
                        }
                    )
                    out_s = out_s.dropna(subset=["trade_date"])
                    out_s = out_s.drop_duplicates(
                        ["trade_date", "symbol", "flow_scope", "source"], keep="last"
                    )
                    stock_count += store.upsert_dataframe("capital_flow", out_s)
                except Exception as exc:  # noqa: BLE001 - single symbol must not abort the loop
                    stock_failed += 1
                    logger.debug("capital_flow stock %s failed: %s", sym, exc)
    except Exception as exc:
        _record_ingest_failure("exclusives_capital_flow", str(exc)[:300])

    return {
        "capital_flow_market": market_count,
        "capital_flow_stock": stock_count,
        "capital_flow_stock_failed": stock_failed,
    }


# ---------------------------------------------------------------------------
# sync_northbound
# ---------------------------------------------------------------------------

def sync_northbound(
    channels: list[str] | None = None,
    include_summary: bool = False,
) -> dict[str, int]:
    """Fetch 北向/southbound historical flow and upsert into northbound_flow.

    include_summary is reserved for a future summary-table extension; ignored for now.
    """
    import akshare as ak

    store = get_store()
    store.init_db()

    if channels is None:
        channels = ["北向资金"]

    updated_at = _now()
    total = 0

    for channel in channels:
        try:
            raw = ak.stock_hsgt_hist_em(symbol=channel)
        except Exception as exc:
            _record_ingest_failure(
                f"exclusives_northbound_{channel}", str(exc)[:300]
            )
            continue

        if raw is None or raw.empty:
            continue

        index_close = _first_existing(
            raw, ["沪深300", "上证指数", "深证指数", "恒生指数"]
        )
        # Corresponding pct column: "<index_name>-涨跌幅"
        index_name_used: str | None = None
        for candidate in ["沪深300", "上证指数", "深证指数", "恒生指数"]:
            if candidate in raw.columns:
                index_name_used = candidate
                break
        index_pct_col = f"{index_name_used}-涨跌幅" if index_name_used else None
        index_pct = (
            _number(raw[index_pct_col])
            if index_pct_col and index_pct_col in raw.columns
            else pd.Series([pd.NA] * len(raw), index=raw.index)
        )

        out = pd.DataFrame(
            {
                "trade_date": pd.to_datetime(raw["日期"], errors="coerce").dt.date,
                "channel": channel,
                "flow_type": "hist",
                "net_buy": _number(raw["当日成交净买额"]),
                "buy_amt": _number(raw["买入成交额"]),
                "sell_amt": _number(raw["卖出成交额"]),
                "accum_net_buy": _number(raw["历史累计净买额"]),
                "fund_inflow": _number(raw["当日资金流入"]),
                "quota_balance": _number(raw["当日余额"]),
                "hold_market_cap": _number(raw["持股市值"]),
                "up_count": pd.NA,
                "flat_count": pd.NA,
                "down_count": pd.NA,
                "index_close": _number(index_close),
                "index_pct": _number(index_pct),
                "lead_stock_name": raw["领涨股"].astype(str),
                "lead_stock_symbol": raw["领涨股-代码"].astype(str),
                "lead_stock_pct": _number(raw["领涨股-涨跌幅"]),
                "source": "akshare.stock_hsgt_hist_em",
                "updated_at": updated_at,
            }
        )
        out["channel"] = out["channel"].fillna("unknown").replace("", "unknown")
        out["flow_type"] = out["flow_type"].fillna("unknown").replace("", "unknown")
        out = out.dropna(subset=["trade_date"])
        out = out.drop_duplicates(
            ["trade_date", "channel", "flow_type", "source"], keep="last"
        )
        total += store.upsert_dataframe("northbound_flow", out)

    return {"northbound_flow": total}


# ---------------------------------------------------------------------------
# update_exclusives
# ---------------------------------------------------------------------------

def update_exclusives(
    days: int = 5,
    top: int = 100,
    channels: list[str] | None = None,
    include_rank: bool = False,
    include_summary: bool = False,
) -> dict[str, int]:
    """Run all three exclusive sync steps and return merged row counts."""
    if channels is None:
        channels = ["北向资金"]

    result: dict[str, int] = {}

    try:
        result.update(sync_lhb(days=days))
    except Exception as exc:
        _record_ingest_failure("exclusives_lhb", str(exc)[:300])
        result["lhb_detail"] = 0

    try:
        result.update(sync_capital_flow(top=top, include_rank=include_rank))
    except Exception as exc:
        _record_ingest_failure("exclusives_capital_flow", str(exc)[:300])
        result.setdefault("capital_flow_market", 0)
        result.setdefault("capital_flow_stock", 0)
        result.setdefault("capital_flow_stock_failed", 0)

    try:
        result.update(
            sync_northbound(channels=channels, include_summary=include_summary)
        )
    except Exception as exc:
        _record_ingest_failure("exclusives_northbound", str(exc)[:300])
        result["northbound_flow"] = 0

    return result
