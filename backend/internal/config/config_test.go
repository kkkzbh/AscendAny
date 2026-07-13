package config

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

const (
	databasePasswordPath = "/run/credentials/ascendany/db_password"
	jwtSigningKeyPath    = "/run/credentials/ascendany/jwt_signing_key"
	passwordPepperPath   = "/run/credentials/ascendany/password_pepper"
)

func TestLoadRequiresSecurityAndDatabaseSettings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "database URL",
			env:  map[string]string{},
			want: "ASCENDANY_DATABASE_URL is required",
		},
		{
			name: "database pool mode",
			env: map[string]string{
				"ASCENDANY_DATABASE_URL": "postgres://ascendany@127.0.0.1:6432/ascendany",
			},
			want: "ASCENDANY_DATABASE_POOL_MODE is required",
		},
		{
			name: "database password file",
			env: map[string]string{
				"ASCENDANY_DATABASE_URL":       "postgres://ascendany@127.0.0.1:6432/ascendany",
				"ASCENDANY_DATABASE_POOL_MODE": "transaction",
			},
			want: "ASCENDANY_DATABASE_PASSWORD_FILE is required",
		},
		{
			name: "JWT signing key file",
			env: map[string]string{
				"ASCENDANY_DATABASE_URL":           "postgres://ascendany@127.0.0.1:6432/ascendany",
				"ASCENDANY_DATABASE_POOL_MODE":     "transaction",
				"ASCENDANY_DATABASE_PASSWORD_FILE": databasePasswordPath,
			},
			want: "ASCENDANY_JWT_SIGNING_KEY_FILE is required",
		},
		{
			name: "password pepper file",
			env: func() map[string]string {
				values := validEnvironment()
				delete(values, "ASCENDANY_PASSWORD_PEPPER_FILE")
				return values
			}(),
			want: "ASCENDANY_PASSWORD_PEPPER_FILE is required",
		},
		{
			name: "auth issuer",
			env: func() map[string]string {
				values := validEnvironment()
				delete(values, "ASCENDANY_AUTH_ISSUER")
				return values
			}(),
			want: "ASCENDANY_AUTH_ISSUER is required",
		},
		{
			name: "auth audience",
			env: func() map[string]string {
				values := validEnvironment()
				delete(values, "ASCENDANY_AUTH_AUDIENCE")
				return values
			}(),
			want: "ASCENDANY_AUTH_AUDIENCE is required",
		},
		{
			name: "access lifetime",
			env: func() map[string]string {
				values := validEnvironment()
				delete(values, "ASCENDANY_AUTH_ACCESS_TTL")
				return values
			}(),
			want: "ASCENDANY_AUTH_ACCESS_TTL is required",
		},
		{
			name: "allowed origins",
			env: func() map[string]string {
				values := validEnvironment()
				delete(values, "ASCENDANY_AUTH_ALLOWED_ORIGINS")
				return values
			}(),
			want: "ASCENDANY_AUTH_ALLOWED_ORIGINS is required",
		},
		{
			name: "refresh lifetime",
			env: func() map[string]string {
				values := validEnvironment()
				delete(values, "ASCENDANY_AUTH_REFRESH_TTL")
				return values
			}(),
			want: "ASCENDANY_AUTH_REFRESH_TTL is required",
		},
		{
			name: "schema version",
			env: func() map[string]string {
				values := validEnvironment()
				delete(values, "ASCENDANY_DATABASE_SCHEMA_VERSION")
				return values
			}(),
			want: "ASCENDANY_DATABASE_SCHEMA_VERSION is required",
		},
		{
			name: "artifact root",
			env: func() map[string]string {
				values := validEnvironment()
				delete(values, "ASCENDANY_ARTIFACT_ROOT")
				return values
			}(),
			want: "ASCENDANY_ARTIFACT_ROOT is required",
		},
		{
			name: "write mode",
			env: func() map[string]string {
				values := validEnvironment()
				delete(values, "ASCENDANY_WRITE_MODE")
				return values
			}(),
			want: "ASCENDANY_WRITE_MODE is required",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := Load(mapLookup(test.env), testReadFile)
			if err == nil || err.Error() != test.want {
				t.Fatalf("Load() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadRejectsWeakJWTSigningKey(t *testing.T) {
	t.Parallel()

	env := validEnvironment()
	readFile := func(path string) ([]byte, error) {
		if path == jwtSigningKeyPath {
			return []byte("development-secret"), nil
		}
		return testReadFile(path)
	}

	_, err := Load(mapLookup(env), readFile)
	if err == nil || !strings.Contains(err.Error(), "at least 32 bytes") {
		t.Fatalf("Load() error = %v, want weak-secret error", err)
	}
}

func TestLoadRejectsWeakPasswordPepper(t *testing.T) {
	t.Parallel()

	env := validEnvironment()
	readFile := func(path string) ([]byte, error) {
		if path == passwordPepperPath {
			return []byte("development-pepper"), nil
		}
		return testReadFile(path)
	}

	_, err := Load(mapLookup(env), readFile)
	if err == nil || !strings.Contains(err.Error(), "at least 32 bytes") {
		t.Fatalf("Load() error = %v, want weak-pepper error", err)
	}
}

func TestLoadAdminBootstrapRequiresOnlyOwnedConfiguration(t *testing.T) {
	t.Parallel()
	env := map[string]string{
		"ASCENDANY_DATABASE_URL":             "postgres://ascendany@127.0.0.1:6432/ascendany",
		"ASCENDANY_DATABASE_POOL_MODE":       "transaction",
		"ASCENDANY_DATABASE_PASSWORD_FILE":   databasePasswordPath,
		"ASCENDANY_DATABASE_SCHEMA_VERSION":  "6",
		"ASCENDANY_PASSWORD_PEPPER_FILE":     passwordPepperPath,
		"ASCENDANY_DATABASE_MIN_CONNECTIONS": "0",
		"ASCENDANY_DATABASE_MAX_CONNECTIONS": "4",
		"ASCENDANY_DATABASE_HEALTH_TIMEOUT":  "750ms",
	}
	got, err := LoadAdminBootstrap(mapLookup(env), testReadFile)
	if err != nil {
		t.Fatalf("LoadAdminBootstrap() error = %v", err)
	}
	if got.Database.ExpectedSchemaVersion != 6 || got.Database.MinConnections != 0 ||
		got.Database.MaxConnections != 4 || got.Database.HealthTimeout != 750*time.Millisecond {
		t.Fatalf("bootstrap database config = %#v", got.Database)
	}
	if got.PasswordPepper != strings.Repeat("p", minimumPasswordPepperBytes) {
		t.Fatal("bootstrap password pepper was not loaded")
	}
}

func TestLoadAdminBootstrapRequiresPasswordPepper(t *testing.T) {
	t.Parallel()
	env := map[string]string{
		"ASCENDANY_DATABASE_URL":            "postgres://ascendany@127.0.0.1:6432/ascendany",
		"ASCENDANY_DATABASE_POOL_MODE":      "transaction",
		"ASCENDANY_DATABASE_PASSWORD_FILE":  databasePasswordPath,
		"ASCENDANY_DATABASE_SCHEMA_VERSION": "6",
	}
	_, err := LoadAdminBootstrap(mapLookup(env), testReadFile)
	if err == nil || err.Error() != "ASCENDANY_PASSWORD_PEPPER_FILE is required" {
		t.Fatalf("LoadAdminBootstrap() error = %v", err)
	}
}

func TestLoadModelActivationRequiresOnlyOwnedConfiguration(t *testing.T) {
	t.Parallel()
	env := map[string]string{
		"ASCENDANY_DATABASE_URL":                      "postgres://ascendany@127.0.0.1:6432/ascendany",
		"ASCENDANY_DATABASE_POOL_MODE":                "transaction",
		"ASCENDANY_DATABASE_PASSWORD_FILE":            databasePasswordPath,
		"ASCENDANY_DATABASE_SCHEMA_VERSION":           "6",
		"ASCENDANY_DATABASE_CONNECT_TIMEOUT":          "4s",
		"ASCENDANY_DATABASE_HEALTH_TIMEOUT":           "750ms",
		"ASCENDANY_RECOMMENDATION_MODEL_PATH":         "/opt/ascendany/v2/models/recommendation-model.json",
		"ASCENDANY_RECOMMENDATION_MODEL_SHA256":       strings.Repeat("a", 64),
		"ASCENDANY_RECOMMENDATION_MODEL_PURPOSE":      "production",
		"ASCENDANY_JWT_SIGNING_KEY_FILE":              "/must/not/be/read",
		"ASCENDANY_PASSWORD_PEPPER_FILE":              "/must/not/be/read",
		"ASCENDANY_IMPORT_WORKER_OWNER":               "must-not-be-read",
		"ASCENDANY_DATABASE_MAX_CONNECTIONS":          "999",
		"ASCENDANY_DATABASE_MIN_CONNECTIONS":          "999",
		"ASCENDANY_DATABASE_MAX_CONNECTION_LIFETIME":  "999h",
		"ASCENDANY_DATABASE_MAX_CONNECTION_IDLE_TIME": "999h",
	}
	readFile := func(path string) ([]byte, error) {
		if path != databasePasswordPath {
			t.Fatalf("LoadModelActivation read unowned credential %q", path)
		}
		return testReadFile(path)
	}
	got, err := LoadModelActivation(mapLookup(env), readFile)
	if err != nil {
		t.Fatalf("LoadModelActivation() error = %v", err)
	}
	if got.Database.ExpectedSchemaVersion != 6 || got.Database.MaxConnections != 1 ||
		got.Database.MinConnections != 0 || got.Database.ConnectTimeout != 4*time.Second ||
		got.Database.HealthTimeout != 750*time.Millisecond || got.Database.Password != strings.Repeat("d", minimumDatabasePasswordBytes) {
		t.Fatalf("activation database config = %#v", got.Database)
	}
	if got.Recommendation.ModelPath != "/opt/ascendany/v2/models/recommendation-model.json" ||
		got.Recommendation.ModelSHA256 != strings.Repeat("a", 64) || got.Recommendation.ModelPurpose != "production" {
		t.Fatalf("activation model config = %#v", got.Recommendation)
	}
}

func TestLoadModelActivationRequiresOnlyDatabaseCredential(t *testing.T) {
	t.Parallel()
	env := map[string]string{
		"ASCENDANY_DATABASE_URL":                 "postgres://ascendany@127.0.0.1:6432/ascendany",
		"ASCENDANY_DATABASE_POOL_MODE":           "transaction",
		"ASCENDANY_DATABASE_SCHEMA_VERSION":      "6",
		"ASCENDANY_RECOMMENDATION_MODEL_PATH":    "/opt/ascendany/v2/models/recommendation-model.json",
		"ASCENDANY_RECOMMENDATION_MODEL_SHA256":  strings.Repeat("a", 64),
		"ASCENDANY_RECOMMENDATION_MODEL_PURPOSE": "production",
	}
	_, err := LoadModelActivation(mapLookup(env), testReadFile)
	if err == nil || err.Error() != "ASCENDANY_DATABASE_PASSWORD_FILE is required" {
		t.Fatalf("LoadModelActivation() error = %v", err)
	}
}

func TestLoadReturnsValidatedConfiguration(t *testing.T) {
	t.Parallel()

	env := validEnvironment()
	env["ASCENDANY_HTTP_LISTEN"] = "127.0.0.1:9090"
	env["ASCENDANY_DATABASE_MIN_CONNECTIONS"] = "0"
	env["ASCENDANY_DATABASE_MAX_CONNECTIONS"] = "8"
	env["ASCENDANY_DATABASE_HEALTH_TIMEOUT"] = "750ms"
	env["ASCENDANY_LOG_LEVEL"] = "WARN"

	got, err := Load(mapLookup(env), testReadFile)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.HTTP.Address != "127.0.0.1:9090" {
		t.Fatalf("HTTP address = %q", got.HTTP.Address)
	}
	if got.HTTP.ClientIPHeader != "CF-Connecting-IP" || len(got.HTTP.TrustedProxyCIDRs) != 1 ||
		got.HTTP.TrustedProxyCIDRs[0].String() != "127.0.0.1/32" {
		t.Fatalf("trusted proxy config = %#v", got.HTTP)
	}
	if got.HTTP.ReadHeaderTimeout != 5*time.Second || got.HTTP.ReadTimeout != 10*time.Minute+30*time.Second ||
		got.HTTP.AuthBodyTimeout != 15*time.Second || got.HTTP.UploadBodyTimeout != 10*time.Minute ||
		got.HTTP.SSEMaxDuration != 15*time.Minute || got.HTTP.SSEReauthInterval != 15*time.Second ||
		got.HTTP.SSEWriteTimeout != 5*time.Second || got.HTTP.MaxActiveSSE != 64 {
		t.Fatalf("HTTP lifetime config = %#v", got.HTTP)
	}
	if got.Database.MinConnections != 0 || got.Database.MaxConnections != 8 {
		t.Fatalf("connection bounds = %d..%d", got.Database.MinConnections, got.Database.MaxConnections)
	}
	if got.Database.HealthTimeout != 750*time.Millisecond {
		t.Fatalf("health timeout = %s", got.Database.HealthTimeout)
	}
	if got.Database.ExpectedSchemaVersion != 6 {
		t.Fatalf("schema version = %d", got.Database.ExpectedSchemaVersion)
	}
	if got.Database.Password != strings.Repeat("d", minimumDatabasePasswordBytes) {
		t.Fatal("database password was not loaded from its credential file")
	}
	if got.Auth.JWTSigningKey != strings.Repeat("s", minimumJWTSecretBytes) {
		t.Fatal("JWT signing key was not loaded from its credential file")
	}
	if got.Auth.PasswordPepper != strings.Repeat("p", minimumPasswordPepperBytes) {
		t.Fatal("password pepper was not loaded from its credential file")
	}
	wantOrigins := "ascendany-app://bundle,capacitor://localhost,http://127.0.0.1:5173,https://ascendany.kkkzbh.cn,https://localhost"
	if got.Auth.Issuer != "ascendany" || got.Auth.Audience != "ascendany-v2" ||
		strings.Join(got.Auth.AllowedOrigins, ",") != wantOrigins {
		t.Fatalf("auth issuer/audience/origins = %q/%q/%q", got.Auth.Issuer, got.Auth.Audience, got.Auth.AllowedOrigins)
	}
	if got.Auth.AccessTTL != 15*time.Minute || got.Auth.RefreshTTL != 30*24*time.Hour {
		t.Fatalf("auth lifetimes = %s/%s", got.Auth.AccessTTL, got.Auth.RefreshTTL)
	}
	if got.Artifact.Root != "/var/lib/ascendany/artifacts" {
		t.Fatalf("artifact root = %q", got.Artifact.Root)
	}
	if got.Artifact.MaxBytes != 128<<20 {
		t.Fatalf("artifact max bytes = %d", got.Artifact.MaxBytes)
	}
	if got.Artifact.OrphanMinAge != 24*time.Hour || got.Artifact.ReconcileInterval != time.Hour {
		t.Fatalf("artifact reconciliation = %#v", got.Artifact)
	}
	if got.Pintia.MaxTotalNodes != 2_000_000 || got.Pintia.MaxTotalStringBytes != 32<<20 ||
		got.Pintia.MaxJSONDepth != 32 || got.Pintia.MaxStringBytes != 8<<20 ||
		got.Pintia.MaxProblems != 1_000 || got.Pintia.MaxParticipants != 20_000 ||
		got.Pintia.MaxProblemResultsPerRanking != 1_000 || got.Pintia.MaxSubmissions != 20_000 ||
		got.Pintia.MaxCaseResultsPerSubmission != 1_000 || got.Pintia.MaxCodeBytes != 1<<20 {
		t.Fatalf("Pintia limits = %#v", got.Pintia)
	}
	if got.Import.WorkerOwner != "km6-import" || got.Import.LeaseDuration != 5*time.Minute ||
		got.Import.RetryDelay != 30*time.Second || got.Import.PollInterval != time.Second {
		t.Fatalf("import worker = %#v", got.Import)
	}
	if got.Analytics.ConfigPath != "/etc/ascendany/v2/analytics.json" ||
		got.Analytics.WorkerOwner != "km6-analytics" ||
		got.Analytics.LeaseDuration != 5*time.Minute ||
		got.Analytics.PollInterval != time.Second {
		t.Fatalf("analytics worker = %#v", got.Analytics)
	}
	if got.Feedback.RateWindow != time.Hour || got.Feedback.RateMaximum != 5 ||
		got.Feedback.DeliveryConfigurationKey != "feedback.delivery.default" ||
		got.Feedback.WorkerOwner != "km6-feedback" || got.Feedback.LeaseDuration != 5*time.Minute ||
		got.Feedback.RetryDelay != 30*time.Second || got.Feedback.PollInterval != time.Second {
		t.Fatalf("feedback runtime = %#v", got.Feedback)
	}
	if got.ChatAgent.WorkerOwner != "km6-chat-agent" || got.ChatAgent.LeaseDuration != 5*time.Minute ||
		got.ChatAgent.PollInterval != time.Second || got.ChatAgent.MaximumContextItems != 200 ||
		got.ChatAgent.MaximumToolRounds != 8 {
		t.Fatalf("chat agent runtime = %#v", got.ChatAgent)
	}
	if got.Recommendation.ModelPath != "/opt/ascendany/current/models/recommendation-model.json" ||
		got.Recommendation.ModelSHA256 != strings.Repeat("a", 64) || got.Recommendation.ModelPurpose != "production" {
		t.Fatalf("recommendation runtime = %#v", got.Recommendation)
	}
	if got.Judge.SocketDirectory != "/run/ascendany-judge" || got.Judge.WorkerUser != "ascendany-judge" ||
		got.Judge.SystemctlPath != "/usr/bin/systemctl" || got.Judge.StartupTimeout != 30*time.Second ||
		got.Judge.SessionTimeout != 30*time.Minute || got.Judge.StopTimeout != 15*time.Second ||
		got.Judge.WorkerOwner != "km6-judge" || got.Judge.LeaseDuration != 5*time.Minute ||
		got.Judge.RetryDelay != 30*time.Second || got.Judge.PollInterval != time.Second ||
		got.Judge.MaximumAttempts != 3 {
		t.Fatalf("judge runtime = %#v", got.Judge)
	}
	if got.LSP.ControlSocket != "/run/ascendany-lsp-control/control.sock" ||
		got.LSP.WorkerUser != "ascendany-lsp" || got.LSP.SystemctlPath != "/usr/bin/systemctl" ||
		got.LSP.MaximumSessions != 64 || got.LSP.MaximumPendingHandshakes != 16 ||
		got.LSP.HandshakeTimeout != 5*time.Second || got.LSP.StartupTimeout != 30*time.Second ||
		got.LSP.StopTimeout != 15*time.Second {
		t.Fatalf("LSP runtime = %#v", got.LSP)
	}
	if got.Write.Enabled {
		t.Fatal("write mode must remain disabled")
	}
	if got.Log.Level != "warn" {
		t.Fatalf("log level = %q", got.Log.Level)
	}
}

func TestLoadRequiresRuntimeWorkerAndLimitSettings(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"ASCENDANY_ARTIFACT_MAX_BYTES",
		"ASCENDANY_ARTIFACT_ORPHAN_MIN_AGE",
		"ASCENDANY_ARTIFACT_RECONCILE_INTERVAL",
		"ASCENDANY_PINTIA_MAX_TOTAL_NODES",
		"ASCENDANY_PINTIA_MAX_TOTAL_STRING_BYTES",
		"ASCENDANY_PINTIA_MAX_JSON_DEPTH",
		"ASCENDANY_PINTIA_MAX_STRING_BYTES",
		"ASCENDANY_PINTIA_MAX_PROBLEMS",
		"ASCENDANY_PINTIA_MAX_PARTICIPANTS",
		"ASCENDANY_PINTIA_MAX_PROBLEM_RESULTS_PER_RANKING",
		"ASCENDANY_PINTIA_MAX_SUBMISSIONS",
		"ASCENDANY_PINTIA_MAX_CASE_RESULTS_PER_SUBMISSION",
		"ASCENDANY_PINTIA_MAX_CODE_BYTES",
		"ASCENDANY_IMPORT_WORKER_OWNER",
		"ASCENDANY_IMPORT_LEASE_DURATION",
		"ASCENDANY_IMPORT_RETRY_DELAY",
		"ASCENDANY_IMPORT_POLL_INTERVAL",
		"ASCENDANY_ANALYTICS_CONFIG",
		"ASCENDANY_ANALYTICS_WORKER_OWNER",
		"ASCENDANY_ANALYTICS_LEASE_DURATION",
		"ASCENDANY_ANALYTICS_POLL_INTERVAL",
		"ASCENDANY_FEEDBACK_RATE_WINDOW",
		"ASCENDANY_FEEDBACK_RATE_MAXIMUM",
		"ASCENDANY_FEEDBACK_DELIVERY_CONFIGURATION_KEY",
		"ASCENDANY_FEEDBACK_WORKER_OWNER",
		"ASCENDANY_FEEDBACK_LEASE_DURATION",
		"ASCENDANY_FEEDBACK_RETRY_DELAY",
		"ASCENDANY_FEEDBACK_POLL_INTERVAL",
		"ASCENDANY_CHAT_AGENT_WORKER_OWNER",
		"ASCENDANY_CHAT_AGENT_LEASE_DURATION",
		"ASCENDANY_CHAT_AGENT_POLL_INTERVAL",
		"ASCENDANY_CHAT_AGENT_MAXIMUM_CONTEXT_ITEMS",
		"ASCENDANY_CHAT_AGENT_MAXIMUM_TOOL_ROUNDS",
		"ASCENDANY_RECOMMENDATION_MODEL_PATH",
		"ASCENDANY_RECOMMENDATION_MODEL_SHA256",
		"ASCENDANY_RECOMMENDATION_MODEL_PURPOSE",
		"ASCENDANY_JUDGE_SOCKET_DIRECTORY",
		"ASCENDANY_JUDGE_WORKER_USER",
		"ASCENDANY_JUDGE_SYSTEMCTL_PATH",
		"ASCENDANY_JUDGE_STARTUP_TIMEOUT",
		"ASCENDANY_JUDGE_SESSION_TIMEOUT",
		"ASCENDANY_JUDGE_STOP_TIMEOUT",
		"ASCENDANY_JUDGE_WORKER_OWNER",
		"ASCENDANY_JUDGE_LEASE_DURATION",
		"ASCENDANY_JUDGE_RETRY_DELAY",
		"ASCENDANY_JUDGE_POLL_INTERVAL",
		"ASCENDANY_JUDGE_MAXIMUM_ATTEMPTS",
		"ASCENDANY_LSP_CONTROL_SOCKET",
		"ASCENDANY_LSP_WORKER_USER",
		"ASCENDANY_LSP_SYSTEMCTL_PATH",
		"ASCENDANY_LSP_MAXIMUM_SESSIONS",
		"ASCENDANY_LSP_MAXIMUM_PENDING_HANDSHAKES",
		"ASCENDANY_LSP_HANDSHAKE_TIMEOUT",
		"ASCENDANY_LSP_STARTUP_TIMEOUT",
		"ASCENDANY_LSP_STOP_TIMEOUT",
		"ASCENDANY_HTTP_TRUSTED_PROXY_CIDRS",
		"ASCENDANY_HTTP_CLIENT_IP_HEADER",
	} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			env := validEnvironment()
			delete(env, name)
			_, err := Load(mapLookup(env), testReadFile)
			if err == nil || err.Error() != name+" is required" {
				t.Fatalf("Load() error = %v, want missing %s", err, name)
			}
		})
	}
}

func TestLoadRejectsNonCanonicalTrustedProxyConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "spaces", key: "ASCENDANY_HTTP_TRUSTED_PROXY_CIDRS", value: "127.0.0.1/32, ::1/128"},
		{name: "host bits", key: "ASCENDANY_HTTP_TRUSTED_PROXY_CIDRS", value: "127.0.0.2/24"},
		{name: "duplicate", key: "ASCENDANY_HTTP_TRUSTED_PROXY_CIDRS", value: "127.0.0.1/32,127.0.0.1/32"},
		{name: "trust all ipv4", key: "ASCENDANY_HTTP_TRUSTED_PROXY_CIDRS", value: "0.0.0.0/0"},
		{name: "inactive loopback", key: "ASCENDANY_HTTP_TRUSTED_PROXY_CIDRS", value: "::1/128"},
		{name: "wrong header", key: "ASCENDANY_HTTP_CLIENT_IP_HEADER", value: "X-Forwarded-For"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			env := validEnvironment()
			env[test.key] = test.value
			if _, err := Load(mapLookup(env), testReadFile); err == nil {
				t.Fatal("Load() error = nil")
			}
		})
	}
}

func TestLoadRejectsNonLoopbackHTTPListener(t *testing.T) {
	t.Parallel()
	for _, address := range []string{"0.0.0.0:8000", "[::]:8000", "192.0.2.10:8000", "localhost:8000"} {
		env := validEnvironment()
		env["ASCENDANY_HTTP_LISTEN"] = address
		_, err := Load(mapLookup(env), testReadFile)
		if err == nil || !strings.Contains(err.Error(), "explicit loopback") {
			t.Fatalf("Load(%q) error = %v", address, err)
		}
	}
}

func TestLoadRejectsUnsafeArtifactRootAndWriteMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		key   string
		value string
		want  string
	}{
		{name: "relative root", key: "ASCENDANY_ARTIFACT_ROOT", value: "var/artifacts", want: "absolute path"},
		{name: "non-normalized root", key: "ASCENDANY_ARTIFACT_ROOT", value: "/var/lib/../artifacts", want: "normalized path"},
		{name: "filesystem root", key: "ASCENDANY_ARTIFACT_ROOT", value: "/", want: "filesystem root"},
		{name: "write mode", key: "ASCENDANY_WRITE_MODE", value: "true", want: "disabled or enabled"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			env := validEnvironment()
			env[test.key] = test.value
			_, err := Load(mapLookup(env), testReadFile)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Load() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadRejectsInvalidAuthLifetimes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		accessTTL  string
		refreshTTL string
		want       string
	}{
		{name: "invalid access", accessTTL: "zero", refreshTTL: "720h", want: "positive Go duration"},
		{name: "zero refresh", accessTTL: "15m", refreshTTL: "0s", want: "positive Go duration"},
		{name: "access not shorter", accessTTL: "24h", refreshTTL: "24h", want: "must be shorter"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			env := validEnvironment()
			env["ASCENDANY_AUTH_ACCESS_TTL"] = test.accessTTL
			env["ASCENDANY_AUTH_REFRESH_TTL"] = test.refreshTTL
			_, err := Load(mapLookup(env), testReadFile)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Load() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadRejectsInvalidHTTPLifetimes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		key   string
		value string
		want  string
	}{
		{name: "invalid read timeout", key: "ASCENDANY_HTTP_READ_TIMEOUT", value: "0s", want: "positive Go duration"},
		{name: "read does not exceed upload", key: "ASCENDANY_HTTP_READ_TIMEOUT", value: "10m", want: "must exceed"},
		{name: "reauthorization equals stream maximum", key: "ASCENDANY_HTTP_SSE_REAUTH_INTERVAL", value: "15m", want: "must be shorter"},
		{name: "write exceeds reauthorization", key: "ASCENDANY_HTTP_SSE_WRITE_TIMEOUT", value: "16s", want: "must not exceed"},
		{name: "zero SSE capacity", key: "ASCENDANY_HTTP_MAX_ACTIVE_SSE", value: "0", want: "positive base-10 integer"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			env := validEnvironment()
			env[test.key] = test.value
			_, err := Load(mapLookup(env), testReadFile)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Load() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadRejectsInvalidAuthAllowedOrigins(t *testing.T) {
	t.Parallel()

	for _, origins := range []string{
		"http://ascendany.kkkzbh.cn",
		"https://ascendany.kkkzbh.cn/",
		"https://ascendany.kkkzbh.cn/path",
		"https://user@ascendany.kkkzbh.cn",
		"https://升任.例子",
		"https://ascendany.kkkzbh.cn,https://ascendany.kkkzbh.cn",
		"https://ascendany.kkkzbh.cn, capacitor://localhost",
		"ascendany-app://other",
		"capacitor://device",
		"file://localhost",
		"https://ascendany.kkkzbh.cn:443",
		"http://127.0.0.1:05173",
	} {
		env := validEnvironment()
		env["ASCENDANY_AUTH_ALLOWED_ORIGINS"] = origins
		_, err := Load(mapLookup(env), testReadFile)
		if err == nil || !strings.Contains(err.Error(), "ASCENDANY_AUTH_ALLOWED_ORIGINS") {
			t.Fatalf("Load(%q) error = %v, want origin rejection", origins, err)
		}
	}
}

func TestLoadRejectsInvalidWorkerAndLimitSettings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		key   string
		value string
		want  string
	}{
		{name: "zero artifact limit", key: "ASCENDANY_ARTIFACT_MAX_BYTES", value: "0", want: "positive base-10"},
		{name: "zero collection limit", key: "ASCENDANY_PINTIA_MAX_SUBMISSIONS", value: "0", want: "positive base-10"},
		{name: "string total exceeds artifact", key: "ASCENDANY_PINTIA_MAX_TOTAL_STRING_BYTES", value: "134217729", want: "must not exceed"},
		{name: "depth exceeds parser contract", key: "ASCENDANY_PINTIA_MAX_JSON_DEPTH", value: "129", want: "must not exceed 128"},
		{name: "string exceeds string total", key: "ASCENDANY_PINTIA_MAX_STRING_BYTES", value: "33554433", want: "must not exceed"},
		{name: "code exceeds string", key: "ASCENDANY_PINTIA_MAX_CODE_BYTES", value: "8388609", want: "must not exceed"},
		{name: "collection exceeds node total", key: "ASCENDANY_PINTIA_MAX_SUBMISSIONS", value: "2000001", want: "must not exceed"},
		{name: "bad import owner", key: "ASCENDANY_IMPORT_WORKER_OWNER", value: "worker name", want: "ASCII letters"},
		{name: "unsafe import lease", key: "ASCENDANY_IMPORT_LEASE_DURATION", value: "299ms", want: "at least 300 milliseconds"},
		{name: "import poll exceeds lease", key: "ASCENDANY_IMPORT_POLL_INTERVAL", value: "5m", want: "must be shorter"},
		{name: "analytics relative path", key: "ASCENDANY_ANALYTICS_CONFIG", value: "analytics.json", want: "absolute path"},
		{name: "unsafe analytics lease", key: "ASCENDANY_ANALYTICS_LEASE_DURATION", value: "299ms", want: "at least 300 milliseconds"},
		{name: "analytics poll exceeds lease", key: "ASCENDANY_ANALYTICS_POLL_INTERVAL", value: "5m", want: "must be shorter"},
		{name: "short feedback window", key: "ASCENDANY_FEEDBACK_RATE_WINDOW", value: "999ms", want: "at least one second"},
		{name: "excessive feedback maximum", key: "ASCENDANY_FEEDBACK_RATE_MAXIMUM", value: "1001", want: "must not exceed 1000"},
		{name: "invalid feedback key", key: "ASCENDANY_FEEDBACK_DELIVERY_CONFIGURATION_KEY", value: "Feedback Key", want: "canonical configuration key"},
		{name: "short feedback retry", key: "ASCENDANY_FEEDBACK_RETRY_DELAY", value: "999ms", want: "at least one second"},
		{name: "feedback poll exceeds lease", key: "ASCENDANY_FEEDBACK_POLL_INTERVAL", value: "5m", want: "must be shorter"},
		{name: "bad chat agent owner", key: "ASCENDANY_CHAT_AGENT_WORKER_OWNER", value: "worker name", want: "ASCII letters"},
		{name: "unsafe chat agent lease", key: "ASCENDANY_CHAT_AGENT_LEASE_DURATION", value: "299ms", want: "at least 300 milliseconds"},
		{name: "chat agent poll exceeds lease", key: "ASCENDANY_CHAT_AGENT_POLL_INTERVAL", value: "5m", want: "must be shorter"},
		{name: "excessive chat context", key: "ASCENDANY_CHAT_AGENT_MAXIMUM_CONTEXT_ITEMS", value: "1001", want: "must not exceed 1000"},
		{name: "excessive chat tool rounds", key: "ASCENDANY_CHAT_AGENT_MAXIMUM_TOOL_ROUNDS", value: "65", want: "must not exceed 64"},
		{name: "relative recommendation model", key: "ASCENDANY_RECOMMENDATION_MODEL_PATH", value: "models/recommendation-model.json", want: "absolute path"},
		{name: "uppercase recommendation digest", key: "ASCENDANY_RECOMMENDATION_MODEL_SHA256", value: strings.Repeat("A", 64), want: "lowercase SHA-256"},
		{name: "short recommendation digest", key: "ASCENDANY_RECOMMENDATION_MODEL_SHA256", value: strings.Repeat("a", 63), want: "lowercase SHA-256"},
		{name: "invalid recommendation purpose", key: "ASCENDANY_RECOMMENDATION_MODEL_PURPOSE", value: "test", want: "production or acceptance_test"},
		{name: "relative judge socket", key: "ASCENDANY_JUDGE_SOCKET_DIRECTORY", value: "run/judge", want: "absolute path"},
		{name: "long judge socket", key: "ASCENDANY_JUDGE_SOCKET_DIRECTORY", value: "/run/" + strings.Repeat("a", 80), want: "socket path limit"},
		{name: "invalid judge worker user", key: "ASCENDANY_JUDGE_WORKER_USER", value: "AscendAny Judge", want: "canonical system user"},
		{name: "relative systemctl", key: "ASCENDANY_JUDGE_SYSTEMCTL_PATH", value: "systemctl", want: "absolute path"},
		{name: "short judge startup", key: "ASCENDANY_JUDGE_STARTUP_TIMEOUT", value: "999ms", want: "between 1s and 1m0s"},
		{name: "long judge session", key: "ASCENDANY_JUDGE_SESSION_TIMEOUT", value: "61m", want: "between 1s and 1h0m0s"},
		{name: "bad judge owner", key: "ASCENDANY_JUDGE_WORKER_OWNER", value: "worker name", want: "ASCII letters"},
		{name: "short judge retry", key: "ASCENDANY_JUDGE_RETRY_DELAY", value: "999ms", want: "at least one second"},
		{name: "judge poll exceeds lease", key: "ASCENDANY_JUDGE_POLL_INTERVAL", value: "5m", want: "must be shorter"},
		{name: "excessive judge attempts", key: "ASCENDANY_JUDGE_MAXIMUM_ATTEMPTS", value: "101", want: "must not exceed 100"},
		{name: "relative LSP socket", key: "ASCENDANY_LSP_CONTROL_SOCKET", value: "run/lsp.sock", want: "absolute path"},
		{name: "long LSP socket", key: "ASCENDANY_LSP_CONTROL_SOCKET", value: "/run/" + strings.Repeat("a", 104), want: "socket path limit"},
		{name: "invalid LSP worker user", key: "ASCENDANY_LSP_WORKER_USER", value: "AscendAny LSP", want: "canonical system user"},
		{name: "relative LSP systemctl", key: "ASCENDANY_LSP_SYSTEMCTL_PATH", value: "systemctl", want: "absolute path"},
		{name: "excessive LSP sessions", key: "ASCENDANY_LSP_MAXIMUM_SESSIONS", value: "10001", want: "must not exceed 10000"},
		{name: "excessive LSP handshakes", key: "ASCENDANY_LSP_MAXIMUM_PENDING_HANDSHAKES", value: "1025", want: "must not exceed 1024"},
		{name: "short LSP handshake", key: "ASCENDANY_LSP_HANDSHAKE_TIMEOUT", value: "999ms", want: "between 1s and 1m0s"},
		{name: "long LSP startup", key: "ASCENDANY_LSP_STARTUP_TIMEOUT", value: "61s", want: "between 1s and 1m0s"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			env := validEnvironment()
			env[test.key] = test.value
			_, err := Load(mapLookup(env), testReadFile)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Load() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadRejectsNonTransactionPoolMode(t *testing.T) {
	t.Parallel()

	env := validEnvironment()
	env["ASCENDANY_DATABASE_POOL_MODE"] = "session"
	_, err := Load(mapLookup(env), testReadFile)
	if err == nil || !strings.Contains(err.Error(), "must be transaction") {
		t.Fatalf("Load() error = %v, want transaction-pool rejection", err)
	}
}

func TestLoadRejectsInvalidDatabaseURLWithoutEchoingIt(t *testing.T) {
	t.Parallel()

	secretURL := "http://admin:do-not-log@localhost:6432/ascendany"
	env := validEnvironment()
	env["ASCENDANY_DATABASE_URL"] = secretURL

	_, err := Load(mapLookup(env), testReadFile)
	if err == nil {
		t.Fatal("Load() error = nil")
	}
	if strings.Contains(err.Error(), secretURL) || strings.Contains(err.Error(), "do-not-log") {
		t.Fatalf("Load() leaked database URL: %v", err)
	}
}

func TestLoadRejectsDatabasePasswordInURL(t *testing.T) {
	t.Parallel()

	for _, databaseURL := range []string{
		"postgres://ascendany:supersecretvalue@127.0.0.1:6432/ascendany",
		"postgres://ascendany@127.0.0.1:6432/ascendany?password=supersecretvalue",
	} {
		env := validEnvironment()
		env["ASCENDANY_DATABASE_URL"] = databaseURL
		_, err := Load(mapLookup(env), testReadFile)
		if err == nil || !strings.Contains(err.Error(), "password") {
			t.Fatalf("Load() error = %v, want password-in-URL rejection", err)
		}
		if strings.Contains(err.Error(), "supersecretvalue") {
			t.Fatalf("Load() leaked database password: %v", err)
		}
	}
}

func TestLoadRejectsUnreadableSecretWithoutLeakingPath(t *testing.T) {
	t.Parallel()

	env := validEnvironment()
	env["ASCENDANY_DATABASE_PASSWORD_FILE"] = "/secret/private/path"
	_, err := Load(mapLookup(env), func(string) ([]byte, error) {
		return nil, fmt.Errorf("permission denied for /secret/private/path")
	})
	if err == nil || err.Error() != "ASCENDANY_DATABASE_PASSWORD_FILE cannot be read" {
		t.Fatalf("Load() error = %v", err)
	}
}

func validEnvironment() map[string]string {
	return map[string]string{
		"ASCENDANY_DATABASE_URL":                           "postgres://ascendany@127.0.0.1:6432/ascendany",
		"ASCENDANY_DATABASE_POOL_MODE":                     "transaction",
		"ASCENDANY_DATABASE_PASSWORD_FILE":                 databasePasswordPath,
		"ASCENDANY_JWT_SIGNING_KEY_FILE":                   jwtSigningKeyPath,
		"ASCENDANY_PASSWORD_PEPPER_FILE":                   passwordPepperPath,
		"ASCENDANY_AUTH_ISSUER":                            "ascendany",
		"ASCENDANY_AUTH_AUDIENCE":                          "ascendany-v2",
		"ASCENDANY_AUTH_ALLOWED_ORIGINS":                   "https://ascendany.kkkzbh.cn,ascendany-app://bundle,capacitor://localhost,https://localhost,http://127.0.0.1:5173",
		"ASCENDANY_AUTH_ACCESS_TTL":                        "15m",
		"ASCENDANY_AUTH_REFRESH_TTL":                       "720h",
		"ASCENDANY_HTTP_TRUSTED_PROXY_CIDRS":               "127.0.0.1/32",
		"ASCENDANY_HTTP_CLIENT_IP_HEADER":                  "CF-Connecting-IP",
		"ASCENDANY_DATABASE_SCHEMA_VERSION":                "6",
		"ASCENDANY_ARTIFACT_ROOT":                          "/var/lib/ascendany/artifacts",
		"ASCENDANY_ARTIFACT_MAX_BYTES":                     "134217728",
		"ASCENDANY_ARTIFACT_ORPHAN_MIN_AGE":                "24h",
		"ASCENDANY_ARTIFACT_RECONCILE_INTERVAL":            "1h",
		"ASCENDANY_PINTIA_MAX_TOTAL_NODES":                 "2000000",
		"ASCENDANY_PINTIA_MAX_TOTAL_STRING_BYTES":          "33554432",
		"ASCENDANY_PINTIA_MAX_JSON_DEPTH":                  "32",
		"ASCENDANY_PINTIA_MAX_STRING_BYTES":                "8388608",
		"ASCENDANY_PINTIA_MAX_PROBLEMS":                    "1000",
		"ASCENDANY_PINTIA_MAX_PARTICIPANTS":                "20000",
		"ASCENDANY_PINTIA_MAX_PROBLEM_RESULTS_PER_RANKING": "1000",
		"ASCENDANY_PINTIA_MAX_SUBMISSIONS":                 "20000",
		"ASCENDANY_PINTIA_MAX_CASE_RESULTS_PER_SUBMISSION": "1000",
		"ASCENDANY_PINTIA_MAX_CODE_BYTES":                  "1048576",
		"ASCENDANY_IMPORT_WORKER_OWNER":                    "km6-import",
		"ASCENDANY_IMPORT_LEASE_DURATION":                  "5m",
		"ASCENDANY_IMPORT_RETRY_DELAY":                     "30s",
		"ASCENDANY_IMPORT_POLL_INTERVAL":                   "1s",
		"ASCENDANY_ANALYTICS_CONFIG":                       "/etc/ascendany/v2/analytics.json",
		"ASCENDANY_ANALYTICS_WORKER_OWNER":                 "km6-analytics",
		"ASCENDANY_ANALYTICS_LEASE_DURATION":               "5m",
		"ASCENDANY_ANALYTICS_POLL_INTERVAL":                "1s",
		"ASCENDANY_FEEDBACK_RATE_WINDOW":                   "1h",
		"ASCENDANY_FEEDBACK_RATE_MAXIMUM":                  "5",
		"ASCENDANY_FEEDBACK_DELIVERY_CONFIGURATION_KEY":    "feedback.delivery.default",
		"ASCENDANY_FEEDBACK_WORKER_OWNER":                  "km6-feedback",
		"ASCENDANY_FEEDBACK_LEASE_DURATION":                "5m",
		"ASCENDANY_FEEDBACK_RETRY_DELAY":                   "30s",
		"ASCENDANY_FEEDBACK_POLL_INTERVAL":                 "1s",
		"ASCENDANY_CHAT_AGENT_WORKER_OWNER":                "km6-chat-agent",
		"ASCENDANY_CHAT_AGENT_LEASE_DURATION":              "5m",
		"ASCENDANY_CHAT_AGENT_POLL_INTERVAL":               "1s",
		"ASCENDANY_CHAT_AGENT_MAXIMUM_CONTEXT_ITEMS":       "200",
		"ASCENDANY_CHAT_AGENT_MAXIMUM_TOOL_ROUNDS":         "8",
		"ASCENDANY_RECOMMENDATION_MODEL_PATH":              "/opt/ascendany/current/models/recommendation-model.json",
		"ASCENDANY_RECOMMENDATION_MODEL_SHA256":            strings.Repeat("a", 64),
		"ASCENDANY_RECOMMENDATION_MODEL_PURPOSE":           "production",
		"ASCENDANY_JUDGE_SOCKET_DIRECTORY":                 "/run/ascendany-judge",
		"ASCENDANY_JUDGE_WORKER_USER":                      "ascendany-judge",
		"ASCENDANY_JUDGE_SYSTEMCTL_PATH":                   "/usr/bin/systemctl",
		"ASCENDANY_JUDGE_STARTUP_TIMEOUT":                  "30s",
		"ASCENDANY_JUDGE_SESSION_TIMEOUT":                  "30m",
		"ASCENDANY_JUDGE_STOP_TIMEOUT":                     "15s",
		"ASCENDANY_JUDGE_WORKER_OWNER":                     "km6-judge",
		"ASCENDANY_JUDGE_LEASE_DURATION":                   "5m",
		"ASCENDANY_JUDGE_RETRY_DELAY":                      "30s",
		"ASCENDANY_JUDGE_POLL_INTERVAL":                    "1s",
		"ASCENDANY_JUDGE_MAXIMUM_ATTEMPTS":                 "3",
		"ASCENDANY_LSP_CONTROL_SOCKET":                     "/run/ascendany-lsp-control/control.sock",
		"ASCENDANY_LSP_WORKER_USER":                        "ascendany-lsp",
		"ASCENDANY_LSP_SYSTEMCTL_PATH":                     "/usr/bin/systemctl",
		"ASCENDANY_LSP_MAXIMUM_SESSIONS":                   "64",
		"ASCENDANY_LSP_MAXIMUM_PENDING_HANDSHAKES":         "16",
		"ASCENDANY_LSP_HANDSHAKE_TIMEOUT":                  "5s",
		"ASCENDANY_LSP_STARTUP_TIMEOUT":                    "30s",
		"ASCENDANY_LSP_STOP_TIMEOUT":                       "15s",
		"ASCENDANY_WRITE_MODE":                             "disabled",
	}
}

func testReadFile(path string) ([]byte, error) {
	switch path {
	case databasePasswordPath:
		return []byte(strings.Repeat("d", minimumDatabasePasswordBytes)), nil
	case jwtSigningKeyPath:
		return []byte(strings.Repeat("s", minimumJWTSecretBytes)), nil
	case passwordPepperPath:
		return []byte(strings.Repeat("p", minimumPasswordPepperBytes)), nil
	default:
		return nil, fmt.Errorf("unknown credential path")
	}
}

func mapLookup(values map[string]string) LookupEnv {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}
