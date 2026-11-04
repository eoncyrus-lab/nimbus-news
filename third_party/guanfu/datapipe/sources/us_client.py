from __future__ import annotations

import os
from datetime import datetime, timedelta
from functools import lru_cache
from io import StringIO
from time import sleep
from typing import Any

import pandas as pd
import requests

from datapipe.classification import infer_us_board, infer_us_exchange
from datapipe.sources.futu_client import (
    fetch_futu_history,
    fetch_futu_us_security_master,
    fetch_futu_us_spot,
)


def _futu_enabled() -> bool:
    """US-only switch: when ``US_SCREENER_DISABLE_FUTU=1`` the US data path skips
    the Futu/OpenD calls entirely and uses the free sources (SEC + akshare/Nasdaq
    directory) directly. A/H code paths never set this env, so they are unaffected.
    """
    return os.getenv("US_SCREENER_DISABLE_FUTU", "").strip() not in {"1", "true", "True"}


NASDAQ_LISTED_URL = "https://www.nasdaqtrader.com/dynamic/SymDir/nasdaqlisted.txt"
OTHER_LISTED_URL = "https://www.nasdaqtrader.com/dynamic/SymDir/otherlisted.txt"
SEC_TICKERS_URL = "https://www.sec.gov/files/company_tickers.json"
SEC_COMPANYFACTS_URL = "https://data.sec.gov/api/xbrl/companyfacts/CIK{cik:010d}.json"
# datagw /sec-xbrl is the shared data.sec.gov fetch point (cached, single UA);
# direct SEC stays as the fallback (2026-07 R2 T-4).
DATAGW_URL = os.getenv("DATAGW_URL", "http://127.0.0.1:8821")
ALPHA_VANTAGE_LISTING_STATUS_URL = "https://www.alphavantage.co/query"

US_DEFAULT_SYMBOLS: tuple[str, ...] = (
    "AAPL",
    "MSFT",
    "NVDA",
    "AMZN",
    "GOOGL",
    "META",
    "AVGO",
    "TSLA",
    "BRK.B",
    "JPM",
    "LLY",
    "V",
    "MA",
    "UNH",
    "XOM",
    "COST",
    "WMT",
    "HD",
    "PG",
    "JNJ",
    "ABBV",
    "MRK",
    "ORCL",
    "AMD",
    "NFLX",
    "CRM",
    "ADBE",
    "QCOM",
    "TXN",
    "INTC",
    "BAC",
    "KO",
    "PEP",
    "MCD",
    "TMO",
    "LIN",
    "CAT",
    "GE",
    "NKE",
    "DIS",
    "BABA",
    "JD",
    "BIDU",
    "NTES",
    "BILI",
    "PDD",
    "LI",
    "NIO",
    "XPEV",
    "SPY",
    "QQQ",
    "DIA",
    "IWM",
    "XLK",
    "SMH",
    "SOXX",
    "XLE",
    "GLD",
)


def _now() -> pd.Timestamp:
    return pd.Timestamp(datetime.now())


def _sec_headers() -> dict[str, str]:
    return {
        "User-Agent": os.getenv(
            "AH_SCREENER_SEC_USER_AGENT",
            "ah-stock-screener/0.1 research-tool contact@example.com",
        ),
        "Accept-Encoding": "gzip, deflate",
    }


def _clean_us_symbol(symbol: object) -> str:
    return str(symbol or "").strip().upper().replace("/", ".")


def _number(series: pd.Series) -> pd.Series:
    return pd.to_numeric(series, errors="coerce")


def _read_symbol_directory(url: str) -> pd.DataFrame:
    response = requests.get(url, timeout=20)
    response.raise_for_status()
    text = response.text.strip()
    frame = pd.read_csv(StringIO(text), sep="|")
    first_column = frame.columns[0]
    frame = frame[~frame[first_column].astype(str).str.contains("File Creation Time", na=False)]
    return frame


def fetch_us_security_master() -> pd.DataFrame:
    if _futu_enabled():
        try:
            futu_master = fetch_futu_us_security_master()
        except Exception:
            futu_master = pd.DataFrame()
        if not futu_master.empty:
            return futu_master

    updated_at = _now()
    rows: list[pd.DataFrame] = []

    try:
        nasdaq = _read_symbol_directory(NASDAQ_LISTED_URL)
        if not nasdaq.empty:
            symbol = nasdaq["Symbol"].map(_clean_us_symbol)
            etf = (
                nasdaq.get("ETF", pd.Series("N", index=nasdaq.index))
                .astype(str)
                .str.upper()
                .eq("Y")
            )
            exchange = pd.Series("NASDAQ", index=nasdaq.index)
            rows.append(
                pd.DataFrame(
                    {
                        "market": "US",
                        "symbol": symbol,
                        "asset_type": etf.map(lambda value: "etf" if value else "stock"),
                        "board": [
                            infer_us_board(exchange.iloc[i], "etf" if etf.iloc[i] else "stock")
                            for i in range(len(nasdaq))
                        ],
                        "name": nasdaq["Security Name"].astype(str),
                        "exchange": exchange,
                        "currency": "USD",
                        "status": "listed",
                        "is_st": False,
                        "is_hk_connect": False,
                        "metadata_source": "nasdaqtrader.nasdaqlisted",
                        "metadata_confidence": "high",
                        "updated_at": updated_at,
                    }
                )
            )
    except Exception:
        pass

    try:
        other = _read_symbol_directory(OTHER_LISTED_URL)
        if not other.empty:
            symbol = other["ACT Symbol"].map(_clean_us_symbol)
            etf = (
                other.get("ETF", pd.Series("N", index=other.index)).astype(str).str.upper().eq("Y")
            )
            exchange = other["Exchange"].map(infer_us_exchange)
            rows.append(
                pd.DataFrame(
                    {
                        "market": "US",
                        "symbol": symbol,
                        "asset_type": etf.map(lambda value: "etf" if value else "stock"),
                        "board": [
                            infer_us_board(exchange.iloc[i], "etf" if etf.iloc[i] else "stock")
                            for i in range(len(other))
                        ],
                        "name": other["Security Name"].astype(str),
                        "exchange": exchange,
                        "currency": "USD",
                        "status": "listed",
                        "is_st": False,
                        "is_hk_connect": False,
                        "metadata_source": "nasdaqtrader.otherlisted",
                        "metadata_confidence": "high",
                        "updated_at": updated_at,
                    }
                )
            )
    except Exception:
        pass

    if rows:
        return pd.concat(rows, ignore_index=True).drop_duplicates(
            ["market", "symbol"], keep="first"
        )

    return pd.DataFrame(
        {
            "market": "US",
            "symbol": list(US_DEFAULT_SYMBOLS),
            "asset_type": [
                "etf"
                if symbol in {"SPY", "QQQ", "DIA", "IWM", "XLK", "SMH", "SOXX", "XLE", "GLD"}
                else "stock"
                for symbol in US_DEFAULT_SYMBOLS
            ],
            "board": [
                "US ETF"
                if symbol in {"SPY", "QQQ", "DIA", "IWM", "XLK", "SMH", "SOXX", "XLE", "GLD"}
                else "US Default"
                for symbol in US_DEFAULT_SYMBOLS
            ],
            "name": list(US_DEFAULT_SYMBOLS),
            "exchange": "UNKNOWN",
            "currency": "USD",
            "status": "listed",
            "is_st": False,
            "is_hk_connect": False,
            "metadata_source": "curated_us_default_universe",
            "metadata_confidence": "medium",
            "updated_at": updated_at,
        }
    )


def normalize_us_delisted_lifecycle(raw: pd.DataFrame, *, source: str) -> pd.DataFrame:
    if raw.empty:
        return pd.DataFrame()
    required = {"symbol", "name", "exchange", "assetType", "ipoDate", "delistingDate", "status"}
    missing = required.difference(raw.columns)
    if missing:
        raise RuntimeError(f"US delisted CSV missing columns: {', '.join(sorted(missing))}")

    updated_at = _now()
    asset_type = (
        raw["assetType"]
        .astype(str)
        .str.lower()
        .map(lambda value: "etf" if value == "etf" else "stock")
    )
    frame = pd.DataFrame(
        {
            "market": "US",
            "symbol": raw["symbol"].map(_clean_us_symbol),
            "name": raw["name"].astype(str).str.strip(),
            "asset_type": asset_type,
            "exchange": raw["exchange"].astype(str).str.strip(),
            "listing_date": pd.to_datetime(raw["ipoDate"], errors="coerce").dt.date,
            "delist_date": pd.to_datetime(raw["delistingDate"], errors="coerce").dt.date,
            "status": "delisted",
            "event_type": "delisting",
            "source": source,
            "updated_at": updated_at,
        }
    )
    frame = frame[frame["symbol"].ne("")]
    frame = frame[frame["status"].eq("delisted")]
    # Tickers can be reused. Include the delisting date in the source key so
    # repeated lifecycle rows do not overwrite each other in existing DBs.
    frame["source"] = (
        source
        + ":"
        + frame["symbol"]
        + ":"
        + pd.to_datetime(frame["delist_date"], errors="coerce")
        .dt.strftime("%Y%m%d")
        .fillna("unknown")
    )
    return frame.drop_duplicates(["market", "symbol", "event_type", "source"])


def fetch_us_delisted_lifecycle(api_key: str | None = None) -> pd.DataFrame:
    api_key = api_key or os.getenv("AH_SCREENER_ALPHA_VANTAGE_KEY")
    if not api_key:
        return pd.DataFrame()

    response = requests.get(
        ALPHA_VANTAGE_LISTING_STATUS_URL,
        params={"function": "LISTING_STATUS", "state": "delisted", "apikey": api_key},
        timeout=30,
    )
    response.raise_for_status()
    text = response.text.strip()
    if not text or "symbol" not in text.splitlines()[0].lower():
        raise RuntimeError("Alpha Vantage LISTING_STATUS did not return a delisted CSV payload")
    raw = pd.read_csv(StringIO(text))
    return normalize_us_delisted_lifecycle(raw, source="alphavantage.listing_status.delisted")


def _normalize_us_history(
    raw: pd.DataFrame, symbol: str, source: str, adj_type: str
) -> pd.DataFrame:
    if raw.empty:
        return pd.DataFrame()
    updated_at = _now()
    date_column = "date" if "date" in raw.columns else "日期"
    close = _number(raw["close"] if "close" in raw.columns else raw["收盘"])
    volume = _number(
        raw["volume"]
        if "volume" in raw.columns
        else raw.get("成交量", pd.Series(pd.NA, index=raw.index))
    )
    return pd.DataFrame(
        {
            "market": "US",
            "symbol": _clean_us_symbol(symbol),
            "trade_date": pd.to_datetime(raw[date_column], errors="coerce"),
            "open": _number(
                raw["open"]
                if "open" in raw.columns
                else raw.get("开盘", pd.Series(pd.NA, index=raw.index))
            ),
            "high": _number(
                raw["high"]
                if "high" in raw.columns
                else raw.get("最高", pd.Series(pd.NA, index=raw.index))
            ),
            "low": _number(
                raw["low"]
                if "low" in raw.columns
                else raw.get("最低", pd.Series(pd.NA, index=raw.index))
            ),
            "close": close,
            "volume": volume,
            "amount": close * volume,
            "adj_type": adj_type,
            "source": source,
            "updated_at": updated_at,
        }
    ).dropna(subset=["trade_date", "close"])


def _fetch_us_history_akshare(symbol: str, adjust: str) -> pd.DataFrame:
    import akshare as ak

    raw = ak.stock_us_daily(symbol=_clean_us_symbol(symbol), adjust=adjust)
    return _normalize_us_history(
        raw, symbol=symbol, source="akshare.stock_us_daily", adj_type=adjust or "raw"
    )


def _fetch_us_history_futu(
    symbol: str, start_date: str, end_date: str, adjust: str
) -> pd.DataFrame:
    """US daily K-lines via the shared Futu OpenD client (empty when unavailable)."""
    return fetch_futu_history("US", symbol, start_date, end_date, adjust=adjust)


def fetch_us_history(symbol: str, start_date: str, end_date: str, adjust: str = "") -> pd.DataFrame:
    start = pd.to_datetime(start_date)
    end = pd.to_datetime(end_date)
    last_error: Exception | None = None
    calls = []
    if _futu_enabled():
        calls.append(
            lambda: _fetch_us_history_futu(
                symbol, start_date=start_date, end_date=end_date, adjust=adjust
            )
        )
    calls.append(lambda: _fetch_us_history_akshare(symbol, adjust=adjust))
    for func in calls:
        try:
            history = func()
            if history.empty:
                raise RuntimeError("empty US history")
            return history[(history["trade_date"] >= start) & (history["trade_date"] <= end)]
        except Exception as exc:
            last_error = exc
            sleep(0.3)
    raise RuntimeError(
        f"All US history sources failed for {symbol}. Last error: {last_error}"
    ) from last_error


def select_us_batch_symbols(
    master: pd.DataFrame,
    *,
    offset: int = 0,
    limit: int = 100,
    include_etf: bool = False,
    asset_type: str | None = None,
) -> list[str]:
    if master.empty or limit <= 0:
        return []
    frame = master.copy()
    if "status" in frame.columns:
        frame = frame[frame["status"].fillna("listed").astype(str).str.lower().eq("listed")]
    if asset_type and "asset_type" in frame.columns:
        frame = frame[
            frame["asset_type"].fillna("stock").astype(str).str.lower().eq(asset_type.lower())
        ]
    elif not include_etf and "asset_type" in frame.columns:
        frame = frame[frame["asset_type"].fillna("stock").astype(str).str.lower().ne("etf")]
    for column in ["asset_type", "exchange"]:
        if column not in frame.columns:
            frame[column] = ""
    frame["symbol"] = frame["symbol"].map(_clean_us_symbol)
    frame = frame[frame["symbol"].ne("")]
    frame = frame.sort_values(["asset_type", "exchange", "symbol"], na_position="last")
    start = max(offset, 0)
    return frame["symbol"].drop_duplicates().iloc[start : start + limit].tolist()


def fetch_us_spot(
    symbols: list[str] | None = None,
    lookback_days: int = 14,
    master: pd.DataFrame | None = None,
) -> tuple[pd.DataFrame, pd.DataFrame]:
    master = fetch_us_security_master() if master is None else master
    selected = [_clean_us_symbol(symbol) for symbol in (symbols or list(US_DEFAULT_SYMBOLS))]
    if _futu_enabled():
        try:
            securities, snapshots = fetch_futu_us_spot(symbols=selected, master=master)
        except Exception:
            securities, snapshots = pd.DataFrame(), pd.DataFrame()
        if not snapshots.empty:
            return securities, snapshots

    master_index = master.set_index("symbol", drop=False)
    end = datetime.now()
    start_date = (end - timedelta(days=lookback_days)).strftime("%Y%m%d")
    end_date = end.strftime("%Y%m%d")
    snapshot_rows: list[dict[str, object]] = []
    for symbol in selected:
        try:
            history = fetch_us_history(symbol, start_date=start_date, end_date=end_date)
        except Exception:
            continue
        if history.empty:
            continue
        latest = history.sort_values("trade_date").iloc[-1]
        previous_close = pd.to_numeric(history["close"], errors="coerce").dropna()
        pct_change = pd.NA
        if len(previous_close) >= 2 and float(previous_close.iloc[-2]) > 0:
            pct_change = (float(previous_close.iloc[-1]) / float(previous_close.iloc[-2]) - 1) * 100
        meta = master_index.loc[symbol] if symbol in master_index.index else None
        asset_type = str(meta["asset_type"]) if meta is not None else "stock"
        board = str(meta["board"]) if meta is not None else infer_us_board("", asset_type)
        snapshot_rows.append(
            {
                "market": "US",
                "symbol": symbol,
                "asset_type": asset_type,
                "board": board,
                "trade_date": latest["trade_date"],
                "name": str(meta["name"]) if meta is not None else symbol,
                "last_price": latest["close"],
                "pct_change": pct_change,
                "volume": latest["volume"],
                "amount": latest["amount"],
                "turnover_rate": pd.NA,
                "pe_ttm": pd.NA,
                "pb": pd.NA,
                "market_cap": pd.NA,
                "source": str(latest["source"]),
                "updated_at": _now(),
            }
        )

    securities = master[master["symbol"].isin(selected)].copy()
    missing = [symbol for symbol in selected if symbol not in set(securities["symbol"])]
    if missing:
        securities = pd.concat(
            [
                securities,
                pd.DataFrame(
                    {
                        "market": "US",
                        "symbol": missing,
                        "asset_type": "stock",
                        "board": "US Default",
                        "name": missing,
                        "exchange": "UNKNOWN",
                        "currency": "USD",
                        "status": "listed",
                        "is_st": False,
                        "is_hk_connect": False,
                        "metadata_source": "curated_us_default_universe",
                        "metadata_confidence": "medium",
                        "updated_at": _now(),
                    }
                ),
            ],
            ignore_index=True,
        )
    return securities.drop_duplicates(["market", "symbol"]), pd.DataFrame(snapshot_rows)


def fetch_us_spot_batch(
    *,
    offset: int = 0,
    limit: int = 100,
    include_etf: bool = False,
    lookback_days: int = 14,
    asset_type: str | None = None,
) -> tuple[pd.DataFrame, pd.DataFrame]:
    master = fetch_us_security_master()
    selected = select_us_batch_symbols(
        master,
        offset=offset,
        limit=limit,
        include_etf=include_etf,
        asset_type=asset_type,
    )
    if not selected:
        return master.iloc[0:0].copy(), pd.DataFrame()
    return fetch_us_spot(symbols=selected, lookback_days=lookback_days, master=master)


@lru_cache(maxsize=1)
def fetch_sec_company_tickers() -> dict[str, dict[str, Any]]:
    # Cached for the process: the full SEC ticker→CIK map was previously re-downloaded
    # once per US symbol during a fundamentals batch (incremental fetching, stage).
    response = requests.get(SEC_TICKERS_URL, headers=_sec_headers(), timeout=20)
    response.raise_for_status()
    raw = response.json()
    mapping: dict[str, dict[str, Any]] = {}
    for value in raw.values():
        ticker = _clean_us_symbol(value.get("ticker"))
        if ticker:
            mapping[ticker] = value
    return mapping


def fetch_sec_companyfacts(symbol: str) -> tuple[dict[str, Any], dict[str, Any]]:
    ticker_map = fetch_sec_company_tickers()
    meta = ticker_map.get(_clean_us_symbol(symbol))
    if not meta:
        raise RuntimeError(f"SEC company ticker map has no CIK for {symbol}")
    cik = int(meta["cik_str"])
    try:  # datagw-first (shared cache), direct SEC fallback
        gw = requests.get(
            f"{DATAGW_URL}/sec-xbrl",
            params={"path": f"/api/xbrl/companyfacts/CIK{cik:010d}.json"},
            timeout=60,
        )
        gw.raise_for_status()
        facts = gw.json().get("data")
        if facts:
            return meta, facts
    except Exception:  # noqa: BLE001 — gateway down/unreachable, go direct
        pass
    response = requests.get(
        SEC_COMPANYFACTS_URL.format(cik=cik), headers=_sec_headers(), timeout=30
    )
    response.raise_for_status()
    return meta, response.json()
