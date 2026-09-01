package lambda

// host_limits_test.go — deriving instance limits from the Docker host.

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/docker"
)

const gib = 1024 * 1024 * 1024

// fakeInfoClient serves a canned Docker /info response, or an error.
type fakeInfoClient struct {
	info   *docker.SystemInfo
	err    error
	called bool
}

func (f *fakeInfoClient) Info(context.Context) (*docker.SystemInfo, error) {
	f.called = true
	if f.err != nil {
		return nil, f.err
	}
	return f.info, nil
}

// autoCfg is a config with every derivable Lambda limit left unset (0 = auto).
func autoCfg() *config.Config {
	return &config.Config{LambdaMaxWarmInstances: 10}
}

func TestResolveRuntimeLimits_derivesFromSmallHost(t *testing.T) {
	// Given: no limits pinned via env, and a small Docker host — two cores,
	// 2 GiB of memory.
	dc := &fakeInfoClient{info: &docker.SystemInfo{NCPU: 2, MemTotal: 2 * gib}}

	// When: limits are resolved.
	got := resolveRuntimeLimits(context.Background(), autoCfg(), dc, zap.NewNop())

	// Then: the limits shrink to what the host can carry. Concurrent starts are
	// floored at 2 (NCPU/2 = 1 clamps up); instances follow the memory budget:
	// 65% of 2048 MiB / 256 MiB per instance = 5.
	if got.maxConcurrentStarts != 2 {
		t.Errorf("maxConcurrentStarts = %d, want 2", got.maxConcurrentStarts)
	}
	if got.pool.MaxInstances != 5 {
		t.Errorf("MaxInstances = %d, want 5", got.pool.MaxInstances)
	}
	if got.pool.MaxInstancesPerFunction != 2 {
		t.Errorf("MaxInstancesPerFunction = %d, want 2 (5/2 = 2)", got.pool.MaxInstancesPerFunction)
	}
	if got.pool.MaxMemoryMB != 1331 {
		t.Errorf("MaxMemoryMB = %d, want 1331 (65%% of 2048 MiB)", got.pool.MaxMemoryMB)
	}
}

func TestResolveRuntimeLimits_derivesFromBigHost(t *testing.T) {
	// Given: a big Docker host — 32 cores, 64 GiB.
	dc := &fakeInfoClient{info: &docker.SystemInfo{NCPU: 32, MemTotal: 64 * gib}}

	// When: limits are resolved.
	got := resolveRuntimeLimits(context.Background(), autoCfg(), dc, zap.NewNop())

	// Then: every derived limit hits its ceiling — a bigger host must not turn
	// Overcast into a fork bomb.
	if got.maxConcurrentStarts != 8 {
		t.Errorf("maxConcurrentStarts = %d, want 8 (ceiling)", got.maxConcurrentStarts)
	}
	if got.pool.MaxInstances != 32 {
		t.Errorf("MaxInstances = %d, want 32 (ceiling)", got.pool.MaxInstances)
	}
	if got.pool.MaxInstancesPerFunction != 16 {
		t.Errorf("MaxInstancesPerFunction = %d, want 16 (32/2)", got.pool.MaxInstancesPerFunction)
	}
}

func TestResolveRuntimeLimits_explicitEnvAlwaysWins(t *testing.T) {
	// Given: every limit pinned via env vars, on a host whose derivation would
	// disagree with all of them.
	cfg := &config.Config{
		LambdaMaxWarmInstances:          10,
		LambdaMaxInstances:              50,
		LambdaMaxInstancesPerFunction:   40,
		LambdaDockerMaxConcurrentStarts: 12,
		LambdaMaxMemoryMB:               8192,
	}
	dc := &fakeInfoClient{info: &docker.SystemInfo{NCPU: 2, MemTotal: 2 * gib}}

	// When: limits are resolved.
	got := resolveRuntimeLimits(context.Background(), cfg, dc, zap.NewNop())

	// Then: the pinned values are used untouched, and Docker is not even asked.
	if dc.called {
		t.Error("Docker /info was called although every limit was pinned")
	}
	if got.maxConcurrentStarts != 12 || got.pool.MaxInstances != 50 || got.pool.MaxInstancesPerFunction != 40 {
		t.Errorf("resolved = (starts=%d, max=%d, perFn=%d), want the pinned (12, 50, 40)",
			got.maxConcurrentStarts, got.pool.MaxInstances, got.pool.MaxInstancesPerFunction)
	}
	if got.pool.MaxMemoryMB != 8192 {
		t.Errorf("MaxMemoryMB = %d, want the pinned 8192", got.pool.MaxMemoryMB)
	}
}

func TestResolveRuntimeLimits_infoFailureFallsBackToBuiltInDefaults(t *testing.T) {
	// Given: nothing pinned and a daemon whose /info cannot be read.
	dc := &fakeInfoClient{err: errors.New("docker info: connection refused")}

	// When: limits are resolved.
	got := resolveRuntimeLimits(context.Background(), autoCfg(), dc, zap.NewNop())

	// Then: the previous hardcoded defaults apply, so a daemon with a broken
	// /info behaves exactly as every release before derivation existed.
	if got.maxConcurrentStarts != 4 {
		t.Errorf("maxConcurrentStarts = %d, want the built-in default 4", got.maxConcurrentStarts)
	}
	if got.pool.MaxInstances != 25 {
		t.Errorf("MaxInstances = %d, want the built-in default 25", got.pool.MaxInstances)
	}
	if got.pool.MaxInstancesPerFunction != 10 {
		t.Errorf("MaxInstancesPerFunction = %d, want the built-in default 10", got.pool.MaxInstancesPerFunction)
	}
	if got.pool.MaxMemoryMB != 0 {
		t.Errorf("MaxMemoryMB = %d, want 0 (unlimited — the pre-budget behaviour)", got.pool.MaxMemoryMB)
	}
}

func TestResolveRuntimeLimits_partialPinDerivesTheRest(t *testing.T) {
	// Given: only the global instance cap pinned, on a big host.
	cfg := autoCfg()
	cfg.LambdaMaxInstances = 6
	dc := &fakeInfoClient{info: &docker.SystemInfo{NCPU: 16, MemTotal: 32 * gib}}

	// When: limits are resolved.
	got := resolveRuntimeLimits(context.Background(), cfg, dc, zap.NewNop())

	// Then: the pinned cap holds, and the per-function limit derives from the
	// effective cap — not from the host's much larger one, which the pin was
	// there to override.
	if got.pool.MaxInstances != 6 {
		t.Errorf("MaxInstances = %d, want the pinned 6", got.pool.MaxInstances)
	}
	if got.pool.MaxInstancesPerFunction != 3 {
		t.Errorf("MaxInstancesPerFunction = %d, want 3 (pinned 6 / 2)", got.pool.MaxInstancesPerFunction)
	}
	if got.maxConcurrentStarts != 8 {
		t.Errorf("maxConcurrentStarts = %d, want 8 (16 cores / 2)", got.maxConcurrentStarts)
	}
}
