package config_test

import (
	"os"
	"testing"
	"time"

	"github.com/your-org/overcast/internal/config"
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
	if cfg.State != config.StateBackendMemory {
		t.Errorf("State: expected memory, got %q", cfg.State)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel: expected info, got %q", cfg.LogLevel)
	}
	if cfg.ShutdownTimeout != 30*time.Second {
		t.Errorf("ShutdownTimeout: expected 30s, got %v", cfg.ShutdownTimeout)
	}
	if cfg.Debug {
		t.Error("Debug: expected false by default")
	}
	if cfg.TLSEnabled() {
		t.Error("TLS: expected disabled by default")
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

// TestLoad_sqliteState verifies SQLite backend is selected correctly.
func TestLoad_sqliteState(t *testing.T) {
	// Given: OVERCAST_STATE=sqlite
	clearEnv(t)
	t.Setenv("OVERCAST_STATE", "sqlite")
	t.Setenv("OVERCAST_DATA_DIR", "/tmp/test-overcast")

	// When: we load config
	cfg, err := config.Load()

	// Then: SQLite backend is selected
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.State != config.StateBackendSQLite {
		t.Errorf("expected sqlite backend, got %q", cfg.State)
	}
	if cfg.DataDir != "/tmp/test-overcast" {
		t.Errorf("DataDir: expected /tmp/test-overcast, got %q", cfg.DataDir)
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

// ---- Helpers ---------------------------------------------------------------

// clearEnv unsets all OVERCAST_* environment variables for test isolation.
// t.Setenv is used for any variables needed in a test — it auto-restores on cleanup.
func clearEnv(t *testing.T) {
	t.Helper()
	awsEmuVars := []string{
		"OVERCAST_HOST", "OVERCAST_PORT", "OVERCAST_SERVICES", "OVERCAST_STATE",
		"OVERCAST_DATA_DIR", "OVERCAST_REGION", "OVERCAST_ACCOUNT_ID",
		"OVERCAST_SIGV4_VALIDATE", "OVERCAST_LOG_LEVEL", "OVERCAST_SHUTDOWN_TIMEOUT",
		"OVERCAST_LAMBDA_NODE_BIN", "OVERCAST_DEBUG", "OVERCAST_TLS_CERT", "OVERCAST_TLS_KEY",
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
