package config_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/config"
)

// TestLoad_defaults verifies that Load() returns sensible defaults when no
// environment variables are set.
func TestLoad_defaults(t *testing.T) {
	// Given: no environment variables set
	clearEnv(t)

	// When: we load config
	cfg, err := config.Load()

	// Then: defaults are applied correctly
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if cfg.Host != "0.0.0.0" {
		t.Errorf("Host: expected 0.0.0.0, got %q", cfg.Host)
	}
	if cfg.Port != 4566 {
		t.Errorf("Port: expected 4566, got %d", cfg.Port)
	}
	if cfg.Region != "us-east-1" {
		t.Errorf("Region: expected us-east-1, got %q", cfg.Region)
	}
	if cfg.AccountID != "000000000000" {
		t.Errorf("AccountID: expected 000000000000, got %q", cfg.AccountID)
	}
	if cfg.State != config.StateBackendHybrid {
		t.Errorf("State: expected hybrid, got %q", cfg.State)
	}
	if cfg.HybridFlushInterval != 5*time.Second {
		t.Errorf("HybridFlushInterval: expected 5s, got %v", cfg.HybridFlushInterval)
	}
	if cfg.WALFsyncMode != "interval" {
		t.Errorf("WALFsyncMode: expected interval, got %q", cfg.WALFsyncMode)
	}
	if cfg.WALFsyncInterval != 100*time.Millisecond {
		t.Errorf("WALFsyncInterval: expected 100ms, got %v", cfg.WALFsyncInterval)
	}
	if cfg.WALMaxLogBytes != 67108864 {
		t.Errorf("WALMaxLogBytes: expected 67108864, got %d", cfg.WALMaxLogBytes)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel: expected info, got %q", cfg.LogLevel)
	}
	if cfg.ShutdownTimeout != 5*time.Second {
		t.Errorf("ShutdownTimeout: expected 5s, got %v", cfg.ShutdownTimeout)
	}
	if cfg.LambdaHotReload {
		t.Error("LambdaHotReload: expected false by default")
	}
	if cfg.LambdaDockerMaxConcurrentStarts != 4 {
		t.Errorf("LambdaDockerMaxConcurrentStarts: expected 4, got %d", cfg.LambdaDockerMaxConcurrentStarts)
	}
	if cfg.LambdaInitTimeout != 10*time.Second {
		t.Errorf("LambdaInitTimeout: expected 10s, got %v", cfg.LambdaInitTimeout)
	}
	if cfg.Debug {
		t.Error("Debug: expected false by default")
	}
	if cfg.TLSEnabled() {
		t.Error("TLS: expected disabled by default")
	}
	if cfg.Hostname != "" {
		t.Errorf("Hostname: expected empty, got %q", cfg.Hostname)
	}
	if cfg.ExternalHostname() != "localhost" {
		t.Errorf("ExternalHostname(): expected localhost, got %q", cfg.ExternalHostname())
	}
	if cfg.ExternalBaseURL() != "http://localhost:4566" {
		t.Errorf("ExternalBaseURL(): expected http://localhost:4566, got %q", cfg.ExternalBaseURL())
	}
	if cfg.MCPReplayLimit != 256 {
		t.Errorf("MCPReplayLimit: expected 256, got %d", cfg.MCPReplayLimit)
	}
	if cfg.MCPRemoteExposure {
		t.Error("MCPRemoteExposure: expected false by default")
	}
	if cfg.MCPAuthToken != "" {
		t.Errorf("MCPAuthToken: expected empty by default, got %q", cfg.MCPAuthToken)
	}
	if cfg.EnforceIAM {
		t.Error("EnforceIAM: expected false by default")
	}
}

func TestLoad_lambdaDockerMaxConcurrentStarts(t *testing.T) {
	// Given: LAMBDA_DOCKER_MAX_CONCURRENT_STARTS is set.
	clearEnv(t)
	t.Setenv("LAMBDA_DOCKER_MAX_CONCURRENT_STARTS", "7")

	// When: we load config.
	cfg, err := config.Load()

	// Then: the Docker startup backpressure limit is parsed.
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LambdaDockerMaxConcurrentStarts != 7 {
		t.Fatalf("LambdaDockerMaxConcurrentStarts = %d, want 7", cfg.LambdaDockerMaxConcurrentStarts)
	}
}

func TestLoad_lambdaInitTimeoutSeconds(t *testing.T) {
	// Given: LAMBDA_INIT_TIMEOUT_SECONDS is set.
	clearEnv(t)
	t.Setenv("LAMBDA_INIT_TIMEOUT_SECONDS", "17")

	// When: we load config.
	cfg, err := config.Load()

	// Then: the Lambda init timeout is parsed as a duration.
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LambdaInitTimeout != 17*time.Second {
		t.Fatalf("LambdaInitTimeout = %v, want 17s", cfg.LambdaInitTimeout)
	}
}

// TestLoad_allServicesEnabled verifies all known services are enabled by default.
func TestLoad_allServicesEnabled(t *testing.T) {
	// Given: no OVERCAST_SERVICES set
	clearEnv(t)

	// When: we load config
	cfg, err := config.Load()

	// Then: all services are enabled
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, svc := range []string{"s3", "sqs", "sns", "dynamodb", "lambda"} {
		if !cfg.Services[svc] {
			t.Errorf("expected service %q to be enabled by default", svc)
		}
	}
}

// TestLoad_serviceSubset verifies OVERCAST_SERVICES filters correctly.
func TestLoad_serviceSubset(t *testing.T) {
	// Given: OVERCAST_SERVICES is set to a subset
	clearEnv(t)
	t.Setenv("OVERCAST_SERVICES", "s3,sqs")

	// When: we load config
	cfg, err := config.Load()

	// Then: only the specified services are enabled
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Services["s3"] {
		t.Error("expected s3 to be enabled")
	}
	if !cfg.Services["sqs"] {
		t.Error("expected sqs to be enabled")
	}
	if cfg.Services["dynamodb"] {
		t.Error("expected dynamodb to be disabled")
	}
}

// TestLoad_unknownService verifies that unknown service names are rejected.
func TestLoad_unknownService(t *testing.T) {
	// Given: OVERCAST_SERVICES contains an unknown service
	clearEnv(t)
	t.Setenv("OVERCAST_SERVICES", "s3,unknownservice")

	// When: we load config
	_, err := config.Load()

	// Then: an error is returned
	if err == nil {
		t.Error("expected error for unknown service, got nil")
	}
}

// TestLoad_invalidPort verifies that non-numeric ports are rejected.
func TestLoad_invalidPort(t *testing.T) {
	// Given: OVERCAST_PORT is not a valid port number
	clearEnv(t)
	t.Setenv("OVERCAST_PORT", "notaport")

	// When: we load config
	_, err := config.Load()

	// Then: an error is returned
	if err == nil {
		t.Error("expected error for invalid port, got nil")
	}
}

// TestLoad_portOutOfRange verifies that ports outside 1-65535 are rejected.
func TestLoad_portOutOfRange(t *testing.T) {
	// Given: OVERCAST_PORT is out of valid range
	cases := []string{"0", "65536", "-1", "99999"}
	for _, p := range cases {
		t.Run("port="+p, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("OVERCAST_PORT", p)

			// When + Then
			_, err := config.Load()
			if err == nil {
				t.Errorf("expected error for port %q, got nil", p)
			}
		})
	}
}

// TestLoad_persistentState verifies the persistent (SQLite) backend is selected correctly.
func TestLoad_persistentState(t *testing.T) {
	// Given: OVERCAST_STATE=persistent
	clearEnv(t)
	t.Setenv("OVERCAST_STATE", "persistent")
	t.Setenv("OVERCAST_DATA_DIR", "/tmp/test-overcast")

	// When: we load config
	cfg, err := config.Load()

	// Then: persistent backend is selected
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.State != config.StateBackendPersistent {
		t.Errorf("expected persistent backend, got %q", cfg.State)
	}
	if cfg.DataDir != "/tmp/test-overcast" {
		t.Errorf("DataDir: expected /tmp/test-overcast, got %q", cfg.DataDir)
	}
}

// TestLoad_sqliteAlias verifies "sqlite" is accepted as a deprecated alias for "persistent".
func TestLoad_sqliteAlias(t *testing.T) {
	// Given: OVERCAST_STATE=sqlite (deprecated alias)
	clearEnv(t)
	t.Setenv("OVERCAST_STATE", "sqlite")

	// When: we load config
	cfg, err := config.Load()

	// Then: it resolves to persistent
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.State != config.StateBackendPersistent {
		t.Errorf("expected sqlite alias to resolve to persistent, got %q", cfg.State)
	}
}

// TestLoad_hybridState verifies the hybrid backend is selected correctly.
func TestLoad_hybridState(t *testing.T) {
	// Given: OVERCAST_STATE=hybrid
	clearEnv(t)
	t.Setenv("OVERCAST_STATE", "hybrid")

	// When: we load config
	cfg, err := config.Load()

	// Then: hybrid backend is selected with the default flush interval
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.State != config.StateBackendHybrid {
		t.Errorf("expected hybrid backend, got %q", cfg.State)
	}
	if cfg.HybridFlushInterval != 5*time.Second {
		t.Errorf("HybridFlushInterval: expected 5s default, got %v", cfg.HybridFlushInterval)
	}
}

// TestLoad_hybridFlushInterval verifies the flush interval can be overridden.
func TestLoad_hybridFlushInterval(t *testing.T) {
	// Given: OVERCAST_HYBRID_FLUSH_INTERVAL=10s
	clearEnv(t)
	t.Setenv("OVERCAST_STATE", "hybrid")
	t.Setenv("OVERCAST_HYBRID_FLUSH_INTERVAL", "10s")

	// When: we load config
	cfg, err := config.Load()

	// Then: the flush interval is 10 seconds
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HybridFlushInterval != 10*time.Second {
		t.Errorf("HybridFlushInterval: expected 10s, got %v", cfg.HybridFlushInterval)
	}
}

// TestLoad_walState verifies the WAL backend is selected correctly.
func TestLoad_walState(t *testing.T) {
	// Given: OVERCAST_STATE=wal
	clearEnv(t)
	t.Setenv("OVERCAST_STATE", "wal")

	// When: we load config
	cfg, err := config.Load()

	// Then: wal backend is selected
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.State != config.StateBackendWAL {
		t.Errorf("expected wal backend, got %q", cfg.State)
	}
}

func TestLoad_walConfigOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("OVERCAST_WAL_FSYNC", "always")
	t.Setenv("OVERCAST_WAL_FSYNC_INTERVAL", "250ms")
	t.Setenv("OVERCAST_WAL_MAX_LOG_BYTES", "4096")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.WALFsyncMode != "always" {
		t.Errorf("WALFsyncMode: expected always, got %q", cfg.WALFsyncMode)
	}
	if cfg.WALFsyncInterval != 250*time.Millisecond {
		t.Errorf("WALFsyncInterval: expected 250ms, got %v", cfg.WALFsyncInterval)
	}
	if cfg.WALMaxLogBytes != 4096 {
		t.Errorf("WALMaxLogBytes: expected 4096, got %d", cfg.WALMaxLogBytes)
	}
}

func TestLoad_invalidWALConfig(t *testing.T) {
	t.Run("invalid fsync mode", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("OVERCAST_WAL_FSYNC", "sometimes")
		if _, err := config.Load(); err == nil {
			t.Fatal("expected error for invalid OVERCAST_WAL_FSYNC")
		}
	})

	t.Run("invalid interval", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("OVERCAST_WAL_FSYNC_INTERVAL", "0s")
		if _, err := config.Load(); err == nil {
			t.Fatal("expected error for invalid OVERCAST_WAL_FSYNC_INTERVAL")
		}
	})

	t.Run("invalid max log bytes", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("OVERCAST_WAL_MAX_LOG_BYTES", "0")
		if _, err := config.Load(); err == nil {
			t.Fatal("expected error for invalid OVERCAST_WAL_MAX_LOG_BYTES")
		}
	})
}

// TestLoad_perServiceState verifies per-service storage overrides are parsed.
func TestLoad_perServiceState(t *testing.T) {
	// Given: OVERCAST_STATE=hybrid and per-service overrides
	clearEnv(t)
	t.Setenv("OVERCAST_STATE", "hybrid")
	t.Setenv("OVERCAST_STATE_S3", "memory")
	t.Setenv("OVERCAST_STATE_SQS", "persistent")

	// When: we load config
	cfg, err := config.Load()

	// Then: per-service modes are recorded
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ServiceStates["s3"] != config.StateBackendMemory {
		t.Errorf("s3: expected memory, got %q", cfg.ServiceStates["s3"])
	}
	if cfg.ServiceStates["sqs"] != config.StateBackendPersistent {
		t.Errorf("sqs: expected persistent, got %q", cfg.ServiceStates["sqs"])
	}
	if _, ok := cfg.ServiceStates["dynamodb"]; ok {
		t.Errorf("dynamodb: expected no override, but found one")
	}
}

// TestLoad_perServiceInvalidState verifies invalid per-service modes are rejected.
func TestLoad_perServiceInvalidState(t *testing.T) {
	// Given: per-service override has an unknown backend
	clearEnv(t)
	t.Setenv("OVERCAST_STATE_S3", "redis")

	// When + Then
	_, err := config.Load()
	if err == nil {
		t.Error("expected error for unknown per-service state backend, got nil")
	}
}

// TestLoad_invalidState verifies that unknown state backends are rejected.
func TestLoad_invalidState(t *testing.T) {
	// Given: OVERCAST_STATE is not a known backend
	clearEnv(t)
	t.Setenv("OVERCAST_STATE", "redis")

	// When + Then
	_, err := config.Load()
	if err == nil {
		t.Error("expected error for unknown state backend, got nil")
	}
}

// TestLoad_tlsBothRequired verifies that TLS requires both cert and key.
func TestLoad_tlsBothRequired(t *testing.T) {
	// Given: only one TLS variable is set
	clearEnv(t)
	t.Setenv("OVERCAST_TLS_CERT", "/path/to/cert.pem")
	// OVERCAST_TLS_KEY is not set

	// When + Then
	_, err := config.Load()
	if err == nil {
		t.Error("expected error when only TLS cert is set (key missing), got nil")
	}
}

// TestLoad_tlsEnabled verifies TLS is enabled when both cert and key are set.
func TestLoad_tlsEnabled(t *testing.T) {
	// Given: both TLS cert and key are set
	clearEnv(t)
	t.Setenv("OVERCAST_TLS_CERT", "/path/to/cert.pem")
	t.Setenv("OVERCAST_TLS_KEY", "/path/to/key.pem")

	// When: we load config
	cfg, err := config.Load()

	// Then: TLS is enabled
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.TLSEnabled() {
		t.Error("expected TLSEnabled() to return true")
	}
}

// TestLoad_debugEnabled verifies debug mode is enabled via env var.
func TestLoad_debugEnabled(t *testing.T) {
	// Given: OVERCAST_DEBUG=true
	clearEnv(t)
	t.Setenv("OVERCAST_DEBUG", "true")

	// When: we load config
	cfg, err := config.Load()

	// Then: debug mode is on
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Debug {
		t.Error("expected Debug to be true")
	}
}

// TestLoad_lambdaHotReloadEnabled verifies Lambda hot-reload mode is enabled via env var.
func TestLoad_lambdaHotReloadEnabled(t *testing.T) {
	clearEnv(t)
	t.Setenv("OVERCAST_LAMBDA_HOT_RELOAD", "true")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.LambdaHotReload {
		t.Error("expected LambdaHotReload to be true")
	}
}

// TestLoad_hostBinding verifies custom host binding.
func TestLoad_hostBinding(t *testing.T) {
	// Given: OVERCAST_HOST is set to localhost only
	clearEnv(t)
	t.Setenv("OVERCAST_HOST", "127.0.0.1")
	t.Setenv("OVERCAST_PORT", "9000")

	// When: we load config
	cfg, err := config.Load()

	// Then: Addr() returns the correct binding
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr() != "127.0.0.1:9000" {
		t.Errorf("Addr(): expected 127.0.0.1:9000, got %q", cfg.Addr())
	}
}

// TestLoad_hostname verifies OVERCAST_HOSTNAME is used for external URLs.
func TestLoad_hostname(t *testing.T) {
	clearEnv(t)
	t.Setenv("OVERCAST_HOSTNAME", "overcast.local")
	t.Setenv("OVERCAST_PORT", "5000")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Hostname != "overcast.local" {
		t.Errorf("Hostname: expected overcast.local, got %q", cfg.Hostname)
	}
	if cfg.ExternalHostname() != "overcast.local" {
		t.Errorf("ExternalHostname(): expected overcast.local, got %q", cfg.ExternalHostname())
	}
	if cfg.ExternalBaseURL() != "http://overcast.local:5000" {
		t.Errorf("ExternalBaseURL(): expected http://overcast.local:5000, got %q", cfg.ExternalBaseURL())
	}
}

// TestLoad_hostnameWithTLS verifies ExternalBaseURL uses https when TLS is enabled.
func TestLoad_hostnameWithTLS(t *testing.T) {
	clearEnv(t)
	t.Setenv("OVERCAST_HOSTNAME", "secure.local")
	t.Setenv("OVERCAST_TLS_CERT", "/tmp/cert.pem")
	t.Setenv("OVERCAST_TLS_KEY", "/tmp/key.pem")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ExternalBaseURL() != "https://secure.local:4566" {
		t.Errorf("ExternalBaseURL(): expected https://secure.local:4566, got %q", cfg.ExternalBaseURL())
	}
}

func TestLoad_eksModeDefault(t *testing.T) {
	clearEnv(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.EKSMode != config.EKSModeMock {
		t.Fatalf("EKSMode: expected %q, got %q", config.EKSModeMock, cfg.EKSMode)
	}
}

func TestLoad_eksModeLive(t *testing.T) {
	clearEnv(t)
	t.Setenv("OVERCAST_EKS_MODE", "live")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.EKSMode != config.EKSModeLive {
		t.Fatalf("EKSMode: expected %q, got %q", config.EKSModeLive, cfg.EKSMode)
	}
}

func TestLoad_eksModeRejectsInvalidValues(t *testing.T) {
	clearEnv(t)
	t.Setenv("OVERCAST_EKS_MODE", "sidecar")

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error for invalid OVERCAST_EKS_MODE, got nil")
	}
	if got := err.Error(); got == "" || !containsAll(got, "OVERCAST_EKS_MODE", "mock", "live") {
		t.Fatalf("unexpected error message: %q", got)
	}
}

func TestLoad_eksDockerDefaults(t *testing.T) {
	clearEnv(t)
	t.Setenv("LAMBDA_DOCKER_SOCKET", "tcp://dind:2375")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.EKSDockerSocket != "tcp://dind:2375" {
		t.Fatalf("EKSDockerSocket: expected tcp://dind:2375, got %q", cfg.EKSDockerSocket)
	}
	if cfg.EKSNetwork != "overcast_eks" {
		t.Fatalf("EKSNetwork: expected overcast_eks, got %q", cfg.EKSNetwork)
	}
}

func TestLoad_eksDockerOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("EKS_DOCKER_SOCKET", "tcp://eksdind:2375")
	t.Setenv("EKS_NETWORK", "custom_eks")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.EKSDockerSocket != "tcp://eksdind:2375" {
		t.Fatalf("EKSDockerSocket: expected tcp://eksdind:2375, got %q", cfg.EKSDockerSocket)
	}
	if cfg.EKSNetwork != "custom_eks" {
		t.Fatalf("EKSNetwork: expected custom_eks, got %q", cfg.EKSNetwork)
	}
}

// TestLoad_invalidShutdownTimeout verifies malformed durations are rejected.
func TestLoad_invalidShutdownTimeout(t *testing.T) {
	// Given: OVERCAST_SHUTDOWN_TIMEOUT is not a valid duration
	clearEnv(t)
	t.Setenv("OVERCAST_SHUTDOWN_TIMEOUT", "notaduration")

	// When + Then
	_, err := config.Load()
	if err == nil {
		t.Error("expected error for invalid shutdown timeout, got nil")
	}
}

// TestLoad_boolVariants verifies all accepted truthy/falsy values for bool vars.
func TestLoad_boolVariants(t *testing.T) {
	truthy := []string{"true", "1", "yes"}
	falsy := []string{"false", "0", "no"}

	for _, v := range truthy {
		t.Run("debug="+v, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("OVERCAST_DEBUG", v)
			cfg, err := config.Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if !cfg.Debug {
				t.Errorf("expected Debug=true for value %q", v)
			}
		})
	}

	for _, v := range falsy {
		t.Run("debug="+v, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("OVERCAST_DEBUG", v)
			cfg, err := config.Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.Debug {
				t.Errorf("expected Debug=false for value %q", v)
			}
		})
	}
}

// TestLoad_ec2VPCStrategyNetnsRejected verifies netns strategy fails fast
// during configuration load with a clear remediation hint.
func TestLoad_ec2VPCStrategyNetnsRejected(t *testing.T) {
	// Given: netns is explicitly selected
	clearEnv(t)
	t.Setenv("OVERCAST_EC2_VPC_STRATEGY", "netns")

	// When: config is loaded
	_, err := config.Load()

	// Then: startup is rejected with guidance
	if err == nil {
		t.Fatal("expected error for OVERCAST_EC2_VPC_STRATEGY=netns, got nil")
	}
	if got := err.Error(); got == "" || !containsAll(got, "OVERCAST_EC2_VPC_STRATEGY", "netns", "shared", "strict", "remapped") {
		t.Fatalf("unexpected error message: %q", got)
	}
}

func TestLoad_mcpReplayLimitOverride(t *testing.T) {
	clearEnv(t)
	t.Setenv("OVERCAST_MCP_REPLAY_LIMIT", "64")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MCPReplayLimit != 64 {
		t.Fatalf("MCPReplayLimit = %d, want 64", cfg.MCPReplayLimit)
	}
}

func TestLoad_mcpReplayLimitRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"abc", "-1"} {
		t.Run(value, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("OVERCAST_MCP_REPLAY_LIMIT", value)
			if _, err := config.Load(); err == nil {
				t.Fatalf("expected error for OVERCAST_MCP_REPLAY_LIMIT=%q", value)
			}
		})
	}
}

func TestLoad_mcpRemoteExposureRequiresAuthToken(t *testing.T) {
	clearEnv(t)
	t.Setenv("OVERCAST_MCP_REMOTE_EXPOSURE", "true")

	if _, err := config.Load(); err == nil {
		t.Fatal("expected error when OVERCAST_MCP_REMOTE_EXPOSURE=true without token")
	}
}

func TestLoad_mcpRemoteExposureWithAuthToken(t *testing.T) {
	clearEnv(t)
	t.Setenv("OVERCAST_MCP_REMOTE_EXPOSURE", "true")
	t.Setenv("OVERCAST_MCP_AUTH_TOKEN", "test-token")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.MCPRemoteExposure {
		t.Fatal("expected MCPRemoteExposure=true")
	}
	if cfg.MCPAuthToken != "test-token" {
		t.Fatalf("MCPAuthToken = %q, want test-token", cfg.MCPAuthToken)
	}
}

func TestLoad_enforceIAMEnabled(t *testing.T) {
	clearEnv(t)
	t.Setenv("OVERCAST_ENFORCE_IAM", "true")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.EnforceIAM {
		t.Fatal("expected EnforceIAM=true")
	}
}

// ---- Helpers ---------------------------------------------------------------

// clearEnv unsets all OVERCAST_* environment variables for test isolation.
// t.Setenv is used for any variables needed in a test — it auto-restores on cleanup.
func clearEnv(t *testing.T) {
	t.Helper()
	awsEmuVars := []string{
		"OVERCAST_HOST", "OVERCAST_PORT", "OVERCAST_SERVICES", "OVERCAST_STATE",
		"OVERCAST_WAL_FSYNC", "OVERCAST_WAL_FSYNC_INTERVAL", "OVERCAST_WAL_MAX_LOG_BYTES",
		"OVERCAST_DATA_DIR", "OVERCAST_DEFAULT_REGION", "OVERCAST_ACCOUNT_ID",
		"OVERCAST_SIGV4_VALIDATE", "OVERCAST_ENFORCE_IAM", "OVERCAST_LOG_LEVEL", "OVERCAST_SHUTDOWN_TIMEOUT",
		"OVERCAST_LAMBDA_HOT_RELOAD",
		"OVERCAST_LAMBDA_NODE_BIN", "OVERCAST_DEBUG", "OVERCAST_TLS_CERT", "OVERCAST_TLS_KEY",
		"OVERCAST_HOSTNAME", "OVERCAST_EKS_MODE", "OVERCAST_EC2_VPC_STRATEGY",
		"OVERCAST_MCP_REPLAY_LIMIT", "OVERCAST_MCP_REMOTE_EXPOSURE", "OVERCAST_MCP_AUTH_TOKEN",
		"EKS_DOCKER_SOCKET", "EKS_NETWORK",
	}
	for _, v := range awsEmuVars {
		original := os.Getenv(v)
		os.Unsetenv(v)
		t.Cleanup(func() {
			if original != "" {
				os.Setenv(v, original)
			} else {
				os.Unsetenv(v)
			}
		})
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}
