package modelprobe

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kkkzbh/AscendAny/backend/internal/chatagent"
	"github.com/kkkzbh/AscendAny/backend/internal/configuration"
	"github.com/kkkzbh/AscendAny/backend/internal/credential"
)

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type ConfigurationReader interface {
	Get(context.Context, string, string) (configuration.Item, bool, error)
}

type Provider interface {
	ProbeModelConnection(context.Context, chatagent.ConfigurationSnapshot) (chatagent.ModelConnectionProbeResult, error)
}

type Clock func() time.Time

type Service struct {
	configurations ConfigurationReader
	provider       Provider
	clock          Clock
}

func NewService(configurations ConfigurationReader, provider Provider) (*Service, error) {
	return newService(configurations, provider, time.Now)
}

func newService(configurations ConfigurationReader, provider Provider, clock Clock) (*Service, error) {
	if configurations == nil || provider == nil || clock == nil {
		return nil, modelProbeError(ErrorInvalidConfiguration, "construct model probe service", errors.New("configuration reader, provider, and clock are required"))
	}
	return &Service{configurations: configurations, provider: provider, clock: clock}, nil
}

func (service *Service) Test(
	ctx context.Context,
	accessToken string,
	configurationKey string,
) (Result, error) {
	if ctx == nil || strings.TrimSpace(accessToken) == "" || !configuration.ValidKey(configurationKey) {
		return Result{}, modelProbeError(ErrorInvalidInput, "validate model probe request", errors.New("context, access token, and canonical configuration key are required"))
	}
	item, found, err := service.configurations.Get(ctx, accessToken, configurationKey)
	if err != nil {
		return Result{}, err
	}
	if !found {
		return Result{}, modelProbeError(ErrorConfigurationMissing, "load model connection configuration", errors.New("configuration was not found"))
	}
	if item.Key != configurationKey || item.Kind != configuration.KindModelConnection {
		return Result{}, modelProbeError(ErrorConfigurationKind, "load model connection configuration", errors.New("configuration key is not owned by model_connection"))
	}
	version := item.ActiveVersion
	if item.HeadRevision < 1 || version == nil || version.Number != item.HeadRevision ||
		!sha256Pattern.MatchString(version.DocumentSHA256) {
		return Result{}, modelProbeError(ErrorStoredDataInvalid, "validate model connection configuration", errors.New("active immutable configuration version is inconsistent"))
	}
	probe, err := service.provider.ProbeModelConnection(ctx, chatagent.ConfigurationSnapshot{
		Key:            item.Key,
		SchemaID:       version.SchemaID,
		Document:       append([]byte(nil), version.Document...),
		DocumentSHA256: version.DocumentSHA256,
		CredentialRef:  cloneOptionalString(version.CredentialRef),
	})
	if err != nil {
		return Result{}, modelProbeError(ErrorProviderRejected, "probe model connection", err)
	}
	checkedAt := service.clock().UTC()
	if checkedAt.IsZero() || probe.LatencyMilliseconds < 0 || !credential.ValidAuthority(probe.Authority) ||
		strings.TrimSpace(probe.Model) != probe.Model || probe.Model == "" || len(probe.Model) > 256 ||
		!utf8.ValidString(probe.Model) {
		return Result{}, modelProbeError(ErrorStoredDataInvalid, "validate model probe result", errors.New("provider returned invalid public probe metadata"))
	}
	return Result{
		ConfigurationKey:          item.Key,
		ConfigurationHeadRevision: item.HeadRevision,
		ConfigurationVersion:      version.Number,
		ConfigurationSHA256:       version.DocumentSHA256,
		Authority:                 probe.Authority,
		Model:                     probe.Model,
		CheckedAt:                 checkedAt,
		LatencyMilliseconds:       probe.LatencyMilliseconds,
	}, nil
}

func cloneOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	owned := *value
	return &owned
}
