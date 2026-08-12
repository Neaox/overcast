package serviceutil

import (
	"context"
	"sync"

	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/docker"
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

// InstanceDomain memoizes one service's InstanceIdentity lookup. Every service
// that stamps docker.LabelInstance needs the identity on two paths — the
// creation path that writes the label and the sweep that reads it — and both
// must agree, so the resolution belongs in one place per service rather than
// at each call site.
//
// A successful resolution is cached for the life of the process: the identity
// is a property of the store, and re-reading it per container would put a
// store round-trip on the creation path for an answer that cannot change. A
// failed one is deliberately not cached, so a store that is briefly
// unavailable at startup does not leave the service unable to sweep anything
// until it is restarted.
//
// The zero value is not usable — see NewInstanceDomain. A nil *InstanceDomain
// resolves to "", which callers must read as "ownership cannot be
// established": stamp nothing, sweep nothing.
type InstanceDomain struct {
	store     state.Store
	namespace string

	mu sync.Mutex
	id string
}

// NewInstanceDomain returns an InstanceDomain reading from st under namespace.
// The namespace is per-service (e.g. "rds:instance") so that services given
// separate backend overrides get separate identities, matching the rule that
// the identity's lifetime is its store's.
//
// Nothing is read until the first Resolve, which keeps service construction
// free of store I/O.
func NewInstanceDomain(st state.Store, namespace string) *InstanceDomain {
	return &InstanceDomain{store: st, namespace: namespace}
}

// Resolve returns this service's sweep-domain identity, minting one on first
// use. Safe for concurrent use. Returns "" when ownership cannot be
// established.
func (d *InstanceDomain) Resolve(ctx context.Context) string {
	if d == nil {
		return ""
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.id == "" {
		d.id = InstanceIdentity(ctx, d.store, d.namespace)
	}
	return d.id
}

// ContainerIsOurs reports whether a container is one this instance created for
// the record that wrote down recordedIDs.
//
// Services match a container to a record by its overcast.resource-id label,
// and that label settles nothing about ownership: a resource ID is usually a
// name the caller chose, and even a minted one is shared by two processes
// pointed at one data directory. Matching on it alone lets one Overcast stop,
// adopt or fail another's live container.
//
// docker.LabelInstance settles it where it is present — it names the state
// store that created the container, and a record lives in exactly one such
// store. Where it is absent, the record's own note of the container ID does:
// this instance wrote that down when it created the container, and container
// IDs are daemon-unique. Records that track several containers (an ECS task)
// pass all of them; one is enough to claim it.
//
// That fallback is where matching parts company with sweeping, which treats an
// unlabelled resource as one it cannot claim. The asymmetry is in what being
// wrong costs. A sweep that refuses an unlabelled container leaks disk until
// someone reclaims it; a matcher that refuses one abandons a running resource
// — reported stopped while it is still serving, or rebuilt behind its back. So
// an unlabelled container is not refused here, it is referred to the only
// other party that can vouch for it. A container created before the label
// existed goes on being managed; one this instance never created is still
// never touched.
func (d *InstanceDomain) ContainerIsOurs(ctx context.Context, containerID, owner string, recordedIDs ...string) bool {
	if domain := d.Resolve(ctx); domain != "" && owner != "" {
		return owner == domain
	}
	// Either the container predates the label, or this instance's own identity
	// could not be read just now. A store that is briefly unavailable must not
	// make every container on the daemon look foreign.
	if containerID == "" {
		return false
	}
	for _, id := range recordedIDs {
		if id != "" && id == containerID {
			return true
		}
	}
	return false
}

// OwnContainer picks the container in candidates that this instance created
// for the record that wrote down recordedIDs, if any.
//
// Callers index containers by resource ID, and on a shared daemon one resource
// ID can name several — every Overcast's container for that name, or a stale
// one beside the live one. An index keyed to a single container per resource
// ID keeps whichever the daemon listed last, which is how a neighbour's comes
// to decide the state of a record whose own container is running.
func (d *InstanceDomain) OwnContainer(ctx context.Context, candidates []*docker.ContainerSummary, recordedIDs ...string) *docker.ContainerSummary {
	for _, c := range candidates {
		if d.ContainerIsOurs(ctx, c.ID, c.Instance(), recordedIDs...) {
			return c
		}
	}
	return nil
}

// ManagedLabels returns docker.ManagedLabels for the resource, stamped with
// this instance's identity. It is what every Docker-backed service should use
// in place of docker.ManagedLabels when the resource it is creating will later
// be swept.
//
// The instance label is added only when the identity resolves. An empty value
// would claim an ownership that was never established, and the sweep would
// then have to special-case it; leaving the label off says the same thing in
// the way the sweep already understands.
func (d *InstanceDomain) ManagedLabels(ctx context.Context, service, resourceID string) map[string]string {
	labels := docker.ManagedLabels(service, resourceID)
	if domain := d.Resolve(ctx); domain != "" {
		labels[docker.LabelInstance] = domain
	}
	return labels
}
