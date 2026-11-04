from __future__ import annotations

import json
import re
from datetime import datetime, timedelta
from html import unescape
from pathlib import Path
from typing import Any
from urllib.parse import urljoin

import pandas as pd
import requests


HKEXNEWS_BASE_URL = "https://www1.hkexnews.hk"
HKEXNEWS_PREFIX_URL = f"{HKEXNEWS_BASE_URL}/search/prefix.do"
HKEXNEWS_TITLE_SEARCH_URL = f"{HKEXNEWS_BASE_URL}/search/titleSearchServlet.do"


def _now() -> datetime:
    return datetime.now()


def _clean_hk_symbol(symbol: object) -> str:
    return str(symbol or "").strip().upper().replace("HK", "").zfill(5)


def _headers() -> dict[str, str]:
    return {
        "User-Agent": "ah-stock-screener/0.1 research-tool",
        "Accept": "application/json,text/html,*/*",
    }


def _strip_html(value: object) -> str:
    text = unescape(str(value or ""))
    text = re.sub(r"<br\s*/?>", " ", text, flags=re.IGNORECASE)
    text = re.sub(r"<[^>]+>", " ", text)
    return re.sub(r"\s+", " ", text).strip()


def parse_hkex_jsonp(text: str) -> dict[str, Any]:
    payload = text.strip()
    match = re.match(r"^[^(]*\((.*)\)\s*;?\s*$", payload, flags=re.DOTALL)
    if match:
        payload = match.group(1)
    parsed = json.loads(payload)
    if not isinstance(parsed, dict):
        raise ValueError("HKEXnews JSONP payload is not an object")
    return parsed


def parse_hkex_title_response(payload: dict[str, Any]) -> list[dict[str, Any]]:
    result = payload.get("result", "[]")
    parsed = json.loads(result) if isinstance(result, str) else result
    if not isinstance(parsed, list):
        return []
    return [item for item in parsed if isinstance(item, dict)]


def normalize_hkex_url(link: object) -> str:
    value = unescape(str(link or "").strip())
    if not value:
        return ""
    return urljoin(HKEXNEWS_BASE_URL, value)


def lookup_hkex_stock_id(symbol: str, lang: str = "EN") -> int | None:
    clean_symbol = _clean_hk_symbol(symbol)
    params = {
        "lang": lang.upper(),
        "type": "A",
        "name": clean_symbol,
        "market": "SEHK",
        "callback": "callback",
    }
    response = requests.get(HKEXNEWS_PREFIX_URL, params=params, headers=_headers(), timeout=20)
    response.raise_for_status()
    payload = parse_hkex_jsonp(response.text)
    stock_info = payload.get("stockInfo") or []
    if not isinstance(stock_info, list):
        return None
    for item in stock_info:
        if _clean_hk_symbol(item.get("code")) == clean_symbol:
            return int(item["stockId"])
    return None


def _date_arg(value: str | None, default: datetime) -> str:
    if not value:
        return default.strftime("%Y%m%d")
    return pd.Timestamp(value).strftime("%Y%m%d")


def _infer_document_type(title: str, headline: str) -> str:
    text = f"{title} {headline}".lower()
    if "annual report" in text:
        return "annual_report"
    if "annual results" in text:
        return "annual_results"
    if "interim report" in text or "interim results" in text:
        return "interim_report"
    if "quarterly" in text:
        return "quarterly_report"
    return "announcement"


def fetch_hkex_announcements(
    *,
    symbol: str,
    from_date: str | None = None,
    to_date: str | None = None,
    keywords: list[str] | None = None,
    limit: int = 20,
    lang: str = "EN",
) -> pd.DataFrame:
    if limit <= 0:
        return pd.DataFrame()
    stock_id = lookup_hkex_stock_id(symbol, lang=lang)
    if stock_id is None:
        return pd.DataFrame()

    end = _now()
    start = end - timedelta(days=365)
    row_range = max(limit * 5 if keywords else limit, 20)
    params = {
        "sortDir": "0",
        "sortByOptions": "DateTime",
        "category": "0",
        "market": "SEHK",
        "stockId": str(stock_id),
        "documentType": "-1",
        "fromDate": _date_arg(from_date, start),
        "toDate": _date_arg(to_date, end),
        "title": "",
        "searchType": "0",
        "t1code": "-2",
        "t2Gcode": "-2",
        "t2code": "-2",
        "rowRange": str(row_range),
        "lang": lang.upper(),
    }
    response = requests.get(HKEXNEWS_TITLE_SEARCH_URL, params=params, headers=_headers(), timeout=30)
    response.raise_for_status()
    rows = parse_hkex_title_response(response.json())
    clean_symbol = _clean_hk_symbol(symbol)
    records: list[dict[str, object]] = []
    for item in rows:
        title = _strip_html(item.get("TITLE"))
        headline = _strip_html(item.get("LONG_TEXT") or item.get("SHORT_TEXT"))
        url = normalize_hkex_url(item.get("FILE_LINK") or item.get("DOD_WEB_PATH"))
        release_datetime = pd.to_datetime(item.get("DATE_TIME"), dayfirst=True, errors="coerce")
        records.append(
            {
                "market": "HK",
                "symbol": clean_symbol,
                "stock_id": stock_id,
                "stock_code": _strip_html(item.get("STOCK_CODE")),
                "stock_name": _strip_html(item.get("STOCK_NAME")),
                "news_id": str(item.get("NEWS_ID") or ""),
                "release_datetime": release_datetime,
                "title": title,
                "headline_category": headline,
                "file_type": _strip_html(item.get("FILE_TYPE")),
                "file_info": _strip_html(item.get("FILE_INFO")),
                "url": url,
                "document_type": _infer_document_type(title, headline),
                "source": "hkexnews.title_search",
            }
        )
    frame = pd.DataFrame(records)
    if frame.empty:
        return frame
    if keywords:
        lowered = [keyword.strip().lower() for keyword in keywords if keyword.strip()]
        if lowered:
            text = (frame["title"].fillna("") + " " + frame["headline_category"].fillna("")).str.lower()
            frame = frame[text.apply(lambda value: any(keyword in value for keyword in lowered))]
    return frame.sort_values("release_datetime", ascending=False).head(limit).reset_index(drop=True)


def _safe_filename(symbol: str, news_id: object, release_datetime: object, url: str) -> str:
    date = pd.to_datetime(release_datetime, errors="coerce")
    date_text = date.strftime("%Y%m%d") if pd.notna(date) else _now().strftime("%Y%m%d")
    suffix = Path(url).suffix.lower() or ".pdf"
    stem = re.sub(r"[^A-Za-z0-9_.-]+", "_", f"{_clean_hk_symbol(symbol)}_{date_text}_{news_id}")
    return f"{stem}{suffix}"


def download_hkex_announcements(announcements: pd.DataFrame, output_dir: Path) -> pd.DataFrame:
    if announcements.empty:
        return pd.DataFrame()
    output_dir = output_dir.expanduser().resolve()
    output_dir.mkdir(parents=True, exist_ok=True)
    rows: list[dict[str, object]] = []
    for _, row in announcements.iterrows():
        url = str(row.get("url") or "")
        if not url.lower().endswith(".pdf"):
            continue
        local_path = output_dir / _safe_filename(
            str(row.get("symbol") or ""),
            row.get("news_id") or "",
            row.get("release_datetime"),
            url,
        )
        if not local_path.exists():
            response = requests.get(url, headers=_headers(), timeout=45)
            response.raise_for_status()
            local_path.write_bytes(response.content)
        item = row.to_dict()
        item["local_path"] = str(local_path)
        rows.append(item)
    return pd.DataFrame(rows)
