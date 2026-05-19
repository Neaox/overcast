package docker

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// ProbeResult is returned by Probe on success.
type ProbeResult struct {
	Client    *Client
	NetworkID string // Docker network ID
}

// Probe creates a Docker client, verifies connectivity with retries, and
// ensures the named network exists. This is the common bootstrap pattern
// shared by Lambda, ECS, and RDS.
//
// Returns nil with a logged warning (not an error) when Docker is unreachable
// — callers degrade gracefully (metadata ops work, container ops return errors).
func Probe(socketPath, network string, logger *zap.Logger) (*ProbeResult, error) {
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

	// Create the Docker network (idempotent). When network is empty the
	// service manages its own networks (e.g. EC2 VPCs).
	var netID string
	if network != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		var err error
		netID, err = dc.CreateNetwork(ctx, network)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("create network %s: %w", network, err)
		}
	}

	return &ProbeResult{Client: dc, NetworkID: netID}, nil
}
