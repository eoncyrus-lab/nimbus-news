package forecast

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/assetprofile"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/client"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
)

const (
	defaultTopK                = 21
	defaultStepDays            = 7
	defaultDiversifyWindowDays = 30
	minSelectedAnalogs         = 5
	minSharedFeatures          = 6
	// expectedProbeWindowDays bounds how far back probeExpectedFeatures scans
	// to find the maximum achievable feature count / weight. 60 days covers
	// the typical ingestion gap for monthly macro series (CAPE, PMI) without
	// adding meaningful cost versus the O(days/step) candidate loop.
	expectedProbeWindowDays = 60

	ahrLegacyLogSlope      = 5.84
	ahrLegacyLogIntercept  = -17.01
	ahrCompressionExponent = 0.75
)

var defaultHorizons = []int{30, 90, 180}

// HorizonsForAsset returns the asset-specific default horizons.
// Falls back to {30,90,180} when no per-asset override is registered.
// Callers should NOT route through DefaultOptions() when filling missing
// horizons — that overwrites Extractors/TopK set elsewhere.
func HorizonsForAsset(asset string) []int {
	if p, ok := assetprofile.For(asset); ok {
		return append([]int(nil), p.Horizons...)
	}
	return append([]int(nil), defaultHorizons...)
}

// Options controls the historical analogue forecast.
type Options struct {
	Horizons            []int              `json:"horizons"`
	TopK                int                `json:"top_k"`
	StepDays            int                `json:"step_days"`
	DiversifyWindowDays int                `json:"diversify_window_days"`
	Extractors          []FeatureExtractor `json:"-"` // pluggable feature extractors
	MinFeatures         int                `json:"-"` // min shared features (default 6)
	UseMahalanobis      bool               `json:"-"` // use Mahalanobis distance
	LearnWeights        bool               `json:"-"` // learn feature weights from data
	Frequency           string             `json:"-"` // "daily" or "monthly"
	// Asset is the canonical key (btc/qqq/spy/gold) used for
	// per-asset reliability lookups. Empty means "no asset claim" — Build
	// will skip writing ReliabilityNote on each HorizonForecast.
	Asset string `json:"-"`
	// RecencyWeighted (G5): when true, analogs from the recent window
	// (default 5 years) receive a 1.25× effective weight in candidate
	// ranking. This partially addresses the concern that pre-2024
	// analogs may not generalize to the post-ETF / post-rate-regime
	// BTC market. When false (default), every era is treated equally.
	RecencyWeighted bool `json:"-"`
	// RecencyWindowYears is the cutoff for RecencyWeighted. Zero → 5.
	RecencyWindowYears int `json:"-"`
	// RegimeGate (G2): when true, analogs whose local regime (bull/bear/
	// fracture) differs from the current regime have their distance
	// penalized (×1.2). Directly addresses Gold's 2017-2022 / 2023-2025
	// regime-splitting walk-forward observation. Cheap & additive.
	RegimeGate bool `json:"-"`
	// DisableHorizonWeights keeps legacy behavior where all forecast horizons
	// share one analog ranking. Default false enables horizon-specific
	// re-ranking so 30d emphasizes short-term technical/risk features while
	// 180d emphasizes valuation/macro features.
	DisableHorizonWeights bool `json:"-"`
}

// Point is an oldest-first daily close price.
type Point struct {
	Date   string  `json:"date"`
	Close  float64 `json:"close"`
	Source string  `json:"source,omitempty"`
}

// FeatureExtractor is a pluggable function that extracts 0+ features from price
// history at a given index. Returns nil, false if insufficient data.
// Points are oldest-first, index i is the target date.
type FeatureExtractor func(points []Point, i int) ([]FeatureValue, bool)

// Forecast is a probabilistic scenario inference, not a deterministic price target.
type Forecast struct {
	Date            string            `json:"date"`
	CurrentPrice    float64           `json:"current_price"`
	Method          string            `json:"method"`
	MethodNote      string            `json:"method_note"`
	ProfileKey      string            `json:"profile_key,omitempty"`
	ProfileVersion  string            `json:"profile_version,omitempty"`
	AssetClass      string            `json:"asset_class,omitempty"`
	FeatureBundle   string            `json:"feature_bundle,omitempty"`
	SkillProfileURI string            `json:"skill_profile_uri,omitempty"`
	Coverage        Coverage          `json:"coverage"`
	CurrentFeatures []FeatureValue    `json:"current_features"`
	Horizons        []HorizonForecast `json:"horizons"`
	Analogs         []Analog          `json:"analogs"`
	Caveats         []string          `json:"caveats"`
}

type Coverage struct {
	FeatureCount      int     `json:"feature_count"`
	ExpectedFeatures  int     `json:"expected_features"`
	FeatureCoverage   float64 `json:"feature_coverage"`
	CandidateCount    int     `json:"candidate_count"`
	SelectedAnalogs   int     `json:"selected_analogs"`
	AverageSimilarity float64 `json:"average_similarity"`
	Confidence        string  `json:"confidence"`
}

type FeatureValue struct {
	Name       string  `json:"name"`
	Value      float64 `json:"value"`
	Normalized float64 `json:"normalized"`
	Weight     float64 `json:"weight"`
	Note       string  `json:"note,omitempty"`
}

type HorizonForecast struct {
	Days                          int     `json:"days"`
	SampleSize                    int     `json:"sample_size"`
	AverageReturnPct              float64 `json:"avg_return_pct"`
	MedianReturnPct               float64 `json:"median_return_pct"`
	P10ReturnPct                  float64 `json:"p10_return_pct"`
	P25ReturnPct                  float64 `json:"p25_return_pct"`
	P75ReturnPct                  float64 `json:"p75_return_pct"`
	P90ReturnPct                  float64 `json:"p90_return_pct"`
	ProbabilityUp                 float64 `json:"probability_up"`
	ProbabilityUpsideContinuation float64 `json:"probability_upside_continuation"`
	ProbabilityRange              float64 `json:"probability_range"`
	ProbabilityDownsidePressure   float64 `json:"probability_downside_pressure"`
	ExpectedPrice                 float64 `json:"expected_price"`
	MedianPrice                   float64 `json:"median_price"`
	P10Price                      float64 `json:"p10_price"`
	P90Price                      float64 `json:"p90_price"`
	DominantScenario              string  `json:"dominant_scenario"`
	DominantLabel                 string  `json:"dominant_label"`
	UpsideThresholdPct            float64 `json:"upside_threshold_pct"`
	DownsideThresholdPct          float64 `json:"downside_threshold_pct"`
	// ReliabilityNote is populated when the (asset, horizon) combination has
	// historically poor or untested directional accuracy. Empty when reliable
	// or no historical data is recorded. Sourced from forecast/reliability.go.
	ReliabilityNote string `json:"reliability_note,omitempty"`
	// HardBlocked is true when dir_hit ≤ 0.50 (at or below coin-flip). Numeric
	// predictions in this struct (median/p10/p90/expected_price) are still
	// populated for debugging but consumers should not render them as
	// actionable. Display layers should surface ReliabilityNote instead.
	HardBlocked bool `json:"hard_blocked,omitempty"`

	// Baseline comparison fields (H3/J14). All in percent. Populated by
	// AnnotateBaselines (caller-driven); absent when no baseline provider
	// is wired. See pkg/forecast/baseline.go.
	RiskFreeReturnPct    float64 `json:"risk_free_return_pct,omitempty"`
	PassiveReturnPct     float64 `json:"passive_return_pct,omitempty"`
	RiskAdjustedDeltaPct float64 `json:"risk_adjusted_delta_pct,omitempty"`
	BaselineNote         string  `json:"baseline_note,omitempty"`

	// Conformal interval fields (G1). Empirical p10/p90 above are quantile
	// estimates — they have no statistical coverage guarantee, especially
	// with small analog samples. These fields expose a finite-sample-
	// corrected split-conformal interval at the specified alpha level.
	// Under analog exchangeability, the interval [ConformalLow, ConformalHigh]
	// contains a future observation with probability ≥ 1-α. See conformal.go.
	ConformalLowPct    float64 `json:"conformal_low_pct,omitempty"`  // % return, lower bound
	ConformalHighPct   float64 `json:"conformal_high_pct,omitempty"` // % return, upper bound
	ConformalAlpha     float64 `json:"conformal_alpha,omitempty"`    // e.g. 0.20 for 80% interval
	ConformalCoverage  float64 `json:"conformal_coverage,omitempty"` // finite-sample achievable coverage ∈ [0,1]
	ConformalLowPrice  float64 `json:"conformal_low_price,omitempty"`
	ConformalHighPrice float64 `json:"conformal_high_price,omitempty"`
	// ConformalCalibrationScale is set when a static asset+horizon calibration
	// widens the interval based on walk-forward realized coverage.
	ConformalCalibrationScale float64 `json:"conformal_calibration_scale,omitempty"`

	// Ensemble cross-check fields (G4). A ridge regression fit on the
	// same (candidate_features, forward_return) pairs predicts the
	// current horizon's return as a scalar; large disagreement with
	// the kNN median flags regime stress. See ensemble.go.
	EnsembleLinearPct       float64 `json:"ensemble_linear_pct,omitempty"`
	EnsembleDisagreementPct float64 `json:"ensemble_disagreement_pct,omitempty"`
}

type Analog struct {
	Date              string             `json:"date"`
	Price             float64            `json:"price"`
	Distance          float64            `json:"distance"`
	Similarity        float64            `json:"similarity"`
	MatchedFeatures   int                `json:"matched_features"`
	ForwardReturnsPct map[string]float64 `json:"forward_returns_pct"`
}

type featureSet struct {
	values []FeatureValue
	byName map[string]FeatureValue
}

type candidate struct {
	index    int
	date     time.Time
	dist     float64
	sim      float64
	matched  int
	features featureSet
}

func DefaultOptions() Options {
	return Options{
		Horizons:            append([]int(nil), defaultHorizons...),
		TopK:                defaultTopK,
		StepDays:            defaultStepDays,
		DiversifyWindowDays: defaultDiversifyWindowDays,
	}
}

func ParseHorizons(raw string) ([]int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return append([]int(nil), defaultHorizons...), nil
	}
	parts := strings.Split(raw, ",")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(strings.TrimSuffix(part, "d"))
		if part == "" {
			continue
		}
		days, err := strconv.Atoi(part)
		if err != nil || days <= 0 {
			return nil, fmt.Errorf("invalid horizon %q", part)
		}
		out = append(out, days)
	}
	return normalizeHorizons(out), nil
}

func PointsFromBTCDaily(points []client.BTCDailyPoint) []Point {
	out := make([]Point, 0, len(points))
	for _, p := range points {
		close, _ := p.Close.Float64()
		out = append(out, Point{Date: p.Date, Close: close, Source: p.Source})
	}
	return normalizePoints(out)
}

func PointsFromSnapshot(snap *model.MarketSnapshot) ([]Point, error) {
	if snap == nil {
		return nil, fmt.Errorf("snapshot is nil")
	}
	if len(snap.BTCPriceHistory) == 0 {
		return nil, fmt.Errorf("snapshot has no BTC price history")
	}
	latestDate, err := parseSnapshotBTCDate(snap)
	if err != nil {
		return nil, err
	}
	out := make([]Point, 0, len(snap.BTCPriceHistory))
	for idx := len(snap.BTCPriceHistory) - 1; idx >= 0; idx-- {
		close, _ := snap.BTCPriceHistory[idx].Float64()
		date := latestDate.AddDate(0, 0, -idx).Format("2006-01-02")
		out = append(out, Point{Date: date, Close: close, Source: "snapshot:BTCPriceHistory"})
	}
	return normalizePoints(out), nil
}

func Build(points []Point, opts Options) (*Forecast, error) {
	points = normalizePoints(points)
	if len(points) == 0 {
		return nil, fmt.Errorf("daily history is empty")
	}
	opts = normalizeOptions(opts)
	if len(opts.Extractors) == 0 {
		return nil, fmt.Errorf("no feature extractors provided")
	}
	maxHorizon := maxInt(opts.Horizons)
	if len(points) <= maxHorizon+200 {
		return nil, fmt.Errorf("history has %d days, need more than %d", len(points), maxHorizon+200)
	}

	currentIdx := len(points) - 1
	currentFeatures := extractFeatures(points, currentIdx, opts.Extractors)
	if len(currentFeatures.values) < opts.MinFeatures {
		return nil, fmt.Errorf("current state lacks enough features: %d < %d", len(currentFeatures.values), opts.MinFeatures)
	}

	candidates := make([]candidate, 0, len(points)/opts.StepDays)
	for i := 0; i+maxHorizon < len(points); i += opts.StepDays {
		fs := extractFeatures(points, i, opts.Extractors)
		if len(fs.values) < opts.MinFeatures {
			continue
		}

		// Store as candidate first (Euclidean distance for initial ranking)
		dist, matched, ok := distance(currentFeatures, fs)
		if !ok {
			continue
		}
		candidates = append(candidates, candidate{
			index:    i,
			date:     mustParseDate(points[i].Date),
			dist:     dist,
			sim:      similarityFromDistance(dist),
			matched:  matched,
			features: fs,
		})
	}

	// If Mahalanobis enabled, re-compute distances using feature covariance
	if opts.UseMahalanobis && len(candidates) > 10 {
		for i := range candidates {
			mDist, mMatched, ok := mahalanobisDistance(currentFeatures, candidates[i].features, candidates)
			if ok {
				candidates[i].dist = mDist
				candidates[i].sim = similarityFromDistance(mDist)
				candidates[i].matched = mMatched
			}
		}
		// Re-sort by Mahalanobis distance
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].dist == candidates[j].dist {
				return candidates[i].date.Before(candidates[j].date)
			}
			return candidates[i].dist < candidates[j].dist
		})
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("no comparable historical analogues with forward returns")
	}
	// G5: recency weighting. Shrinks distances on recent analogs so they
	// bubble up in the sort. 0.8× on distance = ~1.25× effective weight
	// without touching the kNN math elsewhere.
	if opts.RecencyWeighted {
		years := opts.RecencyWindowYears
		if years <= 0 {
			years = 5
		}
		today := mustParseDate(points[currentIdx].Date)
		cutoff := today.AddDate(-years, 0, 0)
		for i := range candidates {
			if candidates[i].date.After(cutoff) {
				candidates[i].dist *= 0.8
				candidates[i].sim = similarityFromDistance(candidates[i].dist)
			}
		}
	}
	// G2: regime gating. Candidates whose local regime (rough bull/bear
	// bucket from Mayer + 90d drawdown) differs from the current state
	// get a 1.2× distance penalty. Cheap approximation of regime-aware
	// kNN without running DetectRegime for every candidate.
	if opts.RegimeGate {
		currentRegime := regimeBucket(points, currentIdx)
		for i := range candidates {
			if regimeBucket(points, candidates[i].index) != currentRegime {
				candidates[i].dist *= 1.2
				candidates[i].sim = similarityFromDistance(candidates[i].dist)
			}
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].dist == candidates[j].dist {
			return candidates[i].date.Before(candidates[j].date)
		}
		return candidates[i].dist < candidates[j].dist
	})
	selected := selectAnalogs(candidates, opts.TopK, opts.DiversifyWindowDays)
	if len(selected) < minSelectedAnalogs {
		return nil, fmt.Errorf("only %d historical analogues selected, need at least %d", len(selected), minSelectedAnalogs)
	}

	analogs := buildAnalogs(points, selected, opts.Horizons)
	horizons := buildHorizonForecasts(points[currentIdx].Close, points, selected, opts.Horizons, currentFeatures, candidates, opts)
	// Annotate each horizon with its reliability caveat (if asset is set
	// and a recorded reliability cell flags this horizon as weak/untested).
	if opts.Asset != "" {
		for i := range horizons {
			horizons[i].ReliabilityNote = HorizonCaveat(opts.Asset, horizons[i].Days)
			horizons[i].HardBlocked = IsHardBlocked(opts.Asset, horizons[i].Days)
		}
	}
	avgSim := averageSimilarity(selected)

	// Weight-based coverage: compare observed feature weight to the max
	// weight any extractor bundle was ever able to achieve in the recent
	// window. This survives adding features (old fixed=11 saturated any
	// bundle with ≥11 features to 100%, losing discrimination).
	expectedCount, expectedWeight := probeExpectedFeatures(points, currentIdx, opts.Extractors)
	observedWeight := 0.0
	for _, fv := range currentFeatures.values {
		observedWeight += fv.Weight
	}
	var featureCoverage float64
	if expectedWeight > 0 {
		featureCoverage = observedWeight / expectedWeight
	} else if expectedCount > 0 {
		featureCoverage = float64(len(currentFeatures.values)) / float64(expectedCount)
	}

	method := "historical_analogue_knn_v2"
	methodNote := "Pluggable feature extractors; returns are empirical forward-return distributions."
	var profileKey, profileVersion, assetClass, featureBundle, skillProfileURI string
	if opts.Asset != "" {
		if p, ok := assetprofile.For(opts.Asset); ok {
			profileKey = p.Key
			profileVersion = p.Version
			assetClass = string(p.Class)
			featureBundle = p.FeatureBundle
			skillProfileURI = p.SkillProfileURI
		}
	}

	return &Forecast{
		Date:            points[currentIdx].Date,
		CurrentPrice:    points[currentIdx].Close,
		Method:          method,
		MethodNote:      methodNote,
		ProfileKey:      profileKey,
		ProfileVersion:  profileVersion,
		AssetClass:      assetClass,
		FeatureBundle:   featureBundle,
		SkillProfileURI: skillProfileURI,
		Coverage: Coverage{
			FeatureCount:      len(currentFeatures.values),
			ExpectedFeatures:  expectedCount,
			FeatureCoverage:   clamp01(featureCoverage),
			CandidateCount:    len(candidates),
			SelectedAnalogs:   len(selected),
			AverageSimilarity: avgSim,
			Confidence:        confidenceLabel(featureCoverage, avgSim, len(selected), opts.TopK),
		},
		CurrentFeatures: currentFeatures.values,
		Horizons:        horizons,
		Analogs:         analogs,
		Caveats: []string{
			"这是历史相似盘面推演，不是确定性价格预测。",
			"样本统计描述的是历史相似状态后的分布，遇到政策、交易所、流动性或宏观断裂时可能失效。",
		},
	}, nil
}

// regimeBucket is a cheap 3-state classifier (bull / bear / fracture)
// used for regime gating. This is a lightweight sibling of DetectRegime
// that can be called once per candidate without dominating kNN cost.
//
// Buckets:
//   - fracture: Mayer < 0.6 AND 90d drawdown < -35% (crisis-like)
//   - bull:     price > 200d SMA OR (Mayer > 1.0 AND dd_90 > -15%)
//   - bear:     otherwise
//
// Falls back to "bull" when history is too short to judge, so very early
// candidates don't get penalized for insufficient signal.
func regimeBucket(points []Point, i int) int {
	if i < 200 {
		return 0 // treat as bull / neutral when history is thin
	}
	mayer, ok := mayerMultiple(points, i)
	if !ok {
		return 0
	}
	dd, _ := drawdown(points, i, 90)
	if mayer < 0.6 && dd < -0.35 {
		return 2 // fracture
	}
	if mayer > 1.0 {
		return 0 // bull
	}
	return 1 // bear
}

// probeExpectedFeatures scans the last `expectedProbeWindowDays` indices and
// returns the maximum achievable feature count and weight sum for the given
// extractor bundle. Used for weight-based coverage so that adding features
// doesn't saturate the metric at 100% (old fixed-11 behavior). Monthly macro
// series (CAPE, PMI) may only tick once per month — 60-day window is enough
// to catch them on their most recent refresh.
func probeExpectedFeatures(points []Point, currentIdx int, extractors []FeatureExtractor) (int, float64) {
	lo := currentIdx - expectedProbeWindowDays
	if lo < 0 {
		lo = 0
	}
	maxCount := 0
	maxWeight := 0.0
	for i := lo; i <= currentIdx; i++ {
		fs := extractFeatures(points, i, extractors)
		if len(fs.values) > maxCount {
			maxCount = len(fs.values)
		}
		w := 0.0
		for _, fv := range fs.values {
			w += fv.Weight
		}
		if w > maxWeight {
			maxWeight = w
		}
	}
	return maxCount, maxWeight
}

// extractFeatures runs all extractors at a given index and collects results.
func extractFeatures(points []Point, i int, extractors []FeatureExtractor) featureSet {
	fs := featureSet{byName: make(map[string]FeatureValue)}
	for _, ex := range extractors {
		fvs, ok := ex(points, i)
		if !ok || len(fvs) == 0 {
			continue
		}
		for _, fv := range fvs {
			if !usableFinite(fv.Normalized) || fv.Weight <= 0 {
				continue
			}
			fs.values = append(fs.values, fv)
			fs.byName[fv.Name] = fv
		}
	}
	return fs
}

func normalizeOptions(opts Options) Options {
	if len(opts.Horizons) == 0 {
		opts.Horizons = append([]int(nil), defaultHorizons...)
	}
	opts.Horizons = normalizeHorizons(opts.Horizons)
	if opts.TopK <= 0 {
		opts.TopK = defaultTopK
	}
	if opts.StepDays <= 0 {
		opts.StepDays = defaultStepDays
	}
	if opts.DiversifyWindowDays <= 0 {
		opts.DiversifyWindowDays = defaultDiversifyWindowDays
	}
	if opts.MinFeatures <= 0 {
		opts.MinFeatures = minSharedFeatures
	}
	return opts
}

func normalizeHorizons(values []int) []int {
	seen := map[int]struct{}{}
	out := make([]int, 0, len(values))
	for _, v := range values {
		if v <= 0 {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Ints(out)
	if len(out) == 0 {
		return append([]int(nil), defaultHorizons...)
	}
	return out
}

func normalizePoints(points []Point) []Point {
	byDate := make(map[string]Point, len(points))
	for _, p := range points {
		t, err := time.Parse("2006-01-02", strings.TrimSpace(p.Date))
		if err != nil || !usablePositive(p.Close) {
			continue
		}
		p.Date = t.UTC().Format("2006-01-02")
		byDate[p.Date] = p
	}
	out := make([]Point, 0, len(byDate))
	for _, p := range byDate {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

func drawdown(points []Point, i, window int) (float64, bool) {
	if i-window+1 < 0 {
		return 0, false
	}
	maxClose := 0.0
	for j := i - window + 1; j <= i; j++ {
		if points[j].Close > maxClose {
			maxClose = points[j].Close
		}
	}
	if !usablePositive(maxClose) {
		return 0, false
	}
	return points[i].Close/maxClose - 1, true
}

func mayerMultiple(points []Point, i int) (float64, bool) {
	ma, ok := sma(points, i, 200)
	if !ok || !usablePositive(ma) {
		return 0, false
	}
	return points[i].Close / ma, true
}

func sma(points []Point, i, window int) (float64, bool) {
	if window <= 0 || i-window+1 < 0 {
		return 0, false
	}
	sum := 0.0
	for j := i - window + 1; j <= i; j++ {
		if !usablePositive(points[j].Close) {
			return 0, false
		}
		sum += points[j].Close
	}
	return sum / float64(window), true
}

func distance(a, b featureSet) (float64, int, bool) {
	sum := 0.0
	weightSum := 0.0
	matched := 0
	for _, av := range a.values {
		bv, ok := b.byName[av.Name]
		if !ok {
			continue
		}
		diff := av.Normalized - bv.Normalized
		sum += av.Weight * diff * diff
		weightSum += av.Weight
		matched++
	}
	required := minSharedFeatures
	if len(a.values)-1 > required {
		required = len(a.values) - 1
	}
	if matched < required || weightSum <= 0 {
		return 0, matched, false
	}
	return math.Sqrt(sum / weightSum), matched, true
}

// mahalanobisDistance computes distance accounting for feature covariance.
// Uses diagonal approximation of inverse covariance (1/var per feature) for
// computational efficiency. This handles collinearity by down-weighting
// features that have high variance in the candidate pool.
func mahalanobisDistance(a, b featureSet, candidates []candidate) (float64, int, bool) {
	// Compute per-feature variance across candidates
	featVars := make(map[string]float64)
	featMeans := make(map[string]float64)
	featCounts := make(map[string]int)
	for _, c := range candidates {
		for _, fv := range c.features.values {
			featMeans[fv.Name] += fv.Normalized
			featCounts[fv.Name]++
		}
	}
	for name, sum := range featMeans {
		if featCounts[name] > 0 {
			featMeans[name] = sum / float64(featCounts[name])
		}
	}
	for _, c := range candidates {
		for _, fv := range c.features.values {
			diff := fv.Normalized - featMeans[fv.Name]
			featVars[fv.Name] += diff * diff
		}
	}
	for name, sum := range featVars {
		if featCounts[name] > 1 {
			featVars[name] = sum / float64(featCounts[name]-1)
		}
	}

	sum := 0.0
	weightSum := 0.0
	matched := 0
	for _, av := range a.values {
		bv, ok := b.byName[av.Name]
		if !ok {
			continue
		}
		diff := av.Normalized - bv.Normalized
		// Mahalanobis: divide by feature variance (diagonal approx)
		invVar := 1.0
		if v, ok := featVars[av.Name]; ok && v > 0.01 {
			invVar = 1.0 / v
		}
		sum += av.Weight * diff * diff * invVar
		weightSum += av.Weight * invVar
		matched++
	}
	required := minSharedFeatures
	if len(a.values)-1 > required {
		required = len(a.values) - 1
	}
	if matched < required || weightSum <= 0 {
		return 0, matched, false
	}
	return math.Sqrt(sum / weightSum), matched, true
}

func selectAnalogs(candidates []candidate, topK, windowDays int) []candidate {
	if topK <= 0 || topK > len(candidates) {
		topK = len(candidates)
	}
	selected := make([]candidate, 0, topK)
	used := map[int]struct{}{}
	for _, c := range candidates {
		if len(selected) >= topK {
			break
		}
		if tooCloseToSelected(c, selected, windowDays) {
			continue
		}
		selected = append(selected, c)
		used[c.index] = struct{}{}
	}
	for _, c := range candidates {
		if len(selected) >= topK {
			break
		}
		if _, ok := used[c.index]; ok {
			continue
		}
		selected = append(selected, c)
		used[c.index] = struct{}{}
	}
	return selected
}

func tooCloseToSelected(c candidate, selected []candidate, windowDays int) bool {
	if windowDays <= 0 {
		return false
	}
	for _, s := range selected {
		days := math.Abs(c.date.Sub(s.date).Hours() / 24)
		if days < float64(windowDays) {
			return true
		}
	}
	return false
}

func buildAnalogs(points []Point, selected []candidate, horizons []int) []Analog {
	out := make([]Analog, 0, len(selected))
	for _, c := range selected {
		forward := map[string]float64{}
		for _, h := range horizons {
			if c.index+h >= len(points) {
				continue
			}
			ret := points[c.index+h].Close/points[c.index].Close - 1
			forward[strconv.Itoa(h)+"d"] = round2(ret * 100)
		}
		out = append(out, Analog{
			Date:              points[c.index].Date,
			Price:             round2(points[c.index].Close),
			Distance:          round4(c.dist),
			Similarity:        round2(c.sim),
			MatchedFeatures:   c.matched,
			ForwardReturnsPct: forward,
		})
	}
	return out
}

func buildHorizonForecasts(currentPrice float64, points []Point, selected []candidate, horizons []int, currentFeatures featureSet, allCandidates []candidate, opts Options) []HorizonForecast {
	out := make([]HorizonForecast, 0, len(horizons))
	for _, h := range horizons {
		selectedForHorizon := selected
		candidatesForHorizon := allCandidates
		currentForHorizon := currentFeatures
		if !opts.DisableHorizonWeights {
			currentForHorizon = horizonWeightedFeatureSet(currentFeatures, h, opts.Asset)
			ranked := rankCandidatesForHorizon(currentFeatures, allCandidates, h, points, opts)
			if len(ranked) > 0 {
				picked := selectAnalogs(ranked, opts.TopK, opts.DiversifyWindowDays)
				if len(picked) >= minSelectedAnalogs {
					selectedForHorizon = picked
					candidatesForHorizon = ranked
				}
			}
		}

		returns := forwardReturnsForCandidates(points, selectedForHorizon, h)
		if len(returns) == 0 {
			continue
		}
		hf := summarizeHorizon(currentPrice, h, returns)
		calibrationReturns := forwardReturnsForCandidates(points, conformalCalibrationCandidates(candidatesForHorizon, selectedForHorizon, opts.TopK), h)
		if len(calibrationReturns) < len(returns) {
			calibrationReturns = returns
		}
		annotateHorizonConformalForAsset(&hf, calibrationReturns, currentPrice, opts.Asset)
		// G4: ridge regression on the full candidate pool (not just
		// selected top-K) — more samples give the linear model more
		// to fit and better detect when kNN is an outlier view.
		annotateHorizonEnsemble(&hf, currentForHorizon, candidatesForHorizon, h, points)
		out = append(out, hf)
	}
	return out
}

func forwardReturnsForCandidates(points []Point, candidates []candidate, horizonDays int) []float64 {
	returns := make([]float64, 0, len(candidates))
	for _, c := range candidates {
		if c.index+horizonDays >= len(points) {
			continue
		}
		returns = append(returns, points[c.index+horizonDays].Close/points[c.index].Close-1)
	}
	return returns
}

func conformalCalibrationCandidates(ranked []candidate, fallback []candidate, topK int) []candidate {
	if len(ranked) == 0 {
		return fallback
	}
	limit := topK * 4
	if limit <= 0 {
		limit = defaultTopK * 4
	}
	if limit > len(ranked) {
		limit = len(ranked)
	}
	if limit < len(fallback) {
		return fallback
	}
	return ranked[:limit]
}

func rankCandidatesForHorizon(current featureSet, candidates []candidate, horizonDays int, points []Point, opts Options) []candidate {
	if len(candidates) == 0 {
		return nil
	}
	currentWeighted := horizonWeightedFeatureSet(current, horizonDays, opts.Asset)
	out := make([]candidate, 0, len(candidates))
	today := time.Time{}
	cutoff := time.Time{}
	if opts.RecencyWeighted && len(points) > 0 {
		years := opts.RecencyWindowYears
		if years <= 0 {
			years = 5
		}
		today = mustParseDate(points[len(points)-1].Date)
		cutoff = today.AddDate(-years, 0, 0)
	}
	currentRegime := 0
	if opts.RegimeGate && len(points) > 0 {
		currentRegime = regimeBucket(points, len(points)-1)
	}
	for _, c := range candidates {
		cw := c
		dist, matched, ok := distance(currentWeighted, horizonWeightedFeatureSet(c.features, horizonDays, opts.Asset))
		if !ok {
			continue
		}
		if opts.RecencyWeighted && !today.IsZero() && c.date.After(cutoff) {
			dist *= 0.8
		}
		if opts.RegimeGate && c.index >= 0 && c.index < len(points) && regimeBucket(points, c.index) != currentRegime {
			dist *= 1.2
		}
		cw.dist = dist
		cw.sim = similarityFromDistance(dist)
		cw.matched = matched
		out = append(out, cw)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].dist == out[j].dist {
			return out[i].date.Before(out[j].date)
		}
		return out[i].dist < out[j].dist
	})
	return out
}

func horizonWeightedFeatureSet(fs featureSet, horizonDays int, asset string) featureSet {
	out := featureSet{
		values: make([]FeatureValue, 0, len(fs.values)),
		byName: make(map[string]FeatureValue, len(fs.byName)),
	}
	for _, fv := range fs.values {
		fv.Weight *= horizonWeightMultiplier(asset, fv.Name, horizonDays)
		out.values = append(out.values, fv)
		out.byName[fv.Name] = fv
	}
	return out
}

func horizonWeightMultiplier(asset, name string, horizonDays int) float64 {
	return assetprofile.HorizonWeightMultiplier(asset, name, horizonDays)
}

func summarizeHorizon(currentPrice float64, days int, returns []float64) HorizonForecast {
	sort.Float64s(returns)
	avg := average(returns)
	median := quantile(returns, 0.50)
	p10 := quantile(returns, 0.10)
	p25 := quantile(returns, 0.25)
	p75 := quantile(returns, 0.75)
	p90 := quantile(returns, 0.90)
	upThresh, downThresh := scenarioThresholds(days)

	up := 0
	upside := 0
	downside := 0
	for _, r := range returns {
		if r > 0 {
			up++
		}
		if r*100 >= upThresh {
			upside++
		}
		if r*100 <= -downThresh {
			downside++
		}
	}
	n := len(returns)
	pUpside := float64(upside) / float64(n)
	pDownside := float64(downside) / float64(n)
	pRange := 1 - pUpside - pDownside
	if pRange < 0 {
		pRange = 0
	}
	scenario, label := dominantScenario(pUpside, pRange, pDownside)
	return HorizonForecast{
		Days:                          days,
		SampleSize:                    n,
		AverageReturnPct:              round2(avg * 100),
		MedianReturnPct:               round2(median * 100),
		P10ReturnPct:                  round2(p10 * 100),
		P25ReturnPct:                  round2(p25 * 100),
		P75ReturnPct:                  round2(p75 * 100),
		P90ReturnPct:                  round2(p90 * 100),
		ProbabilityUp:                 round4(float64(up) / float64(n)),
		ProbabilityUpsideContinuation: round4(pUpside),
		ProbabilityRange:              round4(pRange),
		ProbabilityDownsidePressure:   round4(pDownside),
		ExpectedPrice:                 round2(currentPrice * (1 + avg)),
		MedianPrice:                   round2(currentPrice * (1 + median)),
		P10Price:                      round2(currentPrice * (1 + p10)),
		P90Price:                      round2(currentPrice * (1 + p90)),
		DominantScenario:              scenario,
		DominantLabel:                 label,
		UpsideThresholdPct:            upThresh,
		DownsideThresholdPct:          -downThresh,
	}
}

func scenarioThresholds(days int) (float64, float64) {
	switch {
	case days <= 45:
		return 8, 8
	case days <= 120:
		return 15, 15
	default:
		return 25, 25
	}
}

func dominantScenario(upside, rangeProb, downside float64) (string, string) {
	if upside >= rangeProb && upside >= downside {
		return "upside_continuation", "上行延续"
	}
	if downside >= upside && downside >= rangeProb {
		return "downside_pressure", "下行压力"
	}
	return "range", "区间震荡"
}

func confidenceLabel(featureCoverage, avgSimilarity float64, selected, topK int) string {
	sampleRatio := 0.0
	if topK > 0 {
		sampleRatio = math.Min(1, float64(selected)/float64(topK))
	}
	score := 0.45*clamp01(featureCoverage) + 0.35*clamp01(avgSimilarity/100) + 0.20*sampleRatio
	switch {
	case score >= 0.75:
		return "高"
	case score >= 0.55:
		return "中"
	default:
		return "低"
	}
}

func averageSimilarity(selected []candidate) float64 {
	if len(selected) == 0 {
		return 0
	}
	sum := 0.0
	for _, c := range selected {
		sum += c.sim
	}
	return round2(sum / float64(len(selected)))
}

func similarityFromDistance(dist float64) float64 {
	return clamp01(1-dist/2) * 100
}

func parseSnapshotBTCDate(snap *model.MarketSnapshot) (time.Time, error) {
	for _, raw := range []string{snap.BTCPriceAsOf, snap.Date.Format(time.RFC3339), snap.Date.Format("2006-01-02")} {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			return t.UTC().Truncate(24 * time.Hour), nil
		}
		if t, err := time.Parse("2006-01-02", raw); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("snapshot has no parseable BTC date")
}

func mustParseDate(date string) time.Time {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func average(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if q <= 0 {
		return sorted[0]
	}
	if q >= 1 {
		return sorted[len(sorted)-1]
	}
	pos := q * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	frac := pos - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

func usablePositive(v float64) bool {
	return v > 0 && usableFinite(v)
}

func usableFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func round4(v float64) float64 {
	return math.Round(v*10000) / 10000
}

func maxInt(values []int) int {
	max := 0
	for _, v := range values {
		if v > max {
			max = v
		}
	}
	return max
}
