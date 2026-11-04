// Package envelope produces the standard response wrapper used by the data
// gateway, matching the Python services/data-gateway models.wrap() shape so the
// HTTP contract (and the data-access facade) is unchanged across the migration:
//
//	{provider, endpoint, params, retrieved_at, raw_hash, data}
package envelope

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// Envelope is the standard wrapper. Field order/names mirror the Python wrap().
type Envelope struct {
	Provider    string         `json:"provider"`
	Endpoint    string         `json:"endpoint"`
	Params      map[string]any `json:"params"`
	RetrievedAt string         `json:"retrieved_at"`
	RawHash     string         `json:"raw_hash"`
	Data        any            `json:"data"`
}

// Wrap builds an Envelope. raw_hash is SHA-256 over the exact encoding/json
// representation used for the data wire token. Strict cross-language clients
// validate that token directly instead of re-serializing it in another runtime.
func Wrap(provider, endpoint string, params map[string]any, data any) Envelope {
	if params == nil {
		params = map[string]any{}
	}
	return Envelope{
		Provider:    provider,
		Endpoint:    endpoint,
		Params:      params,
		RetrievedAt: UTCNow(),
		RawHash:     rawHash(data),
		Data:        data,
	}
}

// UTCNow matches the Python utc_now() format ("2006-01-02T15:04:05Z").
func UTCNow() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05Z")
}

func rawHash(data any) string {
	b, err := json.Marshal(data) // Go sorts map keys; struct fields keep order
	if err != nil {
		b = []byte{}
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}
