package config_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/config"
)

// TestLoad_defaults verifies that Load() returns sensible defaults when no
// environment variables are set.
//
// OVERCAST_STATE defaults to "auto" (config.StateSourceAuto), which resolves
// to a concrete backend from evidence of persistence intent (see
// resolveAutoState in state_auto.go; exhaustively covered by
// TestResolveAutoState below). To keep *this* test deterministic regardless
// of what happens to exist on the machine running it, OVERCAST_DATA_DIR is
// pinned to a fresh, empty t.TempDir() and marked OVERCAST_DATA_DIR_SOURCE=
// image — simulating the Docker image's own baked-in default data dir, i.e.
// a genuinely fresh, volume-less, unconfigured run — so none of the three
// auto signals fire and the resolved default is deterministically memory.
func TestLoad_defaults(t *testing.T) {
	// Given: no environment variables set, data dir pinned to a fresh temp
	// dir marked as the image's own default (not user-configured).
	clearEnv(t)
	t.Setenv("OVERCAST_DATA_DIR", t.TempDir())
	t.Setenv("OVERCAST_DATA_DIR_SOURCE", "image")

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
	if cfg.StateConfigured != "auto" {
		t.Errorf("StateConfigured: expected auto, got %q", cfg.StateConfigured)
	}
	if cfg.StateSource != config.StateSourceAuto {
		t.Errorf("StateSource: expected auto, got %q", cfg.StateSource)
	}
	if cfg.State != config.StateBackendMemory {
		t.Errorf("State: expected memory (no persistence signal), got %q", cfg.State)
	}
	if cfg.StateAutoSignal != "" {
		t.Errorf("StateAutoSignal: expected empty for a memory resolution, got %q", cfg.StateAutoSignal)
	}
	if cfg.StateAutoReason == "" {
		t.Error("StateAutoReason: expected a non-empty explanation")
	}
	if cfg.HybridFlushInterval != 5*time.Second {
		t.Errorf("HybridFlushInterval: expected 5s, got %v", cfg.HybridFlushInterval)
	}
	if cfg.HybridSyncMode != "interval" {
		t.Errorf("HybridSyncMode: expected interval, got %q", cfg.HybridSyncMode)
	}
	if cfg.HybridSyncInterval != 100*time.Millisecond {
		t.Errorf("HybridSyncInterval: expected 100ms, got %v", cfg.HybridSyncInterval)
	}
	if cfg.HybridDirtyEntryThreshold != 10000 {
		t.Errorf("HybridDirtyEntryThreshold: expected 10000, got %d", cfg.HybridDirtyEntryThreshold)
	}
	if cfg.HybridDirtyByteThreshold != 8388608 {
		t.Errorf("HybridDirtyByteThreshold: expected 8388608, got %d", cfg.HybridDirtyByteThreshold)
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
	// 0 means "unset": the Lambda runtime derives these from the Docker host
	// (falling back to 4/25/10 when /info is unavailable).
	if cfg.LambdaDockerMaxConcurrentStarts != 0 {
		t.Errorf("LambdaDockerMaxConcurrentStarts: expected 0 (auto), got %d", cfg.LambdaDockerMaxConcurrentStarts)
	}
	if cfg.LambdaMaxInstances != 0 {
		t.Errorf("LambdaMaxInstances: expected 0 (auto), got %d", cfg.LambdaMaxInstances)
	}
	if cfg.LambdaMaxInstancesPerFunction != 0 {
		t.Errorf("LambdaMaxInstancesPerFunction: expected 0 (auto), got %d", cfg.LambdaMaxInstancesPerFunction)
	}
	if cfg.LambdaMaxMemoryMB != 0 {
		t.Errorf("LambdaMaxMemoryMB: expected 0 (auto), got %d", cfg.LambdaMaxMemoryMB)
	}
	if cfg.LambdaSeedRuntimeImages {
		t.Error("LambdaSeedRuntimeImages: expected false by default")
	}
	if !cfg.LambdaProactiveInit {
		t.Error("LambdaProactiveInit: expected true by default (issue #1099)")
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
	if cfg.MCPRemoteExposure {
		t.Error("MCPRemoteExposure: expected false by default")
	}
	if cfg.MCPAuthToken != "" {
		t.Errorf("MCPAuthToken: expected empty by default, got %q", cfg.MCPAuthToken)
	}
	if cfg.EnforceIAM {
		t.Error("EnforceIAM: expected false by default")
	}
	if cfg.EnforceAPIGatewayThrottle {
		t.Error("EnforceAPIGatewayThrottle: expected false by default")
	}
	if cfg.ProtocolStrict {
		t.Error("ProtocolStrict: expected false by default (lenient drift posture)")
	}
	if cfg.CFNSyncWait != time.Second {
		t.Errorf("CFNSyncWait: expected 1s, got %v", cfg.CFNSyncWait)
	}
}

func TestLoad_cfnSyncWaitMS(t *testing.T) {
	// Given: OVERCAST_CFN_SYNC_WAIT_MS is set.
	clearEnv(t)
	t.Setenv("OVERCAST_CFN_SYNC_WAIT_MS", "250")

	// When: we load config.
	cfg, err := config.Load()

	// Then: the CloudFormation sync wait budget is parsed as milliseconds.
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.CFNSyncWait != 250*time.Millisecond {
		t.Fatalf("CFNSyncWait = %v, want 250ms", cfg.CFNSyncWait)
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

func TestLoad_lambdaMaxMemoryMB(t *testing.T) {
	// Given: LAMBDA_MAX_MEMORY_MB is set.
	clearEnv(t)
	t.Setenv("LAMBDA_MAX_MEMORY_MB", "2048")

	// When: we load config.
	cfg, err := config.Load()

	// Then: the aggregate Lambda memory budget is parsed.
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LambdaMaxMemoryMB != 2048 {
		t.Fatalf("LambdaMaxMemoryMB = %d, want 2048", cfg.LambdaMaxMemoryMB)
	}
}

func TestLoad_lambdaSeedRuntimeImages(t *testing.T) {
	// Given: LAMBDA_SEED_RUNTIME_IMAGES is enabled.
	clearEnv(t)
	t.Setenv("LAMBDA_SEED_RUNTIME_IMAGES", "true")

	// When: we load config.
	cfg, err := config.Load()

	// Then: broad startup runtime image seeding is enabled explicitly.
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.LambdaSeedRuntimeImages {
		t.Fatal("LambdaSeedRuntimeImages = false, want true")
	}
}

// TestLoad_lambdaProactiveInitOptOut pins the opt-out direction: the default
// flipped to true in issue #1099, so LAMBDA_PROACTIVE_INIT=false must still
// disable it.
func TestLoad_lambdaProactiveInitOptOut(t *testing.T) {
	// Given: LAMBDA_PROACTIVE_INIT is explicitly disabled.
	clearEnv(t)
	t.Setenv("LAMBDA_PROACTIVE_INIT", "false")

	// When: we load config.
	cfg, err := config.Load()

	// Then: proactive init is off despite the new default.
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LambdaProactiveInit {
		t.Fatal("LambdaProactiveInit = true, want false (explicit opt-out)")
	}
}

// TestLoad_lambdaProactiveInitExplicitTrue pins that an explicit "true" is
// equivalent to the default, so the opt-in spelling used before the flip
// keeps working unchanged.
func TestLoad_lambdaProactiveInitExplicitTrue(t *testing.T) {
	// Given: LAMBDA_PROACTIVE_INIT is explicitly enabled.
	clearEnv(t)
	t.Setenv("LAMBDA_PROACTIVE_INIT", "true")

	// When: we load config.
	cfg, err := config.Load()

	// Then: proactive init is on.
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.LambdaProactiveInit {
		t.Fatal("LambdaProactiveInit = false, want true (explicit opt-in)")
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

// TestLoad_ignoresRemovedServicesVar pins that OVERCAST_SERVICES is inert.
//
// It used to select which services ran. Every service is now always wired, so
// a stale value in an old compose file or CI job must load cleanly rather than
// failing validation or half-configuring anything.
func TestLoad_ignoresRemovedServicesVar(t *testing.T) {
	// Given: a leftover OVERCAST_SERVICES, including a name that never existed
	clearEnv(t)
	t.Setenv("OVERCAST_SERVICES", "s3,sqs,unknownservice")

	// When: we load config
	_, err := config.Load()

	// Then: it is ignored entirely
	if err != nil {
		t.Fatalf("Load should ignore OVERCAST_SERVICES, got: %v", err)
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
	if cfg.StateSource != config.StateSourceExplicit {
		t.Errorf("StateSource: expected explicit for an explicitly-set OVERCAST_STATE, got %q", cfg.StateSource)
	}
	if cfg.StateConfigured != "persistent" {
		t.Errorf("StateConfigured: expected persistent, got %q", cfg.StateConfigured)
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

// TestLoad_hybridSyncConfigOverrides verifies the pending-log sync mode and
// size-triggered flush thresholds (1.3/1.4) can be overridden.
func TestLoad_hybridSyncConfigOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("OVERCAST_HYBRID_SYNC", "always")
	t.Setenv("OVERCAST_HYBRID_SYNC_INTERVAL", "250ms")
	t.Setenv("OVERCAST_HYBRID_DIRTY_ENTRY_THRESHOLD", "42")
	t.Setenv("OVERCAST_HYBRID_DIRTY_BYTE_THRESHOLD", "4096")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HybridSyncMode != "always" {
		t.Errorf("HybridSyncMode: expected always, got %q", cfg.HybridSyncMode)
	}
	if cfg.HybridSyncInterval != 250*time.Millisecond {
		t.Errorf("HybridSyncInterval: expected 250ms, got %v", cfg.HybridSyncInterval)
	}
	if cfg.HybridDirtyEntryThreshold != 42 {
		t.Errorf("HybridDirtyEntryThreshold: expected 42, got %d", cfg.HybridDirtyEntryThreshold)
	}
	if cfg.HybridDirtyByteThreshold != 4096 {
		t.Errorf("HybridDirtyByteThreshold: expected 4096, got %d", cfg.HybridDirtyByteThreshold)
	}
}

func TestLoad_invalidHybridSyncConfig(t *testing.T) {
	t.Run("invalid sync mode", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("OVERCAST_HYBRID_SYNC", "sometimes")
		if _, err := config.Load(); err == nil {
			t.Fatal("expected error for invalid OVERCAST_HYBRID_SYNC")
		}
	})

	t.Run("invalid interval", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("OVERCAST_HYBRID_SYNC_INTERVAL", "0s")
		if _, err := config.Load(); err == nil {
			t.Fatal("expected error for invalid OVERCAST_HYBRID_SYNC_INTERVAL")
		}
	})

	t.Run("invalid dirty byte threshold", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("OVERCAST_HYBRID_DIRTY_BYTE_THRESHOLD", "0")
		if _, err := config.Load(); err == nil {
			t.Fatal("expected error for invalid OVERCAST_HYBRID_DIRTY_BYTE_THRESHOLD")
		}
	})
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

// TestLoad_perServiceStateUnknownService verifies that an override naming a
// service that does not exist is rejected rather than ignored.
//
// Load used to probe os.Getenv("OVERCAST_STATE_"+svc) for each known service,
// which cannot see a variable it does not think to construct. So
// OVERCAST_STATE_CLOUDWATCH_LOGS — the plausible guess for a service actually
// named "logs", and what the docs implied until recently — was a silent no-op:
// the user believed CloudWatch Logs was on its own backend while it quietly
// used the global one. Every misspelling and every post-rename name behaved the
// same way.
func TestLoad_perServiceStateUnknownService(t *testing.T) {
	for _, envKey := range []string{
		"OVERCAST_STATE_CLOUDWATCH_LOGS", // real service, wrong name for it
		"OVERCAST_STATE_S4",              // typo
		"OVERCAST_STATE_NOTASERVICE",
	} {
		t.Run(envKey, func(t *testing.T) {
			// Given: an override naming something that is not a service
			clearEnv(t)
			t.Setenv(envKey, "memory")

			// When: we load config
			_, err := config.Load()

			// Then: it fails, naming the offending variable
			if err == nil {
				t.Fatalf("expected %s to be rejected, got nil error", envKey)
			}
			if !strings.Contains(err.Error(), envKey) {
				t.Errorf("error should name %s, got: %v", envKey, err)
			}
		})
	}
}

// TestLoad_perServiceStateEmptyValueIgnored pins that an empty override is
// treated as unset, not as a service name to validate — matching how every
// other variable in Load behaves.
func TestLoad_perServiceStateEmptyValueIgnored(t *testing.T) {
	// Given: a real service override set to the empty string
	clearEnv(t)
	t.Setenv("OVERCAST_STATE_S3", "")

	// When: we load config
	cfg, err := config.Load()

	// Then: it loads, with no override recorded
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cfg.ServiceStates["s3"]; ok {
		t.Error("s3: expected no override for an empty value")
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

// ---- OVERCAST_STATE=auto (and unset-default) resolution ------------------
//
// These exercise Load() end-to-end for each auto-detection signal
// individually (the pure decision table across all signal combinations is
// covered by TestResolveAutoState_allSignalCombinations in
// state_auto_internal_test.go). Each test pins the environment so the
// resolution is deterministic regardless of the host running the test.

// TestLoad_explicitAutoSameAsUnset verifies that OVERCAST_STATE=auto,
// written out literally, behaves identically to leaving it unset.
func TestLoad_explicitAutoSameAsUnset(t *testing.T) {
	// Given: OVERCAST_STATE=auto (literal), and no persistence signal.
	clearEnv(t)
	t.Setenv("OVERCAST_STATE", "auto")
	t.Setenv("OVERCAST_DATA_DIR", t.TempDir())
	t.Setenv("OVERCAST_DATA_DIR_SOURCE", "image")

	// When
	cfg, err := config.Load()

	// Then: resolves exactly like the unset default (memory, no signal).
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.StateConfigured != "auto" {
		t.Errorf("StateConfigured: expected auto, got %q", cfg.StateConfigured)
	}
	if cfg.StateSource != config.StateSourceAuto {
		t.Errorf("StateSource: expected auto, got %q", cfg.StateSource)
	}
	if cfg.State != config.StateBackendMemory {
		t.Errorf("State: expected memory, got %q", cfg.State)
	}
}

// TestLoad_autoResolvesToHybridWhenDataDirExplicit verifies the
// explicit-data-dir signal: a native run where the user set OVERCAST_DATA_DIR
// themselves (no OVERCAST_DATA_DIR_SOURCE=image marker) is treated as
// deliberate persistence intent.
func TestLoad_autoResolvesToHybridWhenDataDirExplicit(t *testing.T) {
	// This assumes a build with SQLite support: in a -tags nosqlite build,
	// auto never resolves to hybrid regardless of evidence (see
	// TestResolveAutoState_sqliteUnavailableOverridesEveryEvidenceSignal in
	// state_auto_internal_test.go, which covers that gate directly).
	if !config.SQLiteSupported() {
		t.Skip("hybrid is unavailable in a -tags nosqlite build; the SQLite gate is covered directly in state_auto_internal_test.go")
	}

	// Given: OVERCAST_STATE unset, OVERCAST_DATA_DIR explicitly set (as a
	// native user would), with no OVERCAST_DATA_DIR_SOURCE marker.
	clearEnv(t)
	t.Setenv("OVERCAST_DATA_DIR", t.TempDir())

	// When
	cfg, err := config.Load()

	// Then: resolves to hybrid via the explicit-data-dir signal.
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.State != config.StateBackendHybrid {
		t.Errorf("State: expected hybrid, got %q", cfg.State)
	}
	if cfg.StateAutoSignal != "explicit-data-dir" {
		t.Errorf("StateAutoSignal: expected explicit-data-dir, got %q", cfg.StateAutoSignal)
	}
}

// TestLoad_autoResolvesToHybridWhenExistingDatabaseFound verifies the
// regression-guard signal: an overcast.db already present in the data
// directory means someone persisted state there before, so auto must not
// strand it in memory mode even with no other signal present.
func TestLoad_autoResolvesToHybridWhenExistingDatabaseFound(t *testing.T) {
	if !config.SQLiteSupported() {
		t.Skip("hybrid is unavailable in a -tags nosqlite build; the SQLite gate is covered directly in state_auto_internal_test.go")
	}

	// Given: a data directory marked as the image's own default (so the
	// explicit-data-dir signal does NOT fire) but containing an existing
	// overcast.db from a prior run.
	clearEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "overcast.db"), []byte{}, 0o644); err != nil {
		t.Fatalf("seed overcast.db: %v", err)
	}
	t.Setenv("OVERCAST_DATA_DIR", dir)
	t.Setenv("OVERCAST_DATA_DIR_SOURCE", "image")

	// When
	cfg, err := config.Load()

	// Then: resolves to hybrid via the existing-database signal, not memory.
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.State != config.StateBackendHybrid {
		t.Errorf("State: expected hybrid (existing database must not be stranded in memory mode), got %q", cfg.State)
	}
	if cfg.StateAutoSignal != "existing-database" {
		t.Errorf("StateAutoSignal: expected existing-database, got %q", cfg.StateAutoSignal)
	}
}

// TestLoad_explicitStateOverridesAuto verifies that an explicit OVERCAST_STATE
// always wins, even when auto-detection signals are present (e.g. a volume
// mounted at the data dir, or an existing database) — rule 1 from the design:
// "OVERCAST_STATE set by the user wins, exactly as today."
func TestLoad_explicitStateOverridesAuto(t *testing.T) {
	// Given: OVERCAST_STATE=memory explicitly, plus an existing database that
	// would otherwise trigger the auto resolver's hybrid outcome.
	clearEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "overcast.db"), []byte{}, 0o644); err != nil {
		t.Fatalf("seed overcast.db: %v", err)
	}
	t.Setenv("OVERCAST_STATE", "memory")
	t.Setenv("OVERCAST_DATA_DIR", dir)

	// When
	cfg, err := config.Load()

	// Then: the explicit setting wins outright — no auto resolution runs.
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.State != config.StateBackendMemory {
		t.Errorf("State: expected memory (explicit override wins), got %q", cfg.State)
	}
	if cfg.StateSource != config.StateSourceExplicit {
		t.Errorf("StateSource: expected explicit, got %q", cfg.StateSource)
	}
	if cfg.StateAutoSignal != "" {
		t.Errorf("StateAutoSignal: expected empty (auto resolution never ran), got %q", cfg.StateAutoSignal)
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

// TestLoad_tlsAutoMode verifies OVERCAST_TLS=auto enables TLS without
// explicit cert/key paths.
func TestLoad_tlsAutoMode(t *testing.T) {
	// Given: auto TLS mode, no cert/key
	clearEnv(t)
	t.Setenv("OVERCAST_TLS", "auto")

	// When: we load config
	cfg, err := config.Load()

	// Then: TLS is on and recognised as the auto flavour
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.TLSEnabled() {
		t.Error("expected TLSEnabled() with OVERCAST_TLS=auto")
	}
	if !cfg.TLSAuto() {
		t.Error("expected TLSAuto() with OVERCAST_TLS=auto")
	}
}

// TestLoad_tlsAutoCaseInsensitive verifies "AUTO" is accepted too.
func TestLoad_tlsAutoCaseInsensitive(t *testing.T) {
	clearEnv(t)
	t.Setenv("OVERCAST_TLS", "AUTO")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.TLSAuto() {
		t.Error("expected TLSAuto() with OVERCAST_TLS=AUTO")
	}
}

// TestLoad_tlsRejectsUnknownMode verifies a typo'd OVERCAST_TLS fails loudly
// rather than silently serving plain HTTP.
func TestLoad_tlsRejectsUnknownMode(t *testing.T) {
	clearEnv(t)
	t.Setenv("OVERCAST_TLS", "yes-please")

	if _, err := config.Load(); err == nil {
		t.Error("expected error for unknown OVERCAST_TLS value, got nil")
	}
}

// TestLoad_tlsAutoConflictsWithExplicitCert verifies that combining
// OVERCAST_TLS=auto with OVERCAST_TLS_CERT/KEY is rejected: the two are
// different answers to the same question and silently preferring one would
// hide a configuration mistake.
func TestLoad_tlsAutoConflictsWithExplicitCert(t *testing.T) {
	clearEnv(t)
	t.Setenv("OVERCAST_TLS", "auto")
	t.Setenv("OVERCAST_TLS_CERT", "/path/to/cert.pem")
	t.Setenv("OVERCAST_TLS_KEY", "/path/to/key.pem")

	if _, err := config.Load(); err == nil {
		t.Error("expected error when OVERCAST_TLS=auto is combined with explicit cert/key, got nil")
	}
}

// TestLoad_tlsOffValuesAccepted verifies the explicit "off" spellings keep
// TLS disabled rather than erroring.
func TestLoad_tlsOffValuesAccepted(t *testing.T) {
	for _, v := range []string{"", "off", "false", "0"} {
		t.Run("value="+v, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("OVERCAST_TLS", v)

			cfg, err := config.Load()
			if err != nil {
				t.Fatalf("Load with OVERCAST_TLS=%q: %v", v, err)
			}
			if cfg.TLSEnabled() || cfg.TLSAuto() {
				t.Errorf("OVERCAST_TLS=%q unexpectedly enabled TLS", v)
			}
		})
	}
}

// TestTLSAutoSANs verifies the SAN set the auto-minted certificate covers:
// loopback, every built-in wildcard DNS domain (apex, one-level wildcard, and
// the S3 virtual-host level), plus operator-configured names.
func TestTLSAutoSANs(t *testing.T) {
	// Given: a config with a custom hostname and an extra split-horizon host
	clearEnv(t)
	t.Setenv("OVERCAST_HOSTNAME", "overcast.internal")
	t.Setenv("OVERCAST_SPLIT_HORIZON_HOSTS", "dev.example.test")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// When: the SAN set is derived
	sans := cfg.TLSAutoSANs()
	has := func(want string) bool {
		for _, s := range sans {
			if s == want {
				return true
			}
		}
		return false
	}

	// Then: loopback names and addresses are covered
	for _, want := range []string{"localhost", "127.0.0.1", "::1"} {
		if !has(want) {
			t.Errorf("TLSAutoSANs missing %q (got %v)", want, sans)
		}
	}
	// And: each built-in wildcard DNS domain gets apex + wildcard + S3 level
	for _, base := range config.WildcardDNSDomains {
		for _, want := range []string{base, "*." + base, "*.s3." + base} {
			if !has(want) {
				t.Errorf("TLSAutoSANs missing %q (got %v)", want, sans)
			}
		}
	}
	// And: operator-configured names are covered the same way
	for _, want := range []string{
		"overcast.internal", "*.overcast.internal",
		"dev.example.test", "*.dev.example.test",
	} {
		if !has(want) {
			t.Errorf("TLSAutoSANs missing %q (got %v)", want, sans)
		}
	}
}

// TestTLSAutoSANs_ipHostname verifies an IP-literal OVERCAST_HOSTNAME becomes
// a SAN itself without nonsensical wildcard variants.
func TestTLSAutoSANs_ipHostname(t *testing.T) {
	clearEnv(t)
	t.Setenv("OVERCAST_HOSTNAME", "192.168.1.50")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	sans := cfg.TLSAutoSANs()
	foundIP, foundWildcard := false, false
	for _, s := range sans {
		if s == "192.168.1.50" {
			foundIP = true
		}
		if s == "*.192.168.1.50" {
			foundWildcard = true
		}
	}
	if !foundIP {
		t.Errorf("TLSAutoSANs missing IP hostname (got %v)", sans)
	}
	if foundWildcard {
		t.Errorf("TLSAutoSANs contains a wildcard over an IP literal (got %v)", sans)
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

// TestLoad_hotReloadInheritance verifies the umbrella/override relationship
// between OVERCAST_HOT_RELOAD and the per-service flags. The interesting case
// is the last one: an explicit "false" must beat an umbrella "true", which is
// what lets a developer run one service's containers from an image while the
// rest hot reload. That behaviour rests entirely on envBool returning its
// fallback only for an absent variable, so it is pinned here.
func TestLoad_hotReloadInheritance(t *testing.T) {
	tests := []struct {
		name                  string
		umbrella, lambda, ecs string
		wantLambda, wantECS   bool
	}{
		{name: "nothing set"},
		{name: "umbrella on", umbrella: "true", wantLambda: true, wantECS: true},
		{name: "lambda only", lambda: "true", wantLambda: true},
		{name: "ecs only", ecs: "true", wantECS: true},
		{name: "umbrella off, ecs on", umbrella: "false", ecs: "true", wantECS: true},
		{name: "umbrella on, lambda opted out", umbrella: "true", lambda: "false", wantECS: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			if tc.umbrella != "" {
				t.Setenv("OVERCAST_HOT_RELOAD", tc.umbrella)
			}
			if tc.lambda != "" {
				t.Setenv("OVERCAST_LAMBDA_HOT_RELOAD", tc.lambda)
			}
			if tc.ecs != "" {
				t.Setenv("OVERCAST_ECS_HOT_RELOAD", tc.ecs)
			}

			cfg, err := config.Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.LambdaHotReload != tc.wantLambda {
				t.Errorf("LambdaHotReload = %v, want %v", cfg.LambdaHotReload, tc.wantLambda)
			}
			if cfg.ECSHotReload != tc.wantECS {
				t.Errorf("ECSHotReload = %v, want %v", cfg.ECSHotReload, tc.wantECS)
			}
		})
	}
}

// TestLoad_hostBinding verifies custom host binding.
func TestLoad_hostBinding(t *testing.T) {
	// Given: OVERCAST_LISTEN is set to localhost only
	clearEnv(t)
	t.Setenv("OVERCAST_LISTEN", "127.0.0.1")
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
	// And: the single address is the whole bind set
	if got := cfg.Addrs(); len(got) != 1 || got[0] != "127.0.0.1:9000" {
		t.Errorf("Addrs(): expected [127.0.0.1:9000], got %v", got)
	}
}

// TestLoad_hostBindingList verifies OVERCAST_LISTEN accepts several addresses.
func TestLoad_hostBindingList(t *testing.T) {
	// Given: OVERCAST_LISTEN names loopback and a host-local bridge address —
	// the shape the compat launcher uses so a sibling container can reach an
	// emulator that is otherwise invisible outside this machine
	clearEnv(t)
	t.Setenv("OVERCAST_LISTEN", " 127.0.0.1 , 172.17.0.1 ,,127.0.0.1 ")
	t.Setenv("OVERCAST_PORT", "9000")

	// When: we load config
	cfg, err := config.Load()

	// Then: both addresses are bound, in order, with blanks and the repeat
	// dropped — a duplicate would fail the second bind with EADDRINUSE
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"127.0.0.1:9000", "172.17.0.1:9000"}
	if got := cfg.Addrs(); !slices.Equal(got, want) {
		t.Errorf("Addrs(): expected %v, got %v", want, got)
	}
	// And: Host stays the first address, so every reader that predates the
	// list — Addr(), the debug endpoint — sees what it always did
	if cfg.Host != "127.0.0.1" {
		t.Errorf("Host: expected the first address 127.0.0.1, got %q", cfg.Host)
	}
	if cfg.Addr() != "127.0.0.1:9000" {
		t.Errorf("Addr(): expected 127.0.0.1:9000, got %q", cfg.Addr())
	}
}

// TestLoad_hostBindingRejectsWildcardInList verifies a wildcard cannot be
// combined with a specific address.
func TestLoad_hostBindingRejectsWildcardInList(t *testing.T) {
	// Given: OVERCAST_LISTEN pairs a wildcard with a specific address
	// When: we load config
	// Then: it is refused. A wildcard already covers every address, and on
	// Linux the second bind fails with EADDRINUSE — which would surface as a
	// listen error at startup rather than as the configuration mistake it is.
	for _, host := range []string{"0.0.0.0,127.0.0.1", "127.0.0.1,::", "::,127.0.0.1"} {
		clearEnv(t)
		t.Setenv("OVERCAST_LISTEN", host)

		_, err := config.Load()
		if err == nil {
			t.Errorf("OVERCAST_LISTEN=%q: expected an error, got none", host)
			continue
		}
		if !strings.Contains(err.Error(), "OVERCAST_LISTEN") {
			t.Errorf("OVERCAST_LISTEN=%q: error %q does not name the variable", host, err)
		}
	}
}

// TestLoad_hostBindingBracketsIPv6Literals verifies an IPv6 address comes back
// in a form net.Listen accepts.
func TestLoad_hostBindingBracketsIPv6Literals(t *testing.T) {
	// Given: OVERCAST_LISTEN pairing the two loopbacks
	clearEnv(t)
	t.Setenv("OVERCAST_LISTEN", "127.0.0.1,::1")
	t.Setenv("OVERCAST_PORT", "9000")

	// When: we load config
	cfg, err := config.Load()

	// Then: the v6 address is bracketed. Unbracketed it formats as
	// "::1:9000", which net.Listen reads as a different address entirely —
	// the kind of thing nobody writes until a list makes pairing v4 and v6
	// the obvious thing to do.
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"127.0.0.1:9000", "[::1]:9000"}
	if got := cfg.Addrs(); !slices.Equal(got, want) {
		t.Errorf("Addrs(): expected %v, got %v", want, got)
	}
}

// TestLoad_hostBindingRejectsAnEmptyList verifies a list of nothing is an
// error rather than a silent fall back to the wildcard.
func TestLoad_hostBindingRejectsAnEmptyList(t *testing.T) {
	// Given: OVERCAST_LISTEN set to separators and whitespace only
	// When: we load config
	// Then: it is refused. Unset means "use the default"; set to something
	// that names no address means the value is wrong, and defaulting it would
	// bind every interface — the opposite of what anyone setting this wants.
	for _, host := range []string{",", " , ", ",,"} {
		clearEnv(t)
		t.Setenv("OVERCAST_LISTEN", host)

		if _, err := config.Load(); err == nil {
			t.Errorf("OVERCAST_LISTEN=%q: expected an error, got none", host)
		}
	}
}

// TestLoad_hostBindingDefaultsToWildcard verifies the default is unchanged.
func TestLoad_hostBindingDefaultsToWildcard(t *testing.T) {
	// Given: no OVERCAST_LISTEN
	clearEnv(t)

	// When: we load config
	cfg, err := config.Load()

	// Then: a single wildcard bind, as before the list was accepted
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Host != "0.0.0.0" {
		t.Errorf("Host: expected 0.0.0.0, got %q", cfg.Host)
	}
	if got := cfg.Addrs(); len(got) != 1 {
		t.Errorf("Addrs(): expected one address, got %v", got)
	}
}

// TestLoad_listenAlone verifies OVERCAST_LISTEN, the only bind-address
// variable since #870 renamed and removed OVERCAST_HOST, binds correctly.
func TestLoad_listenAlone(t *testing.T) {
	// Given: only OVERCAST_LISTEN is set
	clearEnv(t)
	t.Setenv("OVERCAST_LISTEN", "127.0.0.1")
	t.Setenv("OVERCAST_PORT", "9000")

	// When: we load config
	cfg, err := config.Load()

	// Then: it binds as expected
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr() != "127.0.0.1:9000" {
		t.Errorf("Addr(): expected 127.0.0.1:9000, got %q", cfg.Addr())
	}
}

// TestLoad_overcastHostRemoved_fails verifies that OVERCAST_HOST — the
// pre-#870 name for the bind address — is not silently accepted, ignored, or
// treated as an alias. Overcast is alpha software; the project's policy for a
// removed setting is a loud startup failure naming the replacement, so a
// leftover OVERCAST_HOST from before the rename cannot look like it took
// effect while quietly doing nothing.
func TestLoad_overcastHostRemoved_fails(t *testing.T) {
	// Given: the pre-rename name is set, whether or not OVERCAST_LISTEN also is
	for _, tc := range []struct {
		name      string
		setListen bool
		listenVal string
	}{
		{name: "host alone", setListen: false},
		{name: "host and listen set to the same value", setListen: true, listenVal: "127.0.0.1"},
		{name: "host and listen set to different values", setListen: true, listenVal: "0.0.0.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("OVERCAST_HOST", "127.0.0.1")
			if tc.setListen {
				t.Setenv("OVERCAST_LISTEN", tc.listenVal)
			}

			// When: we load config
			_, err := config.Load()

			// Then: it is refused, naming the replacement variable
			if err == nil {
				t.Fatal("expected an error when OVERCAST_HOST is set, got none")
			}
			for _, want := range []string{"OVERCAST_HOST", "OVERCAST_LISTEN", "removed"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

// TestAddrsWithoutLoad verifies Addrs() on a hand-built Config.
func TestAddrsWithoutLoad(t *testing.T) {
	// Given: a Config assembled in code rather than by Load — the shape every
	// test helper uses, where Hosts is never populated
	cfg := &config.Config{Host: "127.0.0.1", Port: 4566}

	// When: the bind set is read
	// Then: it falls back to the single Host, so a struct literal keeps working
	if got := cfg.Addrs(); len(got) != 1 || got[0] != "127.0.0.1:4566" {
		t.Errorf("Addrs(): expected [127.0.0.1:4566], got %v", got)
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

// TestLoad_splitHorizonHosts verifies OVERCAST_SPLIT_HORIZON_HOSTS is parsed as
// a comma-separated list, since it is written by hand in compose files.
func TestLoad_splitHorizonHosts(t *testing.T) {
	clearEnv(t)
	t.Setenv("OVERCAST_SPLIT_HORIZON_HOSTS", "localhost.example.test, aws.internal ,")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"localhost.example.test", "aws.internal"}
	if !slices.Equal(cfg.SplitHorizonHosts, want) {
		t.Errorf("SplitHorizonHosts: expected %v, got %v", want, cfg.SplitHorizonHosts)
	}
}

// TestLoad_splitHorizonHostsUnset verifies the default is empty rather than a
// single blank entry, which would produce a malformed /etc/hosts line.
func TestLoad_splitHorizonHostsUnset(t *testing.T) {
	clearEnv(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.SplitHorizonHosts) != 0 {
		t.Errorf("SplitHorizonHosts: expected none, got %v", cfg.SplitHorizonHosts)
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

// EFS defaults to live because live mode costs nothing without Docker: it
// creates volumes only while a daemon is reachable and otherwise behaves
// exactly like mock. Defaulting to mock meant a file system silently had no
// storage behind it on a machine that could have backed it for free.
func TestLoad_efsModeDefaultsToLive(t *testing.T) {
	clearEnv(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.EFSMode != config.EFSModeLive {
		t.Fatalf("EFSMode: expected %q, got %q", config.EFSModeLive, cfg.EFSMode)
	}
}

func TestLoad_efsModeLive(t *testing.T) {
	clearEnv(t)
	t.Setenv("OVERCAST_EFS_MODE", "live")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.EFSMode != config.EFSModeLive {
		t.Fatalf("EFSMode: expected %q, got %q", config.EFSModeLive, cfg.EFSMode)
	}
}

// Mock stays reachable as an explicit opt-out for anyone who wants EFS to keep
// its hands off Docker entirely.
func TestLoad_efsModeMock(t *testing.T) {
	clearEnv(t)
	t.Setenv("OVERCAST_EFS_MODE", "mock")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.EFSMode != config.EFSModeMock {
		t.Fatalf("EFSMode: expected %q, got %q", config.EFSModeMock, cfg.EFSMode)
	}
}

// RDS defaults to live for the same reason EFS does: a DB instance nothing
// answers on is not much of a database, and live mode degrades to metadata-only
// by itself when no daemon is reachable.
func TestLoad_rdsModeDefaultsToLive(t *testing.T) {
	clearEnv(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.RDSMode != config.RDSModeLive {
		t.Fatalf("RDSMode: expected %q, got %q", config.RDSModeLive, cfg.RDSMode)
	}
}

// Mock is the opt-out for an environment that has a daemon but cannot afford
// the tens of seconds a real engine takes to accept its first connection.
func TestLoad_rdsModeMock(t *testing.T) {
	clearEnv(t)
	t.Setenv("OVERCAST_RDS_MODE", "mock")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.RDSMode != config.RDSModeMock {
		t.Fatalf("RDSMode: expected %q, got %q", config.RDSModeMock, cfg.RDSMode)
	}
}

func TestLoad_rdsModeRejectsInvalidValues(t *testing.T) {
	clearEnv(t)
	t.Setenv("OVERCAST_RDS_MODE", "metadata")

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error for invalid OVERCAST_RDS_MODE, got nil")
	}
	if got := err.Error(); got == "" || !containsAll(got, "OVERCAST_RDS_MODE", "mock", "live") {
		t.Fatalf("unexpected error message: %q", got)
	}
}

func TestLoad_efsModeRejectsInvalidValues(t *testing.T) {
	clearEnv(t)
	t.Setenv("OVERCAST_EFS_MODE", "nfs")

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error for invalid OVERCAST_EFS_MODE, got nil")
	}
	if got := err.Error(); got == "" || !containsAll(got, "OVERCAST_EFS_MODE", "mock", "live") {
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
}

func TestLoad_eksDockerOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("EKS_DOCKER_SOCKET", "tcp://eksdind:2375")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.EKSDockerSocket != "tcp://eksdind:2375" {
		t.Fatalf("EKSDockerSocket: expected tcp://eksdind:2375, got %q", cfg.EKSDockerSocket)
	}
}

// Every container Overcast starts shares two networks rather than one per
// service, so there is one knob and the control plane is derived from it —
// there is no way to configure half a pair. See internal/dataplane.
func TestLoad_networkPlanes(t *testing.T) {
	clearEnv(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Network != "overcast" {
		t.Fatalf("Network: expected overcast, got %q", cfg.Network)
	}
	if got := cfg.ControlNetwork(); got != "overcast_control" {
		t.Fatalf("ControlNetwork: expected overcast_control, got %q", got)
	}
}

func TestLoad_networkPlanesOverride(t *testing.T) {
	clearEnv(t)
	t.Setenv("OVERCAST_NETWORK", "acme")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Network != "acme" {
		t.Fatalf("Network: expected acme, got %q", cfg.Network)
	}
	if got := cfg.ControlNetwork(); got != "acme_control" {
		t.Fatalf("ControlNetwork: expected acme_control, got %q", got)
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

func TestLoad_enforceAPIGatewayThrottleEnabled(t *testing.T) {
	clearEnv(t)
	t.Setenv("OVERCAST_ENFORCE_APIGATEWAY_THROTTLE", "true")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.EnforceAPIGatewayThrottle {
		t.Fatal("expected EnforceAPIGatewayThrottle=true")
	}
}

func TestLoad_protocolStrictEnabled(t *testing.T) {
	clearEnv(t)
	t.Setenv("OVERCAST_PROTOCOL_STRICT", "true")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.ProtocolStrict {
		t.Fatal("expected ProtocolStrict=true")
	}
}

func TestLoad_protocolStrictDisabledByDefault(t *testing.T) {
	clearEnv(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ProtocolStrict {
		t.Fatal("expected ProtocolStrict=false by default")
	}
}

// ---- Helpers ---------------------------------------------------------------

// clearEnv unsets all OVERCAST_* environment variables for test isolation.
// t.Setenv is used for any variables needed in a test — it auto-restores on cleanup.
func clearEnv(t *testing.T) {
	t.Helper()
	awsEmuVars := []string{
		"OVERCAST_HOST", "OVERCAST_LISTEN", "OVERCAST_PORT", "OVERCAST_SERVICES", "OVERCAST_STATE",
		"OVERCAST_HYBRID_FLUSH_INTERVAL", "OVERCAST_HYBRID_SYNC", "OVERCAST_HYBRID_SYNC_INTERVAL",
		"OVERCAST_HYBRID_DIRTY_ENTRY_THRESHOLD", "OVERCAST_HYBRID_DIRTY_BYTE_THRESHOLD",
		"OVERCAST_WAL_FSYNC", "OVERCAST_WAL_FSYNC_INTERVAL", "OVERCAST_WAL_MAX_LOG_BYTES",
		"OVERCAST_DATA_DIR", "OVERCAST_DATA_DIR_SOURCE", "OVERCAST_DEFAULT_REGION", "OVERCAST_ACCOUNT_ID",
		"OVERCAST_SIGV4_VALIDATE", "OVERCAST_ENFORCE_IAM", "OVERCAST_ENFORCE_APIGATEWAY_THROTTLE",
		"OVERCAST_PROTOCOL_STRICT", "OVERCAST_LOG_LEVEL", "OVERCAST_SHUTDOWN_TIMEOUT",
		"OVERCAST_LAMBDA_HOT_RELOAD",
		"OVERCAST_DEBUG", "OVERCAST_TLS", "OVERCAST_TLS_CERT", "OVERCAST_TLS_KEY",
		"OVERCAST_HOSTNAME", "OVERCAST_SPLIT_HORIZON_HOSTS", "OVERCAST_EKS_MODE", "OVERCAST_EC2_VPC_STRATEGY",
		// The mode defaults these assert are only defaults if the developer
		// running the suite has not exported an opt-out of their own.
		"OVERCAST_EFS_MODE", "OVERCAST_RDS_MODE",
		"OVERCAST_MCP_REMOTE_EXPOSURE", "OVERCAST_MCP_AUTH_TOKEN",
		"EKS_DOCKER_SOCKET", "OVERCAST_NETWORK",
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
