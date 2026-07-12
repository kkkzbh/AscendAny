package administration

import (
	"encoding/json"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

const (
	DefaultPageSize = 20
	MaxPageSize     = 100
)

type AccountQuery struct {
	Principal auth.AccessPrincipal
	Cursor    *string
	Limit     int
}

type StudentQuery struct {
	Principal auth.AccessPrincipal
	Cursor    *string
	Limit     int
}

type AuditQuery struct {
	Principal auth.AccessPrincipal
	Cursor    *string
	Limit     int
}

type AccountStateCommand struct {
	Principal auth.AccessPrincipal
	TargetID  string
	Disabled  bool
}

type AccountPage struct {
	Items      []ManagedAccount `json:"items"`
	NextCursor *string          `json:"nextCursor"`
}

type ManagedAccount struct {
	ID                 string     `json:"id"`
	Username           string     `json:"username"`
	DisplayName        string     `json:"displayName"`
	StudentNumber      *string    `json:"studentNumber"`
	Role               auth.Role  `json:"role"`
	AuthRevision       int64      `json:"authRevision"`
	DisabledAt         *time.Time `json:"disabledAt"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
	ActiveSessionCount int64      `json:"activeSessionCount"`
}

type StudentPage struct {
	Items      []ManagedStudent `json:"items"`
	NextCursor *string          `json:"nextCursor"`
}

type ManagedStudent struct {
	StudentNumber     string                 `json:"studentNumber"`
	PintiaUserID      string                 `json:"pintiaUserId"`
	SourceDisplayName *string                `json:"sourceDisplayName"`
	Account           *StudentAccountBinding `json:"account"`
	Rating            *int64                 `json:"rating"`
}

type StudentAccountBinding struct {
	ID          string     `json:"id"`
	Username    string     `json:"username"`
	DisplayName string     `json:"displayName"`
	DisabledAt  *time.Time `json:"disabledAt"`
}

type AuditPage struct {
	Items      []AuditEvent `json:"items"`
	NextCursor *string      `json:"nextCursor"`
}

type AuditEvent struct {
	ID             string          `json:"id"`
	ActorAccountID *string         `json:"actorAccountId"`
	ActorSessionID *string         `json:"actorSessionId"`
	Type           string          `json:"type"`
	OccurredAt     time.Time       `json:"occurredAt"`
	Payload        json.RawMessage `json:"payload"`
}
