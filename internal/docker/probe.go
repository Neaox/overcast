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

	// Networks is what became of each NetworkSpec: the isolation each plane
	// ended up with, why, and whether a pre-existing network disagreed. Carried
	// out of Probe rather than only logged so /_overcast/health can report it —
	// "which network am I on and is it internal" is a runtime topology fact,
	// and the whole point of #1564 is that it should not take a `docker network
	// inspect` and a guess to find out.
	Networks []NetworkStatus
}

// Probe creates a Docker client, verifies connectivity with retries, and
// brings every given network into the state its spec describes.
//
// "Brings into", not "ensures exists". Docker's create-network call returns an
// existing network unchanged, so a name being present says nothing about its
// isolation, driver, IPAM or options. Every network is inspected and compared
// field by field against its spec, repaired where repair is free, and reported
// loudly where it is not — see EnsureNetwork.
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
	statuses := make([]NetworkStatus, 0, len(networks))
	for _, spec := range networks {
		if spec.Name == "" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		resolved := spec.Resolve(ctx, dc)
		if resolved.Reason != "" {
			// Said once, at info, on every startup. The report that asked for
			// this (#1564) had two engineers on one pinned version get
			// different answers with nothing anywhere saying which they got.
			logger.Info("network isolation",
				zap.String("network", spec.Name),
				zap.Bool("internal", resolved.Internal),
				zap.String("reason", resolved.Reason))
		}
		for _, warning := range resolved.Warnings {
			logger.Warn(warning, zap.String("network", spec.Name))
		}
		statuses = append(statuses, EnsureNetwork(ctx, dc, resolved, logger))
		cancel()
	}

	return &ProbeResult{Client: dc, Networks: statuses}, nil
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
