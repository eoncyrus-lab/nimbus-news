from __future__ import annotations

import os
from dataclasses import dataclass
from pathlib import Path


PROJECT_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_DATA_DIR = PROJECT_ROOT / "data"
DEFAULT_DB_PATH = DEFAULT_DATA_DIR / "datapipe.duckdb"


@dataclass(frozen=True)
class Settings:
    db_path: Path
    min_a_amount: float = 20_000_000
    min_hk_amount: float = 5_000_000
    min_us_amount: float = 3_000_000
    recommend_min_a_amount: float = 50_000_000
    recommend_min_hk_amount: float = 20_000_000
    recommend_min_us_amount: float = 20_000_000
    recommend_min_a_market_cap: float = 10_000_000_000
    recommend_min_hk_market_cap: float = 5_000_000_000
    recommend_min_us_market_cap: float = 5_000_000_000
    recommend_min_us_price: float = 5.0
    recommend_min_hk_non_connect_amount: float = 50_000_000


def get_settings() -> Settings:
    db_path = Path(os.getenv("DATAPIPE_DB", DEFAULT_DB_PATH))
    return Settings(
        db_path=db_path,
        min_a_amount=float(os.getenv("AH_SCREENER_MIN_A_AMOUNT", "20000000")),
        min_hk_amount=float(os.getenv("AH_SCREENER_MIN_HK_AMOUNT", "5000000")),
        min_us_amount=float(os.getenv("AH_SCREENER_MIN_US_AMOUNT", "3000000")),
        recommend_min_a_amount=float(os.getenv("AH_SCREENER_RECOMMEND_MIN_A_AMOUNT", "50000000")),
        recommend_min_hk_amount=float(os.getenv("AH_SCREENER_RECOMMEND_MIN_HK_AMOUNT", "20000000")),
        recommend_min_us_amount=float(os.getenv("AH_SCREENER_RECOMMEND_MIN_US_AMOUNT", "20000000")),
        recommend_min_a_market_cap=float(
            os.getenv("AH_SCREENER_RECOMMEND_MIN_A_MKTCAP", "10000000000")
        ),
        recommend_min_hk_market_cap=float(
            os.getenv("AH_SCREENER_RECOMMEND_MIN_HK_MKTCAP", "5000000000")
        ),
        recommend_min_us_market_cap=float(
            os.getenv("AH_SCREENER_RECOMMEND_MIN_US_MKTCAP", "5000000000")
        ),
        recommend_min_us_price=float(os.getenv("AH_SCREENER_RECOMMEND_MIN_US_PRICE", "5")),
        recommend_min_hk_non_connect_amount=float(
            os.getenv("AH_SCREENER_RECOMMEND_MIN_HK_NON_CONNECT_AMOUNT", "50000000")
        ),
    )
