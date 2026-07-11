package msk

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
)

const redpandaImage = "docker.redpanda.com/redpandadata/redpanda"

// ── Container lifecycle ───────────────────────────────────────────────────────

// startClusterContainer creates (or reuses) and starts a Redpanda Docker
// container for the given MSK cluster ARN. Called in a goroutine with
// dockerWg.Add(1) already called; must defer dockerWg.Done.
func (h *Handler) startClusterContainer(ctx context.Context, clusterARN string) error {
	// Extract the UUID from the ARN (last segment after final '/').
	clusterUUID := arnSuffix(clusterARN)
	containerName := "overcast-msk-" + clusterUUID
	containerPort := "9092/tcp"
	network := h.cfg.MSKNetwork

	// Check for existing container (post-restart reuse).
	if existing, err := h.docker.GetContainerByName(ctx, containerName); err == nil && existing != nil {
		if !existing.HasOvercastLabels(serviceName, clusterARN) {
			return fmt.Errorf("container %q exists but is not an overcast-managed MSK container — refusing to reuse", containerName)
		}
		h.log.Info("MSK: reusing existing container",
			zap.String("cluster", clusterARN),
			zap.String("container", existing.ID),
			zap.String("state", existing.State.Status))

		hostPort := 0
		if bindings, ok := existing.NetworkSettings.Ports[containerPort]; ok && len(bindings) > 0 {
			if p, err := strconv.Atoi(bindings[0].HostPort); err == nil {
				hostPort = p
			}
		}
		if hostPort == 0 {
			if hp, aerr := h.store.allocatePort(ctx, clusterARN, h.cfg.MSKPortBase); aerr == nil {
				hostPort = hp
			}
		} else {
			h.store.allocatePortFixed(ctx, clusterARN, hostPort) //nolint:errcheck
		}
		if !existing.State.Running {
			if err := h.docker.StartContainer(ctx, existing.ID); err != nil {
				return fmt.Errorf("start existing container: %w", err)
			}
		}
		h.setClusterEndpoint(ctx, clusterARN, existing.ID, hostPort)
		addr, port := h.clusterEndpointAddr(ctx, existing.ID, hostPort, network)
		h.scheduleHealthCheck(clusterARN, addr, port)
		return nil
	}

	// Allocate a host port.
	hostPort, aerr := h.store.allocatePort(ctx, clusterARN, h.cfg.MSKPortBase)
	if aerr != nil {
		return fmt.Errorf("allocate port: %s", aerr.Message)
	}

	// Pull image (deduplicated per process lifetime).
	if err := h.puller.Ensure(ctx, redpandaImage); err != nil {
		h.store.releasePort(ctx, hostPort) //nolint:errcheck
		return fmt.Errorf("pull image: %w", err)
	}

	// Ensure Docker network exists.
	if _, err := h.docker.CreateNetwork(ctx, network); err != nil {
		h.log.Warn("MSK: failed to create network (may already exist)",
			zap.String("network", network), zap.Error(err))
	}

	req := &docker.CreateContainerRequest{
		ContainerConfig: &docker.ContainerConfig{
			Image: redpandaImage,
			Cmd: []string{
				"redpanda", "start",
				"--overprovisioned",
				"--smp", "1",
				"--memory", "200M",
				"--reserve-memory", "0M",
				"--node-id", "0",
				"--check=false",
			},
			ExposedPorts: map[string]struct{}{containerPort: {}},
			Labels:       docker.ManagedLabels(serviceName, clusterARN),
		},
		HostConfig: &docker.HostConfig{AutoRemove: true,
			NetworkMode: network,
			PortBindings: map[string][]docker.PortBinding{
				containerPort: {{HostIP: "0.0.0.0", HostPort: strconv.Itoa(hostPort)}},
			},
		},
		NetworkingConfig: &docker.NetworkingConfig{
			EndpointsConfig: map[string]*docker.EndpointSettings{
				network: {},
			},
		},
	}

	containerID, err := h.docker.CreateContainer(ctx, containerName, req)
	if err != nil {
		if docker.IsConflict(err) {
			h.log.Warn("MSK: name conflict on create, retrying reuse",
				zap.String("cluster", clusterARN))
			h.store.releasePort(ctx, hostPort) //nolint:errcheck
			return h.startClusterContainer(ctx, clusterARN)
		}
		h.store.releasePort(ctx, hostPort) //nolint:errcheck
		return fmt.Errorf("create container: %w", err)
	}

	if err := h.docker.StartContainer(ctx, containerID); err != nil {
		h.docker.RemoveContainerForce(containerID) //nolint:errcheck
		h.store.releasePort(ctx, hostPort)         //nolint:errcheck
		return fmt.Errorf("start container: %w", err)
	}

	h.setClusterEndpoint(ctx, clusterARN, containerID, hostPort)
	addr, port := h.clusterEndpointAddr(ctx, containerID, hostPort, network)
	h.scheduleHealthCheck(clusterARN, addr, port)
	return nil
}

// setClusterEndpoint stores the container ID and host port on the cluster,
// and sets the bootstrap endpoint based on Docker-vs-native detection.
func (h *Handler) setClusterEndpoint(ctx context.Context, clusterARN, containerID string, hostPort int) {
	got, aerr := h.store.getCluster(ctx, clusterARN)
	if aerr != nil {
		return
	}
	got.DockerContainerID = containerID
	got.HostPort = hostPort
	h.store.putCluster(ctx, got) //nolint:errcheck
}

// clusterEndpointAddr returns the address and port to health-check.
// Inside Docker: uses container IP on port 9092.
// Outside Docker: uses 127.0.0.1 + hostPort.
func (h *Handler) clusterEndpointAddr(ctx context.Context, containerID string, hostPort int, network string) (string, int) {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		hostname, _ := os.Hostname()
		if hostname != "" {
			_ = h.docker.ConnectNetwork(ctx, network, hostname)
		}
		info, err := h.docker.InspectContainer(ctx, containerID)
		if err == nil {
			if ep, ok := info.NetworkSettings.Networks[network]; ok && ep.IPAddress != "" {
				return ep.IPAddress, 9092
			}
		}
	}
	return "127.0.0.1", hostPort
}

// cleanupClusterContainer releases the port reservation for an MSK cluster.
// Docker container stop/remove is handled by the GC.
//
//nolint:unused // Kept for explicit Docker cleanup call sites.
func (h *Handler) cleanupClusterContainer(ctx context.Context, clusterARN string) {
	if !h.dockerReady.Load() {
		return
	}
	got, aerr := h.store.getCluster(ctx, clusterARN)
	if aerr != nil {
		return
	}
	if got.HostPort > 0 {
		if aerr := h.store.releasePort(ctx, got.HostPort); aerr != nil {
			h.log.Warn("MSK cleanup: release port",
				zap.String("cluster", clusterARN), zap.Int("port", got.HostPort), zap.Error(aerr))
		}
	}
}

// scheduleHealthCheck polls TCP connectivity and transitions the cluster to
// "ACTIVE" once Redpanda responds. Falls back to "ACTIVE" after maxRetries.
func (h *Handler) scheduleHealthCheck(clusterARN, addr string, port int) {
	const maxRetries = 60
	var attempt int
	var check func()
	check = func() {
		attempt++
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(addr, strconv.Itoa(port)), 2*time.Second)
		if err == nil {
			conn.Close()
			ctx := context.Background()
			got, aerr := h.store.getCluster(ctx, clusterARN)
			if aerr != nil {
				return
			}
			if got.State == "CREATING" || got.State == "STARTING" {
				got.State = "ACTIVE"
				h.store.putCluster(ctx, got) //nolint:errcheck
			}
			return
		}
		if attempt < maxRetries {
			h.scheduler.After(clusterARN+":health", 2*time.Second, check)
		} else {
			h.log.Warn("MSK health check timed out", zap.String("cluster", clusterARN), zap.Int("attempts", attempt))
			ctx := context.Background()
			got, aerr := h.store.getCluster(ctx, clusterARN)
			if aerr != nil {
				return
			}
			if got.State == "CREATING" || got.State == "STARTING" {
				got.State = "ACTIVE"
				h.store.putCluster(ctx, got) //nolint:errcheck
			}
		}
	}
	h.scheduler.After(clusterARN+":health", 1*time.Second, check)
}

// ── Docker container event handlers ──────────────────────────────────────────

// handleContainerEvent processes DockerContainerDied and DockerContainerStopped.
func (h *Handler) handleContainerEvent(_ context.Context, e events.Event) {
	p, ok := e.Payload.(events.DockerContainerPayload)
	if !ok || p.Service != serviceName {
		return
	}
	ctx := context.Background()
	cluster, aerr := h.store.getCluster(ctx, p.ResourceID)
	if aerr != nil || cluster == nil {
		return
	}
	switch cluster.State {
	case "ACTIVE", "STARTING", "CREATING":
		cluster.State = "FAILED"
		h.store.putCluster(ctx, cluster) //nolint:errcheck
		h.log.Info("MSK cluster container stopped",
			zap.String("cluster", p.ResourceID), zap.String("action", p.Action))
	}
}

// handleContainerStarted processes DockerContainerStarted events.
func (h *Handler) handleContainerStarted(_ context.Context, e events.Event) {
	p, ok := e.Payload.(events.DockerContainerPayload)
	if !ok || p.Service != serviceName {
		return
	}
	ctx := context.Background()
	cluster, aerr := h.store.getCluster(ctx, p.ResourceID)
	if aerr != nil || cluster == nil {
		return
	}
	switch cluster.State {
	case "FAILED", "STARTING", "CREATING":
		network := h.cfg.MSKNetwork
		addr, port := h.clusterEndpointAddr(ctx, cluster.DockerContainerID, cluster.HostPort, network)
		h.scheduleHealthCheck(p.ResourceID, addr, port)
	}
}

// reconcileContainers is called once at startup after Docker becomes available.
// It compares live container state against stored clusters and corrects status drift.
func (h *Handler) reconcileContainers(ctx context.Context, containers []docker.ContainerSummary) {
	byResource := make(map[string]*docker.ContainerSummary, len(containers))
	for i := range containers {
		rid := containers[i].ResourceID()
		if rid != "" {
			byResource[rid] = &containers[i]
		}
	}

	clusters, aerr := h.store.listClusters(ctx)
	if aerr != nil {
		h.log.Warn("reconcile: failed to list MSK clusters", zap.Error(aerr))
		return
	}
	for _, cluster := range clusters {
		if cluster.DockerContainerID == "" {
			continue
		}
		c := byResource[cluster.ClusterArn]
		network := h.cfg.MSKNetwork
		switch {
		case c == nil:
			if cluster.State == "ACTIVE" || cluster.State == "STARTING" || cluster.State == "CREATING" {
				cluster.State = "FAILED"
				h.store.putCluster(ctx, cluster) //nolint:errcheck
				h.log.Info("reconcile: MSK container missing — marked FAILED",
					zap.String("cluster", cluster.ClusterArn))
			}
		case c.State == "running":
			addr, port := h.clusterEndpointAddr(ctx, cluster.DockerContainerID, cluster.HostPort, network)
			if cluster.State == "CREATING" || cluster.State == "STARTING" || cluster.State == "FAILED" || cluster.State == "ACTIVE" {
				h.scheduleHealthCheck(cluster.ClusterArn, addr, port)
				h.log.Info("reconcile: MSK container running — scheduling health check",
					zap.String("cluster", cluster.ClusterArn))
			}
		default:
			if cluster.State == "ACTIVE" || cluster.State == "STARTING" {
				cluster.State = "FAILED"
				h.store.putCluster(ctx, cluster) //nolint:errcheck
				h.log.Info("reconcile: MSK container not running — marked FAILED",
					zap.String("cluster", cluster.ClusterArn),
					zap.String("containerState", c.State))
			}
		}
	}
}

// ── ARN helpers ───────────────────────────────────────────────────────────────

// arnSuffix returns the last segment of an ARN (after the final '/').
func arnSuffix(arn string) string {
	i := strings.LastIndex(arn, "/")
	if i < 0 {
		return arn
	}
	return arn[i+1:]
}
