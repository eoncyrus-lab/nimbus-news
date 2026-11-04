// Package claim records guanfu's falsifiable forecasts (Claim); they go to
// ~/.guanfu/claims/ for later regression / calibration (Track K of v3 roadmap).
// (2026-07: user Intent records retired — that job moved to the nimbus side.)
//
// Design principles:
//
//   - Claim = tool-emitted, auto-written on every BuildForecast. The user
//     does not opt in; they opt out via GUANFU_NO_CLAIMS=1.
//   - Claims are append-only JSON files on disk — no index, no migration,
//     no renames. Schema fields are additive (old records remain readable).
//   - A Claim references the panel it was built from via a content hash,
//     so calibration can reconstruct the evidence base even if upstream
//     data changes later.
//
// This package does NOT import engine / forecast — those packages import
// this one (one-way dep to avoid cycles). The Claim / Intent types are
// intentionally JSON-native and independent of any domain type.

package claim

import "time"

// SchemaVersion is the current Claim/Intent schema version. Bump only when
// adding a new required field or renaming/removing an existing one. Additive
// optional fields do NOT require a bump.
const SchemaVersion = 1

// Claim is one falsifiable forecast the tool emitted at a point in time.
// Every BuildForecast call produces one Claim per horizon.
type Claim struct {
	// Identity.
	ID      string    `json:"id"`      // uuid-v7-like (time-sortable)
	Asset   string    `json:"asset"`   // btc / qqq / spy / gold / stock_aapl
	AsOf    time.Time `json:"as_of"`   // snapshot time of the source panel
	Horizon int       `json:"horizon"` // days; 1 Claim per (asset, horizon, as_of)

	// Core numeric prediction (subject to HardBlocked — see below).
	PriceAtClaim   float64 `json:"price_at_claim"`
	IntervalLow    float64 `json:"interval_low"`    // p10 return, decimal (not pct)
	IntervalHigh   float64 `json:"interval_high"`   // p90 return
	ExpectedReturn float64 `json:"expected_return"` // median return
	ProbabilityUp  float64 `json:"probability_up"`  // P(return > 0)

	// Reliability guardrail (mirrors forecast.HorizonForecast).
	HardBlocked     bool   `json:"hard_blocked,omitempty"`
	ReliabilityNote string `json:"reliability_note,omitempty"`

	// Evidence bundle — reconstructable on calibration.
	SourceSnapshotSHA string      `json:"source_snapshot_sha"` // sha256 of panel JSON
	FeatureCoverage   float64     `json:"feature_coverage"`    // 0-1
	AnalogCount       int         `json:"analog_count"`
	DominantAnalogs   []AnalogRef `json:"dominant_analogs,omitempty"` // top 3 kNN analogs

	// Narrative — optional, but recommended when emitting via SKILL.
	Scenarios        []ScenarioProb     `json:"scenarios,omitempty"`
	InvalidationCond []InvalidationCond `json:"invalidation_conditions,omitempty"`

	// Meta.
	Method        string `json:"method"` // e.g. "historical_analogue_knn_v2"
	SchemaVersion int    `json:"schema_version"`
}

// AnalogRef is a pointer to a historical point that contributed to the kNN
// ranking — enough to reconstruct its context without copying the full
// panel.
type AnalogRef struct {
	Date       string  `json:"date"`       // YYYY-MM-DD of analog
	Price      float64 `json:"price"`      // price at analog date
	Similarity float64 `json:"similarity"` // 0-100
}

// ScenarioProb is a human-framed probability bucket. Populated when SKILL
// translates the quantile distribution into named scenarios (J4).
type ScenarioProb struct {
	Name       string  `json:"name"`                  // "温和延续" / "区间震荡" / "下行压力"
	Prob       float64 `json:"prob"`                  // 0-1
	Rationale  string  `json:"rationale"`             // one-liner
	AnalogDate string  `json:"analog_date,omitempty"` // YYYY-MM-DD if anchored
}

// InvalidationCond is a concrete indicator-level kill criterion.
type InvalidationCond struct {
	Metric      string  `json:"metric"`   // "funding_rate_pct"
	Operator    string  `json:"operator"` // ">" / "<" / "cross_above" / "cross_below"
	Threshold   float64 `json:"threshold"`
	Description string  `json:"description"` // "若 funding 连续 7d > 0.08%"
}
