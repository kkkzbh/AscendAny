package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/browserorigin"
	"github.com/kkkzbh/AscendAny/backend/internal/inferencemodel"
	"github.com/kkkzbh/AscendAny/backend/internal/workerlease"
)

const minimumJWTSecretBytes = 32
const minimumPasswordPepperBytes = 32
const minimumDatabasePasswordBytes = 16

type LookupEnv func(string) (string, bool)
type ReadFile func(string) ([]byte, error)

type Config struct {
	HTTP           HTTPConfig
	Database       DatabaseConfig
	Auth           AuthConfig
	Artifact       ArtifactConfig
	Pintia         PintiaConfig
	Import         ImportConfig
	Analytics      AnalyticsConfig
	Feedback       FeedbackConfig
	ChatAgent      ChatAgentConfig
	Recommendation RecommendationConfig
	Judge          JudgeConfig
	LSP            LSPConfig
	Write          WriteConfig
	Log            LogConfig
}

type HTTPConfig struct {
	Address           string
	TrustedProxyCIDRs []netip.Prefix
	ClientIPHeader    string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	AuthBodyTimeout   time.Duration
	UploadBodyTimeout time.Duration
	SSEMaxDuration    time.Duration
	SSEReauthInterval time.Duration
	SSEWriteTimeout   time.Duration
	MaxActiveSSE      int
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
}

type DatabaseConfig struct {
	URL                   string
	Password              string
	ExpectedSchemaVersion int64
	MaxConnections        int32
	MinConnections        int32
	ConnectTimeout        time.Duration
	MaxConnectionLifetime time.Duration
	MaxConnectionIdleTime time.Duration
	HealthTimeout         time.Duration
}

type AuthConfig struct {
	JWTSigningKey  string
	PasswordPepper string
	Issuer         string
	Audience       string
	AllowedOrigins []string
	AccessTTL      time.Duration
	RefreshTTL     time.Duration
}

type ArtifactConfig struct {
	Root              string
	MaxBytes          int64
	OrphanMinAge      time.Duration
	ReconcileInterval time.Duration
}

type PintiaConfig struct {
	MaxTotalNodes               int64
	MaxTotalStringBytes         int64
	MaxJSONDepth                int
	MaxStringBytes              int
	MaxProblems                 int
	MaxParticipants             int
	MaxProblemResultsPerRanking int
	MaxSubmissions              int
	MaxCaseResultsPerSubmission int
	MaxCodeBytes                int
}

type ImportConfig struct {
	WorkerOwner   string
	LeaseDuration time.Duration
	RetryDelay    time.Duration
	PollInterval  time.Duration
}

type AnalyticsConfig struct {
	ConfigPath    string
	WorkerOwner   string
	LeaseDuration time.Duration
	PollInterval  time.Duration
}

type FeedbackConfig struct {
	RateWindow               time.Duration
	RateMaximum              int
	DeliveryConfigurationKey string
	WorkerOwner              string
	LeaseDuration            time.Duration
	RetryDelay               time.Duration
	PollInterval             time.Duration
}

type ChatAgentConfig struct {
	WorkerOwner         string
	LeaseDuration       time.Duration
	PollInterval        time.Duration
	MaximumContextItems int
	MaximumToolRounds   int
}

type RecommendationConfig struct {
	ModelPath    string
	ModelSHA256  string
	ModelPurpose inferencemodel.Purpose
}

type JudgeConfig struct {
	SocketDirectory string
	WorkerUser      string
	SystemctlPath   string
	StartupTimeout  time.Duration
	SessionTimeout  time.Duration
	StopTimeout     time.Duration
	WorkerOwner     string
	LeaseDuration   time.Duration
	RetryDelay      time.Duration
	PollInterval    time.Duration
	MaximumAttempts int32
}

type LSPConfig struct {
	ControlSocket            string
	WorkerUser               string
	SystemctlPath            string
	MaximumSessions          int
	MaximumPendingHandshakes int
	HandshakeTimeout         time.Duration
	StartupTimeout           time.Duration
	StopTimeout              time.Duration
}

type WriteConfig struct {
	Enabled bool
}

type LogConfig struct {
	Level string
}

func Load(lookup LookupEnv, readFile ReadFile) (Config, error) {
	if lookup == nil {
		return Config{}, errors.New("environment lookup is required")
	}
	if readFile == nil {
		return Config{}, errors.New("secret file reader is required")
	}

	databaseURL, err := requiredTrimmed(lookup, "ASCENDANY_DATABASE_URL")
	if err != nil {
		return Config{}, err
	}
	if err := validateDatabaseURL(databaseURL); err != nil {
		return Config{}, fmt.Errorf("ASCENDANY_DATABASE_URL: %w", err)
	}
	poolMode, err := requiredTrimmed(lookup, "ASCENDANY_DATABASE_POOL_MODE")
	if err != nil {
		return Config{}, err
	}
	if poolMode != "transaction" {
		return Config{}, errors.New("ASCENDANY_DATABASE_POOL_MODE must be transaction")
	}

	databasePassword, err := loadSecret(
		lookup,
		readFile,
		"ASCENDANY_DATABASE_PASSWORD_FILE",
		minimumDatabasePasswordBytes,
	)
	if err != nil {
		return Config{}, err
	}

	jwtSigningKey, err := loadSecret(
		lookup,
		readFile,
		"ASCENDANY_JWT_SIGNING_KEY_FILE",
		minimumJWTSecretBytes,
	)
	if err != nil {
		return Config{}, err
	}
	passwordPepper, err := loadSecret(
		lookup,
		readFile,
		"ASCENDANY_PASSWORD_PEPPER_FILE",
		minimumPasswordPepperBytes,
	)
	if err != nil {
		return Config{}, err
	}
	issuer, err := requiredTrimmed(lookup, "ASCENDANY_AUTH_ISSUER")
	if err != nil {
		return Config{}, err
	}
	audience, err := requiredTrimmed(lookup, "ASCENDANY_AUTH_AUDIENCE")
	if err != nil {
		return Config{}, err
	}
	allowedOrigins, err := requiredAllowedOrigins(lookup, "ASCENDANY_AUTH_ALLOWED_ORIGINS")
	if err != nil {
		return Config{}, err
	}
	accessTTL, err := requiredPositiveDuration(lookup, "ASCENDANY_AUTH_ACCESS_TTL")
	if err != nil {
		return Config{}, err
	}
	refreshTTL, err := requiredPositiveDuration(lookup, "ASCENDANY_AUTH_REFRESH_TTL")
	if err != nil {
		return Config{}, err
	}
	if accessTTL >= refreshTTL {
		return Config{}, errors.New("ASCENDANY_AUTH_ACCESS_TTL must be shorter than ASCENDANY_AUTH_REFRESH_TTL")
	}

	expectedSchemaVersion, err := requiredPositiveInt64(lookup, "ASCENDANY_DATABASE_SCHEMA_VERSION")
	if err != nil {
		return Config{}, err
	}

	artifactRoot, err := requiredTrimmed(lookup, "ASCENDANY_ARTIFACT_ROOT")
	if err != nil {
		return Config{}, err
	}
	if err := validateArtifactRoot(artifactRoot); err != nil {
		return Config{}, fmt.Errorf("ASCENDANY_ARTIFACT_ROOT: %w", err)
	}
	artifactMaxBytes, err := requiredPositiveInt64(lookup, "ASCENDANY_ARTIFACT_MAX_BYTES")
	if err != nil {
		return Config{}, err
	}
	artifactOrphanMinAge, err := requiredPositiveDuration(lookup, "ASCENDANY_ARTIFACT_ORPHAN_MIN_AGE")
	if err != nil {
		return Config{}, err
	}
	artifactReconcileInterval, err := requiredPositiveDuration(lookup, "ASCENDANY_ARTIFACT_RECONCILE_INTERVAL")
	if err != nil {
		return Config{}, err
	}
	maxTotalNodes, err := requiredPositiveInt64(lookup, "ASCENDANY_PINTIA_MAX_TOTAL_NODES")
	if err != nil {
		return Config{}, err
	}
	maxTotalStringBytes, err := requiredPositiveInt64(lookup, "ASCENDANY_PINTIA_MAX_TOTAL_STRING_BYTES")
	if err != nil {
		return Config{}, err
	}
	if maxTotalStringBytes > artifactMaxBytes {
		return Config{}, errors.New("ASCENDANY_PINTIA_MAX_TOTAL_STRING_BYTES must not exceed ASCENDANY_ARTIFACT_MAX_BYTES")
	}
	maxJSONDepth, err := requiredPositivePlatformLimit(lookup, "ASCENDANY_PINTIA_MAX_JSON_DEPTH")
	if err != nil {
		return Config{}, err
	}
	if maxJSONDepth > 128 {
		return Config{}, errors.New("ASCENDANY_PINTIA_MAX_JSON_DEPTH must not exceed 128")
	}
	maxStringBytes, err := requiredPositivePlatformLimit(lookup, "ASCENDANY_PINTIA_MAX_STRING_BYTES")
	if err != nil {
		return Config{}, err
	}
	if int64(maxStringBytes) > maxTotalStringBytes {
		return Config{}, errors.New("ASCENDANY_PINTIA_MAX_STRING_BYTES must not exceed ASCENDANY_PINTIA_MAX_TOTAL_STRING_BYTES")
	}
	maxProblems, err := requiredPositivePlatformLimit(lookup, "ASCENDANY_PINTIA_MAX_PROBLEMS")
	if err != nil {
		return Config{}, err
	}
	maxParticipants, err := requiredPositivePlatformLimit(lookup, "ASCENDANY_PINTIA_MAX_PARTICIPANTS")
	if err != nil {
		return Config{}, err
	}
	maxProblemResultsPerRanking, err := requiredPositivePlatformLimit(lookup, "ASCENDANY_PINTIA_MAX_PROBLEM_RESULTS_PER_RANKING")
	if err != nil {
		return Config{}, err
	}
	maxSubmissions, err := requiredPositivePlatformLimit(lookup, "ASCENDANY_PINTIA_MAX_SUBMISSIONS")
	if err != nil {
		return Config{}, err
	}
	maxCaseResultsPerSubmission, err := requiredPositivePlatformLimit(lookup, "ASCENDANY_PINTIA_MAX_CASE_RESULTS_PER_SUBMISSION")
	if err != nil {
		return Config{}, err
	}
	maxCodeBytes, err := requiredPositivePlatformLimit(lookup, "ASCENDANY_PINTIA_MAX_CODE_BYTES")
	if err != nil {
		return Config{}, err
	}
	if maxCodeBytes > maxStringBytes {
		return Config{}, errors.New("ASCENDANY_PINTIA_MAX_CODE_BYTES must not exceed ASCENDANY_PINTIA_MAX_STRING_BYTES")
	}
	collectionLimits := []struct {
		name  string
		value int
	}{
		{"ASCENDANY_PINTIA_MAX_PROBLEMS", maxProblems},
		{"ASCENDANY_PINTIA_MAX_PARTICIPANTS", maxParticipants},
		{"ASCENDANY_PINTIA_MAX_PROBLEM_RESULTS_PER_RANKING", maxProblemResultsPerRanking},
		{"ASCENDANY_PINTIA_MAX_SUBMISSIONS", maxSubmissions},
		{"ASCENDANY_PINTIA_MAX_CASE_RESULTS_PER_SUBMISSION", maxCaseResultsPerSubmission},
	}
	for _, limit := range collectionLimits {
		if int64(limit.value) > maxTotalNodes {
			return Config{}, fmt.Errorf("%s must not exceed ASCENDANY_PINTIA_MAX_TOTAL_NODES", limit.name)
		}
	}
	importOwner, importLease, importRetry, importPoll, err := loadWorkerConfig(lookup, "IMPORT")
	if err != nil {
		return Config{}, err
	}
	analyticsPath, err := requiredTrimmed(lookup, "ASCENDANY_ANALYTICS_CONFIG")
	if err != nil {
		return Config{}, err
	}
	if err := validateAbsoluteFilePath(analyticsPath); err != nil {
		return Config{}, fmt.Errorf("ASCENDANY_ANALYTICS_CONFIG: %w", err)
	}
	analyticsOwner, analyticsLease, analyticsPoll, err := loadLeaseWorkerConfig(lookup, "ANALYTICS")
	if err != nil {
		return Config{}, err
	}
	feedbackRateWindow, err := requiredPositiveDuration(lookup, "ASCENDANY_FEEDBACK_RATE_WINDOW")
	if err != nil {
		return Config{}, err
	}
	if feedbackRateWindow < time.Second {
		return Config{}, errors.New("ASCENDANY_FEEDBACK_RATE_WINDOW must be at least one second")
	}
	feedbackRateMaximum, err := requiredPositiveInt64(lookup, "ASCENDANY_FEEDBACK_RATE_MAXIMUM")
	if err != nil {
		return Config{}, err
	}
	if feedbackRateMaximum > 1000 {
		return Config{}, errors.New("ASCENDANY_FEEDBACK_RATE_MAXIMUM must not exceed 1000")
	}
	feedbackDeliveryKey, err := requiredTrimmed(lookup, "ASCENDANY_FEEDBACK_DELIVERY_CONFIGURATION_KEY")
	if err != nil {
		return Config{}, err
	}
	if !validConfigurationKey(feedbackDeliveryKey) {
		return Config{}, errors.New("ASCENDANY_FEEDBACK_DELIVERY_CONFIGURATION_KEY must be a canonical configuration key")
	}
	feedbackOwner, feedbackLease, feedbackRetry, feedbackPoll, err := loadWorkerConfig(lookup, "FEEDBACK")
	if err != nil {
		return Config{}, err
	}
	if feedbackRetry < time.Second {
		return Config{}, errors.New("ASCENDANY_FEEDBACK_RETRY_DELAY must be at least one second")
	}
	chatAgentOwner, chatAgentLease, chatAgentPoll, err := loadLeaseWorkerConfig(lookup, "CHAT_AGENT")
	if err != nil {
		return Config{}, err
	}
	chatAgentMaximumContextItems, err := requiredPositiveInt64(lookup, "ASCENDANY_CHAT_AGENT_MAXIMUM_CONTEXT_ITEMS")
	if err != nil {
		return Config{}, err
	}
	if chatAgentMaximumContextItems > 1000 {
		return Config{}, errors.New("ASCENDANY_CHAT_AGENT_MAXIMUM_CONTEXT_ITEMS must not exceed 1000")
	}
	chatAgentMaximumToolRounds, err := requiredPositiveInt64(lookup, "ASCENDANY_CHAT_AGENT_MAXIMUM_TOOL_ROUNDS")
	if err != nil {
		return Config{}, err
	}
	if chatAgentMaximumToolRounds > 64 {
		return Config{}, errors.New("ASCENDANY_CHAT_AGENT_MAXIMUM_TOOL_ROUNDS must not exceed 64")
	}
	recommendationModelPath, err := requiredTrimmed(lookup, "ASCENDANY_RECOMMENDATION_MODEL_PATH")
	if err != nil {
		return Config{}, err
	}
	if err := validateAbsoluteFilePath(recommendationModelPath); err != nil {
		return Config{}, fmt.Errorf("ASCENDANY_RECOMMENDATION_MODEL_PATH: %w", err)
	}
	recommendationModelSHA256, err := requiredLowercaseSHA256(lookup, "ASCENDANY_RECOMMENDATION_MODEL_SHA256")
	if err != nil {
		return Config{}, err
	}
	recommendationModelPurposeValue, err := requiredTrimmed(lookup, "ASCENDANY_RECOMMENDATION_MODEL_PURPOSE")
	if err != nil {
		return Config{}, err
	}
	recommendationModelPurpose, err := inferencemodel.ParsePurpose(recommendationModelPurposeValue)
	if err != nil {
		return Config{}, fmt.Errorf("ASCENDANY_RECOMMENDATION_MODEL_PURPOSE: %w", err)
	}
	judgeSocketDirectory, err := requiredTrimmed(lookup, "ASCENDANY_JUDGE_SOCKET_DIRECTORY")
	if err != nil {
		return Config{}, err
	}
	if err := validateAbsoluteDirectoryPath(judgeSocketDirectory); err != nil {
		return Config{}, fmt.Errorf("ASCENDANY_JUDGE_SOCKET_DIRECTORY: %w", err)
	}
	if len(filepath.Join(judgeSocketDirectory, "11111111-1111-4111-8111-111111111111.sock")) > 107 {
		return Config{}, errors.New("ASCENDANY_JUDGE_SOCKET_DIRECTORY exceeds the Unix socket path limit")
	}
	judgeWorkerUser, err := requiredSystemUser(lookup, "ASCENDANY_JUDGE_WORKER_USER")
	if err != nil {
		return Config{}, err
	}
	judgeSystemctlPath, err := requiredTrimmed(lookup, "ASCENDANY_JUDGE_SYSTEMCTL_PATH")
	if err != nil {
		return Config{}, err
	}
	if err := validateAbsoluteFilePath(judgeSystemctlPath); err != nil {
		return Config{}, fmt.Errorf("ASCENDANY_JUDGE_SYSTEMCTL_PATH: %w", err)
	}
	judgeStartupTimeout, err := requiredBoundedDuration(lookup, "ASCENDANY_JUDGE_STARTUP_TIMEOUT", time.Second, time.Minute)
	if err != nil {
		return Config{}, err
	}
	judgeSessionTimeout, err := requiredBoundedDuration(lookup, "ASCENDANY_JUDGE_SESSION_TIMEOUT", time.Second, time.Hour)
	if err != nil {
		return Config{}, err
	}
	judgeStopTimeout, err := requiredBoundedDuration(lookup, "ASCENDANY_JUDGE_STOP_TIMEOUT", time.Second, time.Minute)
	if err != nil {
		return Config{}, err
	}
	judgeOwner, judgeLease, judgeRetry, judgePoll, err := loadWorkerConfig(lookup, "JUDGE")
	if err != nil {
		return Config{}, err
	}
	if judgeRetry < time.Second {
		return Config{}, errors.New("ASCENDANY_JUDGE_RETRY_DELAY must be at least one second")
	}
	judgeMaximumAttempts, err := requiredPositiveInt64(lookup, "ASCENDANY_JUDGE_MAXIMUM_ATTEMPTS")
	if err != nil {
		return Config{}, err
	}
	if judgeMaximumAttempts > 100 {
		return Config{}, errors.New("ASCENDANY_JUDGE_MAXIMUM_ATTEMPTS must not exceed 100")
	}
	lspControlSocket, err := requiredTrimmed(lookup, "ASCENDANY_LSP_CONTROL_SOCKET")
	if err != nil {
		return Config{}, err
	}
	if err := validateAbsoluteFilePath(lspControlSocket); err != nil {
		return Config{}, fmt.Errorf("ASCENDANY_LSP_CONTROL_SOCKET: %w", err)
	}
	if len(lspControlSocket) > 107 {
		return Config{}, errors.New("ASCENDANY_LSP_CONTROL_SOCKET exceeds the Unix socket path limit")
	}
	lspWorkerUser, err := requiredSystemUser(lookup, "ASCENDANY_LSP_WORKER_USER")
	if err != nil {
		return Config{}, err
	}
	lspSystemctlPath, err := requiredTrimmed(lookup, "ASCENDANY_LSP_SYSTEMCTL_PATH")
	if err != nil {
		return Config{}, err
	}
	if err := validateAbsoluteFilePath(lspSystemctlPath); err != nil {
		return Config{}, fmt.Errorf("ASCENDANY_LSP_SYSTEMCTL_PATH: %w", err)
	}
	lspMaximumSessions, err := requiredPositiveInt64(lookup, "ASCENDANY_LSP_MAXIMUM_SESSIONS")
	if err != nil {
		return Config{}, err
	}
	if lspMaximumSessions > 10_000 {
		return Config{}, errors.New("ASCENDANY_LSP_MAXIMUM_SESSIONS must not exceed 10000")
	}
	lspMaximumPendingHandshakes, err := requiredPositiveInt64(lookup, "ASCENDANY_LSP_MAXIMUM_PENDING_HANDSHAKES")
	if err != nil {
		return Config{}, err
	}
	if lspMaximumPendingHandshakes > 1_024 {
		return Config{}, errors.New("ASCENDANY_LSP_MAXIMUM_PENDING_HANDSHAKES must not exceed 1024")
	}
	lspHandshakeTimeout, err := requiredBoundedDuration(lookup, "ASCENDANY_LSP_HANDSHAKE_TIMEOUT", time.Second, time.Minute)
	if err != nil {
		return Config{}, err
	}
	lspStartupTimeout, err := requiredBoundedDuration(lookup, "ASCENDANY_LSP_STARTUP_TIMEOUT", time.Second, time.Minute)
	if err != nil {
		return Config{}, err
	}
	lspStopTimeout, err := requiredBoundedDuration(lookup, "ASCENDANY_LSP_STOP_TIMEOUT", time.Second, time.Minute)
	if err != nil {
		return Config{}, err
	}

	writeMode, err := requiredTrimmed(lookup, "ASCENDANY_WRITE_MODE")
	if err != nil {
		return Config{}, err
	}
	if writeMode != "disabled" && writeMode != "enabled" {
		return Config{}, errors.New("ASCENDANY_WRITE_MODE must be disabled or enabled")
	}

	httpAddress := optionalTrimmed(lookup, "ASCENDANY_HTTP_LISTEN", "127.0.0.1:8080")
	if err := validateListenAddress(httpAddress); err != nil {
		return Config{}, fmt.Errorf("ASCENDANY_HTTP_LISTEN: %w", err)
	}
	trustedProxyCIDRs, err := requiredTrustedProxyCIDRs(lookup, "ASCENDANY_HTTP_TRUSTED_PROXY_CIDRS")
	if err != nil {
		return Config{}, err
	}
	if err := validateProxyBoundary(httpAddress, trustedProxyCIDRs); err != nil {
		return Config{}, fmt.Errorf("ASCENDANY_HTTP_TRUSTED_PROXY_CIDRS: %w", err)
	}
	clientIPHeader, err := requiredTrimmed(lookup, "ASCENDANY_HTTP_CLIENT_IP_HEADER")
	if err != nil {
		return Config{}, err
	}
	if clientIPHeader != "CF-Connecting-IP" {
		return Config{}, errors.New("ASCENDANY_HTTP_CLIENT_IP_HEADER must be CF-Connecting-IP")
	}

	readHeaderTimeout, err := optionalPositiveDuration(lookup, "ASCENDANY_HTTP_READ_HEADER_TIMEOUT", 5*time.Second)
	if err != nil {
		return Config{}, err
	}
	readTimeout, err := optionalPositiveDuration(lookup, "ASCENDANY_HTTP_READ_TIMEOUT", 10*time.Minute+30*time.Second)
	if err != nil {
		return Config{}, err
	}
	authBodyTimeout, err := optionalPositiveDuration(lookup, "ASCENDANY_HTTP_AUTH_BODY_TIMEOUT", 15*time.Second)
	if err != nil {
		return Config{}, err
	}
	uploadBodyTimeout, err := optionalPositiveDuration(lookup, "ASCENDANY_HTTP_UPLOAD_BODY_TIMEOUT", 10*time.Minute)
	if err != nil {
		return Config{}, err
	}
	if authBodyTimeout >= readTimeout || uploadBodyTimeout >= readTimeout {
		return Config{}, errors.New("ASCENDANY_HTTP_READ_TIMEOUT must exceed all route body timeouts")
	}
	sseMaxDuration, err := optionalPositiveDuration(lookup, "ASCENDANY_HTTP_SSE_MAX_DURATION", 15*time.Minute)
	if err != nil {
		return Config{}, err
	}
	sseReauthInterval, err := optionalPositiveDuration(lookup, "ASCENDANY_HTTP_SSE_REAUTH_INTERVAL", 15*time.Second)
	if err != nil {
		return Config{}, err
	}
	if sseReauthInterval >= sseMaxDuration {
		return Config{}, errors.New("ASCENDANY_HTTP_SSE_REAUTH_INTERVAL must be shorter than ASCENDANY_HTTP_SSE_MAX_DURATION")
	}
	sseWriteTimeout, err := optionalPositiveDuration(lookup, "ASCENDANY_HTTP_SSE_WRITE_TIMEOUT", 5*time.Second)
	if err != nil {
		return Config{}, err
	}
	if sseWriteTimeout > sseReauthInterval {
		return Config{}, errors.New("ASCENDANY_HTTP_SSE_WRITE_TIMEOUT must not exceed ASCENDANY_HTTP_SSE_REAUTH_INTERVAL")
	}
	maxActiveSSEValue, err := optionalPositiveInt32(lookup, "ASCENDANY_HTTP_MAX_ACTIVE_SSE", 64)
	if err != nil {
		return Config{}, err
	}
	idleTimeout, err := optionalPositiveDuration(lookup, "ASCENDANY_HTTP_IDLE_TIMEOUT", 60*time.Second)
	if err != nil {
		return Config{}, err
	}
	shutdownTimeout, err := optionalPositiveDuration(lookup, "ASCENDANY_HTTP_SHUTDOWN_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, err
	}

	maxConnections, err := optionalPositiveInt32(lookup, "ASCENDANY_DATABASE_MAX_CONNECTIONS", 20)
	if err != nil {
		return Config{}, err
	}
	minConnections, err := optionalNonNegativeInt32(lookup, "ASCENDANY_DATABASE_MIN_CONNECTIONS", 2)
	if err != nil {
		return Config{}, err
	}
	if minConnections > maxConnections {
		return Config{}, errors.New("ASCENDANY_DATABASE_MIN_CONNECTIONS must not exceed ASCENDANY_DATABASE_MAX_CONNECTIONS")
	}
	connectTimeout, err := optionalPositiveDuration(lookup, "ASCENDANY_DATABASE_CONNECT_TIMEOUT", 5*time.Second)
	if err != nil {
		return Config{}, err
	}
	maxConnectionLifetime, err := optionalPositiveDuration(lookup, "ASCENDANY_DATABASE_MAX_CONNECTION_LIFETIME", 30*time.Minute)
	if err != nil {
		return Config{}, err
	}
	maxConnectionIdleTime, err := optionalPositiveDuration(lookup, "ASCENDANY_DATABASE_MAX_CONNECTION_IDLE_TIME", 5*time.Minute)
	if err != nil {
		return Config{}, err
	}
	healthTimeout, err := optionalPositiveDuration(lookup, "ASCENDANY_DATABASE_HEALTH_TIMEOUT", 3*time.Second)
	if err != nil {
		return Config{}, err
	}

	logLevel := strings.ToLower(optionalTrimmed(lookup, "ASCENDANY_LOG_LEVEL", "info"))
	if !isValidLogLevel(logLevel) {
		return Config{}, fmt.Errorf("ASCENDANY_LOG_LEVEL must be one of debug, info, warn, error")
	}

	return Config{
		HTTP: HTTPConfig{
			Address:           httpAddress,
			TrustedProxyCIDRs: trustedProxyCIDRs,
			ClientIPHeader:    clientIPHeader,
			ReadHeaderTimeout: readHeaderTimeout,
			ReadTimeout:       readTimeout,
			AuthBodyTimeout:   authBodyTimeout,
			UploadBodyTimeout: uploadBodyTimeout,
			SSEMaxDuration:    sseMaxDuration,
			SSEReauthInterval: sseReauthInterval,
			SSEWriteTimeout:   sseWriteTimeout,
			MaxActiveSSE:      int(maxActiveSSEValue),
			IdleTimeout:       idleTimeout,
			ShutdownTimeout:   shutdownTimeout,
		},
		Database: DatabaseConfig{
			URL:                   databaseURL,
			Password:              databasePassword,
			ExpectedSchemaVersion: expectedSchemaVersion,
			MaxConnections:        maxConnections,
			MinConnections:        minConnections,
			ConnectTimeout:        connectTimeout,
			MaxConnectionLifetime: maxConnectionLifetime,
			MaxConnectionIdleTime: maxConnectionIdleTime,
			HealthTimeout:         healthTimeout,
		},
		Auth: AuthConfig{
			JWTSigningKey:  jwtSigningKey,
			PasswordPepper: passwordPepper,
			Issuer:         issuer,
			Audience:       audience,
			AllowedOrigins: allowedOrigins,
			AccessTTL:      accessTTL,
			RefreshTTL:     refreshTTL,
		},
		Artifact: ArtifactConfig{
			Root:              artifactRoot,
			MaxBytes:          artifactMaxBytes,
			OrphanMinAge:      artifactOrphanMinAge,
			ReconcileInterval: artifactReconcileInterval,
		},
		Pintia: PintiaConfig{
			MaxTotalNodes:               maxTotalNodes,
			MaxTotalStringBytes:         maxTotalStringBytes,
			MaxJSONDepth:                maxJSONDepth,
			MaxStringBytes:              maxStringBytes,
			MaxProblems:                 maxProblems,
			MaxParticipants:             maxParticipants,
			MaxProblemResultsPerRanking: maxProblemResultsPerRanking,
			MaxSubmissions:              maxSubmissions,
			MaxCaseResultsPerSubmission: maxCaseResultsPerSubmission,
			MaxCodeBytes:                maxCodeBytes,
		},
		Import: ImportConfig{
			WorkerOwner:   importOwner,
			LeaseDuration: importLease,
			RetryDelay:    importRetry,
			PollInterval:  importPoll,
		},
		Analytics: AnalyticsConfig{
			ConfigPath:    analyticsPath,
			WorkerOwner:   analyticsOwner,
			LeaseDuration: analyticsLease,
			PollInterval:  analyticsPoll,
		},
		Feedback: FeedbackConfig{
			RateWindow:               feedbackRateWindow,
			RateMaximum:              int(feedbackRateMaximum),
			DeliveryConfigurationKey: feedbackDeliveryKey,
			WorkerOwner:              feedbackOwner,
			LeaseDuration:            feedbackLease,
			RetryDelay:               feedbackRetry,
			PollInterval:             feedbackPoll,
		},
		ChatAgent: ChatAgentConfig{
			WorkerOwner:         chatAgentOwner,
			LeaseDuration:       chatAgentLease,
			PollInterval:        chatAgentPoll,
			MaximumContextItems: int(chatAgentMaximumContextItems),
			MaximumToolRounds:   int(chatAgentMaximumToolRounds),
		},
		Recommendation: RecommendationConfig{
			ModelPath:    recommendationModelPath,
			ModelSHA256:  recommendationModelSHA256,
			ModelPurpose: recommendationModelPurpose,
		},
		Judge: JudgeConfig{
			SocketDirectory: judgeSocketDirectory,
			WorkerUser:      judgeWorkerUser,
			SystemctlPath:   judgeSystemctlPath,
			StartupTimeout:  judgeStartupTimeout,
			SessionTimeout:  judgeSessionTimeout,
			StopTimeout:     judgeStopTimeout,
			WorkerOwner:     judgeOwner,
			LeaseDuration:   judgeLease,
			RetryDelay:      judgeRetry,
			PollInterval:    judgePoll,
			MaximumAttempts: int32(judgeMaximumAttempts),
		},
		LSP: LSPConfig{
			ControlSocket:            lspControlSocket,
			WorkerUser:               lspWorkerUser,
			SystemctlPath:            lspSystemctlPath,
			MaximumSessions:          int(lspMaximumSessions),
			MaximumPendingHandshakes: int(lspMaximumPendingHandshakes),
			HandshakeTimeout:         lspHandshakeTimeout,
			StartupTimeout:           lspStartupTimeout,
			StopTimeout:              lspStopTimeout,
		},
		Write: WriteConfig{
			Enabled: writeMode == "enabled",
		},
		Log: LogConfig{Level: logLevel},
	}, nil
}

func validConfigurationKey(value string) bool {
	if len(value) < 1 || len(value) > 128 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for index := 1; index < len(value); index++ {
		character := value[index]
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' ||
			character == '_' || character == '.' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func requiredLowercaseSHA256(lookup LookupEnv, name string) (string, error) {
	value, err := requiredTrimmed(lookup, name)
	if err != nil {
		return "", err
	}
	if len(value) != 64 {
		return "", fmt.Errorf("%s must be a lowercase SHA-256", name)
	}
	for _, character := range value {
		if character >= '0' && character <= '9' || character >= 'a' && character <= 'f' {
			continue
		}
		return "", fmt.Errorf("%s must be a lowercase SHA-256", name)
	}
	return value, nil
}

func requiredSystemUser(lookup LookupEnv, name string) (string, error) {
	value, err := requiredTrimmed(lookup, name)
	if err != nil {
		return "", err
	}
	if len(value) > 32 || !((value[0] >= 'a' && value[0] <= 'z') || value[0] == '_') || value[len(value)-1] == '-' {
		return "", fmt.Errorf("%s must be a canonical system user name", name)
	}
	for index := 1; index < len(value); index++ {
		character := value[index]
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') ||
			character == '_' || character == '-' {
			continue
		}
		return "", fmt.Errorf("%s must be a canonical system user name", name)
	}
	return value, nil
}

func requiredTrimmed(lookup LookupEnv, name string) (string, error) {
	value, ok := lookup(name)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return strings.TrimSpace(value), nil
}

func requiredTrustedProxyCIDRs(lookup LookupEnv, name string) ([]netip.Prefix, error) {
	raw, err := requiredTrimmed(lookup, name)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(raw, ",")
	if len(parts) > 16 {
		return nil, fmt.Errorf("%s must contain at most 16 canonical CIDR prefixes", name)
	}
	result := make([]netip.Prefix, 0, len(parts))
	seen := make(map[netip.Prefix]struct{}, len(parts))
	for _, part := range parts {
		prefix, parseErr := netip.ParsePrefix(part)
		if parseErr != nil || prefix.String() != part || prefix.Masked() != prefix {
			return nil, fmt.Errorf("%s must contain comma-separated canonical masked CIDR prefixes without spaces", name)
		}
		if _, exists := seen[prefix]; exists {
			return nil, fmt.Errorf("%s contains duplicate prefix %s", name, prefix)
		}
		seen[prefix] = struct{}{}
		result = append(result, prefix)
	}
	return result, nil
}

func loadSecret(lookup LookupEnv, readFile ReadFile, envName string, minimumBytes int) (string, error) {
	path, err := requiredTrimmed(lookup, envName)
	if err != nil {
		return "", err
	}
	data, err := readFile(path)
	if err != nil {
		return "", fmt.Errorf("%s cannot be read", envName)
	}
	if len(data) < minimumBytes {
		return "", fmt.Errorf("%s must reference at least %d bytes", envName, minimumBytes)
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return "", fmt.Errorf("%s must not contain NUL bytes", envName)
	}
	if !bytes.Equal(data, bytes.TrimSpace(data)) {
		return "", fmt.Errorf("%s content must not start or end with whitespace", envName)
	}
	return string(data), nil
}

func optionalTrimmed(lookup LookupEnv, name, fallback string) string {
	value, ok := lookup(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func requiredPositiveInt64(lookup LookupEnv, name string) (int64, error) {
	value, err := requiredTrimmed(lookup, name)
	if err != nil {
		return 0, err
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive base-10 integer", name)
	}
	return parsed, nil
}

func optionalPositiveInt32(lookup LookupEnv, name string, fallback int32) (int32, error) {
	return optionalInt32(lookup, name, fallback, false)
}

func optionalNonNegativeInt32(lookup LookupEnv, name string, fallback int32) (int32, error) {
	return optionalInt32(lookup, name, fallback, true)
}

func optionalInt32(lookup LookupEnv, name string, fallback int32, allowZero bool) (int32, error) {
	value, ok := lookup(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 32)
	if err != nil || parsed < 0 || (!allowZero && parsed == 0) {
		qualifier := "positive"
		if allowZero {
			qualifier = "non-negative"
		}
		return 0, fmt.Errorf("%s must be a %s base-10 integer", name, qualifier)
	}
	return int32(parsed), nil
}

func optionalPositiveDuration(lookup LookupEnv, name string, fallback time.Duration) (time.Duration, error) {
	value, ok := lookup(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive Go duration", name)
	}
	return parsed, nil
}

func requiredPositiveDuration(lookup LookupEnv, name string) (time.Duration, error) {
	value, err := requiredTrimmed(lookup, name)
	if err != nil {
		return 0, err
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive Go duration", name)
	}
	return parsed, nil
}

func requiredBoundedDuration(lookup LookupEnv, name string, minimum, maximum time.Duration) (time.Duration, error) {
	value, err := requiredPositiveDuration(lookup, name)
	if err != nil {
		return 0, err
	}
	if value < minimum || value > maximum {
		return 0, fmt.Errorf("%s must be between %s and %s", name, minimum, maximum)
	}
	return value, nil
}

func requiredPositivePlatformLimit(lookup LookupEnv, name string) (int, error) {
	value, err := requiredPositiveInt64(lookup, name)
	if err != nil {
		return 0, err
	}
	if value > int64(^uint(0)>>1) {
		return 0, fmt.Errorf("%s exceeds the platform integer range", name)
	}
	return int(value), nil
}

func loadWorkerConfig(lookup LookupEnv, prefix string) (string, time.Duration, time.Duration, time.Duration, error) {
	owner, lease, poll, err := loadLeaseWorkerConfig(lookup, prefix)
	if err != nil {
		return "", 0, 0, 0, err
	}
	base := "ASCENDANY_" + prefix + "_"
	retry, err := requiredPositiveDuration(lookup, base+"RETRY_DELAY")
	if err != nil {
		return "", 0, 0, 0, err
	}
	return owner, lease, retry, poll, nil
}

func loadLeaseWorkerConfig(lookup LookupEnv, prefix string) (string, time.Duration, time.Duration, error) {
	base := "ASCENDANY_" + prefix + "_"
	owner, err := requiredTrimmed(lookup, base+"WORKER_OWNER")
	if err != nil {
		return "", 0, 0, err
	}
	if len(owner) > 128 || strings.IndexFunc(owner, func(value rune) bool {
		return !(value >= 'a' && value <= 'z') &&
			!(value >= 'A' && value <= 'Z') &&
			!(value >= '0' && value <= '9') &&
			value != '.' && value != '_' && value != ':' && value != '-'
	}) >= 0 {
		return "", 0, 0, fmt.Errorf("%sWORKER_OWNER must use 1 to 128 ASCII letters, digits, dot, underscore, colon, or hyphen", base)
	}
	lease, err := requiredPositiveDuration(lookup, base+"LEASE_DURATION")
	if err != nil {
		return "", 0, 0, err
	}
	if _, err := workerlease.ValidateDuration(lease); err != nil {
		return "", 0, 0, fmt.Errorf("%sLEASE_DURATION: %w", base, err)
	}
	poll, err := requiredPositiveDuration(lookup, base+"POLL_INTERVAL")
	if err != nil {
		return "", 0, 0, err
	}
	if poll >= lease {
		return "", 0, 0, fmt.Errorf("%sPOLL_INTERVAL must be shorter than %sLEASE_DURATION", base, base)
	}
	return owner, lease, poll, nil
}

func validateDatabaseURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return errors.New("must be a valid PostgreSQL URL")
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return errors.New("scheme must be postgres or postgresql")
	}
	if parsed.Hostname() == "" {
		return errors.New("host is required")
	}
	if parsed.User == nil || parsed.User.Username() == "" {
		return errors.New("user is required")
	}
	if _, hasPassword := parsed.User.Password(); hasPassword {
		return errors.New("password must be supplied through ASCENDANY_DATABASE_PASSWORD_FILE")
	}
	if parsed.Query().Has("password") {
		return errors.New("password query parameter is forbidden")
	}
	if strings.Trim(parsed.Path, "/") == "" {
		return errors.New("database name is required")
	}
	return nil
}

func requiredAllowedOrigins(lookup LookupEnv, name string) ([]string, error) {
	raw, err := requiredTrimmed(lookup, name)
	if err != nil {
		return nil, err
	}
	origins, err := browserorigin.ParseList(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return origins, nil
}

func validateListenAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("must use host:port form")
	}
	parsedHost, err := netip.ParseAddr(host)
	if err != nil || (parsedHost != netip.MustParseAddr("127.0.0.1") && parsedHost != netip.IPv6Loopback()) {
		return errors.New("must bind one explicit loopback address")
	}
	parsedPort, err := strconv.ParseUint(port, 10, 16)
	if err != nil || parsedPort == 0 {
		return errors.New("port must be between 1 and 65535")
	}
	if net.JoinHostPort(parsedHost.String(), strconv.FormatUint(parsedPort, 10)) != address {
		return errors.New("must use canonical loopback host:port form")
	}
	return nil
}

func validateProxyBoundary(listenAddress string, trusted []netip.Prefix) error {
	host, _, err := net.SplitHostPort(listenAddress)
	if err != nil {
		return errors.New("cannot derive trusted proxy from listen address")
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return errors.New("cannot derive trusted proxy from listen address")
	}
	expected := netip.PrefixFrom(address, address.BitLen())
	if len(trusted) != 1 || trusted[0] != expected {
		return fmt.Errorf("must contain only the active loopback listener %s", expected)
	}
	return nil
}

func validateArtifactRoot(root string) error {
	return validateAbsoluteDirectoryPath(root)
}

func validateAbsoluteDirectoryPath(root string) error {
	if !filepath.IsAbs(root) {
		return errors.New("must be an absolute path")
	}
	if filepath.Clean(root) != root {
		return errors.New("must be a normalized path")
	}
	if root == string(filepath.Separator) {
		return errors.New("filesystem root is forbidden")
	}
	return nil
}

func validateAbsoluteFilePath(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("must be an absolute path")
	}
	if filepath.Clean(path) != path {
		return errors.New("must be a normalized path")
	}
	if path == string(filepath.Separator) {
		return errors.New("must identify a file below the filesystem root")
	}
	return nil
}

func isValidLogLevel(level string) bool {
	switch level {
	case "debug", "info", "warn", "error":
		return true
	default:
		return false
	}
}
