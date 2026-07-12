package examgeneration

import (
	"encoding/json"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

const (
	DefaultEventPageSize = 50
	MaxEventPageSize     = 200
	MaxEventPayloadBytes = 32 << 10
)

type Status string

const (
	StatusQueued     Status = "queued"
	StatusRunning    Status = "running"
	StatusSucceeded  Status = "succeeded"
	StatusSuperseded Status = "superseded"
	StatusFailed     Status = "failed"
)

type EventType string

const (
	EventQueued     EventType = "queued"
	EventRunning    EventType = "running"
	EventSucceeded  EventType = "succeeded"
	EventSuperseded EventType = "superseded"
	EventFailed     EventType = "failed"
)

type Generation struct {
	GenerationID string     `json:"generationId"`
	Status       Status     `json:"status"`
	AttemptCount int32      `json:"attemptCount"`
	CreatedAt    time.Time  `json:"createdAt"`
	StartedAt    *time.Time `json:"startedAt,omitempty"`
	FinishedAt   *time.Time `json:"finishedAt,omitempty"`
	ErrorCode    *string    `json:"errorCode,omitempty"`
	EventHead    int64      `json:"eventHead"`
}

type Event struct {
	Sequence  int64           `json:"sequence"`
	Type      EventType       `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"createdAt"`
}

type EventBatch struct {
	GenerationID string  `json:"generationId"`
	EventHead    int64   `json:"eventHead"`
	Events       []Event `json:"events"`
	Terminal     bool    `json:"terminal"`
}

type CurrentQuery struct {
	Principal auth.AccessPrincipal
	ExamID    string
}

type EventQuery struct {
	Principal     auth.AccessPrincipal
	ExamID        string
	GenerationID  string
	AfterSequence int64
	Limit         int
}
