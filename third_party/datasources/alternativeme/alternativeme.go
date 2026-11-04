// Package alternativeme is the independent alternative.me data source: the
// crypto Fear & Greed Index. Fetch-only and stateless; no API key.
package alternativeme

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/Ricaardo/nimbus-os/datasources/internal/httpx"
)

// BaseURL is the API host. It is a var (not const) so tests can point it at an
// httptest server.
var BaseURL = "https://api.alternative.me"

// FearGreed is one Fear & Greed Index reading. JSON tags give the gateway a
// clean lowercase wire contract.
type FearGreed struct {
	Value          int       `json:"value"`          // 0-100; 0 if the upstream value was unparseable
	Classification string    `json:"classification"` // e.g. "Fear", "Greed", "Extreme Greed"
	Timestamp      time.Time `json:"timestamp"`      // zero if upstream timestamp was missing/invalid
}

// FearGreedIndex returns up to limit readings, newest-first (the endpoint's
// native order). Empty response → (nil, nil).
func FearGreedIndex(ctx context.Context, limit int) ([]FearGreed, error) {
	url := fmt.Sprintf("%s/fng/?limit=%d", BaseURL, limit)
	var resp struct {
		Data []struct {
			Value          string `json:"value"`
			Classification string `json:"value_classification"`
			Timestamp      string `json:"timestamp"`
		} `json:"data"`
	}
	if err := httpx.Default.GetJSON(ctx, url, nil, &resp); err != nil {
		return nil, err
	}
	if len(resp.Data) == 0 {
		return nil, nil
	}
	out := make([]FearGreed, 0, len(resp.Data))
	for _, d := range resp.Data {
		fg := FearGreed{Classification: d.Classification}
		// Lenient: a bad value leaves 0, matching the upstream's loose schema.
		fg.Value, _ = strconv.Atoi(d.Value)
		if ts, err := strconv.ParseInt(d.Timestamp, 10, 64); err == nil && ts > 0 {
			fg.Timestamp = time.Unix(ts, 0).UTC()
		}
		out = append(out, fg)
	}
	return out, nil
}
