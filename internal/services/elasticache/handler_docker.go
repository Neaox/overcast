package elasticache

import (
	"context"
	"strings"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// ── Docker container event handlers ──────────────────────────────────────────
//
// The event payloads only carry a resource ID — not the region the resource
// was stored under — so these handlers locate the resource with a
// cross-region scan and pin that region on the context for store writes.

// Every ElastiCache resource ID is a name the caller chose — a cache cluster
// ID, a replication group ID, a serverless cache name — and nothing keeps
// those unique across the Overcasts sharing a Docker daemon. Two of them can
// each hold a cluster called "sessions", and each one's container carries
// overcast.resource-id=sessions, so matching on that alone lets one mark the
// other's live cache stopped, or adopt it and later stop and delete it. Every
// match below therefore goes through h.instances, which settles ownership by
// the identity of the store that created the container and falls back to the
// record's own note of the container ID — see InstanceDomain.ContainerIsOurs.

// handleContainerEvent processes DockerContainerDied and DockerContainerStopped.
// Handles cache clusters (plain resource ID), replication groups ("rg:" prefix),
// and serverless caches ("serverless:" prefix).
func (h *Handler) handleContainerEvent(_ context.Context, e events.Event) {
	p, ok := e.Payload.(events.DockerContainerPayload)
	if !ok || p.Service != serviceName {
		return
	}

	if rgID, isRG := parseRGResourceID(p.ResourceID); isRG {
		rg, region, found, err := serviceutil.FindRegioned[ReplicationGroup](h.bgCtx, h.store.store, nsReplication, rgID, h.store.defaultRegion)
		if err != nil || !found {
			return
		}
		ctx := middleware.ContextWithRegion(h.bgCtx, region)
		log := h.log.WithRecorder(ctx)
		if !h.instances.ContainerIsOurs(ctx, p.ContainerID, p.Instance, rg.DockerContainerID) {
			// Another Overcast's cache exiting is its business. Marking this
			// group stopped over it takes down a record whose own container
			// never went anywhere.
			return
		}
		switch rg.Status {
		case "available", "starting":
			h.transitionReplicationGroup(ctx, rgID, "stopped", rg.Status)
			log.Info("replication group container stopped",
				zap.String("rg", rgID), zap.String("action", p.Action))
		}
		return
	}
	if name, isServerless := parseServerlessResourceID(p.ResourceID); isServerless {
		cache, region, found, err := serviceutil.FindRegioned[ServerlessCache](h.bgCtx, h.store.store, nsServerless, name, h.store.defaultRegion)
		if err != nil || !found {
			return
		}
		ctx := middleware.ContextWithRegion(h.bgCtx, region)
		log := h.log.WithRecorder(ctx)
		if !h.instances.ContainerIsOurs(ctx, p.ContainerID, p.Instance, cache.DockerContainerID) {
			return
		}
		switch cache.Status {
		case "available", "starting":
			h.transitionServerlessCache(ctx, name, "stopped", cache.Status)
			log.Info("serverless cache container stopped",
				zap.String("cache", name), zap.String("action", p.Action))
		}
		return
	}

	cluster, region, found, err := serviceutil.FindRegioned[CacheCluster](h.bgCtx, h.store.store, nsClusters, p.ResourceID, h.store.defaultRegion)
	if err != nil || !found {
		return
	}
	ctx := middleware.ContextWithRegion(h.bgCtx, region)
	log := h.log.WithRecorder(ctx)
	if !h.instances.ContainerIsOurs(ctx, p.ContainerID, p.Instance, cluster.DockerContainerID) {
		return
	}
	switch cluster.CacheClusterStatus {
	case "available", "starting":
		h.transitionCacheCluster(ctx, p.ResourceID, "stopped", cluster.CacheClusterStatus)
		log.Info("cache cluster container stopped",
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

	if rgID, isRG := parseRGResourceID(p.ResourceID); isRG {
		rg, region, found, err := serviceutil.FindRegioned[ReplicationGroup](h.bgCtx, h.store.store, nsReplication, rgID, h.store.defaultRegion)
		if err != nil || !found || rg.ConfigurationEndpoint == nil {
			return
		}
		if !h.instances.ContainerIsOurs(middleware.ContextWithRegion(h.bgCtx, region), p.ContainerID, p.Instance, rg.DockerContainerID) {
			// Health-checking another Overcast's container would report this
			// group available on an endpoint it does not own.
			return
		}
		switch rg.Status {
		case "stopped", "starting", "creating":
			h.scheduleReplicationGroupHealthCheck(region, rgID, rg.ConfigurationEndpoint.Address, rg.ConfigurationEndpoint.Port)
		}
		return
	}
	if name, isServerless := parseServerlessResourceID(p.ResourceID); isServerless {
		cache, region, found, err := serviceutil.FindRegioned[ServerlessCache](h.bgCtx, h.store.store, nsServerless, name, h.store.defaultRegion)
		if err != nil || !found || cache.Endpoint == nil {
			return
		}
		if !h.instances.ContainerIsOurs(middleware.ContextWithRegion(h.bgCtx, region), p.ContainerID, p.Instance, cache.DockerContainerID) {
			return
		}
		switch cache.Status {
		case "stopped", "starting", "creating":
			h.scheduleServerlessHealthCheck(region, name, cache.Endpoint.Address, cache.Endpoint.Port)
		}
		return
	}

	cluster, region, found, err := serviceutil.FindRegioned[CacheCluster](h.bgCtx, h.store.store, nsClusters, p.ResourceID, h.store.defaultRegion)
	if err != nil || !found {
		return
	}
	if !h.instances.ContainerIsOurs(middleware.ContextWithRegion(h.bgCtx, region), p.ContainerID, p.Instance, cluster.DockerContainerID) {
		return
	}
	switch cluster.CacheClusterStatus {
	case "stopped", "starting", "creating":
		h.scheduleHealthCheck(region, cluster.CacheClusterId, cluster.ConfigurationEndpoint.Address, cluster.ConfigurationEndpoint.Port)
	}
}

// reconcileContainers is called once at startup after Docker becomes available.
// It compares live container state against stored clusters and replication groups
// and corrects any status drift (e.g. containers that exited while Overcast was
// not running). Resources are listed across all regions — the store keys them
// per region, and reconciliation must cover every region, not just the default.
func (h *Handler) reconcileContainers(ctx context.Context, containers []docker.ContainerSummary) {
	log := h.log.WithRecorder(ctx)
	// One resource ID can name several containers — a cache called "sessions"
	// under each Overcast sharing this daemon — so they are collected rather
	// than overwritten, and OwnContainer picks this instance's out of them
	// below. Keyed to a single container, the index kept whichever the daemon
	// listed last, so a neighbour's could decide the state of a record whose
	// own container was running all along.
	byResource := make(map[string][]*docker.ContainerSummary, len(containers))
	for i := range containers {
		rid := containers[i].ResourceID()
		if rid != "" {
			byResource[rid] = append(byResource[rid], &containers[i])
		}
	}

	// Reconcile cache clusters.
	clusters, err := serviceutil.ScanRegions[CacheCluster](ctx, h.store.store, nsClusters, h.store.defaultRegion)
	if err != nil {
		log.Warn("reconcile: failed to list cache clusters", zap.Error(err))
	} else {
		for _, rc := range clusters {
			cluster := rc.Value
			if cluster.DockerContainerID == "" {
				continue
			}
			rctx := middleware.ContextWithRegion(ctx, rc.Region)
			c := h.instances.OwnContainer(rctx, byResource[cluster.CacheClusterId], cluster.DockerContainerID)
			switch {
			case c == nil:
				if cluster.CacheClusterStatus == "available" || cluster.CacheClusterStatus == "starting" || cluster.CacheClusterStatus == "creating" {
					h.transitionCacheCluster(rctx, cluster.CacheClusterId, "stopped", cluster.CacheClusterStatus)
					log.Info("reconcile: container missing — marked stopped",
						zap.String("cluster", cluster.CacheClusterId))
				}
			case c.State == "running":
				// The Docker inspect stays outside the record's lock; only the
				// endpoint it decides is written back, onto a fresh read.
				h.setContainerEndpoint(rctx, cluster)
				if _, aerr := h.mutateCacheCluster(rctx, cluster.CacheClusterId, func(stored *CacheCluster) *protocol.AWSError {
					stored.ConfigurationEndpoint = cluster.ConfigurationEndpoint
					return nil
				}); aerr != nil {
					log.Warn("reconcile: persist cluster endpoint",
						zap.String("cluster", cluster.CacheClusterId), zap.String("error", aerr.Message))
				}
				if cluster.CacheClusterStatus == "creating" || cluster.CacheClusterStatus == "starting" || cluster.CacheClusterStatus == "stopped" || cluster.CacheClusterStatus == "available" {
					h.scheduleHealthCheck(rc.Region, cluster.CacheClusterId, cluster.ConfigurationEndpoint.Address, cluster.ConfigurationEndpoint.Port)
					log.Info("reconcile: container running — scheduling health check",
						zap.String("cluster", cluster.CacheClusterId))
				}
			default:
				if cluster.CacheClusterStatus == "available" || cluster.CacheClusterStatus == "starting" {
					h.transitionCacheCluster(rctx, cluster.CacheClusterId, "stopped", cluster.CacheClusterStatus)
					log.Info("reconcile: container not running — marked stopped",
						zap.String("cluster", cluster.CacheClusterId),
						zap.String("containerState", c.State))
				}
			}
		}
	}

	// Reconcile replication groups.
	rgs, err := serviceutil.ScanRegions[ReplicationGroup](ctx, h.store.store, nsReplication, h.store.defaultRegion)
	if err != nil {
		log.Warn("reconcile: failed to list replication groups", zap.Error(err))
	} else {
		for _, rr := range rgs {
			rg := rr.Value
			if rg.DockerContainerID == "" {
				continue
			}
			rctx := middleware.ContextWithRegion(ctx, rr.Region)
			resourceLabel := "rg:" + rg.ReplicationGroupId
			c := h.instances.OwnContainer(rctx, byResource[resourceLabel], rg.DockerContainerID)
			switch {
			case c == nil:
				if rg.Status == "available" || rg.Status == "starting" || rg.Status == "creating" {
					h.transitionReplicationGroup(rctx, rg.ReplicationGroupId, "stopped", rg.Status)
					log.Info("reconcile: RG container missing — marked stopped",
						zap.String("rg", rg.ReplicationGroupId))
				}
			case c.State == "running":
				h.setReplicationGroupEndpoint(rctx, rg)
				if _, aerr := h.mutateReplicationGroup(rctx, rg.ReplicationGroupId, func(stored *ReplicationGroup) *protocol.AWSError {
					stored.ConfigurationEndpoint = rg.ConfigurationEndpoint
					return nil
				}); aerr != nil {
					log.Warn("reconcile: persist RG endpoint",
						zap.String("rg", rg.ReplicationGroupId), zap.String("error", aerr.Message))
				}
				if rg.Status == "creating" || rg.Status == "starting" || rg.Status == "stopped" || rg.Status == "available" {
					h.scheduleReplicationGroupHealthCheck(rr.Region, rg.ReplicationGroupId, rg.ConfigurationEndpoint.Address, rg.ConfigurationEndpoint.Port)
					log.Info("reconcile: RG container running — scheduling health check",
						zap.String("rg", rg.ReplicationGroupId))
				}
			default:
				if rg.Status == "available" || rg.Status == "starting" {
					h.transitionReplicationGroup(rctx, rg.ReplicationGroupId, "stopped", rg.Status)
					log.Info("reconcile: RG container not running — marked stopped",
						zap.String("rg", rg.ReplicationGroupId),
						zap.String("containerState", c.State))
				}
			}
		}
	}

	serverless, err := serviceutil.ScanRegions[ServerlessCache](ctx, h.store.store, nsServerless, h.store.defaultRegion)
	if err != nil {
		log.Warn("reconcile: failed to list serverless caches", zap.Error(err))
		return
	}
	for _, rs := range serverless {
		cache := rs.Value
		if cache.DockerContainerID == "" {
			continue
		}
		rctx := middleware.ContextWithRegion(ctx, rs.Region)
		resourceLabel := "serverless:" + cache.ServerlessCacheName
		c := h.instances.OwnContainer(rctx, byResource[resourceLabel], cache.DockerContainerID)
		switch {
		case c == nil:
			if cache.Status == "available" || cache.Status == "starting" || cache.Status == "creating" {
				h.transitionServerlessCache(rctx, cache.ServerlessCacheName, "stopped", cache.Status)
				log.Info("reconcile: serverless cache container missing — marked stopped",
					zap.String("cache", cache.ServerlessCacheName))
			}
		case c.State == "running":
			h.setServerlessEndpoint(rctx, cache)
			if _, aerr := h.mutateServerlessCache(rctx, cache.ServerlessCacheName, func(stored *ServerlessCache) *protocol.AWSError {
				stored.Endpoint = cache.Endpoint
				stored.ReaderEndpoint = cache.ReaderEndpoint
				return nil
			}); aerr != nil {
				log.Warn("reconcile: persist serverless cache endpoint",
					zap.String("cache", cache.ServerlessCacheName), zap.String("error", aerr.Message))
			}
			if cache.Status == "creating" || cache.Status == "starting" || cache.Status == "stopped" || cache.Status == "available" {
				h.scheduleServerlessHealthCheck(rs.Region, cache.ServerlessCacheName, cache.Endpoint.Address, cache.Endpoint.Port)
				log.Info("reconcile: serverless cache container running — scheduling health check",
					zap.String("cache", cache.ServerlessCacheName))
			}
		default:
			if cache.Status == "available" || cache.Status == "starting" {
				h.transitionServerlessCache(rctx, cache.ServerlessCacheName, "stopped", cache.Status)
				log.Info("reconcile: serverless cache container not running — marked stopped",
					zap.String("cache", cache.ServerlessCacheName),
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

func parseServerlessResourceID(resourceID string) (string, bool) {
	if strings.HasPrefix(resourceID, "serverless:") {
		return strings.TrimPrefix(resourceID, "serverless:"), true
	}
	return "", false
}
