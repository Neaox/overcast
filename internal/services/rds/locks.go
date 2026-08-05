package rds

// locks.go — serialising the writers of one instance's or one cluster's record.
//
// An instance record has two kinds of writer working on it at once. The API
// handlers are one: modify, stop, start, delete each read the record, edit it
// and write the whole record back. The lifecycle is the other — the scheduled
// status transitions, the background container launch, and the health check
// that polls the engine until it answers. Neither knows about the other, and
// because a record is stored as one blob, the writer that lands second silently
// discards the first's edit.
//
// The health check is the one that made this worth closing. It read the
// instance, dialled the engine — up to dialTimeout, and again every
// healthCheckInterval — and then wrote back the copy it had read *before* the
// dial. Every edit an API call made during that window was reverted: a
// ModifyDBInstance's new instance class silently discarded, or its "modifying"
// status rolled back so the instance looked settled when it was not.
//
// Two rules close it, and both are needed:
//
//   - Every read-modify-write of a record runs under that record's lock, so no
//     two of them interleave. mutateInstance and mutateCluster are how.
//   - A writer that has been away — dialling, pulling an image, starting a
//     container — re-reads the record inside the lock and writes back only the
//     fields it owns, rather than the snapshot it left with. failInstance
//     already worked that way; markAvailable did not, which is where the lost
//     modifies came from.
//
// Slow work stays outside the lock: a critical section here is one read and one
// write. Locks are never nested — an instance mutation does not take a cluster
// lock, or the reverse.

import (
	"context"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/protocol"
)

// lockInstance takes the lock covering one instance record, returning the
// function that releases it. Keyed by region as well as identifier: the same
// instance name in two regions is two records.
func (h *Handler) lockInstance(ctx context.Context, instanceID string) func() {
	return h.instanceLocks.Lock(h.store.region(ctx) + "/" + instanceID)
}

// lockCluster is lockInstance for a DB cluster record.
func (h *Handler) lockCluster(ctx context.Context, clusterID string) func() {
	return h.clusterLocks.Lock(h.store.region(ctx) + "/" + clusterID)
}

// mutateInstance applies edit to an instance's stored record under that
// record's lock, returning the record it wrote. The read, the edit and the
// write are one step, so nothing another writer did in between is lost.
//
// edit returning an error leaves the record untouched and returns that error.
// A missing instance is reported as the store reports it, so a caller that is
// an API handler can pass the error straight back.
func (h *Handler) mutateInstance(ctx context.Context, instanceID string, edit func(inst *DBInstance) *protocol.AWSError) (*DBInstance, *protocol.AWSError) {
	defer h.lockInstance(ctx, instanceID)()

	got, aerr := h.store.getDBInstance(ctx, instanceID)
	if aerr != nil {
		return nil, aerr
	}
	if aerr := edit(got); aerr != nil {
		return nil, aerr
	}
	if aerr := h.store.putDBInstance(ctx, got); aerr != nil {
		return nil, aerr
	}
	return got, nil
}

// transitionInstance moves an instance from one status to another, and does
// nothing at all if it is no longer in the status the transition was scheduled
// for: something else has taken it somewhere on purpose — stopped, deleted,
// modified — and a lifecycle callback must not write over that.
func (h *Handler) transitionInstance(ctx context.Context, instanceID, from, to string) {
	defer h.lockInstance(ctx, instanceID)()

	got, aerr := h.store.getDBInstance(ctx, instanceID)
	if aerr != nil || got == nil || got.DBInstanceStatus != from {
		return
	}
	got.DBInstanceStatus = to
	if aerr := h.store.putDBInstance(ctx, got); aerr != nil {
		h.log.Warn("RDS: persist instance transition",
			zap.String("instance", instanceID), zap.String("status", to),
			zap.String("error", aerr.Message))
	}
}

// mutateCluster is mutateInstance for a DB cluster record.
func (h *Handler) mutateCluster(ctx context.Context, clusterID string, edit func(cl *DBCluster) *protocol.AWSError) (*DBCluster, *protocol.AWSError) {
	defer h.lockCluster(ctx, clusterID)()

	got, aerr := h.store.getDBCluster(ctx, clusterID)
	if aerr != nil {
		return nil, aerr
	}
	if aerr := edit(got); aerr != nil {
		return nil, aerr
	}
	if aerr := h.store.putDBCluster(ctx, got); aerr != nil {
		return nil, aerr
	}
	return got, nil
}

// transitionCluster is transitionInstance for a DB cluster record.
func (h *Handler) transitionCluster(ctx context.Context, clusterID, from, to string) {
	defer h.lockCluster(ctx, clusterID)()

	got, aerr := h.store.getDBCluster(ctx, clusterID)
	if aerr != nil || got == nil || got.Status != from {
		return
	}
	got.Status = to
	if aerr := h.store.putDBCluster(ctx, got); aerr != nil {
		h.log.Warn("RDS: persist cluster transition",
			zap.String("cluster", clusterID), zap.String("status", to),
			zap.String("error", aerr.Message))
	}
}
