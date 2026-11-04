// Bridge between pkg/forecast.Forecast and Claim records. Lives in
// pkg/claim so pkg/forecast stays free of claim dependencies — only
// CLI / MCP call this after a forecast is produced.

package claim

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast"
)

// EmitFromForecast produces one Claim per HorizonForecast. The caller is
// responsible for asset identity (per-asset BuildForecast knows it; the
// forecast struct itself doesn't carry that back here).
//
// panelJSON is the source panel bytes — used as the evidence hash so a
// future calibrator can verify the same snapshot. May be nil if unknown;
// the claim will have an empty SourceSnapshotSHA in that case.
func EmitFromForecast(fc *forecast.Forecast, asset string, panelJSON []byte) []Claim {
	if fc == nil {
		return nil
	}
	asOf, err := time.Parse("2006-01-02", fc.Date)
	if err != nil {
		asOf = time.Now().UTC()
	}
	sha := ""
	if len(panelJSON) > 0 {
		h := sha256.Sum256(panelJSON)
		sha = hex.EncodeToString(h[:])
	}
	analogs := topAnalogRefs(fc.Analogs, 3)

	out := make([]Claim, 0, len(fc.Horizons))
	for _, h := range fc.Horizons {
		out = append(out, Claim{
			ID:                NewID(asOf),
			Asset:             asset,
			AsOf:              asOf,
			Horizon:           h.Days,
			PriceAtClaim:      fc.CurrentPrice,
			IntervalLow:       h.P10ReturnPct / 100.0, // store as decimal, not pct
			IntervalHigh:      h.P90ReturnPct / 100.0,
			ExpectedReturn:    h.MedianReturnPct / 100.0,
			ProbabilityUp:     h.ProbabilityUp,
			HardBlocked:       h.HardBlocked,
			ReliabilityNote:   h.ReliabilityNote,
			SourceSnapshotSHA: sha,
			FeatureCoverage:   fc.Coverage.FeatureCoverage,
			AnalogCount:       fc.Coverage.SelectedAnalogs,
			DominantAnalogs:   analogs,
			Method:            fc.Method,
			SchemaVersion:     SchemaVersion,
		})
	}
	return out
}

// RecordForecast is the fire-and-forget convenience used by CLI / MCP:
// emit claims from a forecast, write them to the ledger, return the count
// written. Honors GUANFU_NO_CLAIMS=1 (returns 0 silently).
//
// Non-fatal: any single record failure is logged via warnOut and does not
// abort the remaining records.
func (l *Ledger) RecordForecast(fc *forecast.Forecast, asset string, panelJSON []byte) int {
	if l == nil || Disabled() {
		return 0
	}
	claims := EmitFromForecast(fc, asset, panelJSON)
	n := 0
	for _, c := range claims {
		if _, err := l.RecordClaim(c); err != nil {
			warnOut("record claim %s/%dd: %v\n", asset, c.Horizon, err)
			continue
		}
		n++
	}
	return n
}

// topAnalogRefs takes the first k analogs (already sorted by similarity
// in forecast.Build) and compresses them to AnalogRef.
func topAnalogRefs(analogs []forecast.Analog, k int) []AnalogRef {
	if k > len(analogs) {
		k = len(analogs)
	}
	out := make([]AnalogRef, 0, k)
	for i := 0; i < k; i++ {
		a := analogs[i]
		out = append(out, AnalogRef{
			Date:       a.Date,
			Price:      a.Price,
			Similarity: a.Similarity,
		})
	}
	return out
}

// PanelJSONHash is a small helper for callers that already have a panel
// and want the SHA the ledger would record. Exposed for tests and for
// calibration tools.
func PanelJSONHash(v any) (string, []byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", nil, err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), b, nil
}
