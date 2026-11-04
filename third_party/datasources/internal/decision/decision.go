// Package decision validates recommendation proposals and policy settlements,
// then persists them with one durable notification. Closing the resulting
// record is deliberately owned by delivery's specialized acknowledgement.
package decision

import (
	"bytes"
	"context"
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

type SettlementOutcome string

const (
	OutcomeAccepted    SettlementOutcome = "accepted"
	OutcomeRejected    SettlementOutcome = "rejected"
	OutcomeNeedsReview SettlementOutcome = "needs_review"
)

var canonicalSymbolPattern = regexp.MustCompile(`^(US:[A-Z0-9][A-Z0-9.-]*|HK:[0-9]{5}|CN:[0-9]{6}|CRYPTO:[A-Z0-9]+-[A-Z0-9]+)$`)

type RecommendationProposal struct {
	Version        int             `json:"version"`
	ID             string          `json:"id"`
	Producer       string          `json:"producer"`
	Symbol         string          `json:"symbol"`
	Recommendation string          `json:"recommendation"`
	AsOf           time.Time       `json:"as_of"`
	Provenance     []string        `json:"provenance"`
	TraceID        string          `json:"trace_id"`
	Payload        json.RawMessage `json:"payload"`
}

type Settlement struct {
	Version    int               `json:"version"`
	ProposalID string            `json:"proposal_id"`
	Outcome    SettlementOutcome `json:"outcome"`
	SettledAt  time.Time         `json:"settled_at"`
	Reasons    []string          `json:"reasons"`
	Payload    json.RawMessage   `json:"payload"`
}

type Notification struct {
	Version        int                    `json:"version"`
	ID             string                 `json:"id"`
	IdempotencyKey string                 `json:"idempotency_key"`
	Proposal       RecommendationProposal `json:"proposal"`
	Settlement     Settlement             `json:"settlement"`
}

type Request struct {
	Proposal    RecommendationProposal
	Settlement  Settlement
	Destination string
	AvailableAt time.Time
	JobID       string
	StepID      string
}

type Result struct {
	Record  controlstore.DecisionRecord
	Outbox  controlstore.OutboxMessage
	Created bool
}

type Service struct{ store *controlstore.Store }

func New(store *controlstore.Store) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("decision: control store is required")
	}
	return &Service{store: store}, nil
}

func (service *Service) Create(ctx context.Context, request Request) (Result, error) {
	proposal, settlement, err := validateAndNormalize(request.Proposal, request.Settlement)
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(request.Destination) == "" {
		return Result{}, fmt.Errorf("decision: destination is required")
	}
	decisionKey := "decision:" + proposal.ID
	notificationKey := "decision-notification:" + proposal.ID
	notification := Notification{
		Version: Version, ID: notificationKey, IdempotencyKey: notificationKey,
		Proposal: proposal, Settlement: settlement,
	}
	proposalJSON, err := json.Marshal(proposal)
	if err != nil {
		return Result{}, fmt.Errorf("decision: encode proposal: %w", err)
	}
	settlementJSON, err := json.Marshal(settlement)
	if err != nil {
		return Result{}, fmt.Errorf("decision: encode settlement: %w", err)
	}
	notificationJSON, err := json.Marshal(notification)
	if err != nil {
		return Result{}, fmt.Errorf("decision: encode notification: %w", err)
	}
	record, outbox, created, err := service.store.CreateDecisionNotification(ctx, controlstore.NewDecisionNotification{
		Decision: controlstore.NewDecision{
			IdempotencyKey: decisionKey, Proposal: proposalJSON, Settlement: settlementJSON,
		},
		Notification: controlstore.NewOutbox{
			Destination: request.Destination, IdempotencyKey: notificationKey,
			Payload: notificationJSON, AvailableAt: request.AvailableAt,
		},
		JobID: request.JobID, StepID: request.StepID,
	})
	if err != nil {
		return Result{}, fmt.Errorf("decision: create notification: %w", err)
	}
	return Result{Record: record, Outbox: outbox, Created: created}, nil
}

func validateAndNormalize(proposal RecommendationProposal, settlement Settlement) (RecommendationProposal, Settlement, error) {
	if proposal.Version != Version {
		return proposal, settlement, fmt.Errorf("decision: proposal version must be %d", Version)
	}
	for field, value := range map[string]string{
		"proposal id": proposal.ID, "proposal producer": proposal.Producer,
		"proposal recommendation": proposal.Recommendation, "proposal trace id": proposal.TraceID,
	} {
		if strings.TrimSpace(value) == "" {
			return proposal, settlement, fmt.Errorf("decision: %s is required", field)
		}
	}
	if !canonicalSymbolPattern.MatchString(proposal.Symbol) {
		return proposal, settlement, fmt.Errorf("decision: proposal symbol %q is not canonical", proposal.Symbol)
	}
	if proposal.AsOf.IsZero() || proposal.AsOf.Location() != time.UTC {
		return proposal, settlement, fmt.Errorf("decision: proposal as_of must be a non-zero UTC timestamp")
	}
	if len(proposal.Provenance) == 0 {
		return proposal, settlement, fmt.Errorf("decision: proposal provenance is required")
	}
	for _, source := range proposal.Provenance {
		if strings.TrimSpace(source) == "" {
			return proposal, settlement, fmt.Errorf("decision: proposal provenance entries must not be blank")
		}
	}
	canonicalProposal, err := canonicalJSON("proposal payload", proposal.Payload)
	if err != nil {
		return proposal, settlement, err
	}
	proposal.Payload = canonicalProposal
	proposal.Provenance = sortedUnique(proposal.Provenance)

	if settlement.Version != Version {
		return proposal, settlement, fmt.Errorf("decision: settlement version must be %d", Version)
	}
	if settlement.ProposalID != proposal.ID {
		return proposal, settlement, fmt.Errorf("decision: settlement proposal_id must match proposal id")
	}
	switch settlement.Outcome {
	case OutcomeAccepted, OutcomeRejected, OutcomeNeedsReview:
	default:
		return proposal, settlement, fmt.Errorf("decision: unsupported settlement outcome %q", settlement.Outcome)
	}
	if settlement.SettledAt.IsZero() || settlement.SettledAt.Location() != time.UTC {
		return proposal, settlement, fmt.Errorf("decision: settlement settled_at must be a non-zero UTC timestamp")
	}
	if len(settlement.Reasons) == 0 {
		return proposal, settlement, fmt.Errorf("decision: settlement reasons are required")
	}
	for _, reason := range settlement.Reasons {
		if strings.TrimSpace(reason) == "" {
			return proposal, settlement, fmt.Errorf("decision: settlement reasons must not be blank")
		}
	}
	canonicalSettlement, err := canonicalJSON("settlement payload", settlement.Payload)
	if err != nil {
		return proposal, settlement, err
	}
	settlement.Payload = canonicalSettlement
	settlement.Reasons = sortedUnique(settlement.Reasons)
	return proposal, settlement, nil
}

func canonicalJSON(field string, payload json.RawMessage) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	value, err := decodeStrictJSONValue(decoder)
	if err != nil {
		return nil, fmt.Errorf("decision: %s must be valid JSON: %w", field, err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("decision: %s must contain one JSON value", field)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("decision: canonicalize %s: %w", field, err)
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
