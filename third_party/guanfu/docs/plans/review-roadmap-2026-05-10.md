# Guanfu Review Roadmap - 2026-05-10

Status: implemented through 2026-05-11 review pass, with the first
AssetProfile registry migration now landed. P4 candidate sources are wired with
source-health gates; CBOE put/call replaced the Stooq default path,
Deribit/CMC context sources are documented, and reliability/backtest tables were
refreshed. Promotion weight should still be reviewed by ablation when fresh
historical coverage is available.

This plan turns the latest project review into an implementation roadmap. The
goal is to improve two product modes without reintroducing China A-share or Hong
Kong stock logic:

- **Market reading**: make the current dashboard more reliable and more useful
  for CNY-based investors.
- **Forecasting**: improve backtest validity, calibration, and feature discipline.

## Principles

1. Fix data quality and evaluation before adding more model inputs.
2. Keep USD/CNY and central-bank data as context first; promote them into
   forecast features only after ablation proves value.
3. Keep HS300, A-share, and Hong Kong stock asset logic out of Guanfu.
4. Every new source must expose source health and a clear stale/missing state.
5. Every model change must be validated by asset-specific backtests.
6. New assets must enter through an AssetProfile/ReadingLens/ForecastProfile
   contract, not by copying BTC or QQQ/SPY logic.

## P0 - Multi-Asset Architecture Boundary

Priority: highest for further expansion.

Current state:

- ADR added: [`docs/architecture/asset-profile-refactor.md`](../architecture/asset-profile-refactor.md).
- Skill profiles added for BTC, equity indices, Gold, and arbitrary US stocks.
- New asset checklist added under `skill/contracts/adding_asset.md`.
- `pkg/assetprofile` added as the forecast-side profile registry.
- CLI, MCP, backtest, and `engine.Asset.BuildForecast` now resolve default
  horizons, feature bundles, static reliability, conformal scale, horizon
  weights, expected feature names, and profile metadata from the registry.
- `Forecast` JSON now exposes `profile_key`, `profile_version`, `asset_class`,
  `feature_bundle`, and `skill_profile_uri`.
- `IndicatorPanel` JSON now exposes profile metadata plus `domain_meta` for
  asset-specific reading lenses.
- Gold verdict no longer extends the equity verdict; VIX risk-off is interpreted
  as a gold safe-haven driver rather than an equity-style bearish macro signal.
- Gold and arbitrary US stocks now build shared technical/macro indicators via
  neutral `BuildMarketPanel`; QQQ/SPY retain the explicit `BuildEquityPanel`
  wrapper.
- Verdict policy metadata now lives in `pkg/assetprofile`: domain order,
  thresholds, regime labels, stance language, and low-coverage threshold for
  BTC/equity-index/Gold/US-stock profiles. Engine scoring helpers still own
  individual indicator interpretation.

Next steps:

- Done: Split raw feature extraction from profile-specific normalization
  (`FeatureScales` per profile, `GenericTechnicalExtractorsWithScales`).
- Done: Move individual indicator scoring rules out of `engine` into
  profile-owned `ScoringRules` contracts.
- Done: `BuildMarketPanel` is free of asset-class semantics; asset-specific
  wrappers exist for QQQ/SPY (`BuildEquityPanel`) and Gold (`BuildMarketPanel`
  + gold valuation domain).

Acceptance:

- Done: Backtest output includes active profile name and profile version.
- Partially done: adding a forecast profile no longer requires updating
  CLI/MCP/backtest switch statements. Adding a new reading lens has metadata
  support, but verdict implementation still touches `engine`.

## P1 - Data Source Health And Output Trust

Priority: highest.

### P1.1 Complete Source Health Coverage

Extend top-level output so each asset view clearly shows:

- source status: `ok`, `partial`, `missing`, `stale`, or `skip`
- latest available date
- fallback usage
- whether the source affects forecast or only market reading

Current state:

- BTC source health exists.
- USD/CNY and global central-bank rates have been added to source health.
- QQQ/SPY put/call and BTC Deribit options now expose source health through
  refreshed PriceStore keys or live BTC source health.
- `stooq_putcall` is a legacy storage key only; default refresh now uses CBOE
  official no-key data and no longer requires `STOOQ_APIKEY`.

Acceptance:

- `guanfu btc --forecast`, `guanfu qqq --forecast`, `guanfu spy --forecast`,
  and `guanfu gold --forecast` expose enough source-health context for users to
  judge forecast reliability without separately reading the refresh table.

### P1.2 Split Refresh Skip Semantics

Make refresh output distinguish:

- fresh skip: data is recent enough
- config skip: missing API key or optional configuration
- stale source: data exists but is too old for strong conclusions

Known cases:

- `stooq_putcall` is now populated from CBOE official no-key data; the storage
  key keeps its legacy name for forecast compatibility.
- `fred_pboc_interbank_rate` is stale and should be treated as macro background,
  not as a strong forecast input.
- `deribit_options` is market-reading impact only; if skew/DVOL is unavailable,
  BTC price history and forecast remain usable.

Acceptance:

- `guanfu refresh` makes actionability obvious: ignore, configure, replace, or
  investigate.

### P1.3 Update Data Source Documentation

Update `docs/DATA-SOURCES.md` with:

- required sources
- optional sources
- fallback sources
- stale thresholds
- impact on market reading vs forecast

Acceptance:

- A new operator can understand which sources are required for core behavior and
  which are optional context.

## P2 - Market Reading Enhancements

These changes can ship before model changes because they are dashboard/context
outputs, not forecast signals.

### P2.1 Add CNY Investor Lens

Add CNY-aware context for USD-denominated assets:

- `asset_price_cny`
- `asset_return_cny_30d`
- `asset_return_cny_90d`
- USD price return vs CNY return spread
- USD/CNY trend warning when CNY movement dominates local-currency returns

Acceptance:

- CNY-based users can see whether their realized local-currency experience is
  driven by asset return, FX return, or both.

### P2.2 Global Central Bank Panel

Keep the current US/EU/JP/CN macro context:

- Fed front-end rate
- ECB deposit rate
- BOJ call rate
- PBoC / China interbank proxy
- US-China rate spread
- developed-market policy-rate average

Acceptance:

- The dashboard explains dollar strength, rate spread, and liquidity backdrop
  without turning them into standalone buy/sell signals.

### P2.3 Explicit Non-Goals

Do not implement:

- HS300 asset logic
- A-share asset logic
- Hong Kong stock asset logic
- China macro forecasting as a Guanfu core asset pipeline

## P3 - Forecast Evaluation And Calibration

Priority: high. This is the core forecasting quality track.

### P3.1 Keep Backtests Asset-Specific

Current state:

- Non-BTC backtests were fixed to avoid BTC-only AHR and halving-cycle features.
- `qqq` and `spy` use equity feature bundles.
- `gold` uses gold feature bundles.
- `btc` keeps BTC core cycle features.
- `backtest all` now reports active profile, profile version, feature bundle,
  missing features, feature coverage, and conformal realized coverage.

Next steps:

- Keep the profile fields stable in JSON output.
- Add per-profile normalization scale tests after raw feature extraction is
  split from normalization.

Acceptance:

- Backtest reports make it clear which features were actually used.

### P3.2 Calibrate Before Adding Features

Latest asset-specific backtest result:

`go run ./cmd/guanfu backtest all --plain` on 2026-05-11 after stale-gated lookup,
horizon-specific re-rank, and asset+horizon conformal calibration:

| Asset | Days | Tests | Hit30d | Hit90d | Hit180d | PIT | CRPS | Coverage |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| qqq | 2757 | 28 | 64.3% | 85.7% | 78.6% | 0.56 | 0.1724 | 100% |
| spy | 2757 | 28 | 67.9% | 78.6% | 82.1% | 0.56 | 0.1224 | 100% |
| btc | 5777 | 62 | 59.7% | 66.1% | 69.3% | 0.47 | 0.5030 | 100% |
| gold | 6447 | 69 | 53.6% | 62.3% | 59.4% | 0.50 | 0.1154 | 100% |

`go test ./pkg/engine -run TestBacktestBundles -v` on 2026-05-11 updated
the static reliability table:

| Asset | Tests | Hit30d | Hit90d | Hit180d |
| --- | ---: | ---: | ---: | ---: |
| btc | 46 | 60.9% | 60.9% | 63.0% |
| qqq | 20 | 70.0% | 75.0% | 80.0% |
| spy | 20 | 60.0% | 75.0% | 85.0% |
| gold | 51 | 45.1% | 62.7% | 52.9% |

Interpretation:

- Directional signal is usable across the four current assets except the
  hard-blocked Gold 30d cell.
- QQQ/SPY are slightly optimistic by PIT.
- Gold calibration is currently best.
- Equity feature coverage is current-state 100%; CBOE put/call now has a max
  age gate, so historical gaps no longer leak through unlimited forward-fill.
- Gold 30d is now hard-blocked after horizon-specific re-rank; Gold 90d is the
  usable gold forecast cell.

Next steps:

- Add asset/horizon-level conformal calibration.
- Compare calibration before and after for PIT and CRPS.
- Avoid adding new features if they improve hit rate but worsen calibration.

Acceptance:

- PIT should move toward `0.45-0.55` without materially worsening CRPS.

### P3.3 Backtest Guardrails

Required validation for any data-source or feature change:

- `go test ./...`
- `go vet ./...`
- `go run ./cmd/guanfu backtest all --plain`
- targeted single-asset backtest for the changed asset

Acceptance:

- No feature or source enters the forecast path without repeatable validation.

## P4 - New Data Sources

Add sources only after P0/P3 are stable.

### P4.1 BTC

Candidate sources:

- historical spot ETF flow
- MVRV Z-score
- NUPL
- SOPR
- stablecoin supply feature validation

Rule:

- On-chain features must be historical series, not latest-only dashboard values.

### P4.2 QQQ / SPY

Candidate sources:

- put/call replacement source (**done**, implemented via CBOE official no-key data under
  the `stooq_putcall` legacy storage key; panel and forecast bundle now expose
  ratio, 30d change, and 252-observation percentile when data exists)
- historical PE/PB if a reliable source exists
- CAPE ablation against current equity bundle

Remaining TODO:

- Optional: implement a cached full CBOE daily-page backfill for 2019-10-07 to
  the recent-window boundary if full post-2019 history becomes necessary.
- Put/call ablation is now reproducible via
  `go run ./cmd/guanfu backtest all --ablate-putcall --plain`; latest run shows
  no hit-rate lift from the three put/call variants yet, so promotion weight
  should stay conservative until full CBOE history is backfilled or more current
  samples accumulate.
- Continue searching for reliable no-key PE/PB or NAAIM/AAII alternatives, but
  do not add latest-only snapshots to forecast bundles.

Rule:

- Do not use latest-only valuation snapshots as forecast features.

### P4.3 Gold

Candidate sources/features:

- real-yield change rate
- COT history
- DXY trend ablation

Rule:

- Prefer rate-of-change features over level-only macro features when the level is
  regime-dependent.

### P4.4 Global Macro

Current status:

- USD/CNY and central-bank rates are market-reading context only.

Promotion rule:

- They can become forecast features only after ablation shows improvement in
  hit rate, PIT, and CRPS.

## P5 - Product Output Separation

Separate user-facing output into two modes.

### Market Reading Mode

Purpose: answer "what is the current market environment?"

Should emphasize:

- price
- valuation
- macro context
- source health
- CNY lens
- stale warnings

### Forecast Mode

Purpose: answer "what did similar historical setups do next?"

Should emphasize:

- forecast horizon
- analog coverage
- confidence
- calibration quality
- invalidation conditions
- source health affecting forecast

## Execution Order

1. P0: AssetProfile architecture boundary and profile registry.
2. P1: source-health coverage, refresh status semantics, data-source docs.
3. P3: backtest report transparency and calibration.
4. P2: CNY investor lens and central-bank dashboard polish.
5. P4: new data sources, each gated by ablation.
6. P5: output separation once the underlying data and forecast paths are stable.

## Current Commit Baseline

The roadmap starts after these recent fixes:

- `643f9da chore: remove hs300 support`
- `241f0f1 feat: add global macro context`
- `beb3e0a fix: harden refresh data sources`
- `c446541 fix: surface macro source health`
- `2ba5ba5 fix: use asset-specific backtest features`

## 2026-05-11 Review Pass Validation

- `go run ./cmd/guanfu --timeout 180s --json refresh --only=stooq_putcall`
  refreshed CBOE put/call: 4307 rows, last_date 2026-05-08.
- `go test ./pkg/engine -run TestBacktestBundles -v` refreshed reliability
  numbers and confirmed QQQ/SPY include put/call features.
- `go run ./cmd/guanfu backtest all --plain` passed for BTC/QQQ/SPY/Gold with
  100% feature coverage.
- `go run ./cmd/guanfu backtest all --ablate-putcall --plain` added a
  reproducible QQQ/SPY put/call ablation table.
- Final gate for this pass: `go test ./...`, `go vet ./...`, `git diff --check`.
