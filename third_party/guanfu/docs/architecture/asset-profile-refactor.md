# ADR: Asset Profile Architecture

Status: accepted; Phase 2 registry implemented, reading-lens refactor pending

Date: 2026-05-11

## Context

Guanfu started as a BTC-first reading and forecasting system. The current code
already supports BTC, QQQ, SPY, Gold, and arbitrary US stock forecasts, but the
asset-specific knowledge is spread across several places:

- `pkg/engine/asset_*.go` chooses snapshot, panel, verdict, and forecast wiring.
- `pkg/assetprofile/profile.go` now owns profile identity, feature bundle keys,
  expected feature names, default horizons, static reliability cells, conformal
  calibration scale, and horizon-specific weight policy.
- `pkg/forecast/features/bundles.go` resolves extractor bundles from profile
  bundle keys.
- `pkg/forecast/forecast.go` consumes profile policy and annotates forecast JSON
  with profile metadata.
- `cmd/guanfu`, `cmd/guanfu-mcp`, and backtest commands resolve horizons and
  feature bundles through the profile registry.
- `skill/SKILL.md` describes all assets, but remains BTC-first in structure.

Before the registry this created a hidden coupling: non-BTC assets no longer
used BTC-only AHR or halving features, but they still inherited BTC-era
assumptions through shared forecast scaling, shared output schema, shared
CLI/MCP contracts, and shared skill wording.

The problem is broader than forecasting. Market reading also needs asset-aware
domain definitions, evidence rules, source health expectations, and downgrade
behavior.

## Decision

Introduce `AssetProfile` as the single source of asset knowledge. The forecast
engine, panel builders, CLI, MCP, and skill should read asset-specific policy
from profiles instead of maintaining parallel switch statements. The first
implementation is intentionally code-first in `pkg/assetprofile` so callers can
share one typed registry before moving the remaining reading-lens contracts into
data files or richer profile structs.

The target layering is:

```text
DataSource        Raw source adapters and PriceStore refresh policies
AssetProfile      Asset identity, class, capabilities, and contracts
ReadingLens       Panel domains, indicators, thresholds, and verdict rules
ForecastProfile   Features, normalization, horizons, reliability, calibration
SkillProfile      Human/AI reading protocol and caveat language
```

The generic forecast engine remains shared. Asset-specific assumptions move out
of `forecast.Build` and into `ForecastProfile`.

## Target Interfaces

Illustrative Go shape:

```go
type AssetClass string

const (
    AssetClassBTC         AssetClass = "btc"
    AssetClassEquityIndex AssetClass = "equity_index"
    AssetClassGold        AssetClass = "gold"
    AssetClassUSStock     AssetClass = "us_stock"
)

type AssetProfile interface {
    Key() string
    Class() AssetClass
    DisplayName() string
    DataSources() []SourceSpec
    ReadingLens() ReadingLens
    ForecastProfile() ForecastProfile
    SkillProfile() SkillProfile
}

type ReadingLens struct {
    Domains       []DomainSpec
    VerdictPolicy VerdictPolicy
    SourcePolicy  SourcePolicy
}

type ForecastProfile struct {
    Horizons       []int
    TopK           map[int]int
    Extractors     []FeatureSpec
    Normalizers    map[string]FeatureNormalizer
    HorizonWeights map[int]map[string]float64
    Reliability    map[int]ReliabilityCell
    Calibration    map[int]ConformalCalibration
}
```

Profiles may share helpers, but each profile owns the semantic meaning of its
domains and features.

## Reading Lenses

BTC remains an 8-domain crypto-cycle lens:

```text
cycle / valuation / network / positioning / macro / flow / technical / cross_asset
```

Equity indices use a different lens:

```text
valuation / earnings_macro / liquidity_rates / credit_risk /
breadth_sentiment / options_positioning / technical
```

Gold uses a macro/positioning lens:

```text
real_yield / usd / inflation / positioning / risk_off / technical / cross_asset
```

Arbitrary US stocks use a conservative single-name lens:

```text
price_action / valuation / fundamentals / macro / sector_relative /
flow_sentiment / event_risk / technical
```

Shared technical indicators are implementation helpers, not a shared reading
philosophy. For example, RSI can appear in all profiles, but the threshold,
weight, and caveat may differ by asset class.

## Forecast Rules

Feature extraction should be split into two phases:

1. Raw feature calculation: return value, date, source, and feature name.
2. Profile normalization: scale, clipping, direction, and weight.

This prevents BTC volatility assumptions from leaking into QQQ, SPY, Gold, or
single stocks. For example, a 30-day return scale suitable for BTC is not a safe
default for SPY or a low-volatility large-cap stock.

The generic engine should not know that Gold has weak 30d history or that QQQ
uses put/call data. It should receive a fully resolved `ForecastProfile`.

## Skill Contract

`skill/SKILL.md` becomes a router and protocol overview. Asset-specific reading
instructions live in `skill/profiles/`:

- `btc.md`
- `equity_index.md`
- `gold.md`
- `us_stock.md`

Reusable contracts live in `skill/contracts/`:

- `asset-profile.md`
- `adding_asset.md`

Skill consumers must load:

1. `tier1.md` for data/reliability rules.
2. The relevant asset profile.
3. `tier2.md` only when making a decision-style synthesis.

## Migration Plan

### Phase 1 - Documentation And Contracts

- Add this ADR.
- Add skill profile files and onboarding contract.
- Update skill README and top-level SKILL routing table.
- Record the architectural boundary in `docs/internals.md`.

### Phase 2 - Profile Registry

Status: implemented in `pkg/assetprofile`.

- Added `pkg/assetprofile`.
- Moved default horizons, reliability cells, conformal calibration, horizon
  weights, feature bundle keys, expected feature names, profile versions, and
  skill-profile URIs behind profile lookups.
- Kept existing `engine.Asset` implementations as orchestrators.
- CLI, MCP, backtest, and asset `BuildForecast` now share the same profile
  registry for horizons and extractor bundle selection.

Acceptance:

- Done: CLI, MCP, backtest, and asset `BuildForecast` all read horizon and
  reliability data from the same profile registry.
- Done: `Forecast` output includes `profile_key`, `profile_version`,
  `asset_class`, `feature_bundle`, and `skill_profile_uri` when `Options.Asset`
  is set.

### Phase 3 - Reading Lens Refactor

Status: implemented.

- Profile registry owns domain order, net-direction thresholds, regime labels,
  stance language, low-coverage threshold, and indicator scoring rules
  (`ScoringRules` map) for BTC / equity index / Gold / US stock.
- `BuildEquityVerdictEnhanced` unified with `BuildProfiledMarketVerdict`; no
  more hardcoded regime/stance strings in `equity_dashboard.go`.
- `scoreEquityDomainEnhanced` deleted; `applyRule` is a pure execution helper
  with no asset-specific knowledge.
- Gold and arbitrary US stocks call neutral `BuildMarketPanel`; QQQ/SPY retain
  `BuildEquityPanel` wrapper.
- `momentum_90d` moved from `Cycle` to `Technical` domain so verdict scoring
  sees it.

Acceptance:

- Done: Gold no longer depends on equity verdict semantics.
- Done: QQQ/SPY and arbitrary US stocks do not expose BTC-only domain expectations.
- Done: All indicator scoring rules (thresholds) live in `pkg/assetprofile`
  `ScoringRules`; engine `applyRule` is asset-agnostic.

### Phase 4 - Forecast Feature Normalization

Status: implemented.

- `FeatureScales map[string]float64` added to each profile.
- Equity scales: return_30d=0.08, return_90d=0.15, return_180d=0.25,
  drawdown_90d=0.20, realized_vol_30d=0.15 (vs BTC 0.30/0.60/1.00/0.40/0.50).
- Gold scales: return_30d=0.06, return_90d=0.12, return_180d=0.20,
  drawdown_90d=0.15, realized_vol_30d=0.10.
- `GenericTechnicalExtractorsWithScales` injects profile scales at bundle build
  time; BTC `CoreExtractors` unchanged.
- Backtest improvement: Gold 180d 52.9% → 65.2% (+12.3pp) after scale fix.

Acceptance:

- Done: BTC, equity index, gold, and US stock profiles use different
  normalization scales for the same raw feature names.
- Done: Backtest reports show profile name and profile version.

### Phase 5 - Arbitrary Asset Onboarding

- Use `skill/contracts/adding_asset.md` as the required checklist.
- Do not add a new asset class until it has a reading lens, forecast profile,
  source-health policy, and reliability downgrade policy.

## Non-Goals

- Do not rewrite the kNN/conformal engine from scratch.
- Do not reintroduce A-share, Hong Kong, or HS300 asset logic into the core.
- Do not let latest-only data enter forecast features without historical replay
  and ablation.
- Do not promote CMC/CoinGecko context into forecast features until a stable
  historical series and backtest evidence exist.

## Risks

- Larger profile surface area can become boilerplate. Mitigation: share helper
  constructors for common technical indicators and source policies.
- Backward-compatible MCP output may constrain schema cleanup. Mitigation:
  expose profile metadata additively and keep old aliases during migration.
- Static reliability tables can become stale. Mitigation: profile version and
  backtest as-of dates must be rendered in reports.

## Open Questions

- Should arbitrary US stocks share one class-level reliability table initially,
  or should unsupported tickers always be labeled untested?
- Should sector ETF support be a separate `sector_etf` class or a specialization
  of `equity_index`?
- Should profile files be code-only, data-driven YAML/JSON, or a hybrid?

## Success Criteria

- There is one source of truth for each asset's horizons, feature bundle,
  reliability, calibration, source-health policy, and skill reading protocol.
- Adding a new asset does not require editing unrelated BTC, QQQ, SPY, or Gold
  logic.
- A user reading `guanfu stock AAPL --forecast` sees single-name caveats that
  are materially different from BTC or index caveats.
- Backtest output names the active profile and profile version.
