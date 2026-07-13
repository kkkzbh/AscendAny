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
	"github.com/kkkzbh/AscendAny/backend/internal/releaseidentity"
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
	CreateCatalogPublicationAuthorization(context.Context, CreateCatalogPublicationAuthorizationCommand, string) (CatalogPublicationAuthorizationRecord, error)
}

func (service *Service) CreateCatalogPublicationAuthorization(
	ctx context.Context,
	command CreateCatalogPublicationAuthorizationCommand,
) (CatalogPublicationAuthorizationResult, error) {
	if ctx == nil || !sha256Pattern.MatchString(command.AccessTokenSHA256) ||
		!validCatalogPublicationIntent(command.PublicationIntent) ||
		!validUTCTime(command.Principal.ExpiresAt) {
		return CatalogPublicationAuthorizationResult{}, configurationError(
			ErrorInvalidQuery,
			"validate catalog publication authorization",
			errors.New("context, release intent, and access-token expiry are required"),
		)
	}
	if err := validateAdminPrincipal(command.Principal); err != nil {
		return CatalogPublicationAuthorizationResult{}, err
	}
	canonical, digest, err := canonicalDocument(command.Document)
	if err != nil {
		return CatalogPublicationAuthorizationResult{}, configurationError(ErrorInvalidQuery, "canonicalize catalog publication authorization document", err)
	}
	if subtle.ConstantTimeCompare([]byte(command.PublicationIntent.TargetCatalogSHA256), []byte(digest)) != 1 {
		return CatalogPublicationAuthorizationResult{}, configurationError(
			ErrorInvalidQuery,
			"bind catalog publication authorization document",
			errors.New("target catalog SHA-256 differs from the canonical document"),
		)
	}
	if err := service.documentValidator.ValidateRecommendationDocument(
		KindKnowledgeCatalog,
		KnowledgeCatalogSchemaID,
		canonical,
	); err != nil {
		return CatalogPublicationAuthorizationResult{}, configurationError(ErrorDocumentInvalid, "validate catalog publication authorization document", err)
	}
	command.Document = canonical
	record, err := service.repository.CreateCatalogPublicationAuthorization(ctx, command, digest)
	if err != nil {
		return CatalogPublicationAuthorizationResult{}, err
	}
	canonicalRequest, canonicalRequestErr := CanonicalCatalogPublicationRequest(record.PublicationRequest)
	parsedRequest, parseRequestErr := ParseCatalogPublicationRequest(canonicalRequest)
	if !canonicalUUIDv4.MatchString(record.AuthorizationID) || !record.ExpiresAt.Equal(command.Principal.ExpiresAt) ||
		!validUTCTime(record.ExpiresAt) || canonicalRequestErr != nil || parseRequestErr != nil ||
		record.PublicationRequest.AuthorizationID != record.AuthorizationID ||
		record.PublicationRequest.CatalogPublicationIntent != command.PublicationIntent ||
		parsedRequest != record.PublicationRequest {
		return CatalogPublicationAuthorizationResult{}, configurationError(
			ErrorStoredDataInvalid,
			"validate catalog publication authorization result",
			errors.New("repository returned different immutable authorization provenance"),
		)
	}
	return CatalogPublicationAuthorizationResult{
		AuthorizationID:    record.AuthorizationID,
		ExpiresAt:          record.ExpiresAt,
		PublicationRequest: record.PublicationRequest,
	}, nil
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
	if command.Kind == KindKnowledgeCatalog {
		if subtle.ConstantTimeCompare([]byte(command.TargetCatalogSHA256), []byte(digest)) != 1 {
			return CreateVersionResult{}, configurationError(
				ErrorInvalidQuery,
				"bind knowledge catalog document",
				errors.New("target catalog SHA-256 differs from the canonical document"),
			)
		}
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
		!validSchemaForKind(command.SchemaID, command.Kind) || !validCredentialRef(command.Kind, command.CredentialRef) ||
		!validKnowledgeCatalogIdentity(command.Key, command.Kind) || !validAnalyticsReviewExpectation(command) {
		return configurationError(ErrorInvalidQuery, "validate create configuration version command", errors.New("configuration metadata violates the write contract"))
	}
	return validateAdminPrincipal(command.Principal)
}

func validAnalyticsReviewExpectation(command CreateVersionCommand) bool {
	valuesPresent := command.PublicationAuthorizationID != "" || command.PublicationAccessTokenSHA256 != "" ||
		len(command.PublicationAuthorizationRequest) != 0 || command.ExpectedAnalyticsGenerationID != nil ||
		command.ExpectedAnalyticsHeadRevision != nil || command.ExpectedInputManifestSHA256 != nil ||
		command.ExpectedCurrentModelHeadRevision != nil || command.ExpectedCurrentModelArtifactSHA256 != nil ||
		command.TargetCatalogSHA256 != "" || command.TargetModelID != "" || command.TargetModelArtifactSHA256 != "" ||
		command.TargetApplicationVersion != "" || command.TargetApplicationCommit != "" || command.TargetApplicationBuildTime != ""
	if command.Kind != KindKnowledgeCatalog {
		return !valuesPresent
	}
	if command.ExpectedAnalyticsGenerationID == nil || command.ExpectedAnalyticsHeadRevision == nil ||
		command.ExpectedInputManifestSHA256 == nil || command.ExpectedCurrentModelHeadRevision == nil ||
		command.ExpectedCurrentModelArtifactSHA256 == nil || *command.ExpectedCurrentModelHeadRevision < 1 ||
		!sha256Pattern.MatchString(*command.ExpectedCurrentModelArtifactSHA256) ||
		!canonicalUUIDv4.MatchString(command.PublicationAuthorizationID) ||
		!sha256Pattern.MatchString(command.PublicationAccessTokenSHA256) ||
		!catalogPublicationRequestMatchesCommand(command) ||
		!sha256Pattern.MatchString(command.TargetCatalogSHA256) || !canonicalUUIDv4.MatchString(command.TargetModelID) ||
		!sha256Pattern.MatchString(command.TargetModelArtifactSHA256) ||
		!validApplicationIdentity(command.TargetApplicationVersion, command.TargetApplicationCommit, command.TargetApplicationBuildTime) {
		return false
	}
	return validAnalyticsReviewAnchors(
		*command.ExpectedAnalyticsGenerationID,
		*command.ExpectedAnalyticsHeadRevision,
		*command.ExpectedInputManifestSHA256,
	)
}

func validCatalogPublicationIntent(intent CatalogPublicationIntent) bool {
	if intent.Schema != CatalogPublicationRequestSchema ||
		ValidateKnowledgeCatalogPublicationExpectation(
			intent.ExpectedConfigurationHeadRevision,
			intent.ExpectedAnalyticsGenerationID,
			intent.ExpectedAnalyticsHeadRevision,
			intent.ExpectedInputManifestSHA256,
			intent.ExpectedCurrentModelHeadRevision,
			intent.ExpectedCurrentModelArtifactSHA256,
		) != nil ||
		!sha256Pattern.MatchString(intent.TargetCatalogSHA256) ||
		!sha256Pattern.MatchString(intent.TargetModelArtifactSHA256) {
		return false
	}
	return validApplicationIdentity(
		intent.TargetApplicationVersion,
		intent.TargetApplicationCommit,
		intent.TargetApplicationBuildTime,
	)
}

func catalogPublicationRequestMatchesCommand(command CreateVersionCommand) bool {
	request, err := ParseCatalogPublicationRequest(command.PublicationAuthorizationRequest)
	if err != nil || request.AuthorizationID != command.PublicationAuthorizationID {
		return false
	}
	return request.CatalogPublicationIntent == (CatalogPublicationIntent{
		Schema:                             CatalogPublicationRequestSchema,
		ExpectedConfigurationHeadRevision:  command.ExpectedHeadRevision,
		ExpectedAnalyticsGenerationID:      *command.ExpectedAnalyticsGenerationID,
		ExpectedAnalyticsHeadRevision:      *command.ExpectedAnalyticsHeadRevision,
		ExpectedInputManifestSHA256:        *command.ExpectedInputManifestSHA256,
		ExpectedCurrentModelHeadRevision:   *command.ExpectedCurrentModelHeadRevision,
		ExpectedCurrentModelArtifactSHA256: *command.ExpectedCurrentModelArtifactSHA256,
		TargetCatalogSHA256:                command.TargetCatalogSHA256,
		TargetModelArtifactSHA256:          command.TargetModelArtifactSHA256,
		TargetApplicationVersion:           command.TargetApplicationVersion,
		TargetApplicationCommit:            command.TargetApplicationCommit,
		TargetApplicationBuildTime:         command.TargetApplicationBuildTime,
	})
}

func validApplicationIdentity(version, commit, buildTime string) bool {
	return releaseidentity.Validate(version, commit, buildTime) == nil
}

func validAnalyticsReviewAnchors(generationID string, headRevision int64, inputManifestSHA256 string) bool {
	if headRevision < 1 || !sha256Pattern.MatchString(inputManifestSHA256) {
		return false
	}
	return validPositiveInt64String(generationID)
}

func validPositiveInt64String(raw string) bool {
	if raw == "" || raw[0] == '0' || len(raw) > 19 {
		return false
	}
	for _, character := range raw {
		if character < '0' || character > '9' {
			return false
		}
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	return err == nil && value > 0
}

// ValidateKnowledgeCatalogPublicationExpectation validates the complete CAS
// and analytics-review anchors required by an offline catalog publication.
func ValidateKnowledgeCatalogPublicationExpectation(
	expectedConfigurationHeadRevision int64,
	expectedAnalyticsGenerationID string,
	expectedAnalyticsHeadRevision int64,
	expectedInputManifestSHA256 string,
	expectedCurrentModelHeadRevision int64,
	expectedCurrentModelArtifactSHA256 string,
) error {
	if expectedConfigurationHeadRevision < 0 || expectedCurrentModelHeadRevision < 1 ||
		!sha256Pattern.MatchString(expectedCurrentModelArtifactSHA256) || !validAnalyticsReviewAnchors(
		expectedAnalyticsGenerationID,
		expectedAnalyticsHeadRevision,
		expectedInputManifestSHA256,
	) {
		return errors.New("catalog publication expectation is invalid")
	}
	return nil
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
	case KindPrompt, KindModelConnection, KindKnowledgeCatalog, KindFeedbackPolicy, KindFeedbackDelivery:
		return true
	default:
		return false
	}
}

func ValidKind(kind Kind) bool { return validKind(kind) }

func ValidKey(key string) bool { return configurationKey.MatchString(key) }

func validKnowledgeCatalogIdentity(key string, kind Kind) bool {
	return (kind == KindKnowledgeCatalog) == (key == KnowledgeCatalogKey)
}

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
		!validKnowledgeCatalogIdentity(item.Key, item.Kind) ||
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
		!validKnowledgeCatalogIdentity(page.Key, page.Kind) || page.Items == nil || len(page.Items) > query.Limit {
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
