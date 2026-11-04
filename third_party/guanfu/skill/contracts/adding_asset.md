# Adding A New Asset

Use this checklist before adding a new core asset or asset class.

## Required Inputs

- Asset key and asset class.
- Price source with enough history for the requested horizons.
- Source-health policy.
- Reading lens, including verdict policy and low-coverage behavior.
- Forecast profile.
- Skill profile.
- Backtest plan.

## Steps

1. Add or reuse a `DataSource` / PriceStore key.
2. Define the `AssetProfile`.
3. Define the `ReadingLens`, including domain metadata, verdict policy, and
   missing/stale downgrade language.
4. Define the `ForecastProfile`.
5. Add a `skill/profiles/<asset-or-class>.md` file.
6. Register the asset in the asset registry.
7. Add CLI/MCP routing through the profile registry, not by duplicating switch
   statements.
8. Run walk-forward backtests.
9. Add reliability rows only after enough samples exist.
10. Update docs and skill routing.

## Hard Rules

- Do not add latest-only data to forecast.
- Do not reuse BTC-specific cycle/valuation/network domains for non-BTC assets.
- Do not call an arbitrary stock forecast reliable without ticker-level or
  class-level evidence.
- Do not add a new asset by copying QQQ/SPY logic unless the new asset is truly
  an equity index/ETF and the profile says so.

## Minimum Validation

```bash
go test ./...
go vet ./...
go run ./cmd/guanfu backtest <asset> --plain
```

For assets with optional feature families, add an ablation command before
promoting those features.

## Promotion Rule

A source starts as `reading` context. It can become a `forecast` feature only
when all of these are true:

- historical daily or regularly sampled series exists
- stale/missing behavior is explicit
- walk-forward backtest passes the regression budget
- docs and skill explain the feature's directionality and caveats
