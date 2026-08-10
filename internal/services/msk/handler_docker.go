package msk

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/dataplane"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

const redpandaImage = "docker.redpanda.com/redpandadata/redpanda"

// kafkaPort is the broker port inside the data plane — what a sibling
// container dials, and what AWS would have returned.
const kafkaPort = 9092

// ── Container lifecycle ───────────────────────────────────────────────────────

// startClusterContainer creates (or reuses) and starts a Redpanda Docker
// container for the given MSK cluster ARN. Called in a goroutine with
// dockerWg.Add(1) already called; must defer dockerWg.Done.
func (h *Handler) startClusterContainer(ctx context.Context, clusterARN string) error {
	// Extract the UUID from the ARN (last segment after final '/').
	clusterUUID := arnSuffix(clusterARN)
	containerName := "overcast-msk-" + clusterUUID
	containerPort := "9092/tcp"
	aliases := h.clusterEndpointAliases(clusterARN)

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
		// Re-attach: a container adopted from an earlier run predates the
		// current alias set. Attaching is idempotent.
		if err := dataplane.AttachAdopted(ctx, h.docker, h.cfg, existing.ID, dataplane.Placement{Aliases: aliases}); err != nil {
			h.log.Warn("MSK: reused container could not join the data plane — "+
				"its bootstrap name will not resolve for sibling containers",
				zap.String("cluster", clusterARN), zap.Error(err))
		}
		h.setClusterEndpoint(ctx, clusterARN, existing.ID, hostPort)
		addr, port := h.clusterEndpointAddr(ctx, existing.ID, hostPort)
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
			NetworkMode: dataplane.Primary(h.cfg),
			PortBindings: map[string][]docker.PortBinding{
				containerPort: {{HostIP: "0.0.0.0", HostPort: strconv.Itoa(hostPort)}},
			},
		},
		NetworkingConfig: dataplane.PrimaryEndpoints(h.cfg),
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

	// Join the data plane before starting, so the bootstrap name resolves from
	// the moment the broker accepts connections.
	if err := dataplane.Attach(ctx, h.docker, h.cfg, containerID, dataplane.Placement{Aliases: aliases}); err != nil {
		h.docker.RemoveContainerForce(containerID) //nolint:errcheck
		h.store.releasePort(ctx, hostPort)         //nolint:errcheck
		return fmt.Errorf("MSK %s: %w", clusterARN, err)
	}

	if err := h.docker.StartContainer(ctx, containerID); err != nil {
		h.docker.RemoveContainerForce(containerID) //nolint:errcheck
		h.store.releasePort(ctx, hostPort)         //nolint:errcheck
		return fmt.Errorf("start container: %w", err)
	}

	h.setClusterEndpoint(ctx, clusterARN, containerID, hostPort)
	addr, port := h.clusterEndpointAddr(ctx, containerID, hostPort)
	h.scheduleHealthCheck(clusterARN, addr, port)
	return nil
}

// setClusterEndpoint stores the container ID and host port on the cluster,
// and sets the bootstrap endpoint based on Docker-vs-native detection.
// setClusterEndpoint records the container ID and host port on the cluster.
//
// The container start takes real time, so the cluster may have been deleted
// meanwhile. deleteCluster stops the container ID it finds on the record —
// which is empty until this call — so a delete landing mid-start leaves the
// Redpanda container running with nothing left to reclaim it. The start
// goroutine is the only party holding the ID, so it owns the teardown.
// Same race as RDS (#412) and ElastiCache (#459).
func (h *Handler) setClusterEndpoint(ctx context.Context, clusterARN, containerID string, hostPort int) {
	if _, aerr := h.mutateCluster(ctx, clusterARN, func(stored *Cluster) *protocol.AWSError {
		if stored.State == "DELETING" {
			return errClusterMovedOn
		}
		stored.DockerContainerID = containerID
		stored.HostPort = hostPort
		return nil
	}); aerr != nil {
		h.teardownOrphanedContainer(ctx, clusterARN, containerID, hostPort)
	}
}

// teardownOrphanedContainer removes a container whose cluster record was
// deleted while the container was still starting, and releases its port.
func (h *Handler) teardownOrphanedContainer(ctx context.Context, clusterARN, containerID string, hostPort int) {
	h.log.Info("MSK: cluster deleted while its container was starting — removing container",
		zap.String("cluster", clusterARN), zap.String("container", containerID))
	if containerID != "" {
		if h.gc != nil {
			h.gc.StopNow(containerID)
			h.gc.ScheduleRemove(containerID)
		} else {
			_ = h.docker.StopContainer(ctx, containerID, 10)     //nolint:errcheck
			_ = h.docker.RemoveContainer(ctx, containerID, true) //nolint:errcheck
		}
	}
	if hostPort > 0 {
		_ = h.store.releasePort(ctx, hostPort) //nolint:errcheck
	}
}

// clusterEndpointAddr returns the address and port Overcast health-checks the
// broker on: the container's own address and 9092 when Overcast runs beside
// it, else loopback and the published port.
func (h *Handler) clusterEndpointAddr(ctx context.Context, containerID string, hostPort int) (string, int) {
	if addr := dataplane.ContainerAddr(ctx, h.docker, h.cfg, containerID); addr != "" {
		return addr, kafkaPort
	}
	return "127.0.0.1", hostPort
}

// clusterEndpointAliases is the set of DNS names a broker container answers to
// on the data plane — the bootstrap host under every base Overcast could mint
// it on. Without these a consumer resolving the bootstrap name reaches
// Overcast, which serves no Kafka, and the connection is refused on a port
// nothing listens on.
func (h *Handler) clusterEndpointAliases(clusterARN string) []string {
	name := clusterNameFromARN(clusterARN)
	if name == "" {
		return nil
	}
	region := serviceutil.ARNRegion(clusterARN)
	return dataplane.Hostnames(h.cfg, func(base string) string {
		return bootstrapHostname(name, region, base)
	})
}

// bootstrapHostname builds the DNS name for a cluster's bootstrap brokers on
// base. AWS's shape is
// `b-{n}.{cluster}.{hash}.c{n}.kafka.{region}.amazonaws.com`; Overcast mints
// the same grammar with the per-broker prefix and account-specific hash
// dropped, since one container serves the whole cluster.
func bootstrapHostname(name, region, base string) string {
	return name + "." + region + ".kafka." + base
}

// clusterNameFromARN returns the cluster name from
// `arn:aws:kafka:{region}:{account}:cluster/{name}/{uuid}` — the segment
// between the final two slashes.
func clusterNameFromARN(arn string) string {
	parts := strings.Split(arn, "/")
	if len(parts) < 3 {
		return ""
	}
	return parts[len(parts)-2]
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
// "ACTIVE" once Redpanda responds. After maxRetries the cluster stays in its
// current state — it is never falsely marked ACTIVE when Kafka is not answering.
func (h *Handler) scheduleHealthCheck(clusterARN, addr string, port int) {
	const maxRetries = 60
	region := serviceutil.ARNRegion(clusterARN)
	var attempt int
	var check func(ctx context.Context)
	check = func(ctx context.Context) {
		attempt++
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(addr, strconv.Itoa(port)), 2*time.Second)
		if err == nil {
			conn.Close()
			h.transitionCluster(ctx, clusterARN, "ACTIVE", "CREATING", "STARTING")
			return
		}
		if attempt < maxRetries {
			h.scheduler.AfterScoped(region, clusterARN, "health", 2*time.Second, check)
		} else {
			h.log.Warn("MSK health check timed out — cluster stays in current state",
				zap.String("cluster", clusterARN), zap.Int("attempts", attempt))
		}
	}
	h.scheduler.AfterScoped(region, clusterARN, "health", 1*time.Second, check)
}

// ── Docker container event handlers ──────────────────────────────────────────

// handleContainerEvent processes DockerContainerDied and DockerContainerStopped.
func (h *Handler) handleContainerEvent(_ context.Context, e events.Event) {
	p, ok := e.Payload.(events.DockerContainerPayload)
	if !ok || p.Service != serviceName {
		return
	}
	ctx := clusterRegionCtx(p.ResourceID)
	cluster, aerr := h.store.getCluster(ctx, p.ResourceID)
	if aerr != nil || cluster == nil {
		return
	}
	switch cluster.State {
	case "ACTIVE", "STARTING", "CREATING":
		h.transitionCluster(ctx, p.ResourceID, "FAILED", cluster.State)
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
	ctx := clusterRegionCtx(p.ResourceID)
	cluster, aerr := h.store.getCluster(ctx, p.ResourceID)
	if aerr != nil || cluster == nil {
		return
	}
	switch cluster.State {
	case "FAILED", "STARTING", "CREATING":
		addr, port := h.clusterEndpointAddr(ctx, cluster.DockerContainerID, cluster.HostPort)
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

	regioned, err := serviceutil.ScanRegions[Cluster](ctx, h.store.store, nsClusters, h.store.defaultRegion)
	if err != nil {
		h.log.Warn("reconcile: failed to list MSK clusters", zap.Error(err))
		return
	}
	for _, rc := range regioned {
		cluster := rc.Value
		if cluster.DockerContainerID == "" {
			continue
		}
		rctx := middleware.ContextWithRegion(ctx, rc.Region)
		c := byResource[cluster.ClusterArn]
		switch {
		case c == nil:
			if cluster.State == "ACTIVE" || cluster.State == "STARTING" || cluster.State == "CREATING" {
				h.transitionCluster(rctx, cluster.ClusterArn, "FAILED", cluster.State)
				h.log.Info("reconcile: MSK container missing — marked FAILED",
					zap.String("cluster", cluster.ClusterArn))
			}
		case c.State == "running":
			addr, port := h.clusterEndpointAddr(rctx, cluster.DockerContainerID, cluster.HostPort)
			if cluster.State == "CREATING" || cluster.State == "STARTING" || cluster.State == "FAILED" || cluster.State == "ACTIVE" {
				h.scheduleHealthCheck(cluster.ClusterArn, addr, port)
				h.log.Info("reconcile: MSK container running — scheduling health check",
					zap.String("cluster", cluster.ClusterArn))
			}
		default:
			if cluster.State == "ACTIVE" || cluster.State == "STARTING" {
				h.transitionCluster(rctx, cluster.ClusterArn, "FAILED", cluster.State)
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
