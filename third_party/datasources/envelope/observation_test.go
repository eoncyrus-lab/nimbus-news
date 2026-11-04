package envelope

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type quoteObservation struct {
	Price float64 `json:"price"`
}

type pointerMarshaledPayload struct {
	Value string `json:"value"`
}

func (p *pointerMarshaledPayload) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"pointer_value": p.Value})
}

func TestObservationEnvelopeVersionAndStatusJSON(t *testing.T) {
	observation, err := NewSuccess("yahoo", "chart.quote", map[string]any{"symbol": "US:AAPL"}, map[string]any{"price": 210.5})
	if err != nil {
		t.Fatalf("NewSuccess() error = %v", err)
	}

	encoded, err := json.Marshal(observation)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got["version"] != float64(ObservationEnvelopeVersion) {
		t.Fatalf("version = %v, want %d", got["version"], ObservationEnvelopeVersion)
	}
	if got["status"] != string(ObservationStatusOK) {
		t.Fatalf("status = %v, want %q", got["status"], ObservationStatusOK)
	}
	if got["error"] != nil {
		t.Fatalf("error = %v, want null", got["error"])
	}
}

func TestObservationEnvelopeEmpty(t *testing.T) {
	observation, err := NewEmpty("finra", "regsho.daily_short_volume", nil, []any{})
	if err != nil {
		t.Fatalf("NewEmpty() error = %v", err)
	}
	if observation.Status != ObservationStatusEmpty {
		t.Fatalf("status = %q, want %q", observation.Status, ObservationStatusEmpty)
	}
	if err := observation.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	encoded, err := json.Marshal(observation)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var got struct {
		Data []any `json:"data"`
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got.Data == nil || len(got.Data) != 0 {
		t.Fatalf("data = %#v, want a non-null empty collection", got.Data)
	}
}

func TestObservationEnvelopeDependencyFailureCannotBecomeLegacySuccess(t *testing.T) {
	wantError := ObservationError{
		Code:       "dependency_unavailable",
		Message:    "Futu OpenD is unavailable",
		Dependency: "futu-opend",
		Retryable:  true,
	}
	observation, err := NewFailure[quoteObservation]("futu", "quote", map[string]any{"symbol": "US:AAPL"}, wantError)
	if err != nil {
		t.Fatalf("NewFailure() error = %v", err)
	}
	if observation.Status != ObservationStatusError {
		t.Fatalf("status = %q, want %q", observation.Status, ObservationStatusError)
	}
	var typedObservation ObservationEnvelope[quoteObservation] = observation
	_ = typedObservation

	legacy, err := observation.ToLegacy()
	if err == nil {
		t.Fatal("ToLegacy() error = nil, want dependency error")
	}
	if !reflect.DeepEqual(legacy, Envelope{}) {
		t.Fatalf("ToLegacy() envelope = %#v, want zero value", legacy)
	}
	var gotError *ObservationError
	if !errors.As(err, &gotError) {
		t.Fatalf("ToLegacy() error type = %T, want *ObservationError", err)
	}
	if !reflect.DeepEqual(*gotError, wantError) {
		t.Fatalf("ToLegacy() error = %#v, want %#v", *gotError, wantError)
	}
}

func TestObservationEnvelopeDegradedHasDataAndError(t *testing.T) {
	data := map[string]any{"price": 209.0, "stale": true}
	observation, err := NewDegraded("yahoo", "chart.quote", nil, data, ObservationError{
		Code:       "stale_fallback",
		Message:    "serving the last cached quote",
		Dependency: "query1.finance.yahoo.com",
		Retryable:  true,
	})
	if err != nil {
		t.Fatalf("NewDegraded() error = %v", err)
	}
	if observation.Error == nil || observation.Data == nil {
		t.Fatalf("degraded observation must contain both data and error: %#v", observation)
	}

	legacy, err := observation.ToLegacy()
	if err != nil {
		t.Fatalf("ToLegacy() error = %v", err)
	}
	if !reflect.DeepEqual(legacy.Data, data) {
		t.Fatalf("legacy data = %#v, want %#v", legacy.Data, data)
	}
}

func TestObservationEnvelopeValidateRejectsInvalidCombinations(t *testing.T) {
	success, err := NewSuccess("yahoo", "chart.quote", nil, map[string]any{"price": 210.5})
	if err != nil {
		t.Fatalf("NewSuccess() error = %v", err)
	}
	failure, err := NewFailure[map[string]any]("yahoo", "chart.quote", nil, ObservationError{Code: "upstream", Message: "request failed", Retryable: true})
	if err != nil {
		t.Fatalf("NewFailure() error = %v", err)
	}

	tests := []struct {
		name        string
		observation ObservationEnvelope[map[string]any]
	}{
		{
			name: "wrong version",
			observation: func() ObservationEnvelope[map[string]any] {
				got := success
				got.Version = 2
				return got
			}(),
		},
		{
			name: "unknown status",
			observation: func() ObservationEnvelope[map[string]any] {
				got := success
				got.Status = "unknown"
				return got
			}(),
		},
		{
			name: "success with error",
			observation: func() ObservationEnvelope[map[string]any] {
				got := success
				got.Error = &ObservationError{Code: "unexpected", Message: "unexpected error"}
				return got
			}(),
		},
		{
			name: "empty without data",
			observation: func() ObservationEnvelope[map[string]any] {
				got := success
				got.Status = ObservationStatusEmpty
				got.Data = nil
				return got
			}(),
		},
		{
			name: "degraded without error",
			observation: func() ObservationEnvelope[map[string]any] {
				got := success
				got.Status = ObservationStatusDegraded
				return got
			}(),
		},
		{
			name: "error with data",
			observation: ObservationEnvelope[map[string]any]{
				Version:     failure.Version,
				Status:      failure.Status,
				Provider:    failure.Provider,
				Endpoint:    failure.Endpoint,
				Params:      failure.Params,
				RetrievedAt: failure.RetrievedAt,
				RawHash:     success.RawHash,
				Data:        success.Data,
				Error:       failure.Error,
			},
		},
		{
			name: "error without structured error",
			observation: ObservationEnvelope[map[string]any]{
				Version:     failure.Version,
				Status:      failure.Status,
				Provider:    failure.Provider,
				Endpoint:    failure.Endpoint,
				Params:      failure.Params,
				RetrievedAt: failure.RetrievedAt,
				RawHash:     failure.RawHash,
			},
		},
		{
			name: "error without code",
			observation: ObservationEnvelope[map[string]any]{
				Version:     failure.Version,
				Status:      failure.Status,
				Provider:    failure.Provider,
				Endpoint:    failure.Endpoint,
				Params:      failure.Params,
				RetrievedAt: failure.RetrievedAt,
				RawHash:     failure.RawHash,
				Error:       &ObservationError{Message: "request failed"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.observation.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want invalid combination to be rejected")
			}
		})
	}
}

func TestObservationEnvelopeLegacyAdapterPreservesFields(t *testing.T) {
	data := []map[string]any{{"symbol": "US:AAPL", "price": 210.5}}
	observation, err := NewSuccess("yahoo", "chart.quote", map[string]any{"symbol": "US:AAPL"}, data)
	if err != nil {
		t.Fatalf("NewSuccess() error = %v", err)
	}
	legacy, err := observation.ToLegacy()
	if err != nil {
		t.Fatalf("ToLegacy() error = %v", err)
	}

	if legacy.Provider != observation.Provider || legacy.Endpoint != observation.Endpoint ||
		legacy.RetrievedAt != observation.RetrievedAt || legacy.RawHash != observation.RawHash ||
		!reflect.DeepEqual(legacy.Params, observation.Params) || !reflect.DeepEqual(legacy.Data, data) {
		t.Fatalf("legacy fields changed: %#v; observation = %#v", legacy, observation)
	}
}

func TestDecodeObservationEnvelopeRejectsWireContractViolations(t *testing.T) {
	success, err := NewSuccess("yahoo", "chart.quote", nil, quoteObservation{Price: 210.5})
	if err != nil {
		t.Fatalf("NewSuccess() error = %v", err)
	}
	failure, err := NewFailure[quoteObservation]("yahoo", "chart.quote", nil, ObservationError{
		Code:      "upstream",
		Message:   "request failed",
		Retryable: false,
	})
	if err != nil {
		t.Fatalf("NewFailure() error = %v", err)
	}
	degraded, err := NewDegraded("yahoo", "chart.quote", nil, quoteObservation{Price: 209.0}, ObservationError{
		Code:      "stale_fallback",
		Message:   "serving cached data",
		Retryable: true,
	})
	if err != nil {
		t.Fatalf("NewDegraded() error = %v", err)
	}
	successJSON := marshalTestJSON(t, success)
	failureJSON := marshalTestJSON(t, failure)
	degradedJSON := marshalTestJSON(t, degraded)
	emptyJSON := withJSONField(t, successJSON, "status", ObservationStatusEmpty)

	for name, payload := range map[string][]byte{
		"valid success":                      successJSON,
		"valid failure":                      failureJSON,
		"schema-valid whitespace dependency": withNestedErrorField(t, failureJSON, "dependency", " "),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeObservationEnvelope[quoteObservation](payload); err != nil {
				t.Fatalf("DecodeObservationEnvelope() error = %v", err)
			}
			var got ObservationEnvelope[quoteObservation]
			if err := json.Unmarshal(payload, &got); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
		})
	}

	tests := []struct {
		name    string
		payload []byte
	}{
		{name: "missing required error", payload: withoutJSONField(t, successJSON, "error")},
		{name: "missing required data", payload: withoutJSONField(t, failureJSON, "data")},
		{name: "missing required retryable", payload: withoutNestedErrorField(t, failureJSON, "retryable")},
		{name: "unknown top-level field", payload: withJSONField(t, successJSON, "unknown", true)},
		{name: "unknown error field", payload: withNestedErrorField(t, failureJSON, "unknown", true)},
		{name: "trailing JSON value", payload: append(append([]byte{}, successJSON...), []byte(` {}`)...)},
		{name: "null required provider", payload: withJSONField(t, successJSON, "provider", nil)},
		{name: "null required endpoint", payload: withJSONField(t, successJSON, "endpoint", nil)},
		{name: "ok with null data", payload: withJSONField(t, successJSON, "data", nil)},
		{name: "empty with null data", payload: withJSONField(t, emptyJSON, "data", nil)},
		{name: "degraded with null data", payload: withJSONField(t, degradedJSON, "data", nil)},
		{name: "degraded with null error", payload: withJSONField(t, degradedJSON, "error", nil)},
		{name: "error with null error", payload: withJSONField(t, failureJSON, "error", nil)},
		{name: "error with null retryable", payload: withNestedErrorField(t, failureJSON, "retryable", nil)},
		{name: "error with empty dependency", payload: withNestedErrorField(t, failureJSON, "dependency", "")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := DecodeObservationEnvelope[quoteObservation](tt.payload); err == nil {
				t.Fatal("DecodeObservationEnvelope() error = nil, want wire contract violation")
			}
			var got ObservationEnvelope[quoteObservation]
			if err := json.Unmarshal(tt.payload, &got); err == nil {
				t.Fatal("json.Unmarshal() error = nil, want wire contract violation")
			}
		})
	}
}

func TestDecodeObservationEnvelopeRejectsDuplicateKeysRecursively(t *testing.T) {
	success, err := NewSuccess(
		"yahoo", "chart.quote", map[string]any{"symbol": "US:AAPL"},
		map[string]any{"outer": map[string]any{"value": 1}},
	)
	if err != nil {
		t.Fatal(err)
	}
	failure, err := NewFailure[map[string]any]("yahoo", "chart.quote", nil, ObservationError{
		Code: "upstream", Message: "request failed", Retryable: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	successJSON := marshalTestJSON(t, success)
	failureJSON := marshalTestJSON(t, failure)
	tests := []struct {
		name    string
		payload []byte
	}{
		{
			name: "top-level data",
			payload: duplicateJSONFragment(t, successJSON,
				`"data":{"outer":{"value":1}}`,
				`"data":{"outer":{"value":1}},"data":{"outer":{"value":1}}`),
		},
		{
			name: "top-level status",
			payload: duplicateJSONFragment(t, successJSON,
				`"status":"ok"`, `"status":"ok","status":"ok"`),
		},
		{
			name: "nested error code",
			payload: duplicateJSONFragment(t, failureJSON,
				`"code":"upstream"`, `"code":"upstream","code":"upstream"`),
		},
		{
			name: "params member",
			payload: duplicateJSONFragment(t, successJSON,
				`"params":{"symbol":"US:AAPL"}`,
				`"params":{"symbol":"US:AAPL","symbol":"US:AAPL"}`),
		},
		{
			name: "nested data member",
			payload: duplicateJSONFragment(t, successJSON,
				`"outer":{"value":1}`, `"outer":{"value":1,"value":1}`),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := DecodeObservationEnvelope[map[string]any](test.payload); err == nil ||
				!strings.Contains(err.Error(), "duplicate object key") {
				t.Fatalf("DecodeObservationEnvelope error=%v", err)
			}
			var observation ObservationEnvelope[map[string]any]
			if err := json.Unmarshal(test.payload, &observation); err == nil ||
				!strings.Contains(err.Error(), "duplicate object key") {
				t.Fatalf("json.Unmarshal error=%v", err)
			}
		})
	}
}

func TestDecodeObservationEnvelopeAllowsArraysAndEscapedKeys(t *testing.T) {
	data := map[string]any{
		"items":     []any{map[string]any{"a": float64(1)}, map[string]any{"b": float64(2)}},
		`quote"key`: "value",
	}
	observation, err := NewSuccess(
		"golden", "escaped", map[string]any{`param"key`: "value"}, data,
	)
	if err != nil {
		t.Fatal(err)
	}
	payload := marshalTestJSON(t, observation)
	decoded, err := DecodeObservationEnvelope[map[string]any](payload)
	if err != nil {
		t.Fatalf("DecodeObservationEnvelope: %v", err)
	}
	if decoded.Data == nil || !reflect.DeepEqual(*decoded.Data, data) {
		t.Fatalf("decoded data=%#v want=%#v", decoded.Data, data)
	}
	var viaJSON ObservationEnvelope[map[string]any]
	if err := json.Unmarshal(payload, &viaJSON); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
}

func TestDecodeObservationEnvelopeRejectsNonFiniteNumbers(t *testing.T) {
	tests := []string{
		`{"version":1,"status":"ok","provider":"stub","endpoint":"hash","params":{},"retrieved_at":"2026-07-20T08:00:00Z","raw_hash":"sha256:0000000000000000000000000000000000000000000000000000000000000000","data":NaN,"error":null}`,
		`{"version":1,"status":"ok","provider":"stub","endpoint":"hash","params":{"value":Infinity},"retrieved_at":"2026-07-20T08:00:00Z","raw_hash":"sha256:0000000000000000000000000000000000000000000000000000000000000000","data":[],"error":null}`,
		`{"version":1,"status":"error","provider":"stub","endpoint":"hash","params":{},"retrieved_at":"2026-07-20T08:00:00Z","raw_hash":"sha256:74234e98afe7498fb5daf1f36ac2d78acc339464f950703b8c019892f982b90b","data":null,"error":{"code":"failed","message":"bad","retryable":-Infinity}}`,
		`{"version":1,"status":"ok","provider":"stub","endpoint":"hash","params":{},"retrieved_at":"2026-07-20T08:00:00Z","raw_hash":"sha256:0000000000000000000000000000000000000000000000000000000000000000","data":1e999,"error":null}`,
	}
	for _, payload := range tests {
		t.Run(payload, func(t *testing.T) {
			if _, err := DecodeObservationEnvelope[any]([]byte(payload)); err == nil {
				t.Fatal("DecodeObservationEnvelope accepted non-finite number")
			}
			var observation ObservationEnvelope[any]
			if err := json.Unmarshal([]byte(payload), &observation); err == nil {
				t.Fatal("json.Unmarshal accepted non-finite number")
			}
		})
	}
}

func duplicateJSONFragment(t *testing.T, payload []byte, old, replacement string) []byte {
	t.Helper()
	if count := bytes.Count(payload, []byte(old)); count != 1 {
		t.Fatalf("fragment %q occurs %d times in %s", old, count, payload)
	}
	return bytes.Replace(payload, []byte(old), []byte(replacement), 1)
}

func TestObservationErrorRawHashMatchesSchemaConstant(t *testing.T) {
	if got := rawHash(nil); got != ObservationNullDataRawHash {
		t.Fatalf("rawHash(nil) = %q, want schema constant %q", got, ObservationNullDataRawHash)
	}
	observation, err := NewFailure[quoteObservation]("yahoo", "chart.quote", nil, ObservationError{
		Code:      "upstream",
		Message:   "request failed",
		Retryable: true,
	})
	if err != nil {
		t.Fatalf("NewFailure() error = %v", err)
	}
	observation.RawHash = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	if err := observation.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want non-null raw_hash rejected for error status")
	}
}

func TestObservationHashCrossLanguageGolden(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("..", "testdata", "observation_hash_golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name     string `json:"name"`
		DataJSON string `json:"data_json"`
		RawHash  string `json:"raw_hash"`
	}
	if err := json.Unmarshal(payload, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			wirePayload := []byte(fmt.Sprintf(
				`{"version":1,"status":"ok","provider":"golden","endpoint":"hash","params":{},"retrieved_at":"2026-07-20T08:00:00Z","raw_hash":%q,"data":%s,"error":null}`,
				fixture.RawHash, fixture.DataJSON,
			))
			observation, err := DecodeObservationEnvelope[any](wirePayload)
			if err != nil {
				t.Fatalf("DecodeObservationEnvelope: %v", err)
			}
			if observation.RawHash != fixture.RawHash {
				t.Fatalf("decoded raw_hash=%q want=%q", observation.RawHash, fixture.RawHash)
			}
			var viaJSON ObservationEnvelope[any]
			if err := json.Unmarshal(wirePayload, &viaJSON); err != nil {
				t.Fatalf("json.Unmarshal: %v", err)
			}

			remarshaled := marshalTestJSON(t, observation)
			assertWireDataHash(t, remarshaled)
			canonicalData := marshalTestJSON(t, *observation.Data)
			if got := envelopeDataJSON(t, remarshaled); !bytes.Equal(got, canonicalData) {
				t.Fatalf("remarshaled data=%s want canonical token=%s", got, canonicalData)
			}
			if _, err := DecodeObservationEnvelope[any](remarshaled); err != nil {
				t.Fatalf("decode remarshaled observation: %v", err)
			}
			if observation.RawHash != fixture.RawHash {
				t.Fatalf("marshal mutated received raw_hash to %q", observation.RawHash)
			}

			legacy, err := observation.ToLegacy()
			if err != nil {
				t.Fatalf("ToLegacy: %v", err)
			}
			legacyJSON := marshalTestJSON(t, legacy)
			assertWireDataHash(t, legacyJSON)
			if got := envelopeDataJSON(t, legacyJSON); !bytes.Equal(got, canonicalData) {
				t.Fatalf("legacy data=%s want canonical token=%s", got, canonicalData)
			}

			wrongHash := bytes.Replace(wirePayload, []byte(fixture.RawHash), []byte(ObservationNullDataRawHash), 1)
			if _, err := DecodeObservationEnvelope[any](wrongHash); err == nil {
				t.Fatal("DecodeObservationEnvelope accepted wrong raw_hash")
			}
			if err := json.Unmarshal(wrongHash, &viaJSON); err == nil {
				t.Fatal("json.Unmarshal accepted wrong raw_hash")
			}
		})
	}
}

func assertWireDataHash(t *testing.T, payload []byte) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	var hash string
	if err := json.Unmarshal(fields["raw_hash"], &hash); err != nil {
		t.Fatal(err)
	}
	if want := rawHashJSON(fields["data"]); hash != want {
		t.Fatalf("wire raw_hash=%q want=%q for data=%s", hash, want, fields["data"])
	}
}

func TestPointerReceiverMarshalerMatchesLegacyWrap(t *testing.T) {
	t.Run("value input does not invoke pointer receiver", func(t *testing.T) {
		data := pointerMarshaledPayload{Value: "quote"}
		baseline := Wrap("custom", "pointer-marshaler", nil, data)
		observation, err := NewSuccess("custom", "pointer-marshaler", nil, data)
		if err != nil {
			t.Fatalf("NewSuccess() error = %v", err)
		}
		legacy, err := observation.ToLegacy()
		if err != nil {
			t.Fatalf("ToLegacy() error = %v", err)
		}

		if !reflect.DeepEqual(legacy.Data, data) {
			t.Fatalf("legacy data = %#v, want original value %#v", legacy.Data, data)
		}
		assertEnvelopeDataAndHashMatch(t, observation, legacy, baseline, `{"value":"quote"}`)
	})

	t.Run("pointer input invokes pointer receiver", func(t *testing.T) {
		data := &pointerMarshaledPayload{Value: "quote"}
		baseline := Wrap("custom", "pointer-marshaler", nil, data)
		observation, err := NewSuccess("custom", "pointer-marshaler", nil, data)
		if err != nil {
			t.Fatalf("NewSuccess() error = %v", err)
		}
		legacy, err := observation.ToLegacy()
		if err != nil {
			t.Fatalf("ToLegacy() error = %v", err)
		}

		if legacy.Data != data {
			t.Fatalf("legacy data = %#v, want original pointer %#v", legacy.Data, data)
		}
		assertEnvelopeDataAndHashMatch(t, observation, legacy, baseline, `{"pointer_value":"quote"}`)
	})
}

func assertEnvelopeDataAndHashMatch[T any](t *testing.T, observation ObservationEnvelope[T], adapted, baseline Envelope, wantData string) {
	t.Helper()
	observationDataJSON := envelopeDataJSON(t, marshalTestJSON(t, observation))
	adaptedDataJSON := envelopeDataJSON(t, marshalTestJSON(t, adapted))
	baselineDataJSON := envelopeDataJSON(t, marshalTestJSON(t, baseline))
	if !bytes.Equal(observationDataJSON, baselineDataJSON) || !bytes.Equal(adaptedDataJSON, baselineDataJSON) {
		t.Fatalf("data JSON mismatch: observation=%s adapted=%s baseline=%s", observationDataJSON, adaptedDataJSON, baselineDataJSON)
	}
	if observation.RawHash != baseline.RawHash || adapted.RawHash != baseline.RawHash {
		t.Fatalf("raw_hash mismatch: observation=%q adapted=%q baseline=%q", observation.RawHash, adapted.RawHash, baseline.RawHash)
	}
	if string(baselineDataJSON) != wantData {
		t.Fatalf("baseline data JSON = %s, want %s", baselineDataJSON, wantData)
	}
}

func marshalTestJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return encoded
}

func withoutJSONField(t *testing.T, payload []byte, field string) []byte {
	t.Helper()
	var object map[string]any
	if err := json.Unmarshal(payload, &object); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	delete(object, field)
	return marshalTestJSON(t, object)
}

func withJSONField(t *testing.T, payload []byte, field string, value any) []byte {
	t.Helper()
	var object map[string]any
	if err := json.Unmarshal(payload, &object); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	object[field] = value
	return marshalTestJSON(t, object)
}

func withoutNestedErrorField(t *testing.T, payload []byte, field string) []byte {
	t.Helper()
	var object map[string]any
	if err := json.Unmarshal(payload, &object); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	structuredError := object["error"].(map[string]any)
	delete(structuredError, field)
	return marshalTestJSON(t, object)
}

func withNestedErrorField(t *testing.T, payload []byte, field string, value any) []byte {
	t.Helper()
	var object map[string]any
	if err := json.Unmarshal(payload, &object); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	structuredError := object["error"].(map[string]any)
	structuredError[field] = value
	return marshalTestJSON(t, object)
}

func envelopeDataJSON(t *testing.T, payload []byte) json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	return object["data"]
}

func TestLegacyEnvelopeJSONGolden(t *testing.T) {
	legacy := Envelope{
		Provider:    "yahoo",
		Endpoint:    "chart.quote",
		Params:      map[string]any{"symbol": "US:AAPL"},
		RetrievedAt: "2026-07-20T12:00:00Z",
		RawHash:     "sha256:e65f231ed375a72ea7e5671fe0a477c7fb4bf707653544cb83182cdb84f9bc60",
		Data:        map[string]any{"price": 210.5},
	}
	encoded, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	const want = `{"provider":"yahoo","endpoint":"chart.quote","params":{"symbol":"US:AAPL"},"retrieved_at":"2026-07-20T12:00:00Z","raw_hash":"sha256:e65f231ed375a72ea7e5671fe0a477c7fb4bf707653544cb83182cdb84f9bc60","data":{"price":210.5}}`
	if string(encoded) != want {
		t.Fatalf("legacy JSON = %s, want %s", encoded, want)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	for _, forbidden := range []string{"version", "status", "error"} {
		if _, ok := fields[forbidden]; ok {
			t.Fatalf("legacy JSON unexpectedly contains %q", forbidden)
		}
	}
}
