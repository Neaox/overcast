package serviceutil

import (
	"context"

	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/state"
)

// instanceKey is the key InstanceIdentity stores the identity under, within
// whatever namespace the caller passes.
const instanceKey = "id"

// InstanceIdentity returns the identity that scopes this instance's sweeps of
// shared Docker resources, minting and recording one on first use. It is the
// value services stamp into docker.LabelInstance.
//
// The identity's lifetime is exactly the lifetime of the store it is read
// from, and that is the whole point rather than an implementation detail. A
// durable backend (persistent, hybrid, wal) hands back the same identity after
// a restart, so a volume this instance orphaned by crashing is still
// recognisably its own and can be swept. Two processes pointed at the same
// data directory read the same identity and therefore share a sweep domain,
// which is correct: they share the records that say which resources are still
// wanted. A memory backend loses the identity along with those records, so the
// next process mints a fresh one and inherits nothing — it can prove ownership
// of nothing that predates it, and so sweeps nothing that predates it.
//
// No caller needs to ask which backend it got. Durability answers the question
// on its own, and answers it the same way for every service, including one
// given a per-service backend override.
//
// Returns "" if the identity can neither be read nor recorded. Callers must
// treat "" as "ownership cannot be established" and sweep nothing: the store
// being unavailable is not evidence that anything on the daemon is litter.
func InstanceIdentity(ctx context.Context, st state.Store, namespace string) string {
	if st == nil {
		return ""
	}
	id, found, err := st.Get(ctx, namespace, instanceKey)
	if err != nil {
		return ""
	}
	if found && id != "" {
		return id
	}

	minted := uuid.NewString()
	if err := st.Set(ctx, namespace, instanceKey, minted); err != nil {
		return ""
	}
	// Re-read so two processes starting against one durable store converge on
	// a single domain instead of each keeping its own. They can still race
	// closely enough that each reads back its own write, in which case the
	// domains stay split — that costs an unswept volume, never a deleted one,
	// which is the direction this whole mechanism errs in.
	if settled, found, err := st.Get(ctx, namespace, instanceKey); err == nil && found && settled != "" {
		return settled
	}
	return minted
}
