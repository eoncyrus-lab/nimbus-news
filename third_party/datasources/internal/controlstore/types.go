package controlstore

import (
	"errors"
	"time"
)

const schemaVersion = 1

var (
	ErrNotFound            = errors.New("controlstore: not found")
	ErrInvalidTransition   = errors.New("controlstore: invalid transition")
	ErrLeaseLost           = errors.New("controlstore: lease lost")
	ErrIdempotencyConflict = errors.New("controlstore: idempotency conflict")
	ErrWriterActive        = errors.New("controlstore: writer already active")
)

type JobStatus string

const (
	JobPending   JobStatus = "pending"
	JobRunning   JobStatus = "running"
	JobSucceeded JobStatus = "succeeded"
	JobFailed    JobStatus = "failed"
	JobCancelled JobStatus = "cancelled"
)

type StepStatus string

const (
	StepPending   StepStatus = "pending"
	StepRunning   StepStatus = "running"
	StepSucceeded StepStatus = "succeeded"
	StepFailed    StepStatus = "failed"
	StepCancelled StepStatus = "cancelled"
)

type InboxStatus string

const (
	InboxPending   InboxStatus = "pending"
	InboxClaimed   InboxStatus = "claimed"
	InboxProcessed InboxStatus = "processed"
	InboxDead      InboxStatus = "dead"
)

type OutboxStatus string

const (
	OutboxPending OutboxStatus = "pending"
	OutboxClaimed OutboxStatus = "claimed"
	OutboxAcked   OutboxStatus = "acked"
	OutboxDead    OutboxStatus = "dead"
)

type DecisionStatus string

const (
	DecisionOpen   DecisionStatus = "open"
	DecisionClosed DecisionStatus = "closed"
)

type Failure struct {
	Code      string
	Message   string
	Retryable bool
}

type JobRun struct {
	ID             string
	Version        int
	Kind           string
	Producer       string
	TraceID        string
	IdempotencyKey string
	Status         JobStatus
	Failure        *Failure
	CreatedAt      time.Time
	StartedAt      *time.Time
	FinishedAt     *time.Time
	UpdatedAt      time.Time
}

type StepRun struct {
	ID          string
	Version     int
	JobID       string
	Name        string
	Status      StepStatus
	Attempt     int
	AvailableAt time.Time
	LeaseOwner  string
	LeaseToken  string
	LeaseUntil  *time.Time
	Failure     *Failure
	CreatedAt   time.Time
	StartedAt   *time.Time
	FinishedAt  *time.Time
	UpdatedAt   time.Time
}

type InboxMessage struct {
	ID             string
	Version        int
	Producer       string
	MessageType    string
	IdempotencyKey string
	Payload        []byte
	PayloadHash    string
	Status         InboxStatus
	LeaseOwner     string
	LeaseToken     string
	LeaseUntil     *time.Time
	Attempt        int
	AvailableAt    time.Time
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type OutboxMessage struct {
	ID             string
	JobID          string
	StepID         string
	Version        int
	Destination    string
	IdempotencyKey string
	Payload        []byte
	PayloadHash    string
	Status         OutboxStatus
	LeaseOwner     string
	LeaseToken     string
	LeaseUntil     *time.Time
	Attempt        int
	AvailableAt    time.Time
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Effect         *OutboxEffect
}

type Cooldown struct {
	Key         string
	Version     int
	CommitUntil time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type DecisionRecord struct {
	ID             string
	Version        int
	IdempotencyKey string
	Proposal       []byte
	ProposalHash   string
	Settlement     []byte
	SettlementHash string
	Status         DecisionStatus
	CreatedAt      time.Time
	ClosedAt       *time.Time
	UpdatedAt      time.Time
}

// OutboxEffect is committed only by the matching specialized acknowledgement.
// Exactly one of the cooldown fields or DecisionID must be set.
type OutboxEffect struct {
	CooldownKey         string
	CooldownCommitUntil time.Time
	DecisionID          string
}

type NewJob struct {
	Kind           string
	Producer       string
	TraceID        string
	IdempotencyKey string
}

type NewStep struct {
	Name        string
	AvailableAt time.Time
}

type NewInbox struct {
	Producer       string
	MessageType    string
	IdempotencyKey string
	Payload        []byte
	// AvailableAt is first-write-wins. On replay, zero does not redeclare the
	// schedule; a non-zero value must equal the stored canonical timestamp.
	AvailableAt time.Time
}

type NewOutbox struct {
	Destination    string
	IdempotencyKey string
	Payload        []byte
	AvailableAt    time.Time
	Effect         *OutboxEffect
}

type NewDecision struct {
	IdempotencyKey string
	Proposal       []byte
	Settlement     []byte
}

type NewDecisionNotification struct {
	Decision     NewDecision
	Notification NewOutbox
	JobID        string
	StepID       string
}
