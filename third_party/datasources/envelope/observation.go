package envelope

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
)

const ObservationEnvelopeVersion = 1

const ObservationNullDataRawHash = "sha256:74234e98afe7498fb5daf1f36ac2d78acc339464f950703b8c019892f982b90b"

// ObservationStatus describes the outcome of an observation explicitly. It is
// never inferred from the payload because empty collections can be valid data.
type ObservationStatus string

const (
	ObservationStatusOK       ObservationStatus = "ok"
	ObservationStatusEmpty    ObservationStatus = "empty"
	ObservationStatusDegraded ObservationStatus = "degraded"
	ObservationStatusError    ObservationStatus = "error"
)

// ObservationError is the structured error carried by degraded and failed
// observations. Retryable is intentionally not optional in the JSON contract.
type ObservationError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Dependency string `json:"dependency,omitempty"`
	Retryable  bool   `json:"retryable"`
}

func (e *ObservationError) Error() string {
	if e.Dependency != "" {
		return fmt.Sprintf("%s: %s (dependency: %s)", e.Code, e.Message, e.Dependency)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// ObservationEnvelope is the versioned data-plane response contract. Data is
// a pointer so presence is explicit and validation does not depend on the
// unreliable nil semantics of interface values.
type ObservationEnvelope[T any] struct {
	Version     int               `json:"version"`
	Status      ObservationStatus `json:"status"`
	Provider    string            `json:"provider"`
	Endpoint    string            `json:"endpoint"`
	Params      map[string]any    `json:"params"`
	RetrievedAt string            `json:"retrieved_at"`
	RawHash     string            `json:"raw_hash"`
	Data        *T                `json:"data"`
	Error       *ObservationError `json:"error"`

	// wireData preserves the exact data value token received by the strict
	// decoder. It is used only to validate that received observation; a later
	// marshal may canonicalize the typed value and emits a hash for that token.
	wireData json.RawMessage
}

// MarshalJSON preserves the legacy Wrap(data) encoding semantics: Data is
// stored as *T for presence tracking, but its JSON is encoded as the caller's T
// value. This avoids invoking a pointer-only Marshaler when the caller supplied
// a value while still honoring it when T itself is a pointer.
func (o ObservationEnvelope[T]) MarshalJSON() ([]byte, error) {
	encodedData, err := marshalObservationValue(o.Data)
	if err != nil {
		return nil, fmt.Errorf("marshal observation envelope data: %w", err)
	}
	type wireEnvelope struct {
		Version     int               `json:"version"`
		Status      ObservationStatus `json:"status"`
		Provider    string            `json:"provider"`
		Endpoint    string            `json:"endpoint"`
		Params      map[string]any    `json:"params"`
		RetrievedAt string            `json:"retrieved_at"`
		RawHash     string            `json:"raw_hash"`
		Data        json.RawMessage   `json:"data"`
		Error       *ObservationError `json:"error"`
	}
	return json.Marshal(wireEnvelope{
		Version:     o.Version,
		Status:      o.Status,
		Provider:    o.Provider,
		Endpoint:    o.Endpoint,
		Params:      o.Params,
		RetrievedAt: o.RetrievedAt,
		RawHash:     rawHashJSON(encodedData),
		Data:        json.RawMessage(encodedData),
		Error:       o.Error,
	})
}

// NewSuccess builds an observation containing usable data.
func NewSuccess[T any](provider, endpoint string, params map[string]any, data T) (ObservationEnvelope[T], error) {
	return newDataObservation(ObservationStatusOK, provider, endpoint, params, data, nil)
}

// NewEmpty builds a successful observation whose payload is a legitimate empty
// collection or object. The caller selects this status explicitly.
func NewEmpty[T any](provider, endpoint string, params map[string]any, data T) (ObservationEnvelope[T], error) {
	return newDataObservation(ObservationStatusEmpty, provider, endpoint, params, data, nil)
}

// NewDegraded builds an observation with usable data and a dependency error.
func NewDegraded[T any](provider, endpoint string, params map[string]any, data T, observationError ObservationError) (ObservationEnvelope[T], error) {
	return newDataObservation(ObservationStatusDegraded, provider, endpoint, params, data, &observationError)
}

// NewFailure builds a typed failed observation. Failed observations never
// contain a data payload and cannot be converted to the legacy success envelope.
func NewFailure[T any](provider, endpoint string, params map[string]any, observationError ObservationError) (ObservationEnvelope[T], error) {
	observation := ObservationEnvelope[T]{
		Version:     ObservationEnvelopeVersion,
		Status:      ObservationStatusError,
		Provider:    provider,
		Endpoint:    endpoint,
		Params:      normalizedParams(params),
		RetrievedAt: UTCNow(),
		RawHash:     ObservationNullDataRawHash,
		Error:       &observationError,
	}
	return observation, observation.Validate()
}

func newDataObservation[T any](status ObservationStatus, provider, endpoint string, params map[string]any, data T, observationError *ObservationError) (ObservationEnvelope[T], error) {
	dataCopy := data
	encodedData, err := marshalObservationValue(&dataCopy)
	if err != nil {
		return ObservationEnvelope[T]{}, fmt.Errorf("marshal observation data: %w", err)
	}
	observation := ObservationEnvelope[T]{
		Version:     ObservationEnvelopeVersion,
		Status:      status,
		Provider:    provider,
		Endpoint:    endpoint,
		Params:      normalizedParams(params),
		RetrievedAt: UTCNow(),
		RawHash:     rawHashJSON(encodedData),
		Data:        &dataCopy,
		Error:       observationError,
	}
	return observation, observation.Validate()
}

func normalizedParams(params map[string]any) map[string]any {
	if params == nil {
		return map[string]any{}
	}
	return params
}

// Validate checks the versioned envelope's structural and status invariants.
func (o ObservationEnvelope[T]) Validate() error {
	if o.Version != ObservationEnvelopeVersion {
		return fmt.Errorf("observation envelope version must be %d", ObservationEnvelopeVersion)
	}
	if strings.TrimSpace(o.Provider) == "" {
		return fmt.Errorf("observation envelope provider is required")
	}
	if strings.TrimSpace(o.Endpoint) == "" {
		return fmt.Errorf("observation envelope endpoint is required")
	}
	if o.Params == nil {
		return fmt.Errorf("observation envelope params must be an object")
	}
	if _, err := json.Marshal(o.Params); err != nil {
		return fmt.Errorf("observation envelope params must be JSON serializable: %w", err)
	}
	if _, err := time.Parse(time.RFC3339, o.RetrievedAt); err != nil {
		return fmt.Errorf("observation envelope retrieved_at must be RFC3339: %w", err)
	}
	if !validRawHash(o.RawHash) {
		return fmt.Errorf("observation envelope raw_hash must be a lowercase sha256 digest")
	}

	switch o.Status {
	case ObservationStatusOK, ObservationStatusEmpty:
		if o.Error != nil {
			return fmt.Errorf("observation status %q must not contain an error", o.Status)
		}
		if err := validateObservationData(o.Data, o.wireData, o.RawHash); err != nil {
			return fmt.Errorf("observation status %q: %w", o.Status, err)
		}
	case ObservationStatusDegraded:
		if err := validateObservationError(o.Error); err != nil {
			return fmt.Errorf("degraded observation: %w", err)
		}
		if err := validateObservationData(o.Data, o.wireData, o.RawHash); err != nil {
			return fmt.Errorf("degraded observation: %w", err)
		}
	case ObservationStatusError:
		if err := validateObservationError(o.Error); err != nil {
			return fmt.Errorf("failed observation: %w", err)
		}
		if o.Data != nil {
			return fmt.Errorf("failed observation must not contain data")
		}
		encodedData := []byte("null")
		if o.wireData != nil {
			encodedData = o.wireData
		}
		if !isJSONNull(encodedData) {
			return fmt.Errorf("failed observation wire data must be null")
		}
		if rawHashJSON(encodedData) != o.RawHash {
			return fmt.Errorf("failed observation raw_hash must describe null data")
		}
	default:
		return fmt.Errorf("unsupported observation status %q", o.Status)
	}

	return nil
}

func validateObservationError(observationError *ObservationError) error {
	if observationError == nil {
		return fmt.Errorf("structured error is required")
	}
	if strings.TrimSpace(observationError.Code) == "" {
		return fmt.Errorf("error code is required")
	}
	if strings.TrimSpace(observationError.Message) == "" {
		return fmt.Errorf("error message is required")
	}
	return nil
}

func validateObservationData[T any](data *T, wireData json.RawMessage, expectedHash string) error {
	if data == nil {
		return fmt.Errorf("usable data is required")
	}
	encoded := []byte(wireData)
	if wireData == nil {
		var err error
		encoded, err = marshalObservationValue(data)
		if err != nil {
			return fmt.Errorf("data must be JSON serializable: %w", err)
		}
	}
	if bytes.Equal(encoded, []byte("null")) {
		return fmt.Errorf("usable data must not be null")
	}
	if rawHashJSON(encoded) != expectedHash {
		return fmt.Errorf("raw_hash does not match data")
	}
	return nil
}

func marshalObservationValue[T any](data *T) ([]byte, error) {
	if data == nil {
		return []byte("null"), nil
	}
	return json.Marshal(*data)
}

func rawHashJSON(encoded []byte) string {
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validRawHash(hash string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(hash, prefix) || len(hash) != len(prefix)+sha256.Size*2 {
		return false
	}
	digest := hash[len(prefix):]
	_, err := hex.DecodeString(digest)
	return err == nil && digest == strings.ToLower(digest)
}

type observationWireEnvelope struct {
	Version     json.RawMessage `json:"version"`
	Status      json.RawMessage `json:"status"`
	Provider    json.RawMessage `json:"provider"`
	Endpoint    json.RawMessage `json:"endpoint"`
	Params      json.RawMessage `json:"params"`
	RetrievedAt json.RawMessage `json:"retrieved_at"`
	RawHash     json.RawMessage `json:"raw_hash"`
	Data        json.RawMessage `json:"data"`
	Error       json.RawMessage `json:"error"`
}

type observationWireError struct {
	Code       json.RawMessage `json:"code"`
	Message    json.RawMessage `json:"message"`
	Dependency json.RawMessage `json:"dependency"`
	Retryable  json.RawMessage `json:"retryable"`
}

// DecodeObservationEnvelope strictly decodes one wire payload. Unlike typed
// in-memory validation, it also enforces required field presence and rejects
// unknown fields and trailing JSON values.
func DecodeObservationEnvelope[T any](payload []byte) (ObservationEnvelope[T], error) {
	var wire observationWireEnvelope
	if err := decodeStrictJSON(payload, &wire); err != nil {
		return ObservationEnvelope[T]{}, fmt.Errorf("decode observation envelope: %w", err)
	}

	required := []struct {
		name  string
		value json.RawMessage
	}{
		{"version", wire.Version},
		{"status", wire.Status},
		{"provider", wire.Provider},
		{"endpoint", wire.Endpoint},
		{"params", wire.Params},
		{"retrieved_at", wire.RetrievedAt},
		{"raw_hash", wire.RawHash},
		{"data", wire.Data},
		{"error", wire.Error},
	}
	for _, field := range required {
		if len(field.value) == 0 {
			return ObservationEnvelope[T]{}, fmt.Errorf("decode observation envelope: required field %q is missing", field.name)
		}
	}

	var observation ObservationEnvelope[T]
	if err := decodeNonNullField("version", wire.Version, &observation.Version); err != nil {
		return ObservationEnvelope[T]{}, err
	}
	if err := decodeNonNullField("status", wire.Status, &observation.Status); err != nil {
		return ObservationEnvelope[T]{}, err
	}
	if err := decodeNonNullField("provider", wire.Provider, &observation.Provider); err != nil {
		return ObservationEnvelope[T]{}, err
	}
	if err := decodeNonNullField("endpoint", wire.Endpoint, &observation.Endpoint); err != nil {
		return ObservationEnvelope[T]{}, err
	}
	if err := decodeNonNullField("params", wire.Params, &observation.Params); err != nil {
		return ObservationEnvelope[T]{}, err
	}
	if err := decodeNonNullField("retrieved_at", wire.RetrievedAt, &observation.RetrievedAt); err != nil {
		return ObservationEnvelope[T]{}, err
	}
	if err := decodeNonNullField("raw_hash", wire.RawHash, &observation.RawHash); err != nil {
		return ObservationEnvelope[T]{}, err
	}
	observation.wireData = bytes.Clone(wire.Data)

	switch observation.Status {
	case ObservationStatusOK, ObservationStatusEmpty:
		if isJSONNull(wire.Data) {
			return ObservationEnvelope[T]{}, fmt.Errorf("decode observation envelope: status %q requires non-null data", observation.Status)
		}
		if !isJSONNull(wire.Error) {
			return ObservationEnvelope[T]{}, fmt.Errorf("decode observation envelope: status %q requires null error", observation.Status)
		}
	case ObservationStatusDegraded:
		if isJSONNull(wire.Data) {
			return ObservationEnvelope[T]{}, fmt.Errorf("decode observation envelope: degraded status requires non-null data")
		}
		observationError, err := decodeObservationError(wire.Error)
		if err != nil {
			return ObservationEnvelope[T]{}, err
		}
		observation.Error = observationError
	case ObservationStatusError:
		if !isJSONNull(wire.Data) {
			return ObservationEnvelope[T]{}, fmt.Errorf("decode observation envelope: error status requires null data")
		}
		observationError, err := decodeObservationError(wire.Error)
		if err != nil {
			return ObservationEnvelope[T]{}, err
		}
		observation.Error = observationError
	default:
		return ObservationEnvelope[T]{}, fmt.Errorf("decode observation envelope: unsupported status %q", observation.Status)
	}

	if observation.Status != ObservationStatusError {
		var data T
		if err := json.Unmarshal(wire.Data, &data); err != nil {
			return ObservationEnvelope[T]{}, fmt.Errorf("decode observation envelope data: %w", err)
		}
		observation.Data = &data
	}
	if err := observation.Validate(); err != nil {
		return ObservationEnvelope[T]{}, fmt.Errorf("decode observation envelope: %w", err)
	}
	return observation, nil
}

// UnmarshalJSON makes the standard JSON entry point enforce the wire contract.
func (o *ObservationEnvelope[T]) UnmarshalJSON(payload []byte) error {
	observation, err := DecodeObservationEnvelope[T](payload)
	if err != nil {
		return err
	}
	*o = observation
	return nil
}

func decodeObservationError(payload json.RawMessage) (*ObservationError, error) {
	if isJSONNull(payload) {
		return nil, fmt.Errorf("decode observation envelope: structured error is required")
	}
	var wire observationWireError
	if err := decodeStrictJSON(payload, &wire); err != nil {
		return nil, fmt.Errorf("decode observation envelope error: %w", err)
	}
	for _, field := range []struct {
		name  string
		value json.RawMessage
	}{
		{"code", wire.Code},
		{"message", wire.Message},
		{"retryable", wire.Retryable},
	} {
		if len(field.value) == 0 {
			return nil, fmt.Errorf("decode observation envelope error: required field %q is missing", field.name)
		}
	}

	observationError := &ObservationError{}
	if err := decodeNonNullField("error.code", wire.Code, &observationError.Code); err != nil {
		return nil, err
	}
	if err := decodeNonNullField("error.message", wire.Message, &observationError.Message); err != nil {
		return nil, err
	}
	if len(wire.Dependency) != 0 {
		if err := decodeNonNullField("error.dependency", wire.Dependency, &observationError.Dependency); err != nil {
			return nil, err
		}
		if observationError.Dependency == "" {
			return nil, fmt.Errorf("decode observation envelope: field %q must not be empty", "error.dependency")
		}
	}
	if err := decodeNonNullField("error.retryable", wire.Retryable, &observationError.Retryable); err != nil {
		return nil, err
	}
	return observationError, nil
}

func decodeNonNullField(name string, payload json.RawMessage, destination any) error {
	if isJSONNull(payload) {
		return fmt.Errorf("decode observation envelope: field %q must not be null", name)
	}
	if err := json.Unmarshal(payload, destination); err != nil {
		return fmt.Errorf("decode observation envelope field %q: %w", name, err)
	}
	return nil
}

func decodeStrictJSON(payload []byte, destination any) error {
	if err := rejectDuplicateObjectKeys(payload); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return fmt.Errorf("trailing JSON: %w", err)
	}
	return nil
}

// rejectDuplicateObjectKeys walks the JSON token stream before typed decoding.
// encoding/json otherwise accepts duplicate object members with last-value-wins,
// which makes the strict wire contract ambiguous across language runtimes.
func rejectDuplicateObjectKeys(payload []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := walkJSONValue(decoder, "$"); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err != nil {
			return err
		}
		return fmt.Errorf("trailing JSON value starting with %v", token)
	}
	return nil
}

func walkJSONValue(decoder *json.Decoder, path string) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		if number, ok := token.(json.Number); ok && strings.ContainsAny(number.String(), ".eE") {
			value, _ := strconv.ParseFloat(number.String(), 64)
			if math.IsInf(value, 0) || math.IsNaN(value) {
				return fmt.Errorf("non-finite JSON number %q at %s", number, path)
			}
		}
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("object key at %s is not a string", path)
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate object key %q at %s", key, path)
			}
			seen[key] = struct{}{}
			if err := walkJSONValue(decoder, path+"."+key); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return fmt.Errorf("object at %s has invalid closing token %v", path, closing)
		}
	case '[':
		index := 0
		for decoder.More() {
			if err := walkJSONValue(decoder, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
			index++
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return fmt.Errorf("array at %s has invalid closing token %v", path, closing)
		}
	default:
		return fmt.Errorf("unexpected closing token %q at %s", delim, path)
	}
	return nil
}

func isJSONNull(payload json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(payload), []byte("null"))
}

// ToLegacy converts observations with usable data to the unchanged legacy
// envelope. A failed observation returns its structured Go error instead.
func (o ObservationEnvelope[T]) ToLegacy() (Envelope, error) {
	if err := o.Validate(); err != nil {
		return Envelope{}, err
	}
	if o.Status == ObservationStatusError {
		return Envelope{}, o.Error
	}
	encodedData, err := marshalObservationValue(o.Data)
	if err != nil {
		return Envelope{}, fmt.Errorf("marshal legacy observation data: %w", err)
	}
	return Envelope{
		Provider:    o.Provider,
		Endpoint:    o.Endpoint,
		Params:      o.Params,
		RetrievedAt: o.RetrievedAt,
		RawHash:     rawHashJSON(encodedData),
		Data:        *o.Data,
	}, nil
}
