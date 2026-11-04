# ⚠ DEPRECATED — July 2026
# Scoring/screening/backtest modules migrated to Go (guanfu/pkg/screener/).
# This file retains only A-share/HK/US data collection pipeline steps.
# Do NOT re-add scoring, expert model, style models, or backtest logic here.
# See guanfu/pkg/screener/ for the replacement Go code.

from __future__ import annotations

import logging
import os
import urllib.request
from datetime import datetime, timedelta
from pathlib import Path
from typing import Literal

import pandas as pd

from datapipe.classification import enrich_security_metadata
from datapipe.config import get_settings
from datapipe.db import get_store, init_db, latest_table as _latest_table
from datapipe.sources.akshare_client import (
    DEFAULT_BENCHMARKS,
    fetch_a_board_tags,
    fetch_a_delisted_lifecycle,
    fetch_a_etf_exposure,
    fetch_a_etf_spot,
    fetch_benchmark_history,
    fetch_history,
    fetch_hk_delisted_lifecycle,
    fetch_hk_etf_spot,
    fetch_spot,
    parse_benchmark,
)
from datapipe.sources.us_client import (
    fetch_us_delisted_lifecycle,
    fetch_us_spot,
    fetch_us_spot_batch,
)
from datapipe.storage import Store

MarketArg = Literal["A", "HK", "US", "ETF", "all"]
WAREHOUSE_URL = os.environ.get("WAREHOUSE_URL", "http://127.0.0.1:8814")
logger = logging.getLogger("ah_screener.pipeline")

# ── warehouse helpers ──────────────────────────────────────────

def _warehouse_pause(ttl_seconds: int) -> bool:
    url = f"{WAREHOUSE_URL}/admin/pause?ttl={ttl_seconds}"
    try:
        request = urllib.request.Request(url, method="POST")
        with urllib.request.urlopen(request, timeout=5) as response:
            if response.status != 200:
                logger.warning("warehouse pause returned status %s", response.status)
                return False
        return True
    except Exception:
        logger.warning("warehouse pause failed", exc_info=True)
        return False

def _warehouse_resume() -> bool:
    url = f"{WAREHOUSE_URL}/admin/resume"
    try:
        request = urllib.request.Request(url, method="POST")
        with urllib.request.urlopen(request, timeout=5) as response:
            if response.status != 200:
                logger.warning("warehouse resume returned status %s", response.status)
                return False
        return True
    except Exception:
        logger.warning("warehouse resume failed", exc_info=True)
        return False

# ── universe / spot ────────────────────────────────────────────

def sync_delisted_universe() -> dict[str, int | str]:
    store = get_store()
    store.init_db()
    result: dict[str, int | str] = {}
    frames: list[pd.DataFrame] = []
    sources = [
        ("A", fetch_a_delisted_lifecycle),
        ("HK", fetch_hk_delisted_lifecycle),
        ("US", fetch_us_delisted_lifecycle),
    ]
    current = store.query_df("SELECT * FROM market_snapshots") if store.db_path.exists() else pd.DataFrame()
    for market, fetch in sources:
        try:
            lifecycle = fetch()
        except Exception as exc:
            result[f"{market}_lifecycle_failed"] = 1
            result[f"{market}_lifecycle_error"] = str(exc)[:200]
            continue
        if market == "HK" and not lifecycle.empty and not current.empty:
            current_hk = current[current["market"].eq("HK")][["symbol", "name"]].drop_duplicates("symbol")
            merged = lifecycle.merge(current_hk, on="symbol", how="left", suffixes=("_lifecycle", "_current"))
            same_name = merged["name_lifecycle"].str.lower().fillna("") == merged["name_current"].str.lower().fillna("")
            mask = merged["name_current"].isna() | same_name
            lifecycle = lifecycle.iloc[mask.values if hasattr(mask, "values") else mask]
        result[f"{market}_lifecycle_rows"] = len(lifecycle)
        if not lifecycle.empty:
            store.execute("DELETE FROM security_lifecycle_events WHERE market = ?", [market])
            frames.append(lifecycle)
    all_lifecycle = pd.concat(frames, ignore_index=True) if frames else pd.DataFrame()
    result["security_lifecycle_events"] = store.upsert_dataframe("security_lifecycle_events", all_lifecycle)
    return result

def _persist_security_universe_snapshot(store: Store, securities: pd.DataFrame, snapshots: pd.DataFrame, source: str) -> int:
    if snapshots.empty:
        return 0
    base = snapshots.copy()
    for column, default in [("name", pd.NA), ("asset_type", "stock"), ("board", pd.NA)]:
        if column not in base.columns:
            base[column] = default
    base["snapshot_date"] = pd.Timestamp(datetime.now()).date()
    base = base[["snapshot_date", "market", "symbol", "name", "asset_type", "board"]]
    meta_columns = ["market", "symbol", "name", "asset_type", "board", "exchange", "currency", "status", "is_st", "is_hk_connect", "metadata_source", "metadata_confidence"]
    meta = securities.copy()
    for column in meta_columns:
        if column not in meta.columns:
            meta[column] = pd.NA
    meta = meta[meta_columns].drop_duplicates(["market", "symbol"], keep="last")
    out = base.merge(meta, on=["market", "symbol"], how="left", suffixes=("_snapshot", ""))
    for column in ["name", "asset_type", "board"]:
        out[column] = out[column].combine_first(out[f"{column}_snapshot"])
    out["asset_type"] = out["asset_type"].fillna("stock")
    out["is_st"] = out["is_st"].map(lambda value: False if pd.isna(value) else bool(value))
    out["is_hk_connect"] = out["is_hk_connect"].map(lambda value: False if pd.isna(value) else bool(value))
    out["source"] = source
    out["updated_at"] = pd.Timestamp(datetime.now())
    columns = ["snapshot_date", "market", "symbol", "name", "asset_type", "board", "exchange", "currency", "status", "is_st", "is_hk_connect", "metadata_source", "metadata_confidence", "source", "updated_at"]
    return store.upsert_dataframe("security_universe_snapshots", out[columns].drop_duplicates(["snapshot_date", "market", "symbol"], keep="last"))

def sync_spot(market: MarketArg) -> dict[str, int]:
    store = get_store()
    store.init_db()
    result: dict[str, int] = {}
    tolerant = market == "all"

    def _ingest(label: str, fetch) -> None:
        try:
            securities, snapshots = fetch()
            result[f"{label}_securities"] = store.upsert_dataframe("securities", securities)
            result[f"{label}_snapshots"] = store.upsert_dataframe("market_snapshots", snapshots)
            result[f"{label}_universe_snapshots"] = _persist_security_universe_snapshot(store, securities, snapshots, source=label)
        except Exception as exc:
            result[f"{label}_failed"] = 1
            result[f"{label}_error"] = str(exc)[:200]
            if not tolerant:
                raise

    markets = ["A", "HK", "US"] if market == "all" else ([] if market == "ETF" else [market])
    for item in markets:
        _ingest(item, lambda item=item: fetch_spot(item))
    if market in {"A", "ETF", "all"}:
        _ingest("A_etf", fetch_a_etf_spot)
    if market in {"HK", "ETF", "all"}:
        _ingest("HK_etf", fetch_hk_etf_spot)
    return result

def sync_us_spot(symbols: list[str], lookback_days: int = 14) -> dict[str, int]:
    store = get_store()
    store.init_db()
    securities, snapshots = fetch_us_spot(symbols=symbols, lookback_days=lookback_days)
    return {
        "US_securities": store.upsert_dataframe("securities", securities),
        "US_snapshots": store.upsert_dataframe("market_snapshots", snapshots),
        "US_universe_snapshots": _persist_security_universe_snapshot(store, securities, snapshots, source="US"),
    }

def sync_us_spot_batch(*, offset: int = 0, limit: int = 100, include_etf: bool = False, lookback_days: int = 14, asset_type: str | None = None) -> dict[str, int]:
    store = get_store()
    store.init_db()
    securities, snapshots = fetch_us_spot_batch(offset=offset, limit=limit, include_etf=include_etf, lookback_days=lookback_days, asset_type=asset_type)
    return {
        "US_securities": store.upsert_dataframe("securities", securities),
        "US_snapshots": store.upsert_dataframe("market_snapshots", snapshots),
        "US_universe_snapshots": _persist_security_universe_snapshot(store, securities, snapshots, source="US_batch"),
        "US_batch_offset": offset,
        "US_batch_limit": limit,
    }

# ── classification / tags ──────────────────────────────────────

def classify_existing_securities() -> dict[str, int]:
    store = get_store()
    store.init_db()
    securities = store.query_df("SELECT * FROM securities")
    if securities.empty:
        return {"securities": 0, "snapshots": 0}
    enriched = enrich_security_metadata(securities)
    security_rows = store.upsert_dataframe("securities", enriched)
    snapshot_count = int(store.query_df("SELECT COUNT(*) AS count FROM market_snapshots")["count"].iloc[0])
    return {"securities": security_rows, "snapshots": snapshot_count}

def sync_a_tags(kind: Literal["industry", "concept"], limit: int | None, force: bool = False, max_age_days: int = 7) -> int:
    store = get_store()
    store.init_db()
    if not force:
        existing = store.query_df("SELECT MAX(updated_at) AS last FROM company_tags WHERE tag_type = ?", [kind])
        last = pd.to_datetime(existing["last"].iloc[0], errors="coerce") if not existing.empty else pd.NaT
        if pd.notna(last) and last >= pd.Timestamp.now() - pd.Timedelta(days=max_age_days):
            return 0
    tags = fetch_a_board_tags(kind=kind, limit=limit)
    return store.upsert_dataframe("company_tags", tags)

def _normalize_tag_symbol(market: str, symbol: object) -> str:
    raw = str(symbol).strip().lower()
    clean = raw.replace("sh", "").replace("sz", "").replace("bj", "").replace("hk", "")
    if market == "US":
        return str(symbol).strip().upper().replace("/", ".")
    return clean.zfill(5 if market == "HK" else 6)

def _prepare_custom_tags(tags: pd.DataFrame, source: str) -> pd.DataFrame:
    required = {"market", "symbol", "tag_name"}
    missing = required.difference(tags.columns)
    if missing:
        raise ValueError(f"Missing custom tag columns: {', '.join(sorted(missing))}")
    frame = tags.copy()
    frame["market"] = frame["market"].astype(str).str.upper().str.strip()
    frame = frame[frame["market"].isin(["A", "HK", "US"])]
    frame["symbol"] = frame.apply(lambda row: _normalize_tag_symbol(str(row["market"]), row["symbol"]), axis=1)
    frame["tag_name"] = frame["tag_name"].astype(str).str.strip()
    frame["tag_type"] = frame["tag_type"].astype(str).str.lower().str.strip() if "tag_type" in frame.columns else "theme"
    frame["evidence_level"] = frame["evidence_level"].astype(str).str.upper().str.strip() if "evidence_level" in frame.columns else "B"
    frame["source"] = frame["source"].astype(str).str.strip() if "source" in frame.columns else source
    frame["source"] = frame["source"].replace("", source)
    frame["updated_at"] = pd.Timestamp(datetime.now())
    frame = frame[frame["tag_name"].ne("")]
    return frame[["market", "symbol", "tag_type", "tag_name", "evidence_level", "source", "updated_at"]].drop_duplicates(["market", "symbol", "tag_type", "tag_name", "source"])

def import_custom_tags(path: Path, source: str = "custom_csv") -> int:
    store = get_store()
    store.init_db()
    if not path.exists():
        raise FileNotFoundError(path)
    tags = pd.read_csv(path)
    return store.upsert_dataframe("company_tags", _prepare_custom_tags(tags, source=source))

def import_industry_mapping(path: Path, source: str = "industry_mapping_csv") -> int:
    store = get_store()
    store.init_db()
    if not path.exists():
        raise FileNotFoundError(path)
    frame = pd.read_csv(path)
    if "tag_name" not in frame.columns:
        for column in ["detailed_industry", "industry", "industry_peer_group", "sub_industry", "sector"]:
            if column in frame.columns:
                frame = frame.rename(columns={column: "tag_name"})
                break
    if "tag_type" not in frame.columns:
        frame["tag_type"] = "industry"
    if "evidence_level" not in frame.columns:
        frame["evidence_level"] = "A"
    return store.upsert_dataframe("company_tags", _prepare_custom_tags(frame, source=source))

# ── history / benchmarks ──────────────────────────────────────

def sync_history(market: MarketArg, top: int = 150, lookback_days: int = 420, include_etf: bool = True, etf_top: int = 120, full: bool = False) -> dict[str, int]:
    store = get_store()
    store.init_db()
    latest = store.query_df("SELECT * FROM market_snapshots")
    if latest.empty:
        raise RuntimeError("No market snapshots found. Run sync-spot first.")
    latest["trade_date"] = pd.to_datetime(latest["trade_date"], errors="coerce")
    latest = latest.sort_values("trade_date").drop_duplicates(["market", "symbol"], keep="last")
    if "asset_type" not in latest.columns:
        latest = latest.assign(asset_type="stock")

    coverage = store.query_df("SELECT market, symbol, MAX(trade_date) AS max_td FROM daily_prices GROUP BY market, symbol")
    cov_map: dict[tuple[str, str], pd.Timestamp] = {}
    if not coverage.empty:
        coverage["max_td"] = pd.to_datetime(coverage["max_td"], errors="coerce")
        cov_map = {(str(r.market), str(r.symbol)): r.max_td for r in coverage.itertuples(index=False)}

    markets = ["A", "HK", "US"] if market == "all" else [market]
    end_dt = datetime.now()
    end = end_dt.strftime("%Y%m%d")
    full_start = (end_dt - timedelta(days=lookback_days)).strftime("%Y%m%d")
    result: dict[str, int] = {}
    for item in markets:
        pool = latest[latest["market"] == item].assign(
            amount_num=lambda df: pd.to_numeric(df["amount"], errors="coerce").fillna(0),
            asset_type=lambda df: df["asset_type"].fillna("stock"),
        )
        target = pool["trade_date"].max()
        stocks = pool[pool["asset_type"].eq("stock")].sort_values("amount_num", ascending=False).head(top)
        etfs = pool[pool["asset_type"].eq("etf")].sort_values("amount_num", ascending=False).head(etf_top) if include_etf else pool.iloc[0:0]
        universe = pd.concat([stocks, etfs], ignore_index=True)
        if item == "HK":
            connect_pool = pool[pool["board"].eq("港股通") & ~pool.index.isin(universe.index)]
            universe = pd.concat([universe, connect_pool], ignore_index=True)
        inserted = skipped = failed = 0
        for row in universe.itertuples(index=False):
            symbol = str(row.symbol)
            nm_max = cov_map.get((item, symbol))
            if not full and nm_max is not None and pd.notna(nm_max) and pd.notna(target) and nm_max >= target:
                skipped += 1; continue
            start = (nm_max - pd.Timedelta(days=7)).strftime("%Y%m%d") if not full and nm_max is not None and pd.notna(nm_max) else full_start
            try:
                history = fetch_history(item, symbol=symbol, start_date=start, end_date=end, asset_type=str(getattr(row, "asset_type", "stock") or "stock"))
            except Exception:
                failed += 1; continue
            inserted += store.upsert_dataframe("daily_prices", history)
        result[f"{item}_history_rows"] = inserted
        result[f"{item}_history_skipped"] = skipped
        result[f"{item}_history_failed_symbols"] = failed
    return result

def sync_benchmarks(benchmarks: list[str] | None = None, lookback_days: int = 430, full: bool = False) -> dict[str, int]:
    store = get_store()
    store.init_db()
    coverage = store.query_df("SELECT market, symbol, MAX(trade_date) AS max_td FROM daily_prices GROUP BY market, symbol")
    cov_map: dict[tuple[str, str], pd.Timestamp] = {}
    if not coverage.empty:
        coverage["max_td"] = pd.to_datetime(coverage["max_td"], errors="coerce")
        cov_map = {(str(r.market), str(r.symbol)): r.max_td for r in coverage.itertuples(index=False)}
    end_dt = datetime.now()
    end = end_dt.strftime("%Y%m%d")
    full_start = (end_dt - timedelta(days=lookback_days)).strftime("%Y%m%d")
    fresh_cut = pd.Timestamp(end_dt.date()) - pd.Timedelta(days=4)
    result: dict[str, int] = {}
    for benchmark in benchmarks or DEFAULT_BENCHMARKS:
        market, symbol = parse_benchmark(benchmark)
        key = f"{market}_{symbol}_benchmark_rows"
        nm_max = cov_map.get((market, str(symbol)))
        if not full and nm_max is not None and pd.notna(nm_max) and nm_max >= fresh_cut:
            result[key] = 0; result[f"{market}_{symbol}_benchmark_skipped"] = 1; continue
        start = (nm_max - pd.Timedelta(days=7)).strftime("%Y%m%d") if not full and nm_max is not None and pd.notna(nm_max) else full_start
        try:
            history = fetch_benchmark_history(f"{market}:{symbol}", start_date=start, end_date=end)
        except Exception:
            result[key] = 0; result[f"{market}_{symbol}_benchmark_failed"] = 1; continue
        result[key] = store.upsert_dataframe("daily_prices", history)
        result[f"{market}_{symbol}_benchmark_failed"] = 0
    return result

# ── ETF exposures ─────────────────────────────────────────────

def sync_etf_exposures(*, market: Literal["A", "all"] = "A", year: str | None = None, limit: int | None = None, min_amount: float = 0.0) -> dict[str, int]:
    if market not in {"A", "all"}:
        raise ValueError("sync_etf_exposures currently supports market='A' or 'all'")
    store = get_store(); store.init_db()
    snapshots = _latest_table(store, "market_snapshots", "trade_date")
    pool = snapshots[snapshots["market"].astype(str).str.upper().eq("A")].copy() if not snapshots.empty else pd.DataFrame()
    if pool.empty:
        return {"etf_universe": 0, "holdings": 0, "industry_allocations": 0, "failed": 0}
    if min_amount > 0 and "amount" in pool.columns:
        pool = pool[pd.to_numeric(pool["amount"], errors="coerce").fillna(0) >= min_amount]
    pool["amount_num"] = pd.to_numeric(pool.get("amount", pd.Series(0, index=pool.index)), errors="coerce").fillna(0)
    pool = pool.sort_values("amount_num", ascending=False).drop_duplicates(["market", "symbol"])
    if limit is not None: pool = pool.head(limit)
    target_year = str(year or datetime.now().year)
    holdings_frames, allocation_frames, failed = [], [], 0
    for symbol in pool["symbol"].astype(str):
        try:
            holdings, allocations = fetch_a_etf_exposure(symbol, target_year)
            if not holdings.empty: holdings_frames.append(holdings)
            if not allocations.empty: allocation_frames.append(allocations)
        except Exception:
            failed += 1
    return {
        "etf_universe": int(len(pool)),
        "holdings": store.upsert_dataframe("etf_holdings", pd.concat(holdings_frames, ignore_index=True)) if holdings_frames else 0,
        "industry_allocations": store.upsert_dataframe("etf_industry_allocations", pd.concat(allocation_frames, ignore_index=True)) if allocation_frames else 0,
        "failed": failed,
    }

# ── data-only full refresh ─────────────────────────────────────

def run_data_refresh(top: int = 120, lookback_days: int = 430) -> dict[str, object]:
    """Data-collection-only refresh: universe, spot, tags, history, benchmarks.
    Scoring/screening is now handled by Go (guanfu/pkg/screener/)."""
    result: dict[str, object] = {}

    def _step(name: str, fn) -> None:
        try:
            result[name] = fn()
        except Exception as exc:
            result[name] = {"failed": str(exc)[:300]}
            logger.warning("step %s failed: %s", name, str(exc)[:200])

    paused = _warehouse_pause(7200)
    try:
        _step("delisted_universe", sync_delisted_universe)
        _step("sync_spot", lambda: sync_spot("all"))
        _step("history", lambda: sync_history("all", top=top, lookback_days=lookback_days))
        _step("benchmarks", lambda: sync_benchmarks(lookback_days=lookback_days))
    finally:
        resumed = _warehouse_resume()
        result["warehouse_pause"] = {"paused": paused, "resumed": resumed}
    return result
