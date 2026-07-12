package agentnotes

import (
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

const (
	DefaultPageSize = 20
	MaxPageSize     = 100
	MaxTitleBytes   = 512
	MaxContentBytes = 131072
)

type State string

const (
	StateActive   State = "active"
	StateArchived State = "archived"
)

type SourceKind string

const SourceUser SourceKind = "user"

type Operation string

const (
	OperationCreate  Operation = "create"
	OperationReplace Operation = "replace"
	OperationArchive Operation = "archive"
	OperationRestore Operation = "restore"
)

type ListQuery struct {
	Principal auth.AccessPrincipal
	Cursor    *string
	Limit     int
}

type DetailQuery struct {
	Principal auth.AccessPrincipal
	NoteID    string
}

type CreateCommand struct {
	Principal            auth.AccessPrincipal
	MutationID           string
	ExpectedHeadRevision int64
	Title                string
	Content              string
}

type ReplaceCommand struct {
	Principal            auth.AccessPrincipal
	NoteID               string
	MutationID           string
	ExpectedHeadRevision int64
	Title                string
	Content              string
}

type StateCommand struct {
	Principal            auth.AccessPrincipal
	NoteID               string
	MutationID           string
	ExpectedHeadRevision int64
}

type UserMutationCommand struct {
	Principal            auth.AccessPrincipal
	NoteID               string
	MutationID           string
	Operation            Operation
	ExpectedHeadRevision int64
	Title                string
	Content              string
	ContentSHA256        string
}

type Page struct {
	Items      []Summary `json:"items"`
	NextCursor *string   `json:"nextCursor"`
}

type Summary struct {
	ID                       string    `json:"id"`
	HeadRevision             int64     `json:"headRevision"`
	State                    State     `json:"state"`
	Title                    string    `json:"title"`
	ContentSHA256            string    `json:"contentSha256"`
	CurrentMutationID        string    `json:"currentMutationId"`
	CurrentOperation         Operation `json:"currentOperation"`
	CurrentRevisionCreatedAt time.Time `json:"currentRevisionCreatedAt"`
	CreatedAt                time.Time `json:"createdAt"`
	UpdatedAt                time.Time `json:"updatedAt"`
}

type Note struct {
	Summary
	Content string `json:"content"`
}

type MutationResult struct {
	Note       Note `json:"note"`
	Idempotent bool `json:"idempotent"`
}

type CreateInput struct {
	MutationID           string `json:"mutationId"`
	ExpectedHeadRevision int64  `json:"expectedHeadRevision"`
	Title                string `json:"title"`
	Content              string `json:"content"`
}

type ReplaceInput struct {
	MutationID           string `json:"mutationId"`
	ExpectedHeadRevision int64  `json:"expectedHeadRevision"`
	Title                string `json:"title"`
	Content              string `json:"content"`
}

type StateInput struct {
	MutationID           string `json:"mutationId"`
	ExpectedHeadRevision int64  `json:"expectedHeadRevision"`
}
