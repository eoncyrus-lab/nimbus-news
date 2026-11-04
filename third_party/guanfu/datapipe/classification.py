from __future__ import annotations

import re

import pandas as pd


ST_PATTERN = re.compile(r"(^|\W)\*?ST|退", re.IGNORECASE)


def normalize_asset_type(value: object, default: str = "stock") -> str:
    text = str(value or "").strip().lower()
    if text in {"stock", "etf"}:
        return text
    return default


def is_st_name(name: object) -> bool:
    return bool(ST_PATTERN.search(str(name or "")))


def infer_a_exchange(symbol: object) -> str:
    clean = str(symbol or "").zfill(6)
    if clean.startswith(("5", "60", "68", "90")):
        return "SSE"
    if clean.startswith(("0", "1", "2", "3")):
        return "SZSE"
    if clean.startswith(("43", "83", "87", "88", "92")):
        return "BSE"
    return "UNKNOWN"


def infer_us_exchange(value: object) -> str:
    text = str(value or "").strip().upper()
    mapping = {
        "Q": "NASDAQ",
        "G": "NASDAQ",
        "S": "NASDAQ",
        "N": "NYSE",
        "A": "NYSE_AMERICAN",
        "P": "NYSE_ARCA",
        "Z": "CBOE_BZX",
        "V": "INVESTORS_EXCHANGE",
    }
    return mapping.get(text, text or "UNKNOWN")


def infer_a_board(symbol: object, asset_type: object = "stock") -> str:
    clean = str(symbol or "").zfill(6)
    if normalize_asset_type(asset_type) == "etf":
        return "ETF"
    if clean.startswith(("688", "689")):
        return "科创板"
    if clean.startswith("30"):
        return "创业板"
    if clean.startswith(("43", "83", "87", "88", "92")):
        return "北交所"
    if clean.startswith(("600", "601", "603", "605", "000", "001", "002", "003")):
        return "主板"
    if clean.startswith(("200", "900")):
        return "B股"
    return "其他A股"


def infer_us_board(exchange: object, asset_type: object = "stock") -> str:
    if normalize_asset_type(asset_type) == "etf":
        return "US ETF"
    normalized = infer_us_exchange(exchange)
    if normalized in {"NASDAQ", "NYSE", "NYSE_ARCA", "NYSE_AMERICAN", "CBOE_BZX"}:
        return normalized
    return "US Other"


def infer_board(market: object, symbol: object, name: object, asset_type: object, is_hk_connect: object = False) -> str:
    normalized_market = str(market or "").upper()
    normalized_asset_type = normalize_asset_type(asset_type)
    if normalized_asset_type == "etf":
        return "ETF"
    if normalized_market == "A":
        return infer_a_board(symbol, normalized_asset_type)
    if normalized_market == "HK":
        return "港股通" if bool(is_hk_connect) else "非港股通"
    if normalized_market == "US":
        return infer_us_board("", normalized_asset_type)
    return "其他"


def infer_status(name: object, asset_type: object = "stock", market: object = "A") -> str:
    if normalize_asset_type(asset_type) == "etf":
        return "listed"
    # ST/退市 语义只对 A 股成立；非 A 股(US/HK 等)不跑名称匹配，避免 "Common Stock"
    # 里的 "St" 等英文误命中 ST_PATTERN。
    if str(market or "").upper() != "A":
        return "listed"
    text = str(name or "")
    if "退" in text:
        return "delisting_risk"
    if is_st_name(text):
        return "st"
    return "listed"


def enrich_security_metadata(df: pd.DataFrame) -> pd.DataFrame:
    if df.empty:
        return df.copy()

    enriched = df.copy()
    if "asset_type" not in enriched.columns:
        enriched["asset_type"] = "stock"
    if "is_hk_connect" not in enriched.columns:
        enriched["is_hk_connect"] = False
    if "is_st" not in enriched.columns:
        enriched["is_st"] = False

    enriched["asset_type"] = enriched["asset_type"].map(normalize_asset_type)
    current_board = enriched.get("board", pd.Series("", index=enriched.index)).astype(str)
    enriched["is_hk_connect"] = (
        enriched["is_hk_connect"].fillna(False).astype(bool) | current_board.eq("港股通")
    )
    # ST/*ST/退市 is an A-share concept only. Gate the whole flag on market=="A"
    # so US/HK never get is_st=True -- including from a stale input status=="st"
    # (the `status` column is recomputed further below, but is_st must not read
    # the pre-recompute value for non-A rows, else corrected US rows keep is_st).
    enriched["is_st"] = enriched.apply(
        lambda row: bool(
            str(row.get("market") or "").upper() == "A"
            and (is_st_name(row.get("name")) or row.get("status") == "st")
        ),
        axis=1,
    )
    enriched["board"] = enriched.apply(
        lambda row: infer_us_board(row.get("exchange"), row.get("asset_type"))
        if str(row.get("market") or "").upper() == "US"
        else infer_board(
            row.get("market"),
            row.get("symbol"),
            row.get("name"),
            row.get("asset_type"),
            row.get("is_hk_connect"),
        ),
        axis=1,
    )
    enriched["status"] = enriched.apply(
        lambda row: infer_status(row.get("name"), row.get("asset_type"), row.get("market")),
        axis=1,
    )
    if "exchange" in enriched.columns:
        a_mask = enriched["market"].astype(str).str.upper().eq("A")
        enriched.loc[a_mask, "exchange"] = enriched.loc[a_mask, "symbol"].map(infer_a_exchange)
    return enriched
