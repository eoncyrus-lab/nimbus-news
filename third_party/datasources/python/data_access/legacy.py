"""Explicit compatibility SDK preserving failure-to-empty legacy behavior.

Apps call these instead of importing yfinance/futu/akshare/openbb or hitting
finance APIs directly (enforced by docs/tools/check-tier-boundary.sh). All calls
go through the data-access facade, which routes by freshness to the right Tier-1
service. Symbols are canonical (US:AAPL / HK:00700 / CN:600519 / CRYPTO:BTC-USD).

    from data_access import legacy as data
    data.quote("US:AAPL", "HK:00700")
    data.history("US:AAPL", limit=30)
    data.fundamentals("US:AAPL"); data.technicals("US:NVDA")
    data.news(symbol="US:NVDA", limit=20)
    data.universe("US", asset_type="stock")
"""
from __future__ import annotations

import json
import os
import urllib.parse
import urllib.request
from typing import Any

FACADE = os.environ.get("DATA_FACADE_URL", "http://127.0.0.1:8800")
TIMEOUT = float(os.environ.get("DATA_FACADE_TIMEOUT", "20"))


def _get(path: str, **params: Any) -> Any:
    q = urllib.parse.urlencode({k: v for k, v in params.items() if v is not None})
    url = f"{FACADE}{path}" + (f"?{q}" if q else "")
    try:
        with urllib.request.urlopen(url, timeout=TIMEOUT) as r:  # noqa: S310
            return json.loads(r.read().decode("utf-8"))
    except Exception:  # noqa: BLE001 — weak dependency: degrade, don't crash the app
        return {}


def quote(*symbols: str) -> list[dict]:
    """Realtime quote(s). Returns [{symbol,last,open,high,low,prev_close,...}]."""
    return _get("/quote", symbols=",".join(symbols)).get("data", [])


def kline(symbol: str, interval: str = "1d", limit: int = 120) -> list[dict]:
    """OHLCV bars via market-hub (indices/HK/CN/ETF/crypto, e.g. "^HSI", "510300.SS",
    "US:SPY"). [{market,symbol,trade_date,open,high,low,close,volume,amount}]."""
    return _get("/kline", symbol=symbol, interval=interval, limit=limit).get("data", [])


def history(symbol: str, from_: str | None = None, to: str | None = None, limit: int | None = None) -> list[dict]:
    return _get("/history", symbol=symbol, **{"from": from_}, to=to, limit=limit).get("data", [])


def fundamentals(symbol: str) -> list[dict]:
    return _get("/fundamentals", symbol=symbol).get("data", [])


def technicals(symbol: str) -> list[dict]:
    return _get("/technicals", symbol=symbol).get("data", [])


def universe(market: str | None = None, asset_type: str | None = None) -> list[dict]:
    return _get("/universe", market=market, asset_type=asset_type).get("data", [])


def screening(market: str | None = None, strategy: str | None = None,
              symbol: str | None = None, limit: int = 50) -> list[dict]:
    """Expert screening rows. Without `symbol`: top picks ranked by score. With
    `symbol` (comma-separated): the latest row per requested symbol (score lookup)."""
    return _get("/screening", market=market, strategy=strategy, symbol=symbol, limit=limit).get("data", [])


def ratings(symbol: str, limit: int = 6) -> list[dict]:
    return _get("/ratings", symbol=symbol, limit=limit).get("data", [])


def profile(symbol: str) -> list[dict]:
    """Company profile: [{symbol,name,sector,industry,exchange,marketCap,
    sharesOutstanding,...}]. marketCap/sharesOutstanding in absolute units."""
    return _get("/profile", symbol=symbol).get("data", [])


def earnings_calendar(from_: str, to: str, symbol: str | None = None) -> list[dict]:
    """Forward/recent earnings calendar [{symbol,date,epsEstimate,epsActual,
    revenueEstimate,revenueActual,quarter,year}], oldest->newest."""
    return _get("/earnings_calendar", **{"from": from_}, to=to, symbol=symbol).get("data", [])


def earnings(symbol: str, limit: int = 8) -> list[dict]:
    """Historical quarterly EPS [{symbol,period,actual,estimate,surprise,...}], newest-first."""
    return _get("/earnings", symbol=symbol, limit=limit).get("data", [])


def insider(symbol: str, from_: str | None = None, to: str | None = None) -> list[dict]:
    """Insider (SEC Form 4) transactions [{name,share,change,filingDate,
    transactionDate,transactionPrice,...}], newest filing first."""
    return _get("/insider", symbol=symbol, **{"from": from_}, to=to).get("data", [])


def short(symbol: str, days: int = 8) -> dict:
    return _get("/short", symbol=symbol, days=days).get("data", {})


def filings(symbol: str, limit: int = 5) -> list[dict]:
    return _get("/filings", symbol=symbol, limit=limit).get("data", [])


def form13f(cik: str, limit: int = 20) -> dict:
    """13F-HR holdings via SEC EDGAR (datagw): {periods: [{period, holdings:[...]}]}.
    (2026-07 R2: replaced the OpenBB path, which had been returning 502.)"""
    return _get("/13f", cik=cik, limit=limit).get("data", {})


def macro(series: str, limit: int = 60) -> list[dict]:
    """FRED macro series, newest first, e.g. macro("DGS10") / macro("VIXCLS")."""
    return _get("/macro", series=series, limit=limit).get("data", [])


def feargreed(limit: int = 1) -> list[dict]:
    """Crypto Fear & Greed index [{value,classification,timestamp}], newest first."""
    return _get("/feargreed", limit=limit).get("data", [])


def funding(symbol: str = "BTC") -> list[dict]:
    """Perp-swap funding rate [{symbol,funding_rate_pct,source,as_of}] (binance->okx)."""
    return _get("/funding", symbol=symbol).get("data", [])


def open_interest(symbol: str = "BTCUSDT", venue: str = "binance") -> dict:
    """Current perp open interest {symbol,open_interest,time} (Go datasources)."""
    return _get("/open_interest", venue=venue, symbol=symbol).get("data", {})


def long_short(symbol: str = "BTCUSDT", venue: str = "binance", period: str = "1h",
               ccy: str | None = None) -> list[dict]:
    """Long/short account-ratio series, ascending. Binance: symbol+period; OKX:
    ccy+period. [{long_short_ratio,long_account,short_account,time}] (Go datasources)."""
    return _get("/long_short", venue=venue, symbol=symbol, period=period, ccy=ccy).get("data", [])


def dvol(currency: str = "BTC") -> dict:
    """Deribit implied-volatility index {currency,value,time} (Go datasources)."""
    return _get("/dvol", currency=currency).get("data", {})


def option(instrument: str) -> dict:
    """Deribit option mark IV + greeks for an instrument, e.g.
    "BTC-25SEP26-66000-P" {mark_iv,delta,gamma,vega,theta,...} (Go datasources)."""
    return _get("/option", instrument=instrument).get("data", {})


def news(symbol: str | None = None, source: str | None = None, limit: int = 20) -> list[dict]:
    out = _get("/news", symbol=symbol, source=source, limit=limit)
    # news platform returns {messages:[...]}; normalize to a list.
    return out.get("messages", out.get("data", [])) if isinstance(out, dict) else out


def company_news(symbol: str, from_: str | None = None, to: str | None = None,
                 limit: int = 20) -> list[dict]:
    """Per-symbol company news via Finnhub [{datetime,headline,source,summary,url,
    image,related,category,id}], newest-first. Defaults to the trailing ~7 days."""
    return _get("/company_news", symbol=symbol, **{"from": from_}, to=to, limit=limit).get("data", [])


def peers(symbol: str) -> list[dict]:
    """Industry peer tickers for a symbol (Finnhub /stock/peers)."""
    return _get("/peers", symbol=symbol).get("data", [])


def basic_financials(symbol: str, metric: str = "all") -> dict:
    """Key metrics (52w high/low, P/E, margins, beta, ...) — {metric,metricType,series}."""
    return _get("/basic_financials", symbol=symbol, metric=metric).get("data", {})


def search(query: str) -> list[dict]:
    """Symbol search [{symbol,displaySymbol,description,type}] (Finnhub /search)."""
    return _get("/search", q=query).get("data", [])


def market_status(exchange: str = "US") -> dict:
    """Market open/closed status {exchange,isOpen,session,holiday,timezone,t}."""
    return _get("/market_status", exchange=exchange).get("data", {})


def economic_calendar(from_: str | None = None, to: str | None = None, limit: int = 200) -> list[dict]:
    """US economic data-release calendar (FRED releases): [{date,event,country,
    currency,release_id}], oldest->newest. Defaults to the next ~14 days. Schedule
    only — no estimate/actual/impact (paid feeds only)."""
    return _get("/economic_calendar", **{"from": from_}, to=to, limit=limit).get("data", [])


def income_statement(symbol: str, period: str = "quarter", limit: int = 8) -> list[dict]:
    """Income statement (period: quarter|annual) [{date,period,revenue,netIncome,
    eps,epsDiluted,grossProfit,operatingIncome,...}], most-recent-first (FMP)."""
    return _get("/income_statement", symbol=symbol, period=period, limit=limit).get("data", [])


def dividends(symbol: str, limit: int = 40) -> list[dict]:
    """Dividend history [{date,dividend,recordDate,paymentDate,yield,...}],
    most-recent-first (FMP free tier)."""
    return _get("/dividends", symbol=symbol, limit=limit).get("data", [])


def balance_sheet(symbol: str, period: str = "quarter", limit: int = 8) -> list[dict]:
    """Balance sheet (period: quarter|annual), most-recent-first (FMP free tier)."""
    return _get("/balance_sheet", symbol=symbol, period=period, limit=limit).get("data", [])


def cash_flow(symbol: str, period: str = "quarter", limit: int = 8) -> list[dict]:
    """Cash-flow statement (period: quarter|annual), most-recent-first (FMP free tier)."""
    return _get("/cash_flow", symbol=symbol, period=period, limit=limit).get("data", [])


def cn_hsgt_flow() -> list[dict]:
    """北向/南向资金净流入概况 (akshare via facade). [{交易日,资金方向,资金净流入,...}]."""
    return _get("/cn_hsgt_flow").get("data", [])


def cn_margin(limit: int = 20) -> list[dict]:
    """上交所融资融券余额近 N 个交易日 (akshare via facade), oldest->newest."""
    return _get("/cn_margin", limit=limit).get("data", [])


def cn_breadth() -> list[dict]:
    """A 股市场活跃度/涨跌家数 (akshare via facade). [{item,value}]."""
    return _get("/cn_breadth").get("data", [])


def cn_ah_premium() -> list[dict]:
    """AH 股实时行情/溢价 (akshare via facade)."""
    return _get("/cn_ah_premium").get("data", [])


def health() -> dict:
    return _get("/health")
