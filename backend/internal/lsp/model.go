package lsp

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"regexp"
	"time"
)

const (
	ControlSchemaV1       = "ascendany.lsp.control.v1"
	WebSocketProtocolV1   = "ascendany.lsp.v1"
	WebSocketTicketPrefix = "ascendany.lsp.ticket."
	PublicWorkspaceURI    = "file:///workspace"
	attachTicketBytes     = 32
)

var canonicalUUIDv4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var attachTicketText = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

type Policy struct {
	MaximumHeaderBytes     int
	MaximumHeaderCount     int
	MaximumBodyBytes       int
	MaximumMessages        int
	MaximumSessionDuration time.Duration
	MaximumWorkspaceBytes  int64
	MaximumWorkspaceFiles  int
	WorkspacePollInterval  time.Duration
}

func DefaultPolicy() Policy {
	return Policy{
		MaximumHeaderBytes:     256,
		MaximumHeaderCount:     2,
		MaximumBodyBytes:       1 << 20,
		MaximumMessages:        4096,
		MaximumSessionDuration: 30 * time.Minute,
		MaximumWorkspaceBytes:  32 << 20,
		MaximumWorkspaceFiles:  512,
		WorkspacePollInterval:  250 * time.Millisecond,
	}
}

func ValidPolicy(policy Policy) bool {
	return policy.MaximumHeaderBytes >= 64 && policy.MaximumHeaderBytes <= 4096 &&
		policy.MaximumHeaderCount >= 1 && policy.MaximumHeaderCount <= 8 &&
		policy.MaximumBodyBytes >= 1024 && policy.MaximumBodyBytes <= 8<<20 &&
		policy.MaximumMessages >= 1 && policy.MaximumMessages <= 1<<20 &&
		policy.MaximumSessionDuration >= time.Second && policy.MaximumSessionDuration <= time.Hour &&
		policy.MaximumWorkspaceBytes >= 1<<20 && policy.MaximumWorkspaceBytes <= 1<<30 &&
		policy.MaximumWorkspaceFiles >= 8 && policy.MaximumWorkspaceFiles <= 10000 &&
		policy.WorkspacePollInterval >= 10*time.Millisecond && policy.WorkspacePollInterval <= time.Second
}

func ValidPublicID(value string) bool { return canonicalUUIDv4.MatchString(value) }

func NewPublicID(random io.Reader) (string, error) {
	if random == nil {
		return "", errors.New("LSP session random source is required")
	}
	var raw [16]byte
	if _, err := io.ReadFull(random, raw[:]); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	const hexadecimal = "0123456789abcdef"
	var output [36]byte
	positions := [...]int{8, 13, 18, 23}
	rawIndex := 0
	for outputIndex := range output {
		isHyphen := false
		for _, position := range positions {
			if outputIndex == position {
				isHyphen = true
				break
			}
		}
		if isHyphen {
			output[outputIndex] = '-'
			continue
		}
		value := raw[rawIndex/2]
		if rawIndex%2 == 0 {
			output[outputIndex] = hexadecimal[value>>4]
		} else {
			output[outputIndex] = hexadecimal[value&0x0f]
		}
		rawIndex++
	}
	return string(output[:]), nil
}

func NewAttachTicket(random io.Reader) (string, error) {
	if random == nil {
		return "", errors.New("LSP attach-ticket random source is required")
	}
	raw := make([]byte, attachTicketBytes)
	if _, err := io.ReadFull(random, raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func ValidAttachTicket(value string) bool {
	if !attachTicketText.MatchString(value) {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(raw) == attachTicketBytes && base64.RawURLEncoding.EncodeToString(raw) == value
}

type Session struct {
	ID            string    `json:"id"`
	WorkspaceURI  string    `json:"workspaceUri"`
	WebSocketPath string    `json:"webSocketPath"`
	AttachTicket  string    `json:"attachTicket"`
	ExpiresAt     time.Time `json:"expiresAt"`
}

type Client interface {
	ReadMessage(context.Context) ([]byte, error)
	WriteMessage(context.Context, []byte) error
}

type Attachment interface {
	Bridge(context.Context, Client) error
	Close()
}

type FailureCode string

const (
	FailureCapacity        FailureCode = "lsp_capacity_exhausted"
	FailureStartup         FailureCode = "lsp_worker_startup_failed"
	FailureSessionNotFound FailureCode = "lsp_session_not_found"
	FailureSessionOwner    FailureCode = "lsp_session_owner_mismatch"
	FailureAlreadyAttached FailureCode = "lsp_session_already_attached"
	FailureProtocol        FailureCode = "lsp_protocol_rejected"
)

type Failure struct {
	Code FailureCode
	Err  error
}

func (failure *Failure) Error() string {
	if failure == nil || failure.Err == nil {
		return "LSP operation failed"
	}
	return failure.Err.Error()
}

func (failure *Failure) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.Err
}
