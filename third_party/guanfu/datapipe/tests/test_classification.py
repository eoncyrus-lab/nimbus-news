from __future__ import annotations

from unittest import TestCase

import pandas as pd

from datapipe.classification import enrich_security_metadata, infer_status


class InferStatusMarketScopeTest(TestCase):
    def test_us_common_stock_name_not_flagged_st(self) -> None:
        # "Common Stock" 里的 "St" 在词边界会命中 A 股 ST 正则，非 A 股必须走正常 status。
        self.assertEqual(
            infer_status("Micron Technology, Inc. - Common Stock", "stock", "US"),
            "listed",
        )

    def test_us_names_with_standard_or_star_not_flagged_st(self) -> None:
        self.assertEqual(infer_status("Standard Industries", "stock", "US"), "listed")
        self.assertEqual(infer_status("Starbucks Corporation", "stock", "US"), "listed")

    def test_hk_name_not_flagged_st(self) -> None:
        self.assertEqual(infer_status("Sunny Star Holdings", "stock", "HK"), "listed")

    def test_a_share_st_still_detected(self) -> None:
        self.assertEqual(infer_status("*ST某某", "stock", "A"), "st")
        self.assertEqual(infer_status("ST康得", "stock", "A"), "st")

    def test_a_share_delisting_still_detected(self) -> None:
        self.assertEqual(infer_status("某某退", "stock", "A"), "delisting_risk")

    def test_a_share_market_defaults_true(self) -> None:
        # 默认 market="A"，保证 akshare 等只传 name/asset_type 的 A 股调用点不退化。
        self.assertEqual(infer_status("*ST某某", "stock"), "st")


class EnrichSecurityMetadataStatusTest(TestCase):
    def test_us_stock_status_and_is_st_not_flagged(self) -> None:
        df = pd.DataFrame(
            [
                {
                    "market": "US",
                    "symbol": "MU",
                    "name": "Micron Technology, Inc. - Common Stock",
                    "asset_type": "stock",
                    "exchange": "NASDAQ",
                    "status": "listed",
                },
                {
                    "market": "US",
                    "symbol": "STX",
                    "name": "Seagate Standard Star Corp",
                    "asset_type": "stock",
                    "exchange": "NASDAQ",
                    "status": "listed",
                },
            ]
        )
        enriched = enrich_security_metadata(df)
        self.assertEqual(list(enriched["status"]), ["listed", "listed"])
        self.assertEqual(list(enriched["is_st"]), [False, False])

    def test_a_share_status_and_is_st_still_flagged(self) -> None:
        df = pd.DataFrame(
            [
                {
                    "market": "A",
                    "symbol": "000048",
                    "name": "*ST康泰",
                    "asset_type": "stock",
                    "exchange": "SZSE",
                    "status": "listed",
                },
                {
                    "market": "A",
                    "symbol": "000504",
                    "name": "南华退",
                    "asset_type": "stock",
                    "exchange": "SZSE",
                    "status": "listed",
                },
            ]
        )
        enriched = enrich_security_metadata(df)
        self.assertEqual(list(enriched["status"]), ["st", "delisting_risk"])
        self.assertEqual(list(enriched["is_st"]), [True, True])
