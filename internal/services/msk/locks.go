package msk

// locks.go — serialising the writers of one cluster's record.
//
// A cluster record is written by the API handlers, by the container start, by
// the health check that polls Redpanda until it answers, by the Docker event
// stream and by the startup reconcile sweep. The record is stored as one blob,
// so without a shared lock the writer that lands second discards the first's
// edit — a delete reverted by a start that began before it, or a state write
// undone by a poll that read the record a moment earlier.
//
// Same rules as the RDS and ElastiCache sides: every read-modify-write happens
// under the record's lock, and a writer that has been away re-reads inside the
// lock rather than writing back the snapshot it left with. Slow work — Docker
// calls, dials — stays outside.

import (
	"context"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/protocol"
)

// errClusterMovedOn abandons a mutation whose guard no longer holds: something
// took the cluster somewhere on purpose while the caller was away. It is never
// returned to a caller — every use pairs with a check for it.
var errClusterMovedOn = &protocol.AWSError{Code: "ClusterMovedOn"}

// mutateCluster applies edit to a cluster's stored record under that record's
// lock, so the read, the edit and the write are one step. edit returning an
// error leaves the record untouched.
//
// Keyed by ARN, which already carries the region.
func (h *Handler) mutateCluster(ctx context.Context, clusterARN string, edit func(*Cluster) *protocol.AWSError) (*Cluster, *protocol.AWSError) {
	defer h.clusterLocks.Lock(clusterARN)()

	got, aerr := h.store.getCluster(ctx, clusterARN)
	if aerr != nil {
		return nil, aerr
	}
	if aerr := edit(got); aerr != nil {
		return nil, aerr
	}
	if aerr := h.store.putCluster(ctx, got); aerr != nil {
		return nil, aerr
	}
	return got, nil
}

// transitionCluster moves a cluster to a state, but only from one of from:
// anything else means something moved it on purpose and a lifecycle callback
// must not write over that.
func (h *Handler) transitionCluster(ctx context.Context, clusterARN, to string, from ...string) {
	if _, aerr := h.mutateCluster(ctx, clusterARN, func(c *Cluster) *protocol.AWSError {
		if !stateIn(c.State, from) {
			return errClusterMovedOn
		}
		c.State = to
		if to == "ACTIVE" {
			// A cluster that has answered has no failure left to explain, and a
			// stale stateInfo on a working cluster is worse than none.
			c.StateInfo = nil
		}
		return nil
	}); aerr != nil && aerr != errClusterMovedOn {
		log := h.log.WithRecorder(ctx)
		log.Warn("MSK: persist cluster transition",
			zap.String("cluster", clusterARN), zap.String("state", to),
			zap.String("error", aerr.Message))
	}
}

// stateIn reports whether state is one of want.
func stateIn(state string, want []string) bool {
	for _, w := range want {
		if state == w {
			return true
		}
	}
	return false
}
