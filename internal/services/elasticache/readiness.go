package elasticache

// readiness.go — what "available" is allowed to mean, and what a cache that
// never comes up is called instead.
//
// # The statuses
//
// Every terminal value here is one AWS actually documents for that shape, and
// they are not the same value: ElastiCache's three resources do not share a
// status vocabulary.
//
//   - ReplicationGroup and ServerlessCache both document `create-failed`.
//     ReplicationGroup's Status is "creating, available, modifying, deleting,
//     create-failed, snapshotting"; ServerlessCache's is "CREATING, AVAILABLE,
//     DELETING, CREATE-FAILED and MODIFYING". (API_ReplicationGroup.html and
//     API_ServerlessCache.html, both checked 2026-08-15.) AWS writes the
//     serverless one in upper case, but every other status Overcast stores on a
//     serverless cache is lower case — `creating`, `available`, `modifying` —
//     so the lower-case spelling is used here rather than making the failure
//     the one upper-case value in a record's lifecycle.
//
//   - CacheCluster documents no `create-failed` at all. CacheClusterStatus is
//     "available, creating, deleted, deleting, incompatible-network, modifying,
//     rebooting cluster nodes, restore-failed, snapshotting"
//     (API_CacheCluster.html, checked 2026-08-15), and of those only
//     `incompatible-network` and `restore-failed` are failures. `restore-failed`
//     is specific to restoring from a snapshot, which is not what happened, so
//     `incompatible-network` is the one AWS-real terminal value left for a
//     cluster whose creation could not complete. It is also load-bearing rather
//     than merely plausible: botocore's elasticache waiters-2.json gives
//     CacheClusterAvailable four failure acceptors — deleted, deleting,
//     incompatible-network, restore-failed — so `aws elasticache wait
//     cache-cluster-available` fails immediately on it and reports the failure,
//     which is exactly the behaviour a stuck "creating" denied it.
//
// The status alone is never the whole answer, so each failure also records the
// reason that produced it — the probe's own last words, not a summary.
//
// # Where the reason lives
//
// On the record, and deliberately not on the wire. Neither CacheCluster,
// ReplicationGroup nor ServerlessCache has a status-reason field in the AWS
// API, and inventing one would put a field in a response that no SDK models.
// This is the same call rds/health.go made for DBInstance.StatusReason and for
// the same reason.

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil/readiness"
)

const (
	// statusCreateFailed is AWS's terminal failure for a replication group and
	// for a serverless cache.
	statusCreateFailed = "create-failed"

	// statusClusterUnreachable is AWS's terminal failure for a cache cluster.
	// See the file comment for why ElastiCache spells the same event two
	// different ways depending on which of its resources it happened to.
	statusClusterUnreachable = "incompatible-network"
)

// cacheReadiness is the timing every ElastiCache readiness watch runs on.
//
// Calibrated against a Redis, Valkey or Memcached container on this machine's
// Docker daemon: each is serving within a second of the container starting once
// its image is local, and the budget is sized for the case that is actually
// slow — a cold daemon under load, where the container is created before the
// image layers have finished settling. It is a minute rather than the five RDS
// gives a database because none of these engines initialises a data directory
// on first boot; there is nothing here for a long budget to be waiting on.
var cacheReadiness = readiness.Policy{
	FirstDelay: 1 * time.Second,
	Interval:   2 * time.Second,
	Budget:     60 * time.Second,
}

// The statuses a readiness watch is entitled to settle. Anything else means
// something moved the record on purpose — a delete, a stop, a modify — and a
// lifecycle callback must not write over that.
//
// Each set includes its own terminal failure so the failure is recoverable: a
// container that comes back after one, through a Docker start event or the
// startup reconcile sweep, schedules a fresh watch and that watch is allowed to
// promote the record to available again. A failure that could never be undone
// would trade one wrong answer for another.
var (
	cacheClusterAwaiting     = []string{"creating", "starting", "modifying", statusClusterUnreachable}
	replicationGroupAwaiting = []string{"creating", "starting", "modifying", statusCreateFailed}
	serverlessAwaiting       = []string{"creating", "starting", "modifying", statusCreateFailed}
)

// ─── Nothing to wait for ─────────────────────────────────────────────────────
//
// A deployment with no Docker starts no container, so no readiness watch is
// ever scheduled and nothing exists that could move the record out of
// "creating". Leaving it there claims progress that will never be made — the
// mirror image of the "available" with nothing behind it that the watches above
// exist to prevent, and just as untrue. `aws elasticache wait` spins out its
// attempts against it, and a CloudFormation stack that waits for the cache
// holds open for its whole budget and then rolls back a cache that was already
// as ready as it was ever going to be.
//
// So a metadata-only resource is ready the moment it is recorded, because there
// is no runtime being claimed and nothing is coming to prove one. That is the
// answer RDS gives on CreateDBInstance and Lambda gives a function with no
// runtime wired, and the one MSK already gives a serverless cluster; these are
// ElastiCache's three shapes agreeing with it.
//
// Two properties this relies on, both deliberate:
//
//   - The transition is guarded on the record still being in "creating", so a
//     delete or a modify arriving in the same moment wins and this becomes a
//     no-op rather than resurrecting what that call decided.
//   - "available" is a status reconciliation still expects a runtime for (see
//     cacheRuntimeExpected), so a daemon that appears later — a restart with a
//     socket configured — still builds a container for the record and runs a
//     real probe against it. Marking it ready is not the same as writing it off.

// settleCacheClusterWithoutRuntime marks a cluster that has no container coming
// available. Scheduled rather than written inline, so it runs on the same
// scheduler as every other transition for this record and orders against them;
// with the real clock a zero delay runs it inline, before the create returns.
func (h *Handler) settleCacheClusterWithoutRuntime(region, clusterID string) {
	h.scheduler.AfterScoped(region, clusterID, "available", 0, func(ctx context.Context) {
		h.transitionCacheCluster(ctx, clusterID, "available", "creating")
	})
}

// settleReplicationGroupWithoutRuntime is the same for a replication group.
func (h *Handler) settleReplicationGroupWithoutRuntime(region, rgID string) {
	h.scheduler.AfterScoped(region, rgID, "available", 0, func(ctx context.Context) {
		h.transitionReplicationGroup(ctx, rgID, "available", "creating")
	})
}

// settleServerlessCacheWithoutRuntime is the same for a serverless cache.
func (h *Handler) settleServerlessCacheWithoutRuntime(region, name string) {
	h.scheduler.AfterScoped(region, name, "available", 0, func(ctx context.Context) {
		h.transitionServerlessCache(ctx, name, "available", "creating")
	})
}

// failCacheCluster settles a cluster whose engine never answered, recording
// why. Re-read inside the record's lock rather than written from a snapshot,
// the same discipline every other writer here follows: a watch that has spent a
// minute waiting must not resurrect a cluster that was deleted while it waited.
func (h *Handler) failCacheCluster(ctx context.Context, clusterID, reason string) {
	if _, aerr := h.mutateCacheCluster(ctx, clusterID, func(c *CacheCluster) *protocol.AWSError {
		if !statusIn(c.CacheClusterStatus, cacheClusterAwaiting) {
			return errRecordMovedOn
		}
		c.CacheClusterStatus = statusClusterUnreachable
		c.StatusReason = reason
		return nil
	}); aerr != nil {
		if aerr != errRecordMovedOn {
			h.log.Warn("ElastiCache: persist failed cluster",
				zap.String("cluster", clusterID), zap.String("error", aerr.Message))
		}
		return
	}
	h.log.Warn("ElastiCache: cache cluster never became ready",
		zap.String("cluster", clusterID), zap.String("status", statusClusterUnreachable),
		zap.String("reason", reason))
}

// failReplicationGroup is failCacheCluster for a replication group. This is the
// branch that used to report "available" — see scheduleReplicationGroupHealthCheck.
func (h *Handler) failReplicationGroup(ctx context.Context, rgID, reason string) {
	if _, aerr := h.mutateReplicationGroup(ctx, rgID, func(rg *ReplicationGroup) *protocol.AWSError {
		if !statusIn(rg.Status, replicationGroupAwaiting) {
			return errRecordMovedOn
		}
		rg.Status = statusCreateFailed
		rg.StatusReason = reason
		return nil
	}); aerr != nil {
		if aerr != errRecordMovedOn {
			h.log.Warn("ElastiCache: persist failed replication group",
				zap.String("rg", rgID), zap.String("error", aerr.Message))
		}
		return
	}
	h.log.Warn("ElastiCache: replication group never became ready",
		zap.String("rg", rgID), zap.String("status", statusCreateFailed),
		zap.String("reason", reason))
}

// failServerlessCache is failCacheCluster for a serverless cache.
func (h *Handler) failServerlessCache(ctx context.Context, name, reason string) {
	if _, aerr := h.mutateServerlessCache(ctx, name, func(c *ServerlessCache) *protocol.AWSError {
		if !statusIn(c.Status, serverlessAwaiting) {
			return errRecordMovedOn
		}
		c.Status = statusCreateFailed
		c.StatusReason = reason
		return nil
	}); aerr != nil {
		if aerr != errRecordMovedOn {
			h.log.Warn("ElastiCache: persist failed serverless cache",
				zap.String("cache", name), zap.String("error", aerr.Message))
		}
		return
	}
	h.log.Warn("ElastiCache: serverless cache never became ready",
		zap.String("cache", name), zap.String("status", statusCreateFailed),
		zap.String("reason", reason))
}
