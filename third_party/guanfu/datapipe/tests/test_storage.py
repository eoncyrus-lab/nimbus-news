from __future__ import annotations

from pathlib import Path
from tempfile import TemporaryDirectory
from unittest import TestCase
from unittest.mock import patch

import duckdb
import pandas as pd

from datapipe.storage import Store


class StoreUpsertTest(TestCase):
    def test_market_snapshots_deduplicate_same_date_key(self) -> None:
        with TemporaryDirectory() as tmp:
            store = Store(Path(tmp) / "test.duckdb")
            store.init_db()
            rows = pd.DataFrame(
                [
                    {
                        "market": "HK",
                        "symbol": "02825",
                        "asset_type": "etf",
                        "board": "HK ETF",
                        "trade_date": "2026-05-22 16:01:00",
                        "name": "ETF A",
                        "last_price": 1.0,
                        "source": "futu.opend.get_market_snapshot",
                        "updated_at": pd.Timestamp("2026-05-24 00:00:00"),
                    },
                    {
                        "market": "HK",
                        "symbol": "02825",
                        "asset_type": "etf",
                        "board": "HK ETF",
                        "trade_date": "2026-05-22 16:08:00",
                        "name": "ETF A",
                        "last_price": 1.1,
                        "source": "futu.opend.get_market_snapshot",
                        "updated_at": pd.Timestamp("2026-05-24 00:01:00"),
                    },
                ]
            )

            self.assertEqual(store.upsert_dataframe("market_snapshots", rows), 1)
            out = store.query_df("SELECT symbol, trade_date, last_price FROM market_snapshots")

        self.assertEqual(len(out), 1)
        self.assertEqual(str(out["trade_date"].iloc[0]), "2026-05-22 00:00:00")
        self.assertEqual(float(out["last_price"].iloc[0]), 1.1)


class StoreConnectLockRetryTest(TestCase):
    def test_write_connect_retries_lock_conflict_then_succeeds(self) -> None:
        with TemporaryDirectory() as tmp:
            store = Store(Path(tmp) / "test.duckdb")
            real_connect = duckdb.connect
            attempts: list[int] = []

            def flaky_connect(path, read_only=False):
                attempts.append(1)
                if len(attempts) <= 2:
                    raise duckdb.IOException(
                        f"Could not set lock on file {path}: another process holds it"
                    )
                return real_connect(path, read_only=read_only)

            with (
                patch("ah_screener.storage.duckdb.connect", side_effect=flaky_connect),
                patch("ah_screener.storage.time.sleep") as mock_sleep,
            ):
                conn = store.connect()
                conn.close()

        self.assertEqual(len(attempts), 3)
        self.assertEqual(mock_sleep.call_count, 2)

    def test_write_connect_gives_up_after_max_retries(self) -> None:
        with TemporaryDirectory() as tmp:
            store = Store(Path(tmp) / "test.duckdb")

            def always_locked(path, read_only=False):
                raise duckdb.IOException(f"Could not set lock on file {path}")

            with (
                patch("ah_screener.storage.duckdb.connect", side_effect=always_locked) as mock_connect,
                patch("ah_screener.storage.time.sleep") as mock_sleep,
            ):
                with self.assertRaises(duckdb.IOException):
                    store.connect()

        self.assertEqual(mock_connect.call_count, 5)
        self.assertEqual(mock_sleep.call_count, 4)

    def test_write_connect_does_not_retry_non_lock_errors(self) -> None:
        with TemporaryDirectory() as tmp:
            store = Store(Path(tmp) / "test.duckdb")

            def other_error(path, read_only=False):
                raise RuntimeError("out of memory")

            with (
                patch("ah_screener.storage.duckdb.connect", side_effect=other_error) as mock_connect,
                patch("ah_screener.storage.time.sleep") as mock_sleep,
            ):
                with self.assertRaises(RuntimeError):
                    store.connect()

        self.assertEqual(mock_connect.call_count, 1)
        mock_sleep.assert_not_called()

    def test_read_only_connect_does_not_retry(self) -> None:
        with TemporaryDirectory() as tmp:
            store = Store(Path(tmp) / "test.duckdb")
            store.init_db()

            def always_locked(path, read_only=False):
                raise duckdb.IOException(f"Could not set lock on file {path}")

            with (
                patch("ah_screener.storage.duckdb.connect", side_effect=always_locked) as mock_connect,
                patch("ah_screener.storage.time.sleep") as mock_sleep,
            ):
                with self.assertRaises(duckdb.IOException):
                    store.connect(read_only=True)

        self.assertEqual(mock_connect.call_count, 1)
        mock_sleep.assert_not_called()
