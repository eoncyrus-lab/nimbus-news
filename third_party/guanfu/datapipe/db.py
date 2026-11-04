"""Database access primitives shared across the codebase.

Extracted from the pipeline god-module so callers depend on a small DB layer rather
than importing the whole orchestration module just to open a store.
"""

from __future__ import annotations

import pandas as pd

from datapipe.config import get_settings
from datapipe.storage import Store


def get_store() -> Store:
    return Store(get_settings().db_path)


def init_db() -> None:
    get_store().init_db()


def latest_table(store: Store, table: str, date_column: str) -> pd.DataFrame:
    """Rows of ``table`` on its most recent ``date_column`` value (whole table else)."""
    df = store.query_df(f"SELECT * FROM {table}")
    if df.empty or date_column not in df.columns:
        return df
    return df[df[date_column] == df[date_column].max()].copy()
