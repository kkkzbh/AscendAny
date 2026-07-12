package configuration

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

var (
	canonicalUUIDv4  = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	configurationKey = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)
	schemaIDPattern  = regexp.MustCompile(`^ascendany[.][a-z][a-z0-9_.-]{0,126}[.]v[1-9][0-9]*$`)
	sha256Pattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type Repository interface {
	LoadItems(context.Context, ListQuery) (ItemPage, error)
	LoadItem(context.Context, ItemQuery) (Item, bool, error)
	LoadVersions(context.Context, VersionsQuery) (VersionPage, bool, error)
	StoreVersion(context.Context, CreateVersionCommand, string) (CreateVersionResult, error)
}

type RecommendationDocumentValidator interface {
	ValidateRecommendationDocument(Kind, string, json.RawMessage) error
}

type Service struct {
	repository        Repository
	documentValidator RecommendationDocumentValidator
}

func NewService(repository Repository, documentValidator RecommendationDocumentValidator) (*Service, error) {
	if repository == nil || documentValidator == nil {
		return nil, configurationError(ErrorInvalidConfiguration, "construct configuration service", errors.New("repository and recommendation document validator are required"))
	}
	return &Service{repository: repository, documentValidator: documentValidator}, nil
}

func (service *Service) List(ctx context.Context, query ListQuery) (ItemPage, error) {
	if err := validateListQuery(ctx, query); err != nil {
		return ItemPage{}, err
	}
	page, err := service.repository.LoadItems(ctx, query)
	if err != nil {
		return ItemPage{}, err
	}
	if err := validateItemPage(page, query.Limit); err != nil {
		return ItemPage{}, configurationError(ErrorStoredDataInvalid, "validate configuration item page", err)
	}
	return page, nil
}

func (service *Service) Get(ctx context.Context, query ItemQuery) (Item, bool, error) {
	if err := validateItemQuery(ctx, query); err != nil {
		return Item{}, false, err
	}
	item, found, err := service.repository.LoadItem(ctx, query)
	if err != nil || !found {
		return Item{}, found, err
	}
	if item.Key != query.Key {
		return Item{}, false, configurationError(ErrorStoredDataInvalid, "validate configuration item", errors.New("repository returned a different key"))
	}
	if err := validateItem(item); err != nil {
		return Item{}, false, configurationError(ErrorStoredDataInvalid, "validate configuration item", err)
	}
	return item, true, nil
}

func (service *Service) ListVersions(ctx context.Context, query VersionsQuery) (VersionPage, bool, error) {
	if err := validateVersionsQuery(ctx, query); err != nil {
		return VersionPage{}, false, err
	}
	page, found, err := service.repository.LoadVersions(ctx, query)
	if err != nil || !found {
		return VersionPage{}, found, err
	}
	if err := validateVersionPage(page, query); err != nil {
		return VersionPage{}, false, configurationError(ErrorStoredDataInvalid, "validate configuration version page", err)
	}
	return page, true, nil
}

func (service *Service) CreateVersion(ctx context.Context, command CreateVersionCommand) (CreateVersionResult, error) {
	if err := validateCreateCommand(ctx, command); err != nil {
		return CreateVersionResult{}, err
	}
	canonical, digest, err := canonicalDocument(command.Document)
	if err != nil {
		return CreateVersionResult{}, configurationError(ErrorInvalidQuery, "canonicalize configuration document", err)
	}
	if err := rejectCredentialFields(command.Kind, canonical); err != nil {
		return CreateVersionResult{}, configurationError(ErrorInvalidQuery, "validate configuration secret boundary", err)
	}
	if command.Kind == KindTraining || command.Kind == KindKnowledgeCatalog {
		if err := service.documentValidator.ValidateRecommendationDocument(command.Kind, command.SchemaID, canonical); err != nil {
			return CreateVersionResult{}, configurationError(ErrorDocumentInvalid, "validate recommendation configuration document", err)
		}
	}
	command.Document = canonical
	result, err := service.repository.StoreVersion(ctx, command, digest)
	if err != nil {
		return CreateVersionResult{}, err
	}
	if result.Item.Key != command.Key || result.Item.Kind != command.Kind {
		return CreateVersionResult{}, configurationError(ErrorStoredDataInvalid, "validate stored configuration version", errors.New("repository returned a different item"))
	}
	if err := validateItem(result.Item); err != nil {
		return CreateVersionResult{}, configurationError(ErrorStoredDataInvalid, "validate stored configuration version", err)
	}
	active := result.Item.ActiveVersion
	if active == nil || subtle.ConstantTimeCompare([]byte(active.DocumentSHA256), []byte(digest)) != 1 ||
		active.SchemaID != command.SchemaID || !sameOptionalString(active.CredentialRef, command.CredentialRef) {
		return CreateVersionResult{}, configurationError(ErrorStoredDataInvalid, "validate stored configuration version", errors.New("active version differs from the requested immutable document"))
	}
	return result, nil
}

func validateListQuery(ctx context.Context, query ListQuery) error {
	if ctx == nil || query.Limit < 1 || query.Limit > MaxPageSize {
		return configurationError(ErrorInvalidQuery, "validate configuration list query", errors.New("context and bounded limit are required"))
	}
	if err := validateAdminPrincipal(query.Principal); err != nil {
		return err
	}
	if query.Kind != nil && !validKind(*query.Kind) {
		return configurationError(ErrorInvalidQuery, "validate configuration list query", errors.New("kind is invalid"))
	}
	if query.AfterKey != nil && !configurationKey.MatchString(*query.AfterKey) {
		return configurationError(ErrorInvalidQuery, "validate configuration list query", errors.New("cursor key is invalid"))
	}
	return nil
}

func validateItemQuery(ctx context.Context, query ItemQuery) error {
	if ctx == nil || !configurationKey.MatchString(query.Key) {
		return configurationError(ErrorInvalidQuery, "validate configuration item query", errors.New("context and canonical key are required"))
	}
	return validateAdminPrincipal(query.Principal)
}

func validateVersionsQuery(ctx context.Context, query VersionsQuery) error {
	if ctx == nil || !configurationKey.MatchString(query.Key) || query.Limit < 1 || query.Limit > MaxPageSize ||
		query.BeforeNumber != nil && *query.BeforeNumber < 2 {
		return configurationError(ErrorInvalidQuery, "validate configuration versions query", errors.New("context, canonical key, cursor, and bounded limit are required"))
	}
	return validateAdminPrincipal(query.Principal)
}

func validateCreateCommand(ctx context.Context, command CreateVersionCommand) error {
	if ctx == nil || !configurationKey.MatchString(command.Key) || !validKind(command.Kind) || command.ExpectedHeadRevision < 0 ||
		!validSchemaForKind(command.SchemaID, command.Kind) || !validCredentialRef(command.Kind, command.CredentialRef) {
		return configurationError(ErrorInvalidQuery, "validate create configuration version command", errors.New("configuration metadata violates the write contract"))
	}
	return validateAdminPrincipal(command.Principal)
}

func validateAdminPrincipal(principal auth.AccessPrincipal) error {
	if !canonicalUUIDv4.MatchString(principal.AccountID) || !canonicalUUIDv4.MatchString(principal.SessionID) ||
		!canonicalUUIDv4.MatchString(principal.JWTID) || principal.AuthRevision < 1 {
		return configurationError(ErrorInvalidQuery, "validate configuration principal", errors.New("canonical access principal is required"))
	}
	if principal.Role != auth.RoleAdmin {
		return configurationError(ErrorPrincipalRejected, "authorize configuration principal", errors.New("administrator role is required"))
	}
	return nil
}

func validKind(kind Kind) bool {
	switch kind {
	case KindPrompt, KindModelConnection, KindTraining, KindKnowledgeCatalog, KindFeedbackPolicy, KindFeedbackDelivery:
		return true
	default:
		return false
	}
}

func ValidKind(kind Kind) bool { return validKind(kind) }

func ValidKey(key string) bool { return configurationKey.MatchString(key) }

func validSchemaForKind(schemaID string, kind Kind) bool {
	if !schemaIDPattern.MatchString(schemaID) {
		return false
	}
	return strings.HasPrefix(schemaID, "ascendany."+string(kind)+".")
}

func validCredentialRef(kind Kind, credentialRef *string) bool {
	if credentialRef == nil {
		return true
	}
	if kind != KindModelConnection && kind != KindFeedbackDelivery {
		return false
	}
	return configurationKey.MatchString(*credentialRef)
}

func rejectCredentialFields(kind Kind, document json.RawMessage) error {
	if kind != KindModelConnection && kind != KindFeedbackDelivery {
		return nil
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	return inspectCredentialFields(value)
}

func inspectCredentialFields(value any) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			normalized := strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.ToLower(key))
			switch normalized {
			case "authorization", "headers", "httpheaders", "cookie", "cookies", "apikey", "apikeyenv", "accesstoken", "refreshtoken", "bearertoken", "password", "secret", "clientsecret", "privatekey", "credential", "credentials", "credentialref":
				return errors.New("credential-bearing fields are forbidden; use credentialRef metadata")
			}
			if err := inspectCredentialFields(nested); err != nil {
				return err
			}
		}
	case []any:
		for _, nested := range typed {
			if err := inspectCredentialFields(nested); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateItemPage(page ItemPage, limit int) error {
	if page.Items == nil || len(page.Items) > limit {
		return errors.New("item page is nil or oversized")
	}
	previous := ""
	for index, item := range page.Items {
		if err := validateItem(item); err != nil {
			return err
		}
		if index > 0 && item.Key <= previous {
			return errors.New("item page is not strictly ordered")
		}
		previous = item.Key
	}
	if page.NextCursor != nil && (len(page.Items) == 0 || *page.NextCursor != page.Items[len(page.Items)-1].Key) {
		return errors.New("item page cursor is invalid")
	}
	return nil
}

func validateItem(item Item) error {
	if !canonicalUUIDv4.MatchString(item.ID) || !configurationKey.MatchString(item.Key) || !validKind(item.Kind) ||
		item.HeadRevision < 0 || !validUTCTime(item.CreatedAt) || !validUTCTime(item.UpdatedAt) || item.UpdatedAt.Before(item.CreatedAt) {
		return errors.New("configuration item violates the public contract")
	}
	if item.HeadRevision == 0 {
		if item.ActiveVersion != nil {
			return errors.New("empty configuration item has an active version")
		}
		return nil
	}
	if item.ActiveVersion == nil {
		return errors.New("configuration item lacks an active version")
	}
	if item.ActiveVersion.Number != item.HeadRevision {
		return errors.New("configuration active version and head revision differ")
	}
	return validateVersion(*item.ActiveVersion, item.Kind)
}

func validateVersionPage(page VersionPage, query VersionsQuery) error {
	if page.Key != query.Key || !configurationKey.MatchString(page.Key) || !validKind(page.Kind) || page.HeadRevision < 1 ||
		page.Items == nil || len(page.Items) > query.Limit {
		return errors.New("version page metadata is invalid")
	}
	var previous int64
	for index, version := range page.Items {
		if err := validateVersion(version, page.Kind); err != nil {
			return err
		}
		if index > 0 && version.Number >= previous {
			return errors.New("version page is not strictly descending")
		}
		if query.BeforeNumber != nil && version.Number >= *query.BeforeNumber {
			return errors.New("version page ignored its cursor")
		}
		previous = version.Number
	}
	if page.NextBeforeNumber != nil && (len(page.Items) == 0 || *page.NextBeforeNumber != page.Items[len(page.Items)-1].Number) {
		return errors.New("version page cursor is invalid")
	}
	return nil
}

func validateVersion(version Version, kind Kind) error {
	versionID, err := strconv.ParseInt(version.ID, 10, 64)
	if err != nil || versionID <= 0 || strconv.FormatInt(versionID, 10) != version.ID ||
		version.Number < 1 || !validSchemaForKind(version.SchemaID, kind) || !sha256Pattern.MatchString(version.DocumentSHA256) ||
		!validCredentialRef(kind, version.CredentialRef) || !canonicalUUIDv4.MatchString(version.CreatedByAccountID) ||
		!canonicalUUIDv4.MatchString(version.CreatedBySessionID) || !validUTCTime(version.CreatedAt) {
		return errors.New("configuration version metadata is invalid")
	}
	canonical, digest, err := canonicalDocument(version.Document)
	if err != nil || !bytes.Equal(canonical, version.Document) || subtle.ConstantTimeCompare([]byte(digest), []byte(version.DocumentSHA256)) != 1 {
		return errors.New("configuration version document hash is invalid")
	}
	if err := rejectCredentialFields(kind, canonical); err != nil {
		return errors.New("configuration version violates the secret boundary")
	}
	version.Document = canonical
	return nil
}

func sameOptionalString(left, right *string) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func validUTCTime(value time.Time) bool {
	if value.IsZero() {
		return false
	}
	_, offset := value.Zone()
	return offset == 0
}
