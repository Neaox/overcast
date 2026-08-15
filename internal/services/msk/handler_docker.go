package msk

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/dataplane"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/serviceutil/readiness"
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
	vpcID := h.vpcForCluster(ctx, clusterARN)

	// Check for existing container (post-restart reuse).
	if existing, err := h.docker.GetContainerByName(ctx, containerName); err == nil && existing != nil {
		if !existing.HasOvercastLabels(serviceName, clusterARN) {
			return fmt.Errorf("container %q exists but is not an overcast-managed MSK container — refusing to reuse", containerName)
		}
		// The labels above prove it is an MSK container for this cluster's ARN;
		// they do not prove it is *this* Overcast's. Reusing what another one
		// created hands both of them a single broker, which this one will later
		// stop and delete on the strength of its own record. Scoping
		// reconciliation alone would not have held: a cluster whose container is
		// gone restarts, and the restart comes straight back through here.
		if owner := existing.Instance(); owner != "" && owner != h.instances.Resolve(ctx) {
			return fmt.Errorf("container %q was created by another Overcast instance (%s=%s) — refusing to reuse it for MSK cluster %q; "+
				"two Overcasts sharing a Docker daemon cannot both run a cluster of that name",
				containerName, docker.LabelInstance, owner, clusterARN)
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
		// current alias set, and predates VPC placement entirely — before this
		// it was on the default plane whatever subnets the cluster named.
		// Attaching is idempotent.
		if placement, err := h.placementFor(ctx, vpcID, aliases); err != nil {
			h.log.Warn("MSK: reused container could not be placed in its VPC",
				zap.String("cluster", clusterARN), zap.Error(err))
		} else if err := dataplane.AttachAdopted(ctx, h.docker, h.cfg, existing.ID, placement); err != nil {
			h.log.Warn("MSK: reused container could not join the data plane — "+
				"its bootstrap name will not resolve for sibling containers",
				zap.String("cluster", clusterARN), zap.Error(err))
		}
		h.setClusterEndpoint(ctx, clusterARN, existing.ID, hostPort)
		addr, port := h.clusterEndpointAddr(ctx, existing.ID, hostPort)
		h.scheduleHealthCheck(clusterARN, addr, port)
		return nil
	}

	// Resolve the plane before anything is acquired: a VPC that cannot take
	// containers should fail here, not after a port reservation and an image
	// pull that then have to be unwound.
	placement, err := h.placementFor(ctx, vpcID, aliases)
	if err != nil {
		return fmt.Errorf("MSK %s: %w", clusterARN, err)
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
			Labels:       h.instances.ManagedLabels(ctx, serviceName, clusterARN),
		},
		HostConfig: &docker.HostConfig{AutoRemove: true,
			NetworkMode: dataplane.Primary(h.cfg),
			PortBindings: map[string][]docker.PortBinding{
				containerPort: {{HostIP: "0.0.0.0", HostPort: strconv.Itoa(hostPort)}},
			},
		},
		NetworkingConfig: dataplane.PrimaryEndpoints(h.cfg),
	}

	containerID, cerr := h.docker.CreateContainer(ctx, containerName, req)
	if cerr != nil {
		if docker.IsConflict(cerr) {
			h.log.Warn("MSK: name conflict on create, retrying reuse",
				zap.String("cluster", clusterARN))
			h.store.releasePort(ctx, hostPort) //nolint:errcheck
			return h.startClusterContainer(ctx, clusterARN)
		}
		h.store.releasePort(ctx, hostPort) //nolint:errcheck
		return fmt.Errorf("create container: %w", cerr)
	}

	// Join the data plane before starting, so the bootstrap name resolves from
	// the moment the broker accepts connections.
	if err := dataplane.Attach(ctx, h.docker, h.cfg, containerID, placement); err != nil {
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

// vpcForCluster returns the VPC an MSK cluster's brokers belong in, or "" for
// the default data plane.
//
// On AWS there is no non-VPC MSK cluster: a provisioned cluster names client
// subnets and its brokers live in the VPC those subnets belong to. Overcast
// stored the subnets and never resolved them, so the broker container landed on
// the default plane while a function or task created *in* the VPC landed on the
// VPC's — which only worked while a VPC-placed resource kept the default plane
// as well.
//
// A cluster whose record has already gone, whose subnets are absent or
// synthetic, or that has no EC2 service to ask still resolves to "". That is
// today's behaviour for those cases and it stays a working cluster, so it must
// not become an error.
func (h *Handler) vpcForCluster(ctx context.Context, clusterARN string) string {
	cluster, aerr := h.store.getCluster(ctx, clusterARN)
	if aerr != nil || cluster == nil {
		return ""
	}
	return h.vpcForSubnets(ctx, cluster.BrokerNodeGroupInfo.ClientSubnets)
}

// vpcForSubnets resolves the first subnet that names a VPC, the same way RDS
// and ElastiCache fill their own VPC field in.
func (h *Handler) vpcForSubnets(ctx context.Context, subnetIDs []string) string {
	if h.vpcResolver == nil {
		return ""
	}
	for _, id := range subnetIDs {
		if vpcID := h.vpcResolver.VpcIDForSubnet(ctx, id); vpcID != "" {
			return vpcID
		}
	}
	return ""
}

// placementFor turns a resolved VPC into the Placement the broker container
// should take, carrying the bootstrap aliases onto whichever plane it lands on.
func (h *Handler) placementFor(ctx context.Context, vpcID string, aliases []string) (dataplane.Placement, error) {
	var resolver dataplane.VPCResolver
	if h.vpcResolver != nil {
		resolver = h.vpcResolver
	}
	placement, err := dataplane.PlaceInVPC(ctx, resolver, vpcID)
	if err != nil {
		return placement, err
	}
	placement.Aliases = aliases
	return placement, nil
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

// scheduleHealthCheck waits for the broker to answer Kafka's ApiVersions and
// transitions the cluster to ACTIVE, or to FAILED carrying why when it never
// does.
//
// Running out of retries used to leave the cluster in CREATING, which is a
// state nothing was coming to change: `aws kafka wait cluster-active` spun on
// it until it gave up with nothing to say, and the reason lived only in a log
// line no API caller reads. See readiness.Watch for why terminal is the answer.
func (h *Handler) scheduleHealthCheck(clusterARN, addr string, port int) {
	region := serviceutil.ARNRegion(clusterARN)
	target := net.JoinHostPort(addr, strconv.Itoa(port))
	readiness.Watch{
		Scheduler:  h.scheduler,
		Clock:      h.clk,
		Region:     region,
		ResourceID: clusterARN,
		Transition: "health",
		Subject:    "the Kafka broker at " + target,
		Policy:     brokerReadiness,
		Probe: func(ctx context.Context) readiness.Result {
			got, aerr := h.store.getCluster(ctx, clusterARN)
			if aerr != nil || got == nil {
				return readiness.Abandoned()
			}
			if !stateIn(got.State, clusterAwaiting) {
				// Deleted, or moved on by something with a reason of its own.
				return readiness.Abandoned()
			}
			if err := probeBroker(ctx, target); err != nil {
				return readiness.Retry(err)
			}
			return readiness.Answered()
		},
		OnReady: func(ctx context.Context) {
			h.transitionCluster(ctx, clusterARN, "ACTIVE", clusterAwaiting...)
		},
		OnFailed: func(ctx context.Context, reason string) {
			h.failCluster(ctx, clusterARN, reason)
		},
	}.Start()
}

// ── Docker container event handlers ──────────────────────────────────────────

// Containers are matched to cluster records by the overcast.resource-id label,
// and nothing about that label is unique to one Overcast: two of them sharing
// a Docker daemon keep separate state stores, so each can hold a container
// carrying the other's ARN — through a restored state directory or a copied
// record. Matching on it alone lets one fail, adopt, or health-check the
// other's live broker, and it cannot tell a stale container for an ARN from
// the live one beside it either. Every match below therefore goes through
// h.instances — see InstanceDomain.ContainerIsOurs.

// handleContainerEvent processes DockerContainerDied and DockerContainerStopped.
func (h *Handler) handleContainerEvent(_ context.Context, e events.Event) {
	p, ok := e.Payload.(events.DockerContainerPayload)
	if !ok || p.Service != serviceName || !h.dockerReady.Load() {
		return
	}
	ctx := clusterRegionCtx(p.ResourceID)
	cluster, aerr := h.store.getCluster(ctx, p.ResourceID)
	if aerr != nil || cluster == nil {
		return
	}
	if !h.instances.ContainerIsOurs(ctx, p.ContainerID, p.Instance, cluster.DockerContainerID) {
		// Another Overcast's broker exiting is its business. Failing this
		// cluster over it reports an outage the broker the record names never
		// had.
		return
	}
	switch cluster.State {
	case "ACTIVE", "STARTING", "CREATING", "HEALING", stateFailed:
		h.transitionCluster(ctx, p.ResourceID, "HEALING", cluster.State)
		h.startClusterAsync(p.ResourceID)
		h.log.Info("MSK cluster container stopped; replacement scheduled",
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
	if !h.instances.ContainerIsOurs(ctx, p.ContainerID, p.Instance, cluster.DockerContainerID) {
		// Health-checking on another Overcast's start would promote this
		// cluster to ACTIVE on a broker it does not own.
		return
	}
	switch cluster.State {
	case "FAILED", "HEALING", "STARTING", "CREATING":
		addr, port := h.clusterEndpointAddr(ctx, cluster.DockerContainerID, cluster.HostPort)
		h.scheduleHealthCheck(p.ResourceID, addr, port)
	}
}

// reconcileContainers is called once at startup after Docker becomes available.
// It compares live container state against stored clusters and corrects status drift.
func (h *Handler) reconcileContainers(ctx context.Context, containers []docker.ContainerSummary) {
	// One resource ID can name several containers — another Overcast's for the
	// same ARN, or a stale one from an earlier run beside the live one — so
	// they are collected rather than overwritten, and OwnContainer picks this
	// instance's out of them below. Keyed to a single container, the index kept
	// whichever the daemon listed last.
	byResource := docker.ContainersByResource(containers)

	regioned, err := serviceutil.ScanRegions[Cluster](ctx, h.store.store, nsClusters, h.store.defaultRegion)
	if err != nil {
		h.log.Warn("reconcile: failed to list MSK clusters", zap.Error(err))
		return
	}
	for _, rc := range regioned {
		cluster := rc.Value
		rctx := middleware.ContextWithRegion(ctx, rc.Region)
		c := h.instances.OwnContainer(rctx, byResource[cluster.ClusterArn], cluster.DockerContainerID)
		switch {
		case c == nil:
			if clusterRuntimeExpected(cluster.State) {
				h.transitionCluster(rctx, cluster.ClusterArn, "HEALING", cluster.State)
				h.startClusterAsync(cluster.ClusterArn)
				h.log.Info("reconcile: MSK container missing; replacement scheduled",
					zap.String("cluster", cluster.ClusterArn))
			}
		case c.State == "running":
			addr, port := h.clusterEndpointAddr(rctx, c.ID, cluster.HostPort)
			if _, aerr := h.mutateCluster(rctx, cluster.ClusterArn, func(stored *Cluster) *protocol.AWSError {
				stored.DockerContainerID = c.ID
				stored.HostPort = port
				return nil
			}); aerr != nil {
				h.log.Warn("reconcile: persist MSK container", zap.String("cluster", cluster.ClusterArn), zap.String("error", aerr.Message))
			}
			if clusterRuntimeExpected(cluster.State) {
				h.scheduleHealthCheck(cluster.ClusterArn, addr, port)
				h.log.Info("reconcile: MSK container running — scheduling health check",
					zap.String("cluster", cluster.ClusterArn))
			}
		default:
			if clusterRuntimeExpected(cluster.State) {
				h.transitionCluster(rctx, cluster.ClusterArn, "HEALING", cluster.State)
				h.startClusterAsync(cluster.ClusterArn)
				h.log.Info("reconcile: MSK container not running; replacement scheduled",
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
