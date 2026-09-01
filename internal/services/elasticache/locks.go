package elasticache

// locks.go — serialising the writers of one cache record.
//
// Every record here — cache cluster, replication group, serverless cache — has
// an API handler and a lifecycle writer editing it at the same time. The
// lifecycle side is the container start (which pulls an image and can take
// minutes), the health check that dials the engine until it answers, the Docker
// event stream, and the startup reconcile sweep. A record is stored as one
// blob, so without a shared lock the writer that lands second discards the
// first's edit — a delete reverted by a container start that began before it,
// or a status write undone by a poll that read the record a moment earlier.
//
// Same two rules as the RDS side (internal/services/rds/locks.go): every
// read-modify-write of a record happens under that record's lock, and a writer
// that has been away re-reads inside the lock rather than writing back the
// snapshot it left with. Slow work — Docker calls, dials — stays outside.

import (
	"context"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/protocol"
)

// errRecordMovedOn abandons a mutation whose guard no longer holds: something
// took the record somewhere on purpose while the caller was away. It is never
// returned to a caller — every use pairs with a check for it.
var errRecordMovedOn = &protocol.AWSError{Code: "RecordMovedOn"}

// ─── Cache clusters ──────────────────────────────────────────────────────────

// mutateCacheCluster applies edit to a cache cluster's stored record under that
// record's lock, so the read, the edit and the write are one step. edit
// returning an error leaves the record untouched.
func (h *Handler) mutateCacheCluster(ctx context.Context, clusterID string, edit func(*CacheCluster) *protocol.AWSError) (*CacheCluster, *protocol.AWSError) {
	defer h.clusterLocks.Lock(h.store.region(ctx) + "/" + clusterID)()

	got, aerr := h.store.getCacheCluster(ctx, clusterID)
	if aerr != nil {
		return nil, aerr
	}
	if aerr := edit(got); aerr != nil {
		return nil, aerr
	}
	if aerr := h.store.putCacheCluster(ctx, got); aerr != nil {
		return nil, aerr
	}
	return got, nil
}

// transitionCacheCluster moves a cluster to a status, but only from one of
// from: anything else means something moved it on purpose and a lifecycle
// callback must not write over that.
func (h *Handler) transitionCacheCluster(ctx context.Context, clusterID, to string, from ...string) {
	if _, aerr := h.mutateCacheCluster(ctx, clusterID, func(c *CacheCluster) *protocol.AWSError {
		if !statusIn(c.CacheClusterStatus, from) {
			return errRecordMovedOn
		}
		c.CacheClusterStatus = to
		if to == "available" {
			// A cluster that has answered has no failure left to explain, and a
			// stale reason on a working record is worse than none.
			c.StatusReason = ""
		}
		return nil
	}); aerr != nil && aerr != errRecordMovedOn {
		h.log.Warn("ElastiCache: persist cluster transition",
			zap.String("cluster", clusterID), zap.String("status", to),
			zap.String("error", aerr.Message))
	}
}

// ─── Replication groups ──────────────────────────────────────────────────────

// mutateReplicationGroup is mutateCacheCluster for a replication group record.
func (h *Handler) mutateReplicationGroup(ctx context.Context, rgID string, edit func(*ReplicationGroup) *protocol.AWSError) (*ReplicationGroup, *protocol.AWSError) {
	defer h.rgLocks.Lock(h.store.region(ctx) + "/" + rgID)()

	got, aerr := h.store.getReplicationGroup(ctx, rgID)
	if aerr != nil {
		return nil, aerr
	}
	if aerr := edit(got); aerr != nil {
		return nil, aerr
	}
	if aerr := h.store.putReplicationGroup(ctx, got); aerr != nil {
		return nil, aerr
	}
	return got, nil
}

// transitionReplicationGroup is transitionCacheCluster for a replication group.
func (h *Handler) transitionReplicationGroup(ctx context.Context, rgID, to string, from ...string) {
	if _, aerr := h.mutateReplicationGroup(ctx, rgID, func(rg *ReplicationGroup) *protocol.AWSError {
		if !statusIn(rg.Status, from) {
			return errRecordMovedOn
		}
		rg.Status = to
		if to == "available" {
			rg.StatusReason = ""
		}
		return nil
	}); aerr != nil && aerr != errRecordMovedOn {
		h.log.Warn("ElastiCache: persist replication group transition",
			zap.String("rg", rgID), zap.String("status", to),
			zap.String("error", aerr.Message))
	}
}

// ─── Serverless caches ───────────────────────────────────────────────────────

// mutateServerlessCache is mutateCacheCluster for a serverless cache record.
func (h *Handler) mutateServerlessCache(ctx context.Context, name string, edit func(*ServerlessCache) *protocol.AWSError) (*ServerlessCache, *protocol.AWSError) {
	defer h.serverlessLocks.Lock(h.store.region(ctx) + "/" + name)()

	got, aerr := h.store.getServerlessCache(ctx, name)
	if aerr != nil {
		return nil, aerr
	}
	if aerr := edit(got); aerr != nil {
		return nil, aerr
	}
	if aerr := h.store.putServerlessCache(ctx, got); aerr != nil {
		return nil, aerr
	}
	return got, nil
}

// transitionServerlessCache is transitionCacheCluster for a serverless cache.
func (h *Handler) transitionServerlessCache(ctx context.Context, name, to string, from ...string) {
	if _, aerr := h.mutateServerlessCache(ctx, name, func(c *ServerlessCache) *protocol.AWSError {
		if !statusIn(c.Status, from) {
			return errRecordMovedOn
		}
		c.Status = to
		if to == "available" {
			c.StatusReason = ""
		}
		return nil
	}); aerr != nil && aerr != errRecordMovedOn {
		h.log.Warn("ElastiCache: persist serverless cache transition",
			zap.String("cache", name), zap.String("status", to),
			zap.String("error", aerr.Message))
	}
}

// statusIn reports whether status is one of want.
func statusIn(status string, want []string) bool {
	for _, w := range want {
		if status == w {
			return true
		}
	}
	return false
}
