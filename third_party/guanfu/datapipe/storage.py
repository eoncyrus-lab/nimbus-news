from __future__ import annotations

import logging
import time
from pathlib import Path

import duckdb
import pandas as pd


logger = logging.getLogger("ah_screener.storage")

_CONNECT_LOCK_RETRIES = 5
_CONNECT_LOCK_RETRY_SECONDS = 2


SCHEMA_SQL = """
CREATE TABLE IF NOT EXISTS securities (
    market VARCHAR NOT NULL,
    symbol VARCHAR NOT NULL,
    name VARCHAR,
    asset_type VARCHAR DEFAULT 'stock',
    board VARCHAR,
    exchange VARCHAR,
    currency VARCHAR,
    status VARCHAR,
    is_st BOOLEAN DEFAULT false,
    is_hk_connect BOOLEAN DEFAULT false,
    metadata_source VARCHAR,
    metadata_confidence VARCHAR,
    updated_at TIMESTAMP,
    PRIMARY KEY (market, symbol)
);

CREATE TABLE IF NOT EXISTS market_snapshots (
    market VARCHAR NOT NULL,
    symbol VARCHAR NOT NULL,
    asset_type VARCHAR DEFAULT 'stock',
    board VARCHAR,
    trade_date DATE NOT NULL,
    name VARCHAR,
    last_price DOUBLE,
    pct_change DOUBLE,
    volume DOUBLE,
    amount DOUBLE,
    turnover_rate DOUBLE,
    pe_ttm DOUBLE,
    pb DOUBLE,
    market_cap DOUBLE,
    source VARCHAR NOT NULL,
    updated_at TIMESTAMP,
    PRIMARY KEY (market, symbol, trade_date, source)
);

CREATE TABLE IF NOT EXISTS security_universe_snapshots (
    snapshot_date DATE NOT NULL,
    market VARCHAR NOT NULL,
    symbol VARCHAR NOT NULL,
    name VARCHAR,
    asset_type VARCHAR DEFAULT 'stock',
    board VARCHAR,
    exchange VARCHAR,
    currency VARCHAR,
    status VARCHAR,
    is_st BOOLEAN DEFAULT false,
    is_hk_connect BOOLEAN DEFAULT false,
    metadata_source VARCHAR,
    metadata_confidence VARCHAR,
    source VARCHAR NOT NULL,
    updated_at TIMESTAMP,
    PRIMARY KEY (snapshot_date, market, symbol)
);

CREATE TABLE IF NOT EXISTS security_lifecycle_events (
    market VARCHAR NOT NULL,
    symbol VARCHAR NOT NULL,
    name VARCHAR,
    asset_type VARCHAR DEFAULT 'stock',
    exchange VARCHAR,
    listing_date DATE,
    delist_date DATE,
    status VARCHAR,
    event_type VARCHAR NOT NULL,
    source VARCHAR NOT NULL,
    updated_at TIMESTAMP,
    PRIMARY KEY (market, symbol, event_type, source)
);

CREATE TABLE IF NOT EXISTS daily_prices (
    market VARCHAR NOT NULL,
    symbol VARCHAR NOT NULL,
    trade_date DATE NOT NULL,
    open DOUBLE,
    high DOUBLE,
    low DOUBLE,
    close DOUBLE,
    volume DOUBLE,
    amount DOUBLE,
    adj_type VARCHAR,
    source VARCHAR NOT NULL,
    updated_at TIMESTAMP,
    PRIMARY KEY (market, symbol, trade_date, adj_type, source)
);

CREATE TABLE IF NOT EXISTS hot_theme_definitions (
    snapshot_date DATE NOT NULL,
    theme_name VARCHAR NOT NULL,
    market VARCHAR NOT NULL,
    weight DOUBLE,
    keywords VARCHAR,
    rationale VARCHAR,
    source VARCHAR,
    updated_at TIMESTAMP,
    PRIMARY KEY (snapshot_date, theme_name, market)
);

CREATE TABLE IF NOT EXISTS technical_indicators (
    snapshot_date DATE NOT NULL,
    market VARCHAR NOT NULL,
    symbol VARCHAR NOT NULL,
    name VARCHAR,
    close DOUBLE,
    ma20 DOUBLE,
    ma60 DOUBLE,
    ma120 DOUBLE,
    return_20d DOUBLE,
    return_60d DOUBLE,
    pct_from_120d_high DOUBLE,
    rsi14 DOUBLE,
    volatility_20d DOUBLE,
    trend_score DOUBLE,
    momentum_score DOUBLE,
    technical_score DOUBLE,
    technical_signal VARCHAR,
    updated_at TIMESTAMP,
    PRIMARY KEY (snapshot_date, market, symbol)
);

CREATE TABLE IF NOT EXISTS company_tags (
    market VARCHAR NOT NULL,
    symbol VARCHAR NOT NULL,
    tag_type VARCHAR NOT NULL,
    tag_name VARCHAR NOT NULL,
    evidence_level VARCHAR NOT NULL,
    source VARCHAR NOT NULL,
    updated_at TIMESTAMP,
    PRIMARY KEY (market, symbol, tag_type, tag_name, source)
);

CREATE TABLE IF NOT EXISTS company_identity_mappings (
    canonical_id VARCHAR NOT NULL,
    market VARCHAR NOT NULL,
    symbol VARCHAR NOT NULL,
    name VARCHAR,
    listing_type VARCHAR,
    source VARCHAR NOT NULL,
    confidence VARCHAR,
    updated_at TIMESTAMP,
    PRIMARY KEY (market, symbol, source)
);

CREATE TABLE IF NOT EXISTS company_documents (
    document_id VARCHAR NOT NULL,
    market VARCHAR NOT NULL,
    symbol VARCHAR NOT NULL,
    document_type VARCHAR,
    report_date DATE,
    title VARCHAR,
    source_url VARCHAR,
    local_path VARCHAR,
    file_sha256 VARCHAR,
    source VARCHAR NOT NULL,
    updated_at TIMESTAMP,
    PRIMARY KEY (document_id)
);

CREATE TABLE IF NOT EXISTS document_extractions (
    document_id VARCHAR NOT NULL,
    market VARCHAR NOT NULL,
    symbol VARCHAR NOT NULL,
    extract_type VARCHAR NOT NULL,
    extract_key VARCHAR NOT NULL,
    extract_value VARCHAR,
    evidence_text VARCHAR,
    evidence_level VARCHAR NOT NULL,
    source VARCHAR NOT NULL,
    updated_at TIMESTAMP,
    PRIMARY KEY (document_id, extract_type, extract_key, source)
);

CREATE TABLE IF NOT EXISTS financial_statement_items (
    market VARCHAR NOT NULL,
    symbol VARCHAR NOT NULL,
    statement_type VARCHAR NOT NULL,
    report_date DATE NOT NULL,
    report_type VARCHAR,
    item_code VARCHAR NOT NULL,
    item_name VARCHAR,
    amount DOUBLE,
    currency VARCHAR,
    source VARCHAR NOT NULL,
    updated_at TIMESTAMP,
    PRIMARY KEY (market, symbol, statement_type, report_date, item_code, source)
);

CREATE TABLE IF NOT EXISTS financial_metrics (
    snapshot_date DATE NOT NULL,
    market VARCHAR NOT NULL,
    symbol VARCHAR NOT NULL,
    name VARCHAR,
    report_date DATE NOT NULL,
    report_type VARCHAR,
    revenue DOUBLE,
    revenue_yoy DOUBLE,
    gross_profit DOUBLE,
    parent_net_profit DOUBLE,
    net_profit_yoy DOUBLE,
    deducted_net_profit DOUBLE,
    operating_cashflow DOUBLE,
    total_assets DOUBLE,
    total_liabilities DOUBLE,
    total_equity DOUBLE,
    roe DOUBLE,
    roa DOUBLE,
    gross_margin DOUBLE,
    net_margin DOUBLE,
    debt_asset_ratio DOUBLE,
    current_ratio DOUBLE,
    cashflow_to_profit DOUBLE,
    ocf_to_revenue DOUBLE,
    rd_expense DOUBLE,
    rd_expense_ratio DOUBLE,
    capex DOUBLE,
    capex_to_revenue DOUBLE,
    capex_to_operating_cashflow DOUBLE,
    innovation_efficiency_score DOUBLE,
    revenue_cagr_3y DOUBLE,
    net_profit_cagr_3y DOUBLE,
    roe_avg_3y DOUBLE,
    roe_stability_score DOUBLE,
    margin_stability_score DOUBLE,
    fundamental_trend_score DOUBLE,
    quality_score DOUBLE,
    growth_score DOUBLE,
    balance_score DOUBLE,
    cashflow_score DOUBLE,
    fundamental_score DOUBLE,
    warnings VARCHAR,
    updated_at TIMESTAMP,
    PRIMARY KEY (snapshot_date, market, symbol)
);

CREATE TABLE IF NOT EXISTS expert_screening_results (
    snapshot_date DATE NOT NULL,
    strategy VARCHAR NOT NULL,
    market VARCHAR NOT NULL,
    symbol VARCHAR NOT NULL,
    name VARCHAR,
    canonical_id VARCHAR,
    expert_score DOUBLE,
    master_score DOUBLE,
    china_master_score DOUBLE,
    fundamental_score DOUBLE,
    detailed_industry VARCHAR,
    industry_peer_group VARCHAR,
    peer_score DOUBLE,
    industry_fit_score DOUBLE,
    valuation_percentile DOUBLE,
    theme_score DOUBLE,
    technical_score DOUBLE,
    liquidity_score DOUBLE,
    valuation_score DOUBLE,
    risk_score DOUBLE,
    last_price DOUBLE,
    amount DOUBLE,
    market_cap DOUBLE,
    is_investable BOOLEAN DEFAULT true,
    is_hk_connect BOOLEAN,
    investability_reasons VARCHAR,
    yield_score DOUBLE DEFAULT 50.0,
    ah_bonus DOUBLE DEFAULT 0.0,
    growth_signal_score DOUBLE DEFAULT 50.0,
    graham_value DOUBLE,
    buffett_quality_proxy DOUBLE,
    fisher_growth DOUBLE,
    lynch_garp DOUBLE,
    oneil_momentum DOUBLE,
    zhang_lei_long_term DOUBLE,
    qiu_guolu_quality_value DOUBLE,
    dan_bin_lin_yuan_compounder DOUBLE,
    deng_xiaofeng_cycle_quality DOUBLE,
    chen_guangming_balanced DOUBLE,
    decision VARCHAR,
    theme_matches VARCHAR,
    reasons VARCHAR,
    updated_at TIMESTAMP,
    PRIMARY KEY (snapshot_date, strategy, market, symbol)
);

CREATE TABLE IF NOT EXISTS industry_valuation_stats (
    snapshot_date DATE NOT NULL,
    market VARCHAR NOT NULL,
    detailed_industry VARCHAR NOT NULL,
    securities INTEGER,
    pe_median DOUBLE,
    pb_median DOUBLE,
    valuation_percentile_median DOUBLE,
    valuation_percentile_top_quartile DOUBLE,
    source VARCHAR NOT NULL,
    updated_at TIMESTAMP,
    PRIMARY KEY (snapshot_date, market, detailed_industry, source)
);

CREATE TABLE IF NOT EXISTS refined_candidates (
    snapshot_date DATE NOT NULL,
    strategy VARCHAR NOT NULL,
    bucket VARCHAR NOT NULL,
    rank_in_bucket INTEGER NOT NULL,
    peer_group VARCHAR,
    style_bucket VARCHAR,
    market VARCHAR NOT NULL,
    symbol VARCHAR NOT NULL,
    name VARCHAR,
    canonical_id VARCHAR,
    expert_score DOUBLE,
    fundamental_score DOUBLE,
    technical_score DOUBLE,
    last_price DOUBLE,
    amount DOUBLE,
    market_cap DOUBLE,
    is_investable BOOLEAN DEFAULT true,
    investability_reasons VARCHAR,
    detailed_industry VARCHAR,
    industry_peer_group VARCHAR,
    peer_score DOUBLE,
    industry_fit_score DOUBLE,
    valuation_percentile DOUBLE,
    theme_matches VARCHAR,
    reasons VARCHAR,
    selection_note VARCHAR,
    snapshot_source VARCHAR DEFAULT 'natural',
    is_replay BOOLEAN DEFAULT false,
    updated_at TIMESTAMP,
    PRIMARY KEY (snapshot_date, strategy, bucket, rank_in_bucket)
);

CREATE TABLE IF NOT EXISTS potential_candidates (
    snapshot_date DATE NOT NULL,
    strategy VARCHAR NOT NULL,
    market VARCHAR NOT NULL,
    symbol VARCHAR NOT NULL,
    name VARCHAR,
    potential_score DOUBLE,
    technical_setup_score DOUBLE,
    relative_strength_score DOUBLE,
    fundamental_turn_score DOUBLE,
    theme_early_score DOUBLE,
    pivot_price DOUBLE,
    target_price DOUBLE,
    stop_price DOUBLE,
    rr_ratio DOUBLE,
    time_stop_days INTEGER,
    hist_win_rate DOUBLE,
    hist_median_excess_40d DOUBLE,
    bias_note VARCHAR,
    scenario_json VARCHAR,
    updated_at TIMESTAMP,
    PRIMARY KEY (snapshot_date, strategy, market, symbol)
);

CREATE TABLE IF NOT EXISTS etf_holdings (
    snapshot_date DATE NOT NULL,
    market VARCHAR NOT NULL,
    symbol VARCHAR NOT NULL,
    report_period VARCHAR NOT NULL,
    component_symbol VARCHAR NOT NULL,
    component_name VARCHAR,
    weight_pct DOUBLE,
    shares DOUBLE,
    market_value DOUBLE,
    source VARCHAR NOT NULL,
    updated_at TIMESTAMP,
    PRIMARY KEY (snapshot_date, market, symbol, report_period, component_symbol, source)
);

CREATE TABLE IF NOT EXISTS etf_industry_allocations (
    snapshot_date DATE NOT NULL,
    market VARCHAR NOT NULL,
    symbol VARCHAR NOT NULL,
    report_date DATE,
    allocation_name VARCHAR NOT NULL,
    weight_pct DOUBLE,
    market_value DOUBLE,
    source VARCHAR NOT NULL,
    updated_at TIMESTAMP,
    PRIMARY KEY (snapshot_date, market, symbol, report_date, allocation_name, source)
);

CREATE TABLE IF NOT EXISTS ingest_failures (
    run_date DATE NOT NULL,
    step VARCHAR NOT NULL,
    message VARCHAR,
    occurred_at TIMESTAMP,
    PRIMARY KEY (run_date, step)
);

CREATE TABLE IF NOT EXISTS lhb_detail (
    trade_date DATE NOT NULL,
    symbol VARCHAR NOT NULL,
    name VARCHAR,
    reason VARCHAR NOT NULL,
    interpretation VARCHAR,
    close_price DOUBLE,
    pct_change DOUBLE,
    lhb_net_buy DOUBLE,
    lhb_buy DOUBLE,
    lhb_sell DOUBLE,
    lhb_turnover DOUBLE,
    market_turnover DOUBLE,
    net_buy_ratio DOUBLE,
    turnover_ratio DOUBLE,
    turnover_rate DOUBLE,
    free_float_mcap DOUBLE,
    d1_chg DOUBLE,
    d2_chg DOUBLE,
    d5_chg DOUBLE,
    d10_chg DOUBLE,
    source VARCHAR NOT NULL,
    updated_at TIMESTAMP,
    PRIMARY KEY (trade_date, symbol, reason, source)
);

CREATE TABLE IF NOT EXISTS capital_flow (
    trade_date DATE NOT NULL,
    symbol VARCHAR NOT NULL,
    flow_scope VARCHAR NOT NULL,
    name VARCHAR,
    close DOUBLE,
    pct_change DOUBLE,
    close2 DOUBLE,
    pct_change2 DOUBLE,
    main_net DOUBLE,
    main_net_pct DOUBLE,
    super_net DOUBLE,
    super_net_pct DOUBLE,
    large_net DOUBLE,
    large_net_pct DOUBLE,
    medium_net DOUBLE,
    medium_net_pct DOUBLE,
    small_net DOUBLE,
    small_net_pct DOUBLE,
    indicator VARCHAR,
    source VARCHAR NOT NULL,
    updated_at TIMESTAMP,
    PRIMARY KEY (trade_date, symbol, flow_scope, source)
);

CREATE TABLE IF NOT EXISTS northbound_flow (
    trade_date DATE NOT NULL,
    channel VARCHAR NOT NULL,
    flow_type VARCHAR NOT NULL,
    net_buy DOUBLE,
    buy_amt DOUBLE,
    sell_amt DOUBLE,
    accum_net_buy DOUBLE,
    fund_inflow DOUBLE,
    quota_balance DOUBLE,
    hold_market_cap DOUBLE,
    up_count DOUBLE,
    flat_count DOUBLE,
    down_count DOUBLE,
    index_close DOUBLE,
    index_pct DOUBLE,
    lead_stock_name VARCHAR,
    lead_stock_symbol VARCHAR,
    lead_stock_pct DOUBLE,
    source VARCHAR NOT NULL,
    updated_at TIMESTAMP,
    PRIMARY KEY (trade_date, channel, flow_type, source)
);
"""

MIGRATION_SQL = """
ALTER TABLE securities ADD COLUMN IF NOT EXISTS asset_type VARCHAR DEFAULT 'stock';
ALTER TABLE securities ADD COLUMN IF NOT EXISTS board VARCHAR;
ALTER TABLE securities ADD COLUMN IF NOT EXISTS is_st BOOLEAN DEFAULT false;
ALTER TABLE securities ADD COLUMN IF NOT EXISTS is_hk_connect BOOLEAN DEFAULT false;
ALTER TABLE securities ADD COLUMN IF NOT EXISTS metadata_source VARCHAR;
ALTER TABLE securities ADD COLUMN IF NOT EXISTS metadata_confidence VARCHAR;
ALTER TABLE market_snapshots ADD COLUMN IF NOT EXISTS asset_type VARCHAR DEFAULT 'stock';
ALTER TABLE market_snapshots ADD COLUMN IF NOT EXISTS board VARCHAR;
ALTER TABLE expert_screening_results ADD COLUMN IF NOT EXISTS china_master_score DOUBLE;
ALTER TABLE expert_screening_results ADD COLUMN IF NOT EXISTS fundamental_score DOUBLE;
ALTER TABLE expert_screening_results ADD COLUMN IF NOT EXISTS industry_peer_group VARCHAR;
ALTER TABLE expert_screening_results ADD COLUMN IF NOT EXISTS peer_score DOUBLE;
ALTER TABLE expert_screening_results ADD COLUMN IF NOT EXISTS industry_fit_score DOUBLE;
ALTER TABLE expert_screening_results ADD COLUMN IF NOT EXISTS canonical_id VARCHAR;
ALTER TABLE expert_screening_results ADD COLUMN IF NOT EXISTS detailed_industry VARCHAR;
ALTER TABLE expert_screening_results ADD COLUMN IF NOT EXISTS valuation_percentile DOUBLE;
ALTER TABLE expert_screening_results ADD COLUMN IF NOT EXISTS last_price DOUBLE;
ALTER TABLE expert_screening_results ADD COLUMN IF NOT EXISTS amount DOUBLE;
ALTER TABLE expert_screening_results ADD COLUMN IF NOT EXISTS market_cap DOUBLE;
ALTER TABLE expert_screening_results ADD COLUMN IF NOT EXISTS is_investable BOOLEAN DEFAULT true;
ALTER TABLE expert_screening_results ADD COLUMN IF NOT EXISTS is_hk_connect BOOLEAN;
ALTER TABLE expert_screening_results ADD COLUMN IF NOT EXISTS investability_reasons VARCHAR;
ALTER TABLE expert_screening_results ADD COLUMN IF NOT EXISTS yield_score DOUBLE DEFAULT 50.0;
ALTER TABLE expert_screening_results ADD COLUMN IF NOT EXISTS ah_bonus DOUBLE DEFAULT 0.0;
-- style_models.py (four-style Phase 1): persists the GROWTH style's growth signal
-- (== fundamental_trend_score) so historical style-tagged rows carry a real value
-- instead of always defaulting to neutral 50 on recompute. Not populated for the
-- default strategy's rows (expert_model.py's row dict does not compute it).
ALTER TABLE expert_screening_results ADD COLUMN IF NOT EXISTS growth_signal_score DOUBLE DEFAULT 50.0;
ALTER TABLE expert_screening_results ADD COLUMN IF NOT EXISTS graham_value DOUBLE;
ALTER TABLE expert_screening_results ADD COLUMN IF NOT EXISTS buffett_quality_proxy DOUBLE;
ALTER TABLE expert_screening_results ADD COLUMN IF NOT EXISTS fisher_growth DOUBLE;
ALTER TABLE expert_screening_results ADD COLUMN IF NOT EXISTS lynch_garp DOUBLE;
ALTER TABLE expert_screening_results ADD COLUMN IF NOT EXISTS oneil_momentum DOUBLE;
ALTER TABLE expert_screening_results ADD COLUMN IF NOT EXISTS zhang_lei_long_term DOUBLE;
ALTER TABLE expert_screening_results ADD COLUMN IF NOT EXISTS qiu_guolu_quality_value DOUBLE;
ALTER TABLE expert_screening_results ADD COLUMN IF NOT EXISTS dan_bin_lin_yuan_compounder DOUBLE;
ALTER TABLE expert_screening_results ADD COLUMN IF NOT EXISTS deng_xiaofeng_cycle_quality DOUBLE;
ALTER TABLE expert_screening_results ADD COLUMN IF NOT EXISTS chen_guangming_balanced DOUBLE;
ALTER TABLE refined_candidates ADD COLUMN IF NOT EXISTS peer_group VARCHAR;
ALTER TABLE refined_candidates ADD COLUMN IF NOT EXISTS style_bucket VARCHAR;
ALTER TABLE refined_candidates ADD COLUMN IF NOT EXISTS selection_note VARCHAR;
ALTER TABLE refined_candidates ADD COLUMN IF NOT EXISTS industry_peer_group VARCHAR;
ALTER TABLE refined_candidates ADD COLUMN IF NOT EXISTS peer_score DOUBLE;
ALTER TABLE refined_candidates ADD COLUMN IF NOT EXISTS industry_fit_score DOUBLE;
ALTER TABLE refined_candidates ADD COLUMN IF NOT EXISTS canonical_id VARCHAR;
ALTER TABLE refined_candidates ADD COLUMN IF NOT EXISTS detailed_industry VARCHAR;
ALTER TABLE refined_candidates ADD COLUMN IF NOT EXISTS valuation_percentile DOUBLE;
ALTER TABLE refined_candidates ADD COLUMN IF NOT EXISTS last_price DOUBLE;
ALTER TABLE refined_candidates ADD COLUMN IF NOT EXISTS amount DOUBLE;
ALTER TABLE refined_candidates ADD COLUMN IF NOT EXISTS market_cap DOUBLE;
ALTER TABLE refined_candidates ADD COLUMN IF NOT EXISTS is_investable BOOLEAN DEFAULT true;
ALTER TABLE refined_candidates ADD COLUMN IF NOT EXISTS investability_reasons VARCHAR;
ALTER TABLE refined_candidates ADD COLUMN IF NOT EXISTS snapshot_source VARCHAR DEFAULT 'natural';
ALTER TABLE refined_candidates ADD COLUMN IF NOT EXISTS is_replay BOOLEAN DEFAULT false;
UPDATE refined_candidates
SET snapshot_source = 'historical_replay', is_replay = true
WHERE COALESCE(reasons, '') LIKE '%historical_replay_signal%';
UPDATE refined_candidates
SET snapshot_source = 'natural', is_replay = false
WHERE snapshot_source IS NULL OR is_replay IS NULL;
ALTER TABLE financial_metrics ADD COLUMN IF NOT EXISTS revenue_cagr_3y DOUBLE;
ALTER TABLE financial_metrics ADD COLUMN IF NOT EXISTS net_profit_cagr_3y DOUBLE;
ALTER TABLE financial_metrics ADD COLUMN IF NOT EXISTS roe_avg_3y DOUBLE;
ALTER TABLE financial_metrics ADD COLUMN IF NOT EXISTS roe_stability_score DOUBLE;
ALTER TABLE financial_metrics ADD COLUMN IF NOT EXISTS margin_stability_score DOUBLE;
ALTER TABLE financial_metrics ADD COLUMN IF NOT EXISTS fundamental_trend_score DOUBLE;
ALTER TABLE financial_metrics ADD COLUMN IF NOT EXISTS rd_expense DOUBLE;
ALTER TABLE financial_metrics ADD COLUMN IF NOT EXISTS rd_expense_ratio DOUBLE;
ALTER TABLE financial_metrics ADD COLUMN IF NOT EXISTS capex DOUBLE;
ALTER TABLE financial_metrics ADD COLUMN IF NOT EXISTS capex_to_revenue DOUBLE;
ALTER TABLE financial_metrics ADD COLUMN IF NOT EXISTS capex_to_operating_cashflow DOUBLE;
ALTER TABLE financial_metrics ADD COLUMN IF NOT EXISTS innovation_efficiency_score DOUBLE;
CREATE TABLE IF NOT EXISTS etf_holdings (
    snapshot_date DATE NOT NULL,
    market VARCHAR NOT NULL,
    symbol VARCHAR NOT NULL,
    report_period VARCHAR NOT NULL,
    component_symbol VARCHAR NOT NULL,
    component_name VARCHAR,
    weight_pct DOUBLE,
    shares DOUBLE,
    market_value DOUBLE,
    source VARCHAR NOT NULL,
    updated_at TIMESTAMP,
    PRIMARY KEY (snapshot_date, market, symbol, report_period, component_symbol, source)
);
CREATE TABLE IF NOT EXISTS etf_industry_allocations (
    snapshot_date DATE NOT NULL,
    market VARCHAR NOT NULL,
    symbol VARCHAR NOT NULL,
    report_date DATE,
    allocation_name VARCHAR NOT NULL,
    weight_pct DOUBLE,
    market_value DOUBLE,
    source VARCHAR NOT NULL,
    updated_at TIMESTAMP,
    PRIMARY KEY (snapshot_date, market, symbol, report_date, allocation_name, source)
);
"""


class Store:
    def __init__(self, db_path: Path) -> None:
        self.db_path = db_path
        self.db_path.parent.mkdir(parents=True, exist_ok=True)

    def connect(self, *, read_only: bool = False) -> duckdb.DuckDBPyConnection:
        if read_only:
            return duckdb.connect(str(self.db_path), read_only=read_only)

        # Write connections can race a resident reader (e.g. the warehouse service) that
        # hasn't released its lock yet; retry a few times before giving up.
        attempt = 0
        while True:
            try:
                return duckdb.connect(str(self.db_path), read_only=read_only)
            except Exception as exc:  # noqa: BLE001 - only lock conflicts are retried below
                is_lock_conflict = isinstance(exc, duckdb.IOException) or (
                    "Could not set lock" in str(exc)
                )
                attempt += 1
                if not is_lock_conflict or attempt >= _CONNECT_LOCK_RETRIES:
                    raise
                logger.warning(
                    "duckdb write connect lock conflict (attempt %s/%s): %s",
                    attempt,
                    _CONNECT_LOCK_RETRIES,
                    exc,
                )
                time.sleep(_CONNECT_LOCK_RETRY_SECONDS)

    def init_db(self) -> None:
        with self.connect() as conn:
            conn.execute(SCHEMA_SQL)
            conn.execute(MIGRATION_SQL)

    def upsert_dataframe(self, table: str, df: pd.DataFrame) -> int:
        if df.empty:
            return 0

        incoming = df.copy()
        if table == "market_snapshots" and "trade_date" in incoming.columns:
            incoming["trade_date"] = pd.to_datetime(incoming["trade_date"], errors="coerce").dt.date
            incoming = incoming.drop_duplicates(
                ["market", "symbol", "trade_date", "source"], keep="last"
            )

        with self.connect() as conn:
            conn.execute(SCHEMA_SQL)
            conn.execute(MIGRATION_SQL)
            conn.register("incoming_df", incoming)
            columns = list(incoming.columns)
            column_sql = ", ".join(columns)
            select_sql = ", ".join(f"incoming_df.{column}" for column in columns)
            conn.execute(
                f"INSERT OR REPLACE INTO {table} ({column_sql}) SELECT {select_sql} FROM incoming_df"
            )
            conn.unregister("incoming_df")
        return len(incoming)

    def query_df(self, sql: str, parameters: object | None = None) -> pd.DataFrame:
        if not self.db_path.exists():
            self.init_db()

        with self.connect(read_only=True) as conn:
            if parameters is None:
                return conn.execute(sql).fetch_df()
            return conn.execute(sql, parameters).fetch_df()

    def execute(self, sql: str, parameters: object | None = None) -> None:
        with self.connect() as conn:
            conn.execute(SCHEMA_SQL)
            conn.execute(MIGRATION_SQL)
            if parameters is None:
                conn.execute(sql)
            else:
                conn.execute(sql, parameters)
