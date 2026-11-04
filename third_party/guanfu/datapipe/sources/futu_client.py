"""Futu OpenD as a preferred quote source across markets (US / HK / A).

Optional dependency by design: if ``futu-api`` is not installed or OpenD is
not reachable, fetchers return empty frames so callers fall back to free/public
sources. OpenD runs locally (default 127.0.0.1:11111); set
AH_SCREENER_USE_FUTU=0 to disable.
"""

from __future__ import annotations

import importlib.util
import os
from time import sleep

import pandas as pd

from datapipe.classification import (
    infer_a_board,
    infer_a_exchange,
    infer_board,
    infer_us_board,
    is_st_name,
)

FUTU_HOST = os.getenv("AH_SCREENER_FUTU_HOST", "127.0.0.1")
FUTU_PORT = int(os.getenv("AH_SCREENER_FUTU_PORT", "11111"))
USE_FUTU = os.getenv("AH_SCREENER_USE_FUTU", "1").lower() not in {"0", "false", "no"}
SNAPSHOT_BATCH_SIZE = 400
HK_BENCHMARK_CODES = {
    "HSI": "HK.800000",
    "HSCEI": "HK.800100",
}


def futu_available() -> bool:
    return USE_FUTU and importlib.util.find_spec("futu") is not None


def futu_code(market: str, symbol: str) -> str:
    """Map a (market, symbol) to a Futu code, e.g. US.AAPL / HK.00700 / SH.600000."""
    market = str(market).upper()
    raw = str(symbol).strip().upper()
    if market == "US":
        return f"US.{raw.replace('.', '-')}"
    if market == "HK":
        digits = "".join(ch for ch in raw if ch.isdigit()).zfill(5)
        return f"HK.{digits}"
    if market == "A":
        digits = "".join(ch for ch in raw if ch.isdigit())
        exchange = infer_a_exchange(digits)
        if exchange == "BSE":
            raise ValueError(f"Futu OpenD does not expose Beijing Stock Exchange codes: {symbol}")
        prefix = "SH" if exchange == "SSE" else "SZ"
        return f"{prefix}.{digits}"
    raise ValueError(f"Unsupported market for Futu: {market}")


def _normalize_history(raw: pd.DataFrame, market: str, symbol: str, adj_type: str) -> pd.DataFrame:
    if raw is None or raw.empty:
        return pd.DataFrame()
    frame = raw.rename(
        columns={
            "time_key": "date",
            "data_date": "date",
            "open": "open",
            "high": "high",
            "low": "low",
            "close": "close",
            "open_price": "open",
            "high_price": "high",
            "low_price": "low",
            "close_price": "close",
            "volume": "volume",
            "turnover": "amount",
        }
    )
    num = lambda col: pd.to_numeric(frame[col], errors="coerce") if col in frame.columns else pd.NA  # noqa: E731
    out = pd.DataFrame(
        {
            "market": market,
            "symbol": str(symbol),
            "trade_date": pd.to_datetime(frame.get("date"), errors="coerce"),
            "open": num("open"),
            "high": num("high"),
            "low": num("low"),
            "close": num("close"),
            "volume": num("volume"),
            "amount": num("amount"),
            "adj_type": adj_type,
            "source": "futu.opend.history_kline",
            "updated_at": pd.Timestamp.now(),
        }
    )
    return out.dropna(subset=["trade_date", "close"])


def _unpack_history_result(result: object) -> tuple[object, object, object | None]:
    """Support both older 2-tuple and current 3-tuple Futu SDK returns."""
    if not isinstance(result, tuple):
        raise RuntimeError(f"Unexpected Futu history response: {type(result).__name__}")
    if len(result) == 3:
        ret, raw, page_req_key = result
        return ret, raw, page_req_key
    if len(result) == 2:
        ret, raw = result
        return ret, raw, None
    raise RuntimeError(f"Unexpected Futu history response tuple length: {len(result)}")


def fetch_futu_history(
    market: str, symbol: str, start_date: str, end_date: str, adjust: str = "qfq"
) -> pd.DataFrame:
    """Daily K-lines from local Futu OpenD; empty frame when unavailable (caller falls back)."""
    code = futu_code(market, symbol)
    return _fetch_futu_history_code(code, market, symbol, start_date, end_date, adjust=adjust)


def _fetch_futu_history_code(
    code: str,
    market: str,
    symbol: str,
    start_date: str,
    end_date: str,
    adjust: str = "qfq",
) -> pd.DataFrame:
    if not futu_available():
        return pd.DataFrame()
    import futu

    quote_ctx = futu.OpenQuoteContext(host=FUTU_HOST, port=FUTU_PORT)
    try:
        autype = getattr(futu, "AuType", None)
        autype_value = getattr(autype, "QFQ", None) if adjust else getattr(autype, "NONE", None)
        if autype_value is None:
            autype_value = "qfq" if adjust else None
        page_req_key = None
        pages: list[pd.DataFrame] = []
        while True:
            result = quote_ctx.request_history_kline(
                code,
                start=pd.to_datetime(start_date).strftime("%Y-%m-%d"),
                end=pd.to_datetime(end_date).strftime("%Y-%m-%d"),
                ktype=futu.KLType.K_DAY,
                autype=autype_value,
                max_count=1000,
                page_req_key=page_req_key,
            )
            ret, raw, page_req_key = _unpack_history_result(result)
            if ret != getattr(futu, "RET_OK", 0):
                raise RuntimeError(str(raw))
            if raw is not None and not raw.empty:
                pages.append(raw)
            if page_req_key is None:
                break
        raw = pd.concat(pages, ignore_index=True) if pages else pd.DataFrame()
        return _normalize_history(raw, market=market, symbol=symbol, adj_type=adjust or "raw")
    finally:
        quote_ctx.close()


def _futu_benchmark_code(market: str, symbol: str) -> str:
    market = str(market).upper()
    raw = str(symbol).strip().upper()
    if market == "A":
        clean = "".join(ch for ch in raw if ch.isdigit()).zfill(6)
        prefix = "SZ" if clean.startswith("399") else "SH"
        return f"{prefix}.{clean}"
    if market == "HK":
        clean = raw.removeprefix("HK.")
        return HK_BENCHMARK_CODES.get(clean, f"HK.{clean}")
    if market == "US":
        return futu_code("US", raw)
    raise ValueError(f"Unsupported benchmark market for Futu: {market}")


def fetch_futu_benchmark_history(
    market: str, symbol: str, start_date: str, end_date: str
) -> pd.DataFrame:
    """Benchmark K-lines via OpenD. Empty when unavailable; caller keeps legacy fallback."""
    code = _futu_benchmark_code(market, symbol)
    history = _fetch_futu_history_code(code, market, symbol, start_date, end_date, adjust="raw")
    if not history.empty:
        history = history.copy()
        history["adj_type"] = "benchmark"
        history["source"] = "futu.opend.history_kline.benchmark"
    return history


def _truthy(value: object) -> bool:
    return bool(value is True or str(value).strip().lower() in {"1", "true", "yes"})


def _futu_symbol(market: str, code: object) -> str:
    raw = str(code or "").strip().upper()
    suffix = raw.split(".", 1)[-1] if "." in raw else raw
    if market == "HK":
        return "".join(ch for ch in suffix if ch.isdigit()).zfill(5)
    if market == "A":
        return "".join(ch for ch in suffix if ch.isdigit()).zfill(6)
    if market == "US":
        return suffix.replace("-", ".")
    return suffix


def _stock_type_asset_type(value: object, default: str) -> str:
    text = str(value or "").strip().upper()
    if "ETF" in text:
        return "etf"
    if "STOCK" in text:
        return "stock"
    return default


def _futu_us_exchange(value: object) -> str:
    text = str(value or "").strip().upper()
    if not text or text == "N/A":
        return "UNKNOWN"
    if "NASDAQ" in text:
        return "NASDAQ"
    if "ARCA" in text:
        return "NYSE_ARCA"
    if "AMEX" in text or "AMERICAN" in text:
        return "NYSE_AMERICAN"
    if "NYSE" in text:
        return "NYSE"
    return text


def _futu_currency(market: str) -> str:
    return {"A": "CNY", "HK": "HKD", "US": "USD"}.get(market, "")


def _futu_board(
    market: str,
    symbol: str,
    asset_type: str,
    exchange: str,
    *,
    is_hk_connect: bool = False,
) -> str:
    if market == "A":
        return infer_a_board(symbol, asset_type)
    if market == "HK":
        if asset_type == "etf":
            return "HK ETF"
        return infer_board("HK", symbol, "", asset_type, is_hk_connect)
    if market == "US":
        return infer_us_board(exchange, asset_type)
    return ""


def _normalize_futu_spot(
    market: str,
    basics: pd.DataFrame,
    snapshot: pd.DataFrame,
    *,
    default_asset_type: str = "stock",
    hk_connect_symbols: set[str] | None = None,
    hk_connect_source: str = "futu.opend.get_plate_stock:HK.GangGuTong",
    hk_connect_confidence: str = "high",
) -> tuple[pd.DataFrame, pd.DataFrame]:
    now = pd.Timestamp.now()
    if basics is None or basics.empty:
        return pd.DataFrame(), pd.DataFrame()

    b = basics.copy()
    b["symbol"] = b["code"].map(lambda value: _futu_symbol(market, value))
    b = b[b["symbol"].ne("")]
    b = b.drop_duplicates("symbol", keep="first")
    if b.empty:
        return pd.DataFrame(), pd.DataFrame()

    name_series = b["name"] if "name" in b.columns else b.get("stock_name", b["symbol"])
    asset_type = b.get("stock_type", pd.Series(default_asset_type, index=b.index)).map(
        lambda value: _stock_type_asset_type(value, default_asset_type)
    )
    hk_connect_symbols = hk_connect_symbols or set()
    is_hk_connect = (
        b["symbol"].isin(hk_connect_symbols) if market == "HK" else pd.Series(False, index=b.index)
    )
    if market == "A":
        exchange = b["symbol"].map(infer_a_exchange)
    elif market == "HK":
        exchange = pd.Series("HKEX", index=b.index)
    elif market == "US":
        exchange = b.get("exchange_type", pd.Series("UNKNOWN", index=b.index)).map(
            _futu_us_exchange
        )
    else:
        exchange = pd.Series("UNKNOWN", index=b.index)
    status = b.get("delisting", pd.Series(False, index=b.index)).map(
        lambda value: "delisted" if _truthy(value) else "listed"
    )

    securities = pd.DataFrame(
        {
            "market": market,
            "symbol": b["symbol"],
            "asset_type": asset_type,
            "board": [
                _futu_board(
                    market,
                    symbol,
                    str(asset),
                    exch,
                    is_hk_connect=bool(connect),
                )
                for symbol, asset, exch, connect in zip(
                    b["symbol"], asset_type, exchange, is_hk_connect, strict=False
                )
            ],
            "name": name_series.astype(str),
            "exchange": exchange,
            "currency": _futu_currency(market),
            "status": status,
            "is_st": name_series.map(is_st_name) if market == "A" else False,
            "is_hk_connect": is_hk_connect,
            "metadata_source": "futu.opend.get_stock_basicinfo"
            + (
                f"; hk_connect={hk_connect_source}"
                if market == "HK" and default_asset_type == "stock"
                else ""
            ),
            "metadata_confidence": hk_connect_confidence if market == "HK" else "high",
            "updated_at": now,
        }
    )

    snap = snapshot.copy() if snapshot is not None and not snapshot.empty else pd.DataFrame()
    if not snap.empty:
        snap["symbol"] = snap["code"].map(lambda value: _futu_symbol(market, value))
        snap = snap[snap["symbol"].ne("")]
        snap = snap.drop_duplicates("symbol", keep="last").set_index("symbol")

    symbols = securities["symbol"]

    def _aligned(names: list[str]) -> pd.Series:
        """First available snapshot column, reindexed to ``symbols`` (NA when absent)."""
        if not snap.empty:
            for name in names:
                if name in snap.columns:
                    return pd.Series(
                        snap[name].reindex(symbols.to_numpy()).to_numpy(), index=symbols.index
                    )
        return pd.Series(pd.NA, index=symbols.index)

    def _num(names: list[str]) -> pd.Series:
        return pd.to_numeric(_aligned(names), errors="coerce")

    last = _num(["last_price"])
    prev = _num(["prev_close_price"])
    pct = ((last / prev - 1) * 100).where(last.notna() & prev.notna() & prev.ne(0), pd.NA)
    trade_date = pd.to_datetime(_aligned(["update_time"]), errors="coerce").fillna(now.normalize())

    snapshots = pd.DataFrame(
        {
            "market": market,
            "symbol": symbols,
            "asset_type": securities["asset_type"],
            "board": securities["board"],
            "trade_date": trade_date,
            "name": securities["name"],
            "last_price": last,
            "pct_change": pct,
            "volume": _num(["volume"]),
            "amount": _num(["turnover"]),
            "turnover_rate": _num(["turnover_rate"]),
            "pe_ttm": _num(["pe_ttm", "pe_ttm_ratio", "pe_ratio"]),
            "pb": _num(["pb_rate", "pb_ratio"]),
            "market_cap": _num(["total_market_val", "market_val"]),
            "source": "futu.opend.get_market_snapshot",
            "updated_at": now,
        }
    )
    if not snapshots.empty and not snap.empty:
        snapshots = snapshots.dropna(subset=["last_price"])
    return securities.drop_duplicates(["market", "symbol"]), snapshots


def _normalize_hk_etf(
    basics: pd.DataFrame, snapshot: pd.DataFrame
) -> tuple[pd.DataFrame, pd.DataFrame]:
    """Build (securities, market_snapshots) frames from Futu HK ETF basics + snapshot.

    Pure function (unit-testable) so the Futu field mapping is verifiable without OpenD.
    """
    now = pd.Timestamp.now()
    if basics is None or basics.empty:
        return pd.DataFrame(), pd.DataFrame()
    b = basics.copy()
    b["symbol"] = b["code"].astype(str).str.split(".").str[-1].str.zfill(5)
    b = b.drop_duplicates("symbol", keep="first")  # Futu can list a code more than once
    snap = snapshot.copy() if snapshot is not None and not snapshot.empty else pd.DataFrame()
    if not snap.empty:
        snap["symbol"] = snap["code"].astype(str).str.split(".").str[-1].str.zfill(5)
        snap = snap.drop_duplicates("symbol", keep="first").set_index("symbol")

    def _col(sym: str, name: str):
        return (
            snap.loc[sym, name]
            if (not snap.empty and sym in snap.index and name in snap.columns)
            else pd.NA
        )

    securities = pd.DataFrame(
        {
            "market": "HK",
            "symbol": b["symbol"],
            "name": b.get("name", b["symbol"]),
            "asset_type": "etf",
            "board": "HK ETF",
            "exchange": "HKEX",
            "currency": "HKD",
            "status": "listed",
            "is_st": False,
            "is_hk_connect": False,
            "metadata_source": "futu.opend.get_stock_basicinfo",
            "metadata_confidence": "high",
            "updated_at": now,
        }
    )
    rows = []
    for _, row in b.iterrows():
        sym = row["symbol"]
        last = pd.to_numeric(pd.Series([_col(sym, "last_price")]), errors="coerce").iloc[0]
        prev = pd.to_numeric(pd.Series([_col(sym, "prev_close_price")]), errors="coerce").iloc[0]
        pct = ((last / prev - 1) * 100) if pd.notna(last) and pd.notna(prev) and prev else pd.NA
        rows.append(
            {
                "market": "HK",
                "symbol": sym,
                "asset_type": "etf",
                "board": "HK ETF",
                "trade_date": pd.to_datetime(_col(sym, "update_time"), errors="coerce"),
                "name": row.get("name", sym),
                "last_price": last,
                "pct_change": pct,
                "volume": pd.to_numeric(pd.Series([_col(sym, "volume")]), errors="coerce").iloc[0],
                "amount": pd.to_numeric(pd.Series([_col(sym, "turnover")]), errors="coerce").iloc[
                    0
                ],
                "turnover_rate": pd.to_numeric(
                    pd.Series([_col(sym, "turnover_rate")]), errors="coerce"
                ).iloc[0],
                "pe_ttm": pd.NA,
                "pb": pd.NA,
                "market_cap": pd.NA,
                "source": "futu.opend.get_market_snapshot",
                "updated_at": now,
            }
        )
    snapshots = pd.DataFrame(rows)
    if not snapshots.empty:
        snapshots["trade_date"] = snapshots["trade_date"].fillna(now.normalize())
    return securities, snapshots


def _normalize_hk_stock(
    basics: pd.DataFrame,
    snapshot: pd.DataFrame,
    *,
    hk_connect_symbols: set[str] | None = None,
    hk_connect_source: str = "futu.hk_connect.unavailable",
    hk_connect_confidence: str = "low",
) -> tuple[pd.DataFrame, pd.DataFrame]:
    now = pd.Timestamp.now()
    if basics is None or basics.empty:
        return pd.DataFrame(), pd.DataFrame()
    b = basics.copy()
    b["symbol"] = b["code"].astype(str).str.split(".").str[-1].str.zfill(5)
    b = b.drop_duplicates("symbol", keep="first")
    name = b["name"] if "name" in b.columns else b.get("stock_name", b["symbol"])
    hk_connect_symbols = hk_connect_symbols or set()
    is_hk_connect = b["symbol"].isin(hk_connect_symbols)

    snap = snapshot.copy() if snapshot is not None and not snapshot.empty else pd.DataFrame()
    if not snap.empty:
        snap["symbol"] = snap["code"].astype(str).str.split(".").str[-1].str.zfill(5)
        snap = snap.drop_duplicates("symbol", keep="last").set_index("symbol")

    def _col(sym: str, name: str):
        return (
            snap.loc[sym, name]
            if (not snap.empty and sym in snap.index and name in snap.columns)
            else pd.NA
        )

    securities = pd.DataFrame(
        {
            "market": "HK",
            "symbol": b["symbol"],
            "name": name,
            "asset_type": "stock",
            "board": is_hk_connect.map(lambda value: infer_board("HK", "", "", "stock", value)),
            "exchange": "HKEX",
            "currency": "HKD",
            "status": "listed",
            "is_st": False,
            "is_hk_connect": is_hk_connect,
            "metadata_source": f"futu.opend.get_stock_basicinfo; hk_connect={hk_connect_source}",
            "metadata_confidence": hk_connect_confidence,
            "updated_at": now,
        }
    )
    rows = []
    for _, row in b.iterrows():
        sym = row["symbol"]
        last = pd.to_numeric(pd.Series([_col(sym, "last_price")]), errors="coerce").iloc[0]
        prev = pd.to_numeric(pd.Series([_col(sym, "prev_close_price")]), errors="coerce").iloc[0]
        pct = ((last / prev - 1) * 100) if pd.notna(last) and pd.notna(prev) and prev else pd.NA
        rows.append(
            {
                "market": "HK",
                "symbol": sym,
                "asset_type": "stock",
                "board": row.get("board", "港股通" if sym in hk_connect_symbols else "非港股通"),
                "trade_date": pd.to_datetime(_col(sym, "update_time"), errors="coerce"),
                "name": row.get("name", row.get("stock_name", sym)),
                "last_price": last,
                "pct_change": pct,
                "volume": pd.to_numeric(pd.Series([_col(sym, "volume")]), errors="coerce").iloc[0],
                "amount": pd.to_numeric(pd.Series([_col(sym, "turnover")]), errors="coerce").iloc[
                    0
                ],
                "turnover_rate": pd.to_numeric(
                    pd.Series([_col(sym, "turnover_rate")]), errors="coerce"
                ).iloc[0],
                "pe_ttm": pd.to_numeric(pd.Series([_col(sym, "pe_ttm")]), errors="coerce").iloc[0],
                "pb": pd.to_numeric(pd.Series([_col(sym, "pb_rate")]), errors="coerce").iloc[0],
                "market_cap": pd.to_numeric(
                    pd.Series([_col(sym, "total_market_val")]), errors="coerce"
                ).iloc[0],
                "source": "futu.opend.get_market_snapshot",
                "updated_at": now,
            }
        )
    snapshots = pd.DataFrame(rows)
    if not snapshots.empty:
        snapshots["trade_date"] = snapshots["trade_date"].fillna(now.normalize())
    return securities, snapshots


def _market_enums_for(futu, market: str) -> list[object]:
    if market == "A":
        return [futu.Market.SH, futu.Market.SZ]
    if market == "HK":
        return [futu.Market.HK]
    if market == "US":
        return [futu.Market.US]
    raise ValueError(f"Unsupported market for Futu spot: {market}")


def _security_type_for(futu, asset_type: str | None) -> object:
    if asset_type == "etf":
        return futu.SecurityType.ETF
    if asset_type == "stock":
        return futu.SecurityType.STOCK
    return futu.SecurityType.NONE


def _fetch_stock_basicinfo(
    quote_ctx: object,
    futu: object,
    market: str,
    asset_type: str | None,
    codes: list[str] | None = None,
) -> pd.DataFrame:
    frames: list[pd.DataFrame] = []
    security_type = _security_type_for(futu, asset_type)
    if codes is not None:
        for i in range(0, len(codes), SNAPSHOT_BATCH_SIZE):
            chunk = codes[i : i + SNAPSHOT_BATCH_SIZE]
            if not chunk:
                continue
            ret, data = quote_ctx.get_stock_basicinfo(
                _market_enums_for(futu, market)[0], security_type, chunk
            )
            if ret == getattr(futu, "RET_OK", 0) and data is not None and not data.empty:
                frames.append(data)
        return pd.concat(frames, ignore_index=True) if frames else pd.DataFrame()

    for market_enum in _market_enums_for(futu, market):
        ret, data = quote_ctx.get_stock_basicinfo(market_enum, security_type)
        if ret == getattr(futu, "RET_OK", 0) and data is not None and not data.empty:
            frames.append(data)
    return pd.concat(frames, ignore_index=True) if frames else pd.DataFrame()


def _fetch_market_snapshot(quote_ctx: object, futu: object, codes: list[str]) -> pd.DataFrame:
    frames: list[pd.DataFrame] = []
    for i in range(0, len(codes), SNAPSHOT_BATCH_SIZE):
        chunk = codes[i : i + SNAPSHOT_BATCH_SIZE]
        if not chunk:
            continue
        ret, data = quote_ctx.get_market_snapshot(chunk)
        if ret == getattr(futu, "RET_OK", 0) and data is not None and not data.empty:
            frames.append(data)
    return pd.concat(frames, ignore_index=True) if frames else pd.DataFrame()


def fetch_futu_spot(
    market: str,
    asset_type: str | None = "stock",
    *,
    symbols: list[str] | None = None,
    include_snapshot: bool = True,
    hk_connect_symbols: set[str] | None = None,
    hk_connect_source: str = "futu.opend.get_plate_stock:HK.GangGuTong",
    hk_connect_confidence: str = "high",
) -> tuple[pd.DataFrame, pd.DataFrame]:
    """Security master + optional snapshot via OpenD across A/HK/US."""
    if not futu_available():
        return pd.DataFrame(), pd.DataFrame()
    import futu

    normalized_market = str(market).upper()
    codes: list[str] | None = None
    if symbols is not None:
        codes = []
        for symbol in symbols:
            try:
                codes.append(futu_code(normalized_market, symbol))
            except ValueError:
                continue
        if not codes:
            return pd.DataFrame(), pd.DataFrame()

    quote_ctx = futu.OpenQuoteContext(host=FUTU_HOST, port=FUTU_PORT)
    try:
        basics = _fetch_stock_basicinfo(quote_ctx, futu, normalized_market, asset_type, codes)
        if basics.empty:
            return pd.DataFrame(), pd.DataFrame()
        snapshot = (
            _fetch_market_snapshot(quote_ctx, futu, basics["code"].astype(str).tolist())
            if include_snapshot
            else pd.DataFrame()
        )
        return _normalize_futu_spot(
            normalized_market,
            basics,
            snapshot,
            default_asset_type=asset_type or "stock",
            hk_connect_symbols=hk_connect_symbols,
            hk_connect_source=hk_connect_source,
            hk_connect_confidence=hk_connect_confidence,
        )
    finally:
        quote_ctx.close()


def fetch_futu_hk_connect_symbols() -> tuple[set[str], str, str]:
    """Hong Kong Stock Connect membership via Futu plate constituents."""
    if not futu_available():
        return set(), "futu.opend.unavailable", "low"
    import futu

    quote_ctx = futu.OpenQuoteContext(host=FUTU_HOST, port=FUTU_PORT)
    try:
        ret, data = quote_ctx.get_plate_stock("HK.GangGuTong")
        if ret != getattr(futu, "RET_OK", 0) or data is None or data.empty:
            return set(), "futu.opend.get_plate_stock:HK.GangGuTong.empty", "low"
        symbols = {
            _futu_symbol("HK", code)
            for code in data["code"].dropna().astype(str)
            if _futu_symbol("HK", code) != "00000"
        }
        return symbols, "futu.opend.get_plate_stock:HK.GangGuTong", "high"
    finally:
        quote_ctx.close()


def fetch_futu_hk_etf_spot() -> tuple[pd.DataFrame, pd.DataFrame]:
    """HK ETF universe + spot via Futu OpenD; empty when unavailable (caller falls back)."""
    return fetch_futu_spot("HK", "etf")


def fetch_futu_hk_spot(
    *,
    hk_connect_symbols: set[str] | None = None,
    hk_connect_source: str = "futu.hk_connect.unavailable",
    hk_connect_confidence: str = "low",
) -> tuple[pd.DataFrame, pd.DataFrame]:
    """HK stock universe + spot via Futu OpenD; empty when unavailable."""
    return fetch_futu_spot(
        "HK",
        "stock",
        hk_connect_symbols=hk_connect_symbols,
        hk_connect_source=hk_connect_source,
        hk_connect_confidence=hk_connect_confidence,
    )


def fetch_futu_a_spot() -> tuple[pd.DataFrame, pd.DataFrame]:
    """A-share SH/SZ stock universe + spot via Futu OpenD."""
    return fetch_futu_spot("A", "stock")


def fetch_futu_a_etf_spot() -> tuple[pd.DataFrame, pd.DataFrame]:
    """A-share SH/SZ ETF universe + spot via Futu OpenD."""
    return fetch_futu_spot("A", "etf")


def fetch_futu_us_security_master() -> pd.DataFrame:
    """US stock + ETF security master via OpenD."""
    frames = [
        fetch_futu_spot("US", "stock", include_snapshot=False)[0],
        fetch_futu_spot("US", "etf", include_snapshot=False)[0],
    ]
    frames = [frame for frame in frames if not frame.empty]
    return (
        pd.concat(frames, ignore_index=True).drop_duplicates(["market", "symbol"])
        if frames
        else pd.DataFrame()
    )


def fetch_futu_us_spot(
    symbols: list[str] | None = None, master: pd.DataFrame | None = None
) -> tuple[pd.DataFrame, pd.DataFrame]:
    """US spot snapshots via OpenD. `master` is optional metadata enrichment."""
    securities, snapshots = fetch_futu_spot("US", None, symbols=symbols)
    if securities.empty:
        return securities, snapshots
    if master is not None and not master.empty:
        meta = master.drop_duplicates(["market", "symbol"], keep="last")
        # Master is the full per-asset-type universe, so its metadata wins on overlap;
        # snapshot-derived rows only fill symbols the master does not cover.
        securities = pd.concat([meta[meta["symbol"].isin(securities["symbol"])], securities])
        securities = securities.drop_duplicates(["market", "symbol"], keep="first")
        meta_columns = ["market", "symbol", "asset_type", "board", "name"]
        snapshots = snapshots.drop(columns=["asset_type", "board", "name"], errors="ignore").merge(
            securities[meta_columns],
            on=["market", "symbol"],
            how="left",
        )
    return securities, snapshots


def fetch_futu_a_board_tags(kind: str, limit: int | None = None) -> pd.DataFrame:
    """A-share industry/concept tags via OpenD plate constituents."""
    normalized = str(kind).lower()
    if normalized not in {"industry", "concept"} or not futu_available():
        return pd.DataFrame()
    import futu

    quote_ctx = futu.OpenQuoteContext(host=FUTU_HOST, port=FUTU_PORT)
    try:
        plate_class = futu.Plate.INDUSTRY if normalized == "industry" else futu.Plate.CONCEPT
        plate_frames: list[pd.DataFrame] = []
        for market_enum in (futu.Market.SH, futu.Market.SZ):
            ret, plates = quote_ctx.get_plate_list(market_enum, plate_class)
            if ret == getattr(futu, "RET_OK", 0) and plates is not None and not plates.empty:
                plate_frames.append(plates)
        if not plate_frames:
            return pd.DataFrame()
        plates = (
            pd.concat(plate_frames, ignore_index=True)
            .dropna(subset=["code", "plate_name"])
            .drop_duplicates("code")
        )
        if limit is not None:
            plates = plates.head(limit)

        rows: list[pd.DataFrame] = []
        for plate in plates.to_dict("records"):
            ret, data = quote_ctx.get_plate_stock(str(plate["code"]))
            if ret == getattr(futu, "RET_OK", 0) and data is not None and not data.empty:
                frame = data[["code"]].copy()
                frame["plate_name"] = str(plate["plate_name"])
                rows.append(frame)
            sleep(0.2)
        if not rows:
            return pd.DataFrame()
        raw = pd.concat(rows, ignore_index=True)
    finally:
        quote_ctx.close()

    updated_at = pd.Timestamp.now()
    out = pd.DataFrame(
        {
            "market": "A",
            "symbol": raw["code"].map(lambda value: _futu_symbol("A", value)),
            "tag_type": normalized,
            "tag_name": raw["plate_name"].astype(str),
            "evidence_level": "C",
            "source": "futu.opend.get_plate_stock",
            "updated_at": updated_at,
        }
    )
    out = out[out["symbol"].str.fullmatch(r"\d{6}", na=False)]
    out = out[out["symbol"].map(infer_a_exchange).ne("BSE")]
    return out.drop_duplicates(["market", "symbol", "tag_type", "tag_name", "source"])
