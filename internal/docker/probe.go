package docker

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// legacyNetworks are the per-service networks Overcast created before the
// two-plane model (docs/plans/container-network-topology.md). An upgraded
// installation still has them, and they are pure clutter once nothing joins
// them — but a container adopted from an earlier run may still be attached, so
// removal is attempted and never forced: Docker refuses to remove a network
// with active endpoints, which is exactly the outcome wanted there.
var legacyNetworks = []string{
	"overcast_lambda",
	"overcast_ecs",
	"overcast_rds",
	"overcast_elasticache",
	"overcast_msk",
	"overcast_eks",
	"overcast_efs",
}

// ProbeResult is returned by Probe on success.
type ProbeResult struct {
	Client *Client
}

// NetworkSpec is one network the supervisor ensures at startup.
type NetworkSpec struct {
	// Name is the Docker network name.
	Name string

	// Internal cuts the network off from the wider network — no egress, no
	// route to anything Docker did not put on it.
	//
	// It matters for the control plane specifically. A VPC with no internet
	// gateway is created `--internal` to model a private subnet, but a
	// container on it is also on the control plane, so if *that* has egress the
	// VPC's isolation is defeated and the flag is decoration.
	//
	// Ignored when InternalMode is set.
	Internal bool

	// InternalMode decides Internal dynamically, using the Docker client Probe
	// has just dialled and verified — the seam a caller needs when the right
	// answer depends on facts about the daemon (a native Linux kernel vs. Docker
	// Desktop's VM) that are not knowable until a client exists to ask, and
	// which may not be knowable at all from a static config value computed
	// before Probe runs. Takes precedence over Internal when set.
	//
	// Called once per spec, after the client is confirmed available and before
	// the network is created — the one point in this function where the
	// client exists but no ordering has yet committed to an answer.
	InternalMode func(ctx context.Context, dc *Client) bool
}

// Probe creates a Docker client, verifies connectivity with retries, and
// ensures the given networks exist. This is the common bootstrap pattern
// shared by every service that runs containers.
//
// A network that already exists with a different Internal setting is left as
// it is, with a warning naming the fix. Changing the flag means deleting and
// recreating the network, which would sever every container already attached —
// a worse outcome at startup than an isolation property that is one `docker
// network rm` away from correct.
//
// Returns nil with a logged warning (not an error) when Docker is unreachable
// — callers degrade gracefully (metadata ops work, container ops return errors).
func Probe(socketPath string, networks []NetworkSpec, logger *zap.Logger) (*ProbeResult, error) {
	dc := NewClient(socketPath, logger)

	// Retry briefly — when running inside a devcontainer the socket is
	// bind-mounted from the host and may not be responsive immediately.
	available := false
	for attempt := 1; attempt <= 5; attempt++ {
		if dc.Available(2 * time.Second) {
			available = true
			break
		}
		if attempt < 5 {
			logger.Debug("Docker not yet available, retrying",
				zap.Int("attempt", attempt), zap.String("socket", socketPath))
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
		}
	}

	if !available {
		return nil, fmt.Errorf("docker not available at %s after 5 attempts", socketPath)
	}

	logger.Info("Docker available", zap.String("socket", socketPath))

	// Create the planes (idempotent). Doing it here, before any client is
	// handed to a service, is what lets services create containers straight
	// onto a network without each re-ensuring it first.
	for _, spec := range networks {
		if spec.Name == "" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		internal := spec.Internal
		if spec.InternalMode != nil {
			internal = spec.InternalMode(ctx, dc)
		}
		_, err := dc.CreateNetworkWithOptions(ctx, CreateNetworkOptions{
			Name:     spec.Name,
			Internal: internal,
		})
		if err == nil {
			warnOnInternalDrift(ctx, dc, spec.Name, internal, logger)
		}
		cancel()
		if err != nil {
			return nil, fmt.Errorf("create network %s: %w", spec.Name, err)
		}
	}

	return &ProbeResult{Client: dc}, nil
}

// warnOnInternalDrift reports a pre-existing network whose Internal flag does
// not match what this run asked for, which CreateNetworkWithOptions silently
// tolerates because it treats "already exists" as success.
//
// wantInternal is the resolved value — spec.Internal after InternalMode has
// had its say, not the static field — so a network whose isolation now
// depends on the daemon probe is compared against what was actually decided.
func warnOnInternalDrift(ctx context.Context, dc *Client, name string, wantInternal bool, logger *zap.Logger) {
	info, err := dc.InspectNetwork(ctx, name)
	if err != nil || info == nil || info.Internal == wantInternal {
		return
	}
	logger.Warn("Docker network predates this configuration and its isolation differs — "+
		"remove it while nothing is attached to have it recreated",
		zap.String("network", name),
		zap.Bool("internal_now", info.Internal),
		zap.Bool("internal_wanted", wantInternal))
}

// RemoveLegacyNetworks drops the pre-two-plane per-service networks. Networks
// that still have containers attached are left in place — Docker refuses the
// removal and the refusal is logged at debug, since an adopted container is a
// good reason to keep one and not a fault.
//
// keep names the planes currently in use, which must survive even when one of
// them was configured to a legacy name: OVERCAST_NETWORK=overcast_eks is a
// plausible migration setting, and without this the plane created moments
// earlier would be removed and every later attach would fail.
//
// Each name is inspected before the attempt, because RemoveNetwork treats a
// missing network as success: without the check, a fresh installation that
// never had these would report removing all seven on every startup.
func RemoveLegacyNetworks(ctx context.Context, dc *Client, logger *zap.Logger, keep ...string) {
	if dc == nil {
		return
	}
	inUse := make(map[string]struct{}, len(keep))
	for _, name := range keep {
		inUse[name] = struct{}{}
	}
	for _, name := range legacyNetworks {
		if _, ok := inUse[name]; ok {
			continue
		}
		if _, err := dc.InspectNetwork(ctx, name); err != nil {
			continue // absent, which is the normal case
		}
		if err := dc.RemoveNetwork(ctx, name); err != nil {
			logger.Debug("legacy network retained — still in use",
				zap.String("network", name), zap.Error(err))
			continue
		}
		logger.Info("removed legacy per-service Docker network",
			zap.String("network", name))
	}
}
