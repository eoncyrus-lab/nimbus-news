from __future__ import annotations

from unittest import TestCase
from unittest.mock import patch

import pandas as pd

from datapipe.hk_connect import HKConnectUniverse, LiveDataSource, SnapshotDataSource


class HKConnectUniverseTest(TestCase):
    """Smoke tests for HKConnectUniverse using the bundled snapshot data source.

    These tests are offline and fast — they read only the files already
    committed to ``src/ah_screener/data/hk_connect/``.
    """

    @classmethod
    def setUpClass(cls) -> None:
        cls.universe = HKConnectUniverse(SnapshotDataSource())

    def test_eligible_universe_is_non_empty(self) -> None:
        eligible = self.universe.eligible_universe()
        self.assertGreater(len(eligible), 0, "eligible_universe() must return at least one row")

    def test_eligible_universe_has_connect_eligible_column(self) -> None:
        eligible = self.universe.eligible_universe()
        self.assertIn("connect_eligible", eligible.columns)

    def test_all_eligible_rows_are_marked_true(self) -> None:
        eligible = self.universe.eligible_universe()
        self.assertTrue(
            eligible["connect_eligible"].all(),
            "All rows from eligible_universe() must have connect_eligible == True",
        )

    def test_full_universe_contains_eligible_subset(self) -> None:
        full = self.universe.full_universe()
        eligible = self.universe.eligible_universe()
        self.assertGreater(len(full), len(eligible))
        self.assertIn("connect_eligible", full.columns)

    def test_build_report_returns_non_empty_markdown(self) -> None:
        report = self.universe.build_report()
        self.assertIsInstance(report, str)
        self.assertGreater(len(report), 200)
        self.assertIn("港股通", report)


class LiveDataSourceFallbackTest(TestCase):
    """Tests that LiveDataSource falls back to snapshot when live fetch fails.

    All tests are fully offline: the live fetch helpers are monkeypatched to
    raise an exception so we never touch the network.
    """

    def _make_live(self) -> LiveDataSource:
        """Return a LiveDataSource backed by the real bundled SnapshotDataSource."""
        return LiveDataSource(fallback=SnapshotDataSource(), refresh_snapshots=False)

    def test_hkex_securities_falls_back_on_network_error(self) -> None:
        src = self._make_live()
        with patch(
            "ah_screener.hk_connect._http_get_bytes", side_effect=RuntimeError("network down")
        ):
            df, label = src.get_hkex_securities()
        # Should have received the snapshot DataFrame, not raised
        self.assertIsInstance(df, pd.DataFrame)
        self.assertGreater(len(df), 0)
        self.assertIn("stock_code", df.columns)

    def test_sse_southbound_falls_back_on_network_error(self) -> None:
        src = self._make_live()
        with patch(
            "ah_screener.hk_connect._http_get_bytes", side_effect=RuntimeError("network down")
        ):
            df, update_date = src.get_sse_southbound()
        self.assertIsInstance(df, pd.DataFrame)
        self.assertGreater(len(df), 0)
        self.assertIn("stock_code", df.columns)

    def test_szse_southbound_falls_back_on_network_error(self) -> None:
        src = self._make_live()
        with patch(
            "ah_screener.hk_connect._http_get_bytes", side_effect=RuntimeError("network down")
        ):
            df, update_date = src.get_szse_southbound()
        self.assertIsInstance(df, pd.DataFrame)
        self.assertGreater(len(df), 0)
        self.assertIn("stock_code", df.columns)

    def test_tradingview_quotes_falls_back_on_network_error(self) -> None:
        src = self._make_live()
        with patch(
            "ah_screener.hk_connect._http_post_json", side_effect=RuntimeError("network down")
        ):
            df = src.get_tradingview_quotes()
        self.assertIsInstance(df, pd.DataFrame)
        self.assertGreater(len(df), 0)
        self.assertIn("stock_code", df.columns)

    def test_universe_builds_when_all_live_fetches_fail(self) -> None:
        """Full universe build completes via fallback even if every live fetch raises."""
        src = self._make_live()
        with (
            patch(
                "ah_screener.hk_connect._http_get_bytes", side_effect=RuntimeError("network down")
            ),
            patch(
                "ah_screener.hk_connect._http_post_json", side_effect=RuntimeError("network down")
            ),
        ):
            universe = HKConnectUniverse(src)
            eligible = universe.eligible_universe()
        self.assertGreater(len(eligible), 0)
