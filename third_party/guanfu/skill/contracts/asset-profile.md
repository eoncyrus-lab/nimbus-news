# Asset Profile Contract

Every supported asset or asset class must define five contracts.

Code registry: `pkg/assetprofile/profile.go`.

The Go registry is currently authoritative for profile identity, forecast-side
policy, and outer reading verdict policy: canonical key, asset class, display
name, profile version, reading domain metadata, verdict domain order, verdict
thresholds, regime labels, stance language, low-coverage threshold, default
horizons, feature bundle key, expected feature names for missing-feature
diagnostics, static reliability rows, conformal calibration scale,
horizon-specific weight boosts, and `skill_profile_uri`.

The Markdown profile files remain authoritative for AI reading protocol,
indicator-level caveat language, and asset-specific interpretation examples.
Indicator scoring rules and raw feature normalization scales are still being
migrated out of engine/extractor helpers into profile-backed contracts.

## 1. Data Contract

- canonical asset key
- display name
- asset class
- required sources
- optional sources
- source freshness limits
- fallback behavior
- whether each source affects `reading`, `forecast`, or both

## 2. Reading Contract

- domain list
- indicator list per domain
- raw value meaning
- directionality: bullish, bearish, crowded, risk-off, informational
- threshold rules
- missing/stale downgrade language
- verdict aggregation policy
- verdict domain order, net-direction thresholds, regime labels, stance
  language, and low-coverage behavior

## 3. Forecast Contract

- default horizons
- allowed feature names
- feature normalizers and clipping scales
- feature weights
- horizon-specific feature boosts
- TopK/window defaults
- conformal calibration policy
- reliability policy

## 4. Skill Contract

- what to load before answering
- required caveats
- forbidden analogies or domain transfers
- output language for hard-blocked forecasts
- profile-specific examples

## 5. Validation Contract

- minimum data history
- walk-forward command
- ablation commands
- regression budget
- promotion rule from reading-only to forecast feature

No asset should be considered first-class until all five contracts exist.
