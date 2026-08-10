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

// Probe creates a Docker client, verifies connectivity with retries, and
// ensures the given networks exist. This is the common bootstrap pattern
// shared by every service that runs containers.
//
// Returns nil with a logged warning (not an error) when Docker is unreachable
// — callers degrade gracefully (metadata ops work, container ops return errors).
func Probe(socketPath string, networks []string, logger *zap.Logger) (*ProbeResult, error) {
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
	for _, network := range networks {
		if network == "" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, err := dc.CreateNetwork(ctx, network)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("create network %s: %w", network, err)
		}
	}

	return &ProbeResult{Client: dc}, nil
}

// RemoveLegacyNetworks drops the pre-two-plane per-service networks. Networks
// that still have containers attached are left in place — Docker refuses the
// removal and the refusal is logged at debug, since an adopted container is a
// good reason to keep one and not a fault.
//
// Each name is inspected before the attempt, because RemoveNetwork treats a
// missing network as success: without the check, a fresh installation that
// never had these would report removing all seven on every startup.
func RemoveLegacyNetworks(ctx context.Context, dc *Client, logger *zap.Logger) {
	if dc == nil {
		return
	}
	for _, name := range legacyNetworks {
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
