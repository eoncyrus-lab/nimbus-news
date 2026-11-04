// Package alert persists raw alerts independently of optional LLM enrichment.
// The durable outbox row and its cooldown effect are created in the same
// controlstore transaction; only delivery's specialized ack commits cooldown.
package alert

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Ricaardo/nimbus-os/datasources/internal/controlstore"
)

const Version = 1

type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

var canonicalSymbolPattern = regexp.MustCompile(`^(US:[A-Z0-9][A-Z0-9.-]*|HK:[0-9]{5}|CN:[0-9]{6}|CRYPTO:[A-Z0-9]+-[A-Z0-9]+)$`)

type AlertEvent struct {
	Version     int             `json:"version"`
	ID          string          `json:"id"`
	Producer    string          `json:"producer"`
	Kind        string          `json:"kind"`
	Symbol      string          `json:"symbol"`
	Severity    Severity        `json:"severity"`
	ObservedAt  time.Time       `json:"observed_at"`
	RetrievedAt time.Time       `json:"retrieved_at"`
	Provenance  []string        `json:"provenance"`
	Stale       bool            `json:"stale"`
	TraceID     string          `json:"trace_id"`
	Data        json.RawMessage `json:"data"`

	// Enrichment is deliberately outside the raw wire contract and identity.
	// An unavailable, malformed, or changed LLM result cannot block raw alert
	// persistence or delivery.
	Enrichment json.RawMessage `json:"-"`
}

type DeliveryEnvelope struct {
	Version        int        `json:"version"`
	ID             string     `json:"id"`
	IdempotencyKey string     `json:"idempotency_key"`
	Event          AlertEvent `json:"event"`
}

type Request struct {
	Event         AlertEvent
	Destination   string
	CooldownKey   string
	CooldownUntil time.Time
}

type Result struct {
	Key        string
	Job        controlstore.JobRun
	Step       controlstore.StepRun
	Created    bool
	Suppressed bool
}

type Options struct {
	Worker string
	Lease  time.Duration
	Now    func() time.Time
}

type Service struct {
	store  *controlstore.Store
	worker string
	lease  time.Duration
	now    func() time.Time
}

func New(store *controlstore.Store, options Options) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("alert: control store is required")
	}
	if strings.TrimSpace(options.Worker) == "" {
		return nil, fmt.Errorf("alert: worker is required")
	}
	if options.Lease <= 0 {
		return nil, fmt.Errorf("alert: lease must be positive")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, worker: options.Worker, lease: options.Lease, now: now}, nil
}

// DeterministicKey hashes canonical raw event content. It excludes ID,
// RetrievedAt, TraceID, and Enrichment so retries and optional LLM work do not
// create a second raw alert.
func DeterministicKey(event AlertEvent) (string, error) {
	if err := validateEvent(event); err != nil {
		return "", err
	}
	canonicalData, err := canonicalJSON(event.Data)
	if err != nil {
		return "", err
	}
	provenance := sortedUnique(event.Provenance)
	identity, err := json.Marshal(struct {
		Version    int             `json:"version"`
		Producer   string          `json:"producer"`
		Kind       string          `json:"kind"`
		Symbol     string          `json:"symbol"`
		Severity   Severity        `json:"severity"`
		ObservedAt time.Time       `json:"observed_at"`
		Provenance []string        `json:"provenance"`
		Stale      bool            `json:"stale"`
		Data       json.RawMessage `json:"data"`
	}{event.Version, event.Producer, event.Kind, event.Symbol, event.Severity, event.ObservedAt, provenance, event.Stale, canonicalData})
	if err != nil {
		return "", fmt.Errorf("alert: encode identity: %w", err)
	}
	sum := sha256.Sum256(identity)
	return "alert:sha256:" + hex.EncodeToString(sum[:]), nil
}

func (service *Service) Emit(ctx context.Context, request Request) (Result, error) {
	key, err := DeterministicKey(request.Event)
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(request.Destination) == "" {
		return Result{}, fmt.Errorf("alert: destination is required")
	}
	if strings.TrimSpace(request.CooldownKey) == "" {
		return Result{}, fmt.Errorf("alert: cooldown key is required")
	}
	if request.CooldownUntil.IsZero() {
		return Result{}, fmt.Errorf("alert: cooldown_until is required")
	}
	now := service.now().UTC()
	if !request.CooldownUntil.After(now) {
		return Result{}, fmt.Errorf("alert: cooldown_until must be in the future")
	}
	if cooldown, err := service.store.GetCooldown(ctx, request.CooldownKey); err == nil {
		if cooldown.CommitUntil.After(now) {
			return Result{Key: key, Suppressed: true}, nil
		}
	} else if !errors.Is(err, controlstore.ErrNotFound) {
		return Result{}, fmt.Errorf("alert: read cooldown: %w", err)
	}

	event := request.Event
	event.ID = key
	event.Provenance = sortedUnique(event.Provenance)
	envelope := DeliveryEnvelope{Version: Version, ID: key, IdempotencyKey: key, Event: event}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return Result{}, fmt.Errorf("alert: encode delivery envelope: %w", err)
	}
	jobKind := "alert:" + request.Destination
	job, created, err := service.store.CreateJob(ctx, controlstore.NewJob{
		Kind: jobKind, Producer: event.Producer, TraceID: key, IdempotencyKey: key,
	}, []controlstore.NewStep{{Name: "persist-raw"}})
	if err != nil {
		return Result{}, fmt.Errorf("alert: create job: %w", err)
	}
	result := Result{Key: key, Job: job, Created: created}
	switch job.Status {
	case controlstore.JobPending:
		job, err = service.store.StartJob(ctx, job.ID)
		if err != nil {
			return result, fmt.Errorf("alert: start job: %w", err)
		}
		result.Job = job
	case controlstore.JobRunning:
	case controlstore.JobSucceeded:
		result.Step, err = service.store.GetStepByName(ctx, job.ID, "persist-raw")
		return result, err
	default:
		return result, fmt.Errorf("%w: alert job %q is %s", controlstore.ErrInvalidTransition, job.ID, job.Status)
	}
	step, err := service.store.GetStepByName(ctx, job.ID, "persist-raw")
	if err != nil {
		return result, fmt.Errorf("alert: read step: %w", err)
	}
	result.Step = step
	if step.Status == controlstore.StepSucceeded {
		return result, nil
	}
	running, err := service.store.StartStep(ctx, job.ID, step.ID, service.worker, service.lease)
	if err != nil {
		return result, fmt.Errorf("alert: start step: %w", err)
	}
	result.Step, err = service.store.CompleteStepAndEnqueue(ctx, step.ID, running.LeaseToken, []controlstore.NewOutbox{{
		Destination: request.Destination, IdempotencyKey: key, Payload: payload,
		Effect: &controlstore.OutboxEffect{CooldownKey: request.CooldownKey, CooldownCommitUntil: request.CooldownUntil.UTC()},
	}})
	if err != nil {
		return result, fmt.Errorf("alert: persist raw alert: %w", err)
	}
	result.Job, _ = service.store.GetJob(ctx, job.ID)
	return result, nil
}

func validateEvent(event AlertEvent) error {
	if event.Version != Version {
		return fmt.Errorf("alert: event version must be %d", Version)
	}
	for field, value := range map[string]string{
		"producer": event.Producer, "kind": event.Kind, "trace_id": event.TraceID,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("alert: %s is required", field)
		}
	}
	if !canonicalSymbolPattern.MatchString(event.Symbol) {
		return fmt.Errorf("alert: symbol %q is not canonical", event.Symbol)
	}
	switch event.Severity {
	case SeverityInfo, SeverityWarning, SeverityCritical:
	default:
		return fmt.Errorf("alert: unsupported severity %q", event.Severity)
	}
	if event.ObservedAt.IsZero() || event.ObservedAt.Location() != time.UTC {
		return fmt.Errorf("alert: observed_at must be a non-zero UTC timestamp")
	}
	if event.RetrievedAt.IsZero() || event.RetrievedAt.Location() != time.UTC {
		return fmt.Errorf("alert: retrieved_at must be a non-zero UTC timestamp")
	}
	if len(event.Provenance) == 0 {
		return fmt.Errorf("alert: provenance is required")
	}
	for _, source := range event.Provenance {
		if strings.TrimSpace(source) == "" {
			return fmt.Errorf("alert: provenance entries must not be blank")
		}
	}
	if _, err := canonicalJSON(event.Data); err != nil {
		return err
	}
	return nil
}

func canonicalJSON(data json.RawMessage) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := decodeStrictJSONValue(decoder)
	if err != nil {
		return nil, fmt.Errorf("alert: data must be valid JSON: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("alert: data must contain one JSON value")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("alert: canonicalize data: %w", err)
	}
	return encoded, nil
}

func decodeStrictJSONValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		if number, ok := token.(json.Number); ok {
			value, err := strconv.ParseFloat(number.String(), 64)
			if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
				return nil, fmt.Errorf("non-finite JSON number %q", number)
			}
		}
		return token, nil
	}
	switch delimiter {
	case '{':
		object := make(map[string]any)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, fmt.Errorf("object key must be a string")
			}
			if _, exists := object[key]; exists {
				return nil, fmt.Errorf("duplicate object field %q", key)
			}
			value, err := decodeStrictJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		closing, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		if closing != json.Delim('}') {
			return nil, fmt.Errorf("object is not closed")
		}
		return object, nil
	case '[':
		array := make([]any, 0)
		for decoder.More() {
			value, err := decodeStrictJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		closing, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		if closing != json.Delim(']') {
			return nil, fmt.Errorf("array is not closed")
		}
		return array, nil
	default:
		return nil, fmt.Errorf("unexpected delimiter %q", delimiter)
	}
}

func sortedUnique(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
