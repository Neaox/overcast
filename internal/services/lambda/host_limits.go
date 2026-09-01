package lambda

// host_limits.go — deriving Lambda instance limits from the Docker host.
//
// The instance limits are host protection, not quota emulation (see
// AGENTS.md § Non-goals), and the hardcoded defaults — 25 containers, 10 per
// function, 4 concurrent starts — assume a mid-size development machine. On a
// small host they are dangerous: every cold start bursts to initBurstCPUs
// (2.0) during INIT, so a burst of starts can saturate a two-core laptop, and
// 25 containers of even modest MemorySize can push the machine into swap. On
// a big CI box they leave capacity idle.
//
// When the operator has not pinned a limit through its env var, it is derived
// here from the resources of the machine that will actually run the
// containers — Docker's GET /info (NCPU, MemTotal) — not from the machine the
// Overcast process runs on, which is the wrong machine whenever Overcast is
// itself containerised or the daemon is remote (Docker Desktop VM, DinD
// sidecar, tcp:// endpoint).
//
// Derivation happens once, when the Lambda container runtime initialises: the
// Docker client does not exist any earlier, and that initialisation already
// runs on a background goroutine, so startup is never blocked. If /info
// fails, the previous hardcoded defaults apply and Overcast behaves exactly
// as it did before derivation existed.

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/docker"
)

// dockerInfoClient is the single Docker call limit derivation needs. It is an
// interface so tests can serve canned /info responses without a daemon.
type dockerInfoClient interface {
	Info(ctx context.Context) (*docker.SystemInfo, error)
}

const (
	// defaultMaxConcurrentStarts bounds concurrent container starts when
	// neither LAMBDA_DOCKER_MAX_CONCURRENT_STARTS nor Docker /info supplies a
	// value. (The per-pool fallbacks defaultMaxInstances and
	// defaultMaxInstancesPerFunction live in runtime_pool_admission.go.)
	defaultMaxConcurrentStarts = 4

	// dockerInfoTimeout bounds the /info call so a wedged daemon cannot stall
	// Lambda runtime initialisation.
	dockerInfoTimeout = 5 * time.Second

	// derivedMemoryFraction is how much of the Docker host's memory Lambda
	// containers may collectively claim. 65% leaves headroom for the daemon,
	// the OS, and whatever else the machine is doing — Overcast is a dev tool
	// sharing a workstation, not the sole tenant of a fleet host.
	derivedMemoryFraction = 0.65

	// derivedInstanceFootprintMiB is the assumed per-container cost when
	// sizing the global instance cap: AWS's 128 MiB default MemorySize plus
	// image/runtime overhead, rounded to a power of two.
	derivedInstanceFootprintMiB = 256

	// Concurrent starts are held to half the host's cores because each start
	// bursts to initBurstCPUs (2.0) during INIT — NCPU/2 starts is the point
	// where INIT alone could claim every core.
	minDerivedConcurrentStarts = 2
	maxDerivedConcurrentStarts = 8

	minDerivedInstances = 4
	maxDerivedInstances = 32

	minDerivedPerFunction = 2

	mib = 1024 * 1024
)

// resolvedLimits is what the Lambda container runtime actually runs with:
// the pool's instance limits plus the cold-start concurrency bound.
type resolvedLimits struct {
	pool                PoolLimits
	maxConcurrentStarts int
}

// pinned reports whether every derivable limit was set explicitly, in which
// case Docker never needs to be asked.
func (r resolvedLimits) pinned() bool {
	return r.maxConcurrentStarts > 0 && r.pool.MaxInstances > 0 &&
		r.pool.MaxInstancesPerFunction > 0 && r.pool.MaxMemoryMB > 0
}

// resolveRuntimeLimits returns the instance limits the Lambda runtime should
// run with. Limits pinned via env vars are used exactly as given; unset ones
// (zero in cfg) are derived from Docker /info; and when /info cannot be read
// the previous hardcoded defaults apply. Derived values are logged once so an
// operator can see why their machine got its limits.
func resolveRuntimeLimits(ctx context.Context, cfg *config.Config, dc dockerInfoClient, log *zap.Logger) resolvedLimits {
	limits := resolvedLimits{
		pool: PoolLimits{
			MaxWarmPerFunction:      cfg.LambdaMaxWarmInstances,
			MaxInstances:            cfg.LambdaMaxInstances,
			MaxInstancesPerFunction: cfg.LambdaMaxInstancesPerFunction,
			MaxMemoryMB:             cfg.LambdaMaxMemoryMB,
		},
		maxConcurrentStarts: cfg.LambdaDockerMaxConcurrentStarts,
	}
	if limits.pinned() {
		return limits
	}

	infoCtx, cancel := context.WithTimeout(ctx, dockerInfoTimeout)
	defer cancel()
	info, err := dc.Info(infoCtx)
	if err != nil {
		log.Warn("lambda: Docker /info unavailable — unset instance limits use built-in defaults",
			zap.Int("max_concurrent_starts", defaultMaxConcurrentStarts),
			zap.Int("max_instances", defaultMaxInstances),
			zap.Int("max_instances_per_function", defaultMaxInstancesPerFunction),
			zap.String("max_memory", "unlimited"),
			zap.Error(err))
		info = nil
	}

	if limits.maxConcurrentStarts < 1 {
		limits.maxConcurrentStarts = defaultMaxConcurrentStarts
		if info != nil && info.NCPU > 0 {
			limits.maxConcurrentStarts = clampInt(info.NCPU/2, minDerivedConcurrentStarts, maxDerivedConcurrentStarts)
		}
	}
	if limits.pool.MaxInstances < 1 {
		limits.pool.MaxInstances = defaultMaxInstances
		if info != nil && info.MemTotal > 0 {
			budgetMiB := int(float64(info.MemTotal) * derivedMemoryFraction / mib)
			limits.pool.MaxInstances = clampInt(budgetMiB/derivedInstanceFootprintMiB, minDerivedInstances, maxDerivedInstances)
		}
	}
	if limits.pool.MaxInstancesPerFunction < 1 {
		limits.pool.MaxInstancesPerFunction = defaultMaxInstancesPerFunction
		if info != nil && info.MemTotal > 0 {
			// Derive from the effective global cap — pinned or derived — so a
			// pinned LAMBDA_MAX_INSTANCES keeps its authority over both.
			limits.pool.MaxInstancesPerFunction = clampInt(limits.pool.MaxInstances/2, minDerivedPerFunction, limits.pool.MaxInstances)
		}
	}
	if limits.pool.MaxMemoryMB < 1 && info != nil && info.MemTotal > 0 {
		// No fixed fallback here: without host facts the budget stays 0
		// (unlimited), which is exactly the pre-budget behaviour.
		limits.pool.MaxMemoryMB = int(float64(info.MemTotal) * derivedMemoryFraction / mib)
	}

	if info != nil {
		log.Info("lambda: derived instance limits from the Docker host",
			zap.Int("host_ncpu", info.NCPU),
			zap.Int64("host_mem_total_mb", info.MemTotal/mib),
			zap.Int("max_concurrent_starts", limits.maxConcurrentStarts),
			zap.Int("max_instances", limits.pool.MaxInstances),
			zap.Int("max_instances_per_function", limits.pool.MaxInstancesPerFunction),
			zap.Int("max_memory_mb", limits.pool.MaxMemoryMB),
		)
	}
	return limits
}

// clampInt bounds v to [lo, hi].
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
