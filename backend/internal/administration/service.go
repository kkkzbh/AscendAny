package administration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

const maxAuditPayloadBytes = 64 << 10

var canonicalUUIDv4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type Repository interface {
	LoadAccounts(context.Context, AccountQuery) (AccountPage, error)
	LoadStudents(context.Context, StudentQuery) (StudentPage, error)
	LoadAudit(context.Context, AuditQuery) (AuditPage, error)
	SetAccountDisabled(context.Context, AccountStateCommand) (ManagedAccount, error)
}

type Service struct {
	repository Repository
}

func NewService(repository Repository) (*Service, error) {
	if repository == nil {
		return nil, adminError(ErrorInvalidConfiguration, "construct administration service", errors.New("repository is required"))
	}
	return &Service{repository: repository}, nil
}

func NewProductionService(repository Repository) (*Service, error) {
	return NewService(repository)
}

func (service *Service) ListAccounts(ctx context.Context, query AccountQuery) (AccountPage, error) {
	if err := validateAccountQuery(ctx, query); err != nil {
		return AccountPage{}, err
	}
	page, err := service.repository.LoadAccounts(ctx, query)
	if err != nil {
		return AccountPage{}, err
	}
	if err := validateAccountPage(page, query.Limit); err != nil {
		return AccountPage{}, adminError(ErrorStoredDataInvalid, "validate managed account page", err)
	}
	return page, nil
}

func (service *Service) ListStudents(ctx context.Context, query StudentQuery) (StudentPage, error) {
	if err := validateStudentQuery(ctx, query); err != nil {
		return StudentPage{}, err
	}
	page, err := service.repository.LoadStudents(ctx, query)
	if err != nil {
		return StudentPage{}, err
	}
	if err := validateStudentPage(page, query.Limit); err != nil {
		return StudentPage{}, adminError(ErrorStoredDataInvalid, "validate managed student page", err)
	}
	return page, nil
}

func (service *Service) ListAudit(ctx context.Context, query AuditQuery) (AuditPage, error) {
	if err := validateAuditQuery(ctx, query); err != nil {
		return AuditPage{}, err
	}
	page, err := service.repository.LoadAudit(ctx, query)
	if err != nil {
		return AuditPage{}, err
	}
	if err := validateAuditPage(page, query.Limit); err != nil {
		return AuditPage{}, adminError(ErrorStoredDataInvalid, "validate audit page", err)
	}
	return page, nil
}

func (service *Service) SetAccountDisabled(
	ctx context.Context,
	principal auth.AccessPrincipal,
	targetID string,
	disabled bool,
) (ManagedAccount, error) {
	if ctx == nil || !canonicalUUIDv4.MatchString(targetID) {
		return ManagedAccount{}, adminError(ErrorInvalidQuery, "validate managed account state command", errors.New("context and canonical target ID are required"))
	}
	if err := validateAdminPrincipal(principal); err != nil {
		return ManagedAccount{}, err
	}
	account, err := service.repository.SetAccountDisabled(ctx, AccountStateCommand{
		Principal: principal,
		TargetID:  targetID,
		Disabled:  disabled,
	})
	if err != nil {
		return ManagedAccount{}, err
	}
	if account.ID != targetID {
		return ManagedAccount{}, adminError(ErrorStoredDataInvalid, "validate managed account mutation", errors.New("repository returned a different account"))
	}
	if err := validateManagedAccount(account); err != nil {
		return ManagedAccount{}, adminError(ErrorStoredDataInvalid, "validate managed account mutation", err)
	}
	if (account.DisabledAt != nil) != disabled {
		return ManagedAccount{}, adminError(ErrorStoredDataInvalid, "validate managed account mutation", errors.New("repository returned the wrong disabled state"))
	}
	return account, nil
}

func validateAccountQuery(ctx context.Context, query AccountQuery) error {
	if ctx == nil || query.Limit < 1 || query.Limit > MaxPageSize {
		return adminError(ErrorInvalidQuery, "validate managed account query", errors.New("context and bounded limit are required"))
	}
	if err := validateAdminPrincipal(query.Principal); err != nil {
		return err
	}
	if query.Cursor != nil && !canonicalUUIDv4.MatchString(*query.Cursor) {
		return adminError(ErrorInvalidQuery, "validate managed account query", errors.New("cursor must be a canonical UUIDv4"))
	}
	return nil
}

func validateStudentQuery(ctx context.Context, query StudentQuery) error {
	if ctx == nil || query.Limit < 1 || query.Limit > MaxPageSize {
		return adminError(ErrorInvalidQuery, "validate managed student query", errors.New("context and bounded limit are required"))
	}
	if err := validateAdminPrincipal(query.Principal); err != nil {
		return err
	}
	if query.Cursor != nil {
		if _, err := DecodeStudentCursor(*query.Cursor); err != nil {
			return adminError(ErrorInvalidQuery, "validate managed student query", err)
		}
	}
	return nil
}

func validateAuditQuery(ctx context.Context, query AuditQuery) error {
	if ctx == nil || query.Limit < 1 || query.Limit > MaxPageSize {
		return adminError(ErrorInvalidQuery, "validate audit query", errors.New("context and bounded limit are required"))
	}
	if err := validateAdminPrincipal(query.Principal); err != nil {
		return err
	}
	if query.Cursor != nil {
		if _, err := parseAuditID(*query.Cursor); err != nil {
			return adminError(ErrorInvalidQuery, "validate audit query", err)
		}
	}
	return nil
}

func validateAdminPrincipal(principal auth.AccessPrincipal) error {
	if !canonicalUUIDv4.MatchString(principal.AccountID) || !canonicalUUIDv4.MatchString(principal.SessionID) ||
		!canonicalUUIDv4.MatchString(principal.JWTID) || principal.AuthRevision < 1 {
		return adminError(ErrorInvalidQuery, "validate administration principal", errors.New("canonical access principal is required"))
	}
	if principal.Role != auth.RoleAdmin {
		return adminError(ErrorPrincipalRejected, "authorize administration principal", errors.New("administrator role is required"))
	}
	return nil
}

func validateAccountPage(page AccountPage, limit int) error {
	if page.Items == nil || len(page.Items) > limit {
		return errors.New("managed account page is nil or oversized")
	}
	seen := make(map[string]struct{}, len(page.Items))
	var previous time.Time
	for index, account := range page.Items {
		if err := validateManagedAccount(account); err != nil {
			return err
		}
		if _, exists := seen[account.ID]; exists {
			return errors.New("managed account ID is duplicated")
		}
		seen[account.ID] = struct{}{}
		if index > 0 && account.CreatedAt.After(previous) {
			return errors.New("managed account page order is ascending")
		}
		previous = account.CreatedAt
	}
	if page.NextCursor != nil && (len(page.Items) == 0 || *page.NextCursor != page.Items[len(page.Items)-1].ID || !canonicalUUIDv4.MatchString(*page.NextCursor)) {
		return errors.New("managed account next cursor is invalid")
	}
	return nil
}

func validateManagedAccount(account ManagedAccount) error {
	if !canonicalUUIDv4.MatchString(account.ID) || strings.TrimSpace(account.Username) != account.Username || account.Username == "" ||
		strings.TrimSpace(account.DisplayName) != account.DisplayName || account.DisplayName == "" ||
		(account.Role != auth.RoleAdmin && account.Role != auth.RoleStudent) || account.AuthRevision < 1 ||
		!validUTCTime(account.CreatedAt) || !validUTCTime(account.UpdatedAt) || account.UpdatedAt.Before(account.CreatedAt) || account.ActiveSessionCount < 0 {
		return errors.New("managed account violates the public contract")
	}
	if account.DisabledAt != nil && (!validUTCTime(*account.DisabledAt) || account.DisabledAt.Before(account.CreatedAt) || account.ActiveSessionCount != 0) {
		return errors.New("managed account disabled state is invalid")
	}
	if account.Role == auth.RoleStudent {
		if account.StudentNumber == nil || validateStudentNumber(*account.StudentNumber) != nil {
			return errors.New("managed student account lacks a canonical student number")
		}
	} else if account.StudentNumber != nil {
		return errors.New("managed admin account owns a student number")
	}
	return nil
}

func validateStudentPage(page StudentPage, limit int) error {
	if page.Items == nil || len(page.Items) > limit {
		return errors.New("managed student page is nil or oversized")
	}
	seen := make(map[string]struct{}, len(page.Items))
	previous := ""
	for index, student := range page.Items {
		if err := validateManagedStudent(student); err != nil {
			return err
		}
		if _, exists := seen[student.StudentNumber]; exists {
			return errors.New("managed student number is duplicated")
		}
		seen[student.StudentNumber] = struct{}{}
		if index > 0 && student.StudentNumber <= previous {
			return errors.New("managed student page is not strictly ascending")
		}
		previous = student.StudentNumber
	}
	if page.NextCursor != nil {
		if len(page.Items) == 0 {
			return errors.New("empty student page has a next cursor")
		}
		studentNumber, err := DecodeStudentCursor(*page.NextCursor)
		if err != nil || studentNumber != page.Items[len(page.Items)-1].StudentNumber {
			return errors.New("managed student next cursor is invalid")
		}
	}
	return nil
}

func validateManagedStudent(student ManagedStudent) error {
	if validateStudentNumber(student.StudentNumber) != nil || strings.TrimSpace(student.PintiaUserID) == "" {
		return errors.New("managed student identity is invalid")
	}
	if student.SourceDisplayName != nil && strings.TrimSpace(*student.SourceDisplayName) == "" {
		return errors.New("managed student source display name is empty")
	}
	if student.Rating != nil && *student.Rating < 0 {
		return errors.New("managed student rating is negative")
	}
	if student.Account != nil {
		account := student.Account
		if !canonicalUUIDv4.MatchString(account.ID) || strings.TrimSpace(account.Username) == "" || strings.TrimSpace(account.DisplayName) == "" ||
			account.Username != strings.TrimSpace(account.Username) || account.DisplayName != strings.TrimSpace(account.DisplayName) {
			return errors.New("managed student account binding is invalid")
		}
		if account.DisabledAt != nil && !validUTCTime(*account.DisabledAt) {
			return errors.New("managed student account disabled time is invalid")
		}
	}
	return nil
}

func validateAuditPage(page AuditPage, limit int) error {
	if page.Items == nil || len(page.Items) > limit {
		return errors.New("audit page is nil or oversized")
	}
	var previous int64
	for index, event := range page.Items {
		identifier, err := parseAuditID(event.ID)
		if err != nil || index > 0 && identifier >= previous || strings.TrimSpace(event.Type) == "" || !validUTCTime(event.OccurredAt) {
			return errors.New("audit event violates ordering or identity contract")
		}
		previous = identifier
		if event.ActorAccountID != nil && !canonicalUUIDv4.MatchString(*event.ActorAccountID) || event.ActorSessionID != nil && !canonicalUUIDv4.MatchString(*event.ActorSessionID) {
			return errors.New("audit actor identity is invalid")
		}
		if event.ActorSessionID != nil && event.ActorAccountID == nil {
			return errors.New("audit session lacks an account")
		}
		if err := validateAuditPayload(event.Payload); err != nil {
			return err
		}
	}
	if page.NextCursor != nil && (len(page.Items) == 0 || *page.NextCursor != page.Items[len(page.Items)-1].ID) {
		return errors.New("audit next cursor is invalid")
	}
	return nil
}

func validateAuditPayload(payload json.RawMessage) error {
	if len(payload) < 2 || len(payload) > maxAuditPayloadBytes || !json.Valid(payload) {
		return errors.New("audit payload is invalid or oversized")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil || object == nil {
		return errors.New("audit payload must be an object")
	}
	return nil
}

func parseAuditID(value string) (int64, error) {
	if value == "" || value[0] == '0' || len(value) > 19 {
		return 0, errors.New("audit cursor must be a canonical positive int64")
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, errors.New("audit cursor must be decimal")
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 1 {
		return 0, errors.New("audit cursor must be a canonical positive int64")
	}
	return parsed, nil
}

func ValidAuditCursor(value string) bool {
	_, err := parseAuditID(value)
	return err == nil
}

func ValidAccountID(value string) bool {
	return canonicalUUIDv4.MatchString(value)
}

func validUTCTime(value time.Time) bool {
	if value.IsZero() {
		return false
	}
	_, offset := value.Zone()
	return offset == 0
}
