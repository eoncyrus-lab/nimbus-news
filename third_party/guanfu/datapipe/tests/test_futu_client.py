from __future__ import annotations

from unittest import TestCase

import pytest

from datapipe.sources import futu_client


class FutuClientTest(TestCase):
    def test_code_mapping_across_markets(self) -> None:
        self.assertEqual(futu_client.futu_code("US", "AAPL"), "US.AAPL")
        self.assertEqual(futu_client.futu_code("US", "BRK.B"), "US.BRK-B")
        self.assertEqual(futu_client.futu_code("HK", "700"), "HK.00700")
        self.assertEqual(futu_client.futu_code("HK", "00700"), "HK.00700")
        self.assertEqual(futu_client.futu_code("A", "600000"), "SH.600000")
        self.assertEqual(futu_client.futu_code("A", "000001"), "SZ.000001")
        self.assertEqual(futu_client.futu_code("A", "510300"), "SH.510300")  # SH ETF
        self.assertEqual(futu_client.futu_code("A", "159915"), "SZ.159915")  # SZ ETF

    def test_unsupported_market_raises(self) -> None:
        with self.assertRaises(ValueError):
            futu_client.futu_code("JP", "7203")
        with self.assertRaises(ValueError):
            futu_client.futu_code("A", "830799")

    def test_fetch_returns_empty_when_disabled(self) -> None:
        original = futu_client.USE_FUTU
        futu_client.USE_FUTU = False
        try:
            out = futu_client.fetch_futu_history("HK", "00700", "20260101", "20260131")
        finally:
            futu_client.USE_FUTU = original
        self.assertTrue(out.empty)

    def test_fetch_history_handles_current_sdk_pagination(self) -> None:
        import pandas as pd

        futu = pytest.importorskip("futu", reason="futu extra not installed (uv sync --extra futu)")

        original_ctx = futu.OpenQuoteContext
        calls: list[object] = []

        class FakeQuoteContext:
            def __init__(self, *args, **kwargs) -> None:
                pass

            def request_history_kline(self, *args, **kwargs):
                page_req_key = kwargs.get("page_req_key")
                calls.append(page_req_key)
                if page_req_key is None:
                    return (
                        futu.RET_OK,
                        pd.DataFrame(
                            [
                                {
                                    "time_key": "2026-01-02 00:00:00",
                                    "open": 1.0,
                                    "high": 1.2,
                                    "low": 0.9,
                                    "close": 1.1,
                                    "volume": 100,
                                    "turnover": 110,
                                }
                            ]
                        ),
                        b"next",
                    )
                return (
                    futu.RET_OK,
                    pd.DataFrame(
                        [
                            {
                                "time_key": "2026-01-03 00:00:00",
                                "open": 1.1,
                                "high": 1.3,
                                "low": 1.0,
                                "close": 1.2,
                                "volume": 120,
                                "turnover": 144,
                            }
                        ]
                    ),
                    None,
                )

            def close(self) -> None:
                pass

        futu.OpenQuoteContext = FakeQuoteContext
        try:
            out = futu_client.fetch_futu_history("US", "AAPL", "20260101", "20260105")
        finally:
            futu.OpenQuoteContext = original_ctx

        self.assertEqual(calls, [None, b"next"])
        self.assertEqual(len(out), 2)
        self.assertEqual(list(out["close"]), [1.1, 1.2])

    def test_hk_benchmark_aliases_use_futu_index_codes(self) -> None:
        self.assertEqual(futu_client._futu_benchmark_code("HK", "HSI"), "HK.800000")
        self.assertEqual(futu_client._futu_benchmark_code("HK", "HSCEI"), "HK.800100")

    def test_fetch_a_board_tags_uses_plate_constituents(self) -> None:
        import pandas as pd

        futu = pytest.importorskip("futu", reason="futu extra not installed (uv sync --extra futu)")

        original_ctx = futu.OpenQuoteContext
        case = self

        class FakeQuoteContext:
            def __init__(self, *args, **kwargs) -> None:
                pass

            def get_plate_list(self, market, plate_class):
                case.assertEqual(plate_class, futu.Plate.CONCEPT)
                return (
                    futu.RET_OK,
                    pd.DataFrame([{"code": "SH.LIST0301", "plate_name": "汽车电子概念"}]),
                )

            def get_plate_stock(self, plate_code):
                case.assertEqual(plate_code, "SH.LIST0301")
                return (
                    futu.RET_OK,
                    pd.DataFrame(
                        [
                            {"code": "SH.600000"},
                            {"code": "SZ.000001"},
                            {"code": "BJ.830799"},
                        ]
                    ),
                )

            def close(self) -> None:
                pass

        futu.OpenQuoteContext = FakeQuoteContext
        try:
            out = futu_client.fetch_futu_a_board_tags("concept", limit=1)
        finally:
            futu.OpenQuoteContext = original_ctx

        self.assertEqual(set(out["symbol"]), {"600000", "000001"})
        self.assertEqual(out["tag_type"].unique().tolist(), ["concept"])
        self.assertEqual(out["source"].unique().tolist(), ["futu.opend.get_plate_stock"])


class FutuHkEtfNormalizeTest(TestCase):
    def test_normalize_hk_etf_builds_frames(self) -> None:
        import pandas as pd

        from datapipe.sources.futu_client import _normalize_hk_etf

        basics = pd.DataFrame([{"code": "HK.02800", "name": "盈富基金"}, {"code": "HK.03033", "name": "南方恒生科技"}])
        snap = pd.DataFrame(
            [
                {"code": "HK.02800", "last_price": 25.0, "prev_close_price": 24.0, "volume": 1e6,
                 "turnover": 2.5e7, "turnover_rate": 0.5, "update_time": "2026-05-23 16:00:00"},
                {"code": "HK.03033", "last_price": 6.0, "prev_close_price": 6.0, "volume": 5e5,
                 "turnover": 3e6, "turnover_rate": 0.3, "update_time": "2026-05-23 16:00:00"},
            ]
        )
        securities, snapshots = _normalize_hk_etf(basics, snap)
        self.assertEqual(set(securities["symbol"]), {"02800", "03033"})
        self.assertTrue((securities["asset_type"] == "etf").all())
        s = snapshots.set_index("symbol")
        self.assertAlmostEqual(float(s.loc["02800", "pct_change"]), (25.0 / 24.0 - 1) * 100, places=3)
        self.assertEqual(float(s.loc["02800", "last_price"]), 25.0)

    def test_normalize_hk_etf_empty(self) -> None:
        import pandas as pd

        from datapipe.sources.futu_client import _normalize_hk_etf

        sec, snap = _normalize_hk_etf(pd.DataFrame(), pd.DataFrame())
        self.assertTrue(sec.empty and snap.empty)

    def test_normalize_a_spot_uses_snapshot_valuation_fields(self) -> None:
        import pandas as pd

        from datapipe.sources.futu_client import _normalize_futu_spot

        basics = pd.DataFrame(
            [{"code": "SH.600519", "name": "贵州茅台", "stock_type": "STOCK", "delisting": False}]
        )
        snap = pd.DataFrame(
            [
                {
                    "code": "SH.600519",
                    "last_price": 100.0,
                    "prev_close_price": 95.0,
                    "volume": 1000,
                    "turnover": 100000,
                    "turnover_rate": 0.2,
                    "pe_ttm_ratio": 20.0,
                    "pb_ratio": 5.0,
                    "total_market_val": 1e12,
                    "update_time": "2026-05-22 15:00:00",
                }
            ]
        )

        securities, snapshots = _normalize_futu_spot("A", basics, snap)

        self.assertEqual(securities.iloc[0]["exchange"], "SSE")
        self.assertEqual(securities.iloc[0]["board"], "主板")
        self.assertEqual(float(snapshots.iloc[0]["pe_ttm"]), 20.0)
        self.assertEqual(float(snapshots.iloc[0]["pb"]), 5.0)

    def test_normalize_us_spot_infers_etf_board(self) -> None:
        import pandas as pd

        from datapipe.sources.futu_client import _normalize_futu_spot

        basics = pd.DataFrame(
            [
                {
                    "code": "US.SPY",
                    "name": "SPDR S&P 500 ETF",
                    "stock_type": "ETF",
                    "exchange_type": "US_NYSE_ARCA",
                    "delisting": False,
                }
            ]
        )
        snap = pd.DataFrame(
            [{"code": "US.SPY", "last_price": 500.0, "prev_close_price": 490.0}]
        )

        securities, snapshots = _normalize_futu_spot("US", basics, snap)

        self.assertEqual(securities.iloc[0]["symbol"], "SPY")
        self.assertEqual(securities.iloc[0]["asset_type"], "etf")
        self.assertEqual(securities.iloc[0]["board"], "US ETF")
        self.assertEqual(float(snapshots.iloc[0]["pct_change"]), (500.0 / 490.0 - 1) * 100)


class FutuHkEtfDedupTest(TestCase):
    def test_normalize_hk_etf_dedups_codes(self) -> None:
        import pandas as pd

        from datapipe.sources.futu_client import _normalize_hk_etf

        basics = pd.DataFrame([{"code": "HK.02800", "name": "盈富"}, {"code": "HK.02800", "name": "盈富"}])
        snap = pd.DataFrame(
            [
                {"code": "HK.02800", "last_price": 25.0, "prev_close_price": 24.0},
                {"code": "HK.02800", "last_price": 25.0, "prev_close_price": 24.0},
            ]
        )
        sec, snaps = _normalize_hk_etf(basics, snap)
        self.assertEqual(len(sec), 1)
        self.assertEqual(len(snaps), 1)


class FutuHkStockNormalizeTest(TestCase):
    def test_normalize_hk_stock_builds_frames(self) -> None:
        import pandas as pd

        from datapipe.sources.futu_client import _normalize_hk_stock

        basics = pd.DataFrame(
            [
                {"code": "HK.00700", "name": "腾讯控股"},
                {"code": "HK.09988", "name": "阿里巴巴-W"},
            ]
        )
        snap = pd.DataFrame(
            [
                {
                    "code": "HK.00700",
                    "last_price": 400.0,
                    "prev_close_price": 390.0,
                    "volume": 1e6,
                    "turnover": 4e8,
                    "turnover_rate": 0.3,
                    "pe_ttm": 20.0,
                    "pb_rate": 4.0,
                    "total_market_val": 3.8e12,
                    "update_time": "2026-05-23 16:00:00",
                },
                {
                    "code": "HK.09988",
                    "last_price": 120.0,
                    "prev_close_price": 120.0,
                    "volume": 2e6,
                    "turnover": 2.4e8,
                    "turnover_rate": 0.4,
                    "pe_ttm": 18.0,
                    "pb_rate": 2.2,
                    "total_market_val": 2.2e12,
                    "update_time": "2026-05-23 16:00:00",
                },
            ]
        )
        securities, snapshots = _normalize_hk_stock(
            basics,
            snap,
            hk_connect_symbols={"00700"},
            hk_connect_source="test.hk_connect",
            hk_connect_confidence="high",
        )

        self.assertEqual(set(securities["symbol"]), {"00700", "09988"})
        self.assertTrue((securities["asset_type"] == "stock").all())
        self.assertTrue(securities.set_index("symbol").loc["00700", "is_hk_connect"])
        s = snapshots.set_index("symbol")
        self.assertAlmostEqual(float(s.loc["00700", "pct_change"]), (400.0 / 390.0 - 1) * 100)
        self.assertEqual(float(s.loc["00700", "market_cap"]), 3.8e12)
