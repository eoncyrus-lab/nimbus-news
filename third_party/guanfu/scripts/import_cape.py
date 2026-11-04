#!/usr/bin/env python3
"""Import Shiller CAPE data from Yale/Official XLS → PriceStore JSON.

Usage:
    python3 scripts/import_cape.py                                    # download + update
    python3 scripts/import_cape.py --file /tmp/ie_data.xls             # parse local file
    python3 scripts/import_cape.py --output /tmp/cape_test.json        # write to different path

Dependencies: pandas, openpyxl (pip install pandas openpyxl)

Data source: https://shillerdata.com/ie_data.xls  (Yale / Robert Shiller)
Output: ~/.guanfu/prices/spx_cape.json  (PriceStore format)
"""

import argparse
import json
import math
import os
import sys
import tempfile
import urllib.request

# 2026-07: shillerdata.com no longer serves a stable /ie_data.xls path — the
# workbook lives behind a rotating wsimg blob URL that must be scraped from the
# homepage each time. NOTE: the old Yale mirror (econ.yale.edu/~shiller) still
# responds 200 but is FROZEN at 2023-10 — deliberately NOT used as a fallback
# (silently-stale is worse than failing loudly; see source_health philosophy).
SHILLER_HOME = "https://shillerdata.com/"
DEFAULT_OUTPUT = os.path.expanduser("~/.guanfu/prices/spx_cape.json")
UA = "guanfu import_cape (personal research tool)"


def resolve_shiller_url() -> str:
    """Scrape the shillerdata.com homepage for the current ie_data.xls link."""
    import re

    req = urllib.request.Request(SHILLER_HOME, headers={"User-Agent": UA})
    with urllib.request.urlopen(req, timeout=30) as resp:
        html = resp.read().decode("utf-8", errors="replace")
    m = re.search(r'href="([^"]*ie_data\.xls[^"]*)"', html)
    if not m:
        raise RuntimeError(
            "shillerdata.com homepage has no ie_data.xls link — layout drifted again"
        )
    url = m.group(1)
    if url.startswith("//"):
        url = "https:" + url
    elif url.startswith("/"):
        url = "https://shillerdata.com" + url
    return url


def parse_cape_from_xls(xls_path: str) -> list[dict]:
    """Parse Shiller XLS, extract CAPE column, return PriceStore-formatted list."""
    import pandas as pd

    df = pd.read_excel(xls_path, sheet_name="Data", header=7)
    out = []
    for _, row in df.iterrows():
        d, c = row.get("Date"), row.get("CAPE")
        if pd.isna(d) or pd.isna(c):
            continue
        try:
            s = f"{d:.2f}"
            y, m = s.split(".")
            date_str = f"{int(y):04d}-{int(m):02d}-01"
            cape = float(c)
            if not (math.isfinite(cape) and 1 < cape < 100):
                continue
            out.append({"date": date_str, "close": round(cape, 4), "source": "shiller:cape"})
        except Exception:
            continue
    return out


def main():
    parser = argparse.ArgumentParser(description="Import Shiller CAPE data")
    parser.add_argument("--file", "-f", help="Local ie_data.xls path (skip download)")
    parser.add_argument("--output", "-o", help=f"Output path (default: {DEFAULT_OUTPUT})")
    args = parser.parse_args()

    if args.file:
        xls_path = args.file
        print(f"Reading local file: {xls_path}")
    else:
        url = resolve_shiller_url()
        print(f"Downloading from {url}...")
        req = urllib.request.Request(url, headers={"User-Agent": UA})
        with urllib.request.urlopen(req, timeout=120) as resp:
            data = resp.read()
        with tempfile.NamedTemporaryFile(suffix=".xls", delete=False) as tmp:
            tmp.write(data)
            xls_path = tmp.name
        print(f"Downloaded {len(data)} bytes")

    points = parse_cape_from_xls(xls_path)

    if not args.file:
        os.unlink(xls_path)

    if len(points) < 10:
        print(f"ERROR: only parsed {len(points)} points — something is wrong", file=sys.stderr)
        sys.exit(1)

    output_path = args.output or DEFAULT_OUTPUT
    os.makedirs(os.path.dirname(output_path), exist_ok=True)

    # Backup existing file if present
    if os.path.exists(output_path):
        bak = output_path + ".bak"
        os.rename(output_path, bak)
        print(f"Backed up old file to {bak}")

    with open(output_path, "w") as f:
        json.dump(points, f, indent=2)

    print(f"\nWrote {len(points)} CAPE data points to {output_path}")
    print(f"  Range: {points[0]['date']} to {points[-1]['date']}")
    print(f"  Latest CAPE: {points[-1]['close']:.2f}")
    print(f"  Historical range: {min(p['close'] for p in points):.2f} — "
          f"{max(p['close'] for p in points):.2f}")


if __name__ == "__main__":
    main()
