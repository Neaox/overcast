package main

import (
	"context"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
)

// TestBuildServiceRoutes_routesByNamespacePrefix is a regression test for
// storage-plan item 3.14: per-service OVERCAST_STATE_<SERVICE> overrides must
// be routed by storage-namespace prefix (state.NamespacedStore.storeFor
// matches the segment before ":" in namespaces like "cfn:stacks"), not by the
// raw config service name. cloudformation, apigateway, and eventbridge have
// namespace prefixes ("cfn", "apigw", "eb") shorter than their config names —
// keying the route map by config name silently makes the override a no-op
// for exactly these three services.
//
// The global default mode is set to "persistent" purely so it differs from
// each override's "memory" mode (buildServiceRoutes skips building a
// dedicated store when the override mode matches the global default). The
// default store passed in is still a plain in-memory store constructed
// directly, and each override also resolves to a memory-backed store via
// buildStore's default case, so no SQLite file I/O happens in this test.
func TestBuildServiceRoutes_routesByNamespacePrefix(t *testing.T) {
	tests := []struct {
		service string
		prefix  string
		// namespace is a representative namespace the service actually
		// persists under, used for the end-to-end write assertion below.
		namespace string
	}{
		{"cloudformation", "cfn", "cfn:stacks"},
		{"apigateway", "apigw", "apigw:apis"},
		{"eventbridge", "eb", "eb:buses"},
		// Control case: sqs's config name already equals its namespace
		// prefix, so it must keep routing correctly too.
		{"sqs", "sqs", "sqs:queues"},
		// Colonless case: ssm persists under the bare namespace "ssm" (no
		// colon anywhere). Its override routes via NamespacedStore's
		// whole-name rule for colonless namespaces (fixed alongside 3.14 —
		// see internal/state/namespaced.go storeFor). The write-through
		// assertion below is what pins the two fixes composing end-to-end:
		// config-name → route key here, colonless namespace → route match
		// in the store.
		{"ssm", "ssm", "ssm"},
	}

	for _, tt := range tests {
		t.Run(tt.service, func(t *testing.T) {
			// Given: a config where this one service overrides the global
			// default storage mode
			cfg := &config.Config{
				State:   config.StateBackendPersistent,
				DataDir: t.TempDir(),
				ServiceStates: map[string]config.StateBackend{
					tt.service: config.StateBackendMemory,
				},
			}
			defaultStore := state.NewMemoryStore()
			logger := zap.NewNop()

			// When: we build the per-service routes
			result, err := buildServiceRoutes(cfg, defaultStore, logger)
			if err != nil {
				t.Fatalf("buildServiceRoutes: %v", err)
			}

			// Then: resolving by the service's real storage-namespace prefix
			// returns the override store, not the default store
			routed := state.Unwrap(result, tt.prefix)
			if routed == defaultStore {
				t.Errorf("state.Unwrap(result, %q) returned the default store — override for service %q did not take effect", tt.prefix, tt.service)
			}

			// And: an unrelated prefix still falls back to the default store
			other := state.Unwrap(result, "some-other-service")
			if other != defaultStore {
				t.Errorf("state.Unwrap(result, %q) = %v, want the default store (no override registered)", "some-other-service", other)
			}

			// And: a write through the wrapped store, against a namespace
			// the service actually uses, lands in the override store and
			// not the default — the end-to-end behavior the route-map key
			// and NamespacedStore's routing rules exist to produce.
			ctx := context.Background()
			if err := result.Set(ctx, tt.namespace, "k", "v"); err != nil {
				t.Fatalf("Set through wrapped store: %v", err)
			}
			if _, found, err := routed.Get(ctx, tt.namespace, "k"); err != nil || !found {
				t.Errorf("write to namespace %q did not land in the override store (found=%v err=%v)", tt.namespace, found, err)
			}
			if _, found, _ := defaultStore.Get(ctx, tt.namespace, "k"); found {
				t.Errorf("write to namespace %q leaked into the default store", tt.namespace)
			}
		})
	}
}

// TestBuildServiceRoutes_noOverrides verifies that buildServiceRoutes returns
// the default store unchanged when there are no per-service overrides, and
// when every configured override matches the global default mode (in which
// case building a dedicated store would be redundant).
func TestBuildServiceRoutes_noOverrides(t *testing.T) {
	logger := zap.NewNop()

	t.Run("empty ServiceStates", func(t *testing.T) {
		cfg := &config.Config{State: config.StateBackendMemory, DataDir: t.TempDir()}
		defaultStore := state.NewMemoryStore()

		result, err := buildServiceRoutes(cfg, defaultStore, logger)
		if err != nil {
			t.Fatalf("buildServiceRoutes: %v", err)
		}
		if result != defaultStore {
			t.Errorf("expected defaultStore to be returned unchanged, got a different store")
		}
	})

	t.Run("override matches global default", func(t *testing.T) {
		cfg := &config.Config{
			State:   config.StateBackendMemory,
			DataDir: t.TempDir(),
			ServiceStates: map[string]config.StateBackend{
				"sqs": config.StateBackendMemory, // same as cfg.State — should be skipped
			},
		}
		defaultStore := state.NewMemoryStore()

		result, err := buildServiceRoutes(cfg, defaultStore, logger)
		if err != nil {
			t.Fatalf("buildServiceRoutes: %v", err)
		}
		if result != defaultStore {
			t.Errorf("expected defaultStore to be returned unchanged when override mode matches global default")
		}
	})
}

// TestBuildServiceRoutes_warnsOnIneffectiveOverride verifies that overriding
// a service config.ServiceOverrideIneffective flags (e.g. "sts", whose
// session state actually lives under the "iam:sessions" namespace) still
// builds and routes a store as normal — the override is harmless — but logs
// a Warn so an operator relying on OVERCAST_STATE_STS isn't left assuming it
// took effect. A normal, effective override (sqs) must not log that Warn.
func TestBuildServiceRoutes_warnsOnIneffectiveOverride(t *testing.T) {
	t.Run("ineffective override warns", func(t *testing.T) {
		core, logs := observer.New(zapcore.DebugLevel)
		logger := zap.New(core)
		cfg := &config.Config{
			State:   config.StateBackendPersistent,
			DataDir: t.TempDir(),
			ServiceStates: map[string]config.StateBackend{
				"sts": config.StateBackendMemory,
			},
		}

		if _, err := buildServiceRoutes(cfg, state.NewMemoryStore(), logger); err != nil {
			t.Fatalf("buildServiceRoutes: %v", err)
		}

		warnings := logs.FilterMessage("service state override has no effect").All()
		if len(warnings) != 1 {
			t.Fatalf("expected exactly 1 'service state override has no effect' warning, got %d", len(warnings))
		}
		if got := warnings[0].ContextMap()["service"]; got != "sts" {
			t.Errorf("warning service field = %v, want %q", got, "sts")
		}
		if reason, _ := warnings[0].ContextMap()["reason"].(string); reason == "" {
			t.Error("expected a non-empty reason field on the warning")
		}
	})

	t.Run("effective override does not warn", func(t *testing.T) {
		core, logs := observer.New(zapcore.DebugLevel)
		logger := zap.New(core)
		cfg := &config.Config{
			State:   config.StateBackendPersistent,
			DataDir: t.TempDir(),
			ServiceStates: map[string]config.StateBackend{
				"sqs": config.StateBackendMemory,
			},
		}

		if _, err := buildServiceRoutes(cfg, state.NewMemoryStore(), logger); err != nil {
			t.Fatalf("buildServiceRoutes: %v", err)
		}

		if n := logs.FilterMessage("service state override has no effect").Len(); n != 0 {
			t.Errorf("expected no ineffective-override warnings for sqs, got %d", n)
		}
	})
}
