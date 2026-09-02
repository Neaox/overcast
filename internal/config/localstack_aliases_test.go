package config_test

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/internal/config"
)

// localstack_aliases_test.go covers the LocalStack-compatibility aliases added
// by the drop-in-replacement audit: LS_LOG, ENFORCE_IAM,
// LAMBDA_REMOVE_CONTAINERS and DNS_ADDRESS, plus the expanded
// recognised-but-inert table. The earlier aliases (EDGE_PORT, GATEWAY_LISTEN,
// DEFAULT_REGION, DATA_DIR, DEBUG, PERSISTENCE, LOCALSTACK_HOST) are covered
// in config_test.go where they were added.

// TestLoad_lsLogAlias covers the level vocabulary, the two spellings that
// differ from Overcast's, and the rejection of anything else.
func TestLoad_lsLogAlias(t *testing.T) {
	tests := []struct {
		lsLog string
		want  string
	}{
		{"debug", "debug"},
		{"info", "info"},
		{"error", "error"},
		{"warn", "warn"},
		{"warning", "warn"},
		{"trace", "trace"},
		{"trace-internal", "trace"},
		{"DEBUG", "debug"},
		{"  warning  ", "warn"},
	}
	for _, tt := range tests {
		t.Run(tt.lsLog, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("LS_LOG", tt.lsLog)

			cfg, err := config.Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.LogLevel != tt.want {
				t.Fatalf("LogLevel = %q, want %q", cfg.LogLevel, tt.want)
			}
			if got := cfg.LocalStackAliasesUsed["OVERCAST_LOG_LEVEL"]; got != "LS_LOG" {
				t.Fatalf("LocalStackAliasesUsed[OVERCAST_LOG_LEVEL] = %q, want LS_LOG", got)
			}
		})
	}
}

func TestLoad_lsLogAliasRejectsUnknownLevel(t *testing.T) {
	clearEnv(t)
	t.Setenv("LS_LOG", "verbose")

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected an error: an unrecognised LS_LOG must not silently fall back to info")
	}
	if !containsAll(err.Error(), "LS_LOG", "verbose") {
		t.Fatalf("error should name the variable and the value, got: %v", err)
	}
}

// TestLoad_lsLogAndDebugDisagree pins the conflict rule across the two
// LocalStack variables that both feed the log level. LocalStack documents
// LS_LOG as overriding DEBUG; here disagreement fails naming both rather than
// one quietly winning.
func TestLoad_lsLogAndDebugDisagree(t *testing.T) {
	clearEnv(t)
	t.Setenv("DEBUG", "1")
	t.Setenv("LS_LOG", "error")

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected an error: DEBUG=1 and LS_LOG=error disagree")
	}
	if !containsAll(err.Error(), "DEBUG", "LS_LOG") {
		t.Fatalf("error should name both variables, got: %v", err)
	}
}

// TestLoad_lsLogAndDebugAgree is the case a compose file migrated line by
// line actually produces: both set, both meaning debug.
func TestLoad_lsLogAndDebugAgree(t *testing.T) {
	clearEnv(t)
	t.Setenv("DEBUG", "1")
	t.Setenv("LS_LOG", "debug")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LogLevel != "debug" {
		t.Fatalf("LogLevel = %q, want debug", cfg.LogLevel)
	}
	source := cfg.LocalStackAliasesUsed["OVERCAST_LOG_LEVEL"]
	if !containsAll(source, "DEBUG", "LS_LOG") {
		t.Fatalf("alias source = %q, want both contributing variables named", source)
	}
}

func TestLoad_enforceIAMAlias(t *testing.T) {
	t.Run("ENFORCE_IAM=1 turns enforcement on", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("ENFORCE_IAM", "1")

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if !cfg.EnforceIAM {
			t.Fatal("EnforceIAM = false, want true")
		}
		if got := cfg.LocalStackAliasesUsed["OVERCAST_ENFORCE_IAM"]; got != "ENFORCE_IAM" {
			t.Fatalf("alias source = %q, want ENFORCE_IAM", got)
		}
	})

	t.Run("spellings that mean the same thing are not a conflict", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("ENFORCE_IAM", "1")
		t.Setenv("OVERCAST_ENFORCE_IAM", "true")

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load: %v — 1 and true are the same value, not a disagreement", err)
		}
		if !cfg.EnforceIAM {
			t.Fatal("EnforceIAM = false, want true")
		}
	})

	t.Run("genuine disagreement fails naming both", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("ENFORCE_IAM", "0")
		t.Setenv("OVERCAST_ENFORCE_IAM", "true")

		_, err := config.Load()
		if err == nil {
			t.Fatal("expected an error")
		}
		if !containsAll(err.Error(), "ENFORCE_IAM", "OVERCAST_ENFORCE_IAM") {
			t.Fatalf("error should name both variables, got: %v", err)
		}
	})
}

// TestLoad_lambdaRemoveContainersAlias covers the polarity inversion, which is
// the whole of the difference between the two variables.
func TestLoad_lambdaRemoveContainersAlias(t *testing.T) {
	t.Run("LAMBDA_REMOVE_CONTAINERS=0 keeps containers", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("LAMBDA_REMOVE_CONTAINERS", "0")

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if !cfg.LambdaKeepContainers {
			t.Fatal("LambdaKeepContainers = false, want true — remove=0 means keep")
		}
		if got := cfg.LocalStackAliasesUsed["LAMBDA_KEEP_CONTAINERS"]; got != "LAMBDA_REMOVE_CONTAINERS" {
			t.Fatalf("alias source = %q, want LAMBDA_REMOVE_CONTAINERS", got)
		}
	})

	t.Run("LAMBDA_REMOVE_CONTAINERS=1 is LocalStack's default and Overcast's", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("LAMBDA_REMOVE_CONTAINERS", "1")

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.LambdaKeepContainers {
			t.Fatal("LambdaKeepContainers = true, want false")
		}
	})

	t.Run("the inverted spellings agree rather than conflict", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("LAMBDA_REMOVE_CONTAINERS", "0")
		t.Setenv("LAMBDA_KEEP_CONTAINERS", "1")

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load: %v — remove=0 and keep=1 say the same thing", err)
		}
		if !cfg.LambdaKeepContainers {
			t.Fatal("LambdaKeepContainers = false, want true")
		}
	})

	t.Run("genuine disagreement fails naming both", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("LAMBDA_REMOVE_CONTAINERS", "1")
		t.Setenv("LAMBDA_KEEP_CONTAINERS", "true")

		_, err := config.Load()
		if err == nil {
			t.Fatal("expected an error")
		}
		if !containsAll(err.Error(), "LAMBDA_REMOVE_CONTAINERS", "LAMBDA_KEEP_CONTAINERS") {
			t.Fatalf("error should name both variables, got: %v", err)
		}
	})
}

// TestLoad_dnsAddressAlias: only DNS_ADDRESS=0 maps, because only that value
// means anything Overcast can act on.
func TestLoad_dnsAddressAlias(t *testing.T) {
	t.Run("DNS_ADDRESS=0 turns the resolver off", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("DNS_ADDRESS", "0")

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.DNSEnabled {
			t.Fatal("DNSEnabled = true, want false")
		}
		if got := cfg.LocalStackAliasesUsed["OVERCAST_DNS"]; got != "DNS_ADDRESS" {
			t.Fatalf("alias source = %q, want DNS_ADDRESS", got)
		}
	})

	t.Run("a bind address leaves the resolver alone", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("DNS_ADDRESS", "127.0.0.1")

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if !cfg.DNSEnabled {
			t.Fatal("DNSEnabled = false, want true — a bind address is not an off switch")
		}
		if _, aliased := cfg.LocalStackAliasesUsed["OVERCAST_DNS"]; aliased {
			t.Fatal("a bind address must not be recorded as having supplied OVERCAST_DNS")
		}
	})

	t.Run("DNS_ADDRESS=0 with OVERCAST_DNS=true fails naming both", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("DNS_ADDRESS", "0")
		t.Setenv("OVERCAST_DNS", "true")

		_, err := config.Load()
		if err == nil {
			t.Fatal("expected an error")
		}
		if !containsAll(err.Error(), "DNS_ADDRESS", "OVERCAST_DNS") {
			t.Fatalf("error should name both variables, got: %v", err)
		}
	})
}

// TestLoad_ignoredLocalStackVarsExpanded checks that the variables added by
// the drop-in audit are reported, including a prefix-matched one, and that a
// variable Overcast knows nothing about is not — the list is a statement that
// something was considered, so it must not become a catch-all.
func TestLoad_ignoredLocalStackVarsExpanded(t *testing.T) {
	clearEnv(t)
	t.Setenv("SQS_ENDPOINT_STRATEGY", "domain")
	t.Setenv("LAMBDA_DOCKER_NETWORK", "bridge")
	t.Setenv("PROVIDER_OVERRIDE_APIGATEWAY", "next_gen")
	t.Setenv("SOMETHING_ELSE_ENTIRELY", "1")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v — an inert variable must never fail startup", err)
	}
	for _, want := range []string{"SQS_ENDPOINT_STRATEGY", "LAMBDA_DOCKER_NETWORK", "PROVIDER_OVERRIDE_APIGATEWAY"} {
		if !slices.Contains(cfg.IgnoredLocalStackVars, want) {
			t.Errorf("IgnoredLocalStackVars = %v, want it to contain %s", cfg.IgnoredLocalStackVars, want)
		}
	}
	if slices.Contains(cfg.IgnoredLocalStackVars, "SOMETHING_ELSE_ENTIRELY") {
		t.Error("an unrelated variable must not be reported as a recognised LocalStack setting")
	}
}

// TestIgnoredLocalStackVars_allHaveReasons: being on the inert list says the
// variable was considered and found to have nothing to map onto. A default
// "recognised but has no effect" tells the operator nothing they could not
// already see, so every entry owes a real sentence.
func TestIgnoredLocalStackVars_allHaveReasons(t *testing.T) {
	clearEnv(t)
	for _, name := range allIgnoredLocalStackVars(t) {
		reason := config.IgnoredLocalStackReason(name)
		if reason == "" || reason == "recognised but has no effect" {
			t.Errorf("%s has no specific reason — add one to IgnoredLocalStackReason", name)
		}
		if strings.HasSuffix(reason, ".") {
			t.Errorf("%s: reason %q should not end in a full stop — it is appended to a log sentence", name, reason)
		}
	}
}

// allIgnoredLocalStackVars recovers the inert table's contents through the
// exported surface: set every candidate, load, and read back what Load
// reported. Keeps the test honest about the real list rather than a copy of
// it that would drift.
func allIgnoredLocalStackVars(t *testing.T) []string {
	t.Helper()
	candidates := []string{
		"SERVICES", "EAGER_SERVICE_LOADING", "LOCALSTACK_API_KEY", "LOCALSTACK_AUTH_TOKEN",
		"ACTIVATE_PRO", "MAIN_CONTAINER_NAME", "DISABLE_EVENTS", "SKIP_SSL_CERT_DOWNLOAD",
		"DISABLE_CORS_CHECKS", "DISABLE_CORS_HEADERS", "EXTRA_CORS_ALLOWED_ORIGINS",
		"EXTRA_CORS_ALLOWED_HEADERS", "SQS_ENDPOINT_STRATEGY", "S3_SKIP_SIGNATURE_VALIDATION",
		"IAM_SOFT_MODE", "LAMBDA_KEEPALIVE_MS", "LAMBDA_DOCKER_NETWORK", "LAMBDA_DOCKER_FLAGS",
		"LAMBDA_RUNTIME_EXECUTOR", "SNAPSHOT_SAVE_STRATEGY", "SNAPSHOT_LOAD_STRATEGY",
		"SNAPSHOT_FLUSH_INTERVAL", "ALLOW_NONSTANDARD_REGIONS", "ENABLE_CONFIG_UPDATES",
		"PROVIDER_OVERRIDE_CLOUDWATCH",
	}
	for _, name := range candidates {
		t.Setenv(name, "set")
		t.Cleanup(func() { os.Unsetenv(name) })
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.IgnoredLocalStackVars) != len(candidates) {
		t.Fatalf("reported %d inert variables, set %d — the candidate list here has drifted from the real table:\n got: %v\nwant: %v",
			len(cfg.IgnoredLocalStackVars), len(candidates), cfg.IgnoredLocalStackVars, candidates)
	}
	return cfg.IgnoredLocalStackVars
}
