package elasticache

import (
	"context"
	"strings"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
)

// ── Docker container event handlers ──────────────────────────────────────────

// handleContainerEvent processes DockerContainerDied and DockerContainerStopped.
// Handles both cache clusters (plain resource ID) and replication groups ("rg:" prefix).
func (h *Handler) handleContainerEvent(_ context.Context, e events.Event) {
	p, ok := e.Payload.(events.DockerContainerPayload)
	if !ok || p.Service != serviceName {
		return
	}

	ctx := h.bgCtx

	if rgID, isRG := parseRGResourceID(p.ResourceID); isRG {
		rg, aerr := h.store.getReplicationGroup(ctx, rgID)
		if aerr != nil || rg == nil {
			return
		}
		switch rg.Status {
		case "available", "starting":
			rg.Status = "stopped"
			h.store.putReplicationGroup(ctx, rg) //nolint:errcheck
			h.log.Info("replication group container stopped",
				zap.String("rg", rgID), zap.String("action", p.Action))
		}
		return
	}

	cluster, aerr := h.store.getCacheCluster(ctx, p.ResourceID)
	if aerr != nil || cluster == nil {
		return
	}
	switch cluster.CacheClusterStatus {
	case "available", "starting":
		cluster.CacheClusterStatus = "stopped"
		h.store.putCacheCluster(ctx, cluster) //nolint:errcheck
		h.log.Info("cache cluster container stopped",
			zap.String("cluster", p.ResourceID), zap.String("action", p.Action))
	}
}

// handleContainerStarted processes DockerContainerStarted events. Handles both
// cache clusters and replication groups.
func (h *Handler) handleContainerStarted(_ context.Context, e events.Event) {
	p, ok := e.Payload.(events.DockerContainerPayload)
	if !ok || p.Service != serviceName {
		return
	}

	ctx := context.Background()

	if rgID, isRG := parseRGResourceID(p.ResourceID); isRG {
		rg, aerr := h.store.getReplicationGroup(ctx, rgID)
		if aerr != nil || rg == nil || rg.ConfigurationEndpoint == nil {
			return
		}
		switch rg.Status {
		case "stopped", "starting", "creating":
			h.scheduleReplicationGroupHealthCheck(rgID, rg.ConfigurationEndpoint.Address, rg.ConfigurationEndpoint.Port)
		}
		return
	}

	cluster, aerr := h.store.getCacheCluster(ctx, p.ResourceID)
	if aerr != nil || cluster == nil {
		return
	}
	switch cluster.CacheClusterStatus {
	case "stopped", "starting", "creating":
		h.scheduleHealthCheck(cluster.CacheClusterId, cluster.ConfigurationEndpoint.Address, cluster.ConfigurationEndpoint.Port)
	}
}

// reconcileContainers is called once at startup after Docker becomes available.
// It compares live container state against stored clusters and replication groups
// and corrects any status drift (e.g. containers that exited while Overcast was not running).
func (h *Handler) reconcileContainers(ctx context.Context, containers []docker.ContainerSummary) {
	byResource := make(map[string]*docker.ContainerSummary, len(containers))
	for i := range containers {
		rid := containers[i].ResourceID()
		if rid != "" {
			byResource[rid] = &containers[i]
		}
	}

	// Reconcile cache clusters.
	clusters, aerr := h.store.listCacheClusters(ctx)
	if aerr != nil {
		h.log.Warn("reconcile: failed to list cache clusters", zap.Error(aerr))
	} else {
		for _, cluster := range clusters {
			if cluster.DockerContainerID == "" {
				continue
			}
			c := byResource[cluster.CacheClusterId]
			switch {
			case c == nil:
				if cluster.CacheClusterStatus == "available" || cluster.CacheClusterStatus == "starting" || cluster.CacheClusterStatus == "creating" {
					cluster.CacheClusterStatus = "stopped"
					h.store.putCacheCluster(ctx, cluster) //nolint:errcheck
					h.log.Info("reconcile: container missing — marked stopped",
						zap.String("cluster", cluster.CacheClusterId))
				}
			case c.State == "running":
				h.setContainerEndpoint(ctx, cluster)
				h.store.putCacheCluster(ctx, cluster) //nolint:errcheck
				if cluster.CacheClusterStatus == "creating" || cluster.CacheClusterStatus == "starting" || cluster.CacheClusterStatus == "stopped" || cluster.CacheClusterStatus == "available" {
					h.scheduleHealthCheck(cluster.CacheClusterId, cluster.ConfigurationEndpoint.Address, cluster.ConfigurationEndpoint.Port)
					h.log.Info("reconcile: container running — scheduling health check",
						zap.String("cluster", cluster.CacheClusterId))
				}
			default:
				if cluster.CacheClusterStatus == "available" || cluster.CacheClusterStatus == "starting" {
					cluster.CacheClusterStatus = "stopped"
					h.store.putCacheCluster(ctx, cluster) //nolint:errcheck
					h.log.Info("reconcile: container not running — marked stopped",
						zap.String("cluster", cluster.CacheClusterId),
						zap.String("containerState", c.State))
				}
			}
		}
	}

	// Reconcile replication groups.
	rgs, aerr := h.store.listReplicationGroups(ctx)
	if aerr != nil {
		h.log.Warn("reconcile: failed to list replication groups", zap.Error(aerr))
		return
	}
	for _, rg := range rgs {
		if rg.DockerContainerID == "" {
			continue
		}
		resourceLabel := "rg:" + rg.ReplicationGroupId
		c := byResource[resourceLabel]
		switch {
		case c == nil:
			if rg.Status == "available" || rg.Status == "starting" || rg.Status == "creating" {
				rg.Status = "stopped"
				h.store.putReplicationGroup(ctx, rg) //nolint:errcheck
				h.log.Info("reconcile: RG container missing — marked stopped",
					zap.String("rg", rg.ReplicationGroupId))
			}
		case c.State == "running":
			h.setReplicationGroupEndpoint(ctx, rg)
			h.store.putReplicationGroup(ctx, rg) //nolint:errcheck
			if rg.Status == "creating" || rg.Status == "starting" || rg.Status == "stopped" || rg.Status == "available" {
				h.scheduleReplicationGroupHealthCheck(rg.ReplicationGroupId, rg.ConfigurationEndpoint.Address, rg.ConfigurationEndpoint.Port)
				h.log.Info("reconcile: RG container running — scheduling health check",
					zap.String("rg", rg.ReplicationGroupId))
			}
		default:
			if rg.Status == "available" || rg.Status == "starting" {
				rg.Status = "stopped"
				h.store.putReplicationGroup(ctx, rg) //nolint:errcheck
				h.log.Info("reconcile: RG container not running — marked stopped",
					zap.String("rg", rg.ReplicationGroupId),
					zap.String("containerState", c.State))
			}
		}
	}
}

// parseRGResourceID returns (rgID, true) when the resource label has the "rg:" prefix.
func parseRGResourceID(resourceID string) (string, bool) {
	if strings.HasPrefix(resourceID, "rg:") {
		return strings.TrimPrefix(resourceID, "rg:"), true
	}
	return "", false
}
