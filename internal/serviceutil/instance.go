package serviceutil

import (
	"context"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/state"
)

// InstanceKey is the key InstanceIdentity stores the identity under, within
// whatever namespace the caller passes.
//
// Exported so the reset path can recognise and preserve it. See
// IsInstanceNamespace.
const InstanceKey = "id"

// InstanceNamespaceSuffix is what every namespace holding a sweep-domain
// identity ends in: "ec2:instance", "lambda:instance", and so on for each
// service that stamps docker.LabelInstance.
const InstanceNamespaceSuffix = ":instance"

// IsInstanceNamespace reports whether a namespace holds a sweep-domain
// identity rather than emulated state.
//
// It exists for one caller: the reset path, which must leave these alone. A
// reset wipes what the emulator has been *asked to remember* — every bucket,
// queue, function and VPC. The identity is not that. It names the instance
// doing the remembering, and it is stamped on Docker networks, containers and
// volumes that outlive any single reset.
//
// Wiping it made every one of those resources unclaimable: the next start
// minted a fresh identity, and the sweep — which removes only what carries
// *this* instance's label — could no longer prove it had created them, so it
// left them alone for ever. One leaked Docker network per VPC per reset, which
// on a CI loop exhausts Docker's address pools (#1605).
//
// The label stays honest through a reset precisely because the identity does:
// the reset's own leftovers are still stamped as ours, no record claims them
// any more, and the next reconcile's orphan sweep removes them for that reason.
//
// An identity derived from the data directory (Anchor) would survive the reset
// on its own; the one this protects is the identity a store from before
// anchors existed still holds, which is the only label its older resources
// carry.
func IsInstanceNamespace(namespace string) bool {
	return strings.HasSuffix(namespace, InstanceNamespaceSuffix)
}

// InstanceIdentity returns the identity that scopes this instance's sweeps of
// shared Docker resources, recording one on first use. It is the value
// services stamp into docker.LabelInstance.
//
// The identity names the data directory, and that is the whole point rather
// than an implementation detail. Instances that share a data directory share
// records, and so share a sweep domain: the records are what say which
// resources are still wanted. Instances with different data directories are
// different domains, whatever daemon they share, and neither can touch the
// other's resources.
//
// Three sources, in order:
//
//   - The store. A durable backend (persistent, hybrid, wal) hands back the
//     identity it recorded, so a volume this instance orphaned by crashing is
//     still recognisably its own and can be swept — and a store from before
//     anchors existed keeps the UUID its resources are already labelled with.
//   - The anchor: an identity derived from where the data directory is
//     (DataDirAnchor). It is recorded in the store on first use, and it is
//     what makes the identity survive the store being wiped: the same
//     directory derives the same anchor, so the networks the previous
//     incarnation created still carry *this* instance's label and the orphan
//     sweep reclaims them, rather than leaving their CIDRs held against the
//     very next deploy. A memory backend derives it on every start for the
//     same reason.
//   - A minted UUID, where the environment gives no anchor. Its lifetime is
//     the store's, so a wiped or memory-backed store then inherits nothing —
//     it can prove ownership of nothing that predates it, and sweeps nothing
//     that predates it.
//
// Returns "" if the store can neither be read nor written. Callers must treat
// "" as "ownership cannot be established" and sweep nothing: the store being
// unavailable is not evidence that anything on the daemon is litter. The
// anchor does not stand in for the store here, because a store that cannot
// be read may hold an older identity the resources are labelled with, and a
// label stamped from the anchor meanwhile would be the wrong one.
func InstanceIdentity(ctx context.Context, st state.Store, namespace string, anchor Anchor) string {
	if st == nil {
		return ""
	}
	id, found, err := st.Get(ctx, namespace, InstanceKey)
	if err != nil {
		return ""
	}
	if found && id != "" {
		return id
	}

	minted := anchor.ID
	if minted == "" {
		minted = uuid.NewString()
	}
	if err := st.Set(ctx, namespace, InstanceKey, minted); err != nil {
		return ""
	}
	// Re-read so two processes starting against one durable store converge on
	// a single domain instead of each keeping its own. They can still race
	// closely enough that each reads back its own write, in which case the
	// domains stay split — that costs an unswept volume, never a deleted one,
	// which is the direction this whole mechanism errs in. Two anchored
	// processes cannot even race: they derive the same value.
	if settled, found, err := st.Get(ctx, namespace, InstanceKey); err == nil && found && settled != "" {
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
// is a property of the data directory, and re-reading it per container would
// put a store round-trip on the creation path for an answer that cannot
// change. A failed one is deliberately not cached, so a store that is briefly
// unavailable at startup does not leave the service unable to sweep anything
// until it is restarted.
//
// The zero value is not usable — see NewInstanceDomain. A nil *InstanceDomain
// resolves to "", which callers must read as "ownership cannot be
// established": stamp nothing, sweep nothing.
type InstanceDomain struct {
	store     state.Store
	namespace string
	anchor    Anchor

	mu sync.Mutex
	id string
}

// NewInstanceDomain returns an InstanceDomain reading from st under namespace,
// with no anchor: the identity is whatever the store holds, minted if it holds
// none. That is the right constructor only where no data directory is known;
// a service constructs its domain with NewAnchoredInstanceDomain.
//
// The namespace is per-service (e.g. "rds:instance") so that a service given
// a separate backend override keeps its own record of the identity. With an
// anchor every service derives the same value regardless.
//
// Nothing is read until the first Resolve, which keeps service construction
// free of store I/O.
func NewInstanceDomain(st state.Store, namespace string) *InstanceDomain {
	return NewAnchoredInstanceDomain(st, namespace, Anchor{})
}

// NewAnchoredInstanceDomain is NewInstanceDomain with the data directory's
// anchor, which every service that stamps docker.LabelInstance should pass:
// it is what lets the identity outlive the store. See InstanceIdentity for
// how the two combine.
func NewAnchoredInstanceDomain(st state.Store, namespace string, anchor Anchor) *InstanceDomain {
	return &InstanceDomain{store: st, namespace: namespace, anchor: anchor}
}

// Resolve returns this service's sweep-domain identity, recording one on first
// use. Safe for concurrent use. Returns "" when ownership cannot be
// established.
func (d *InstanceDomain) Resolve(ctx context.Context) string {
	if d == nil {
		return ""
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.id == "" {
		d.id = InstanceIdentity(ctx, d.store, d.namespace, d.anchor)
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
// docker.LabelInstance settles it where it is present — it names the data
// directory whose store created the container, and a record lives in exactly
// one such store. Where it is absent, the record's own note of the container
// ID does: this instance wrote that down when it created the container, and
// container IDs are daemon-unique. Records that track several containers (an
// ECS task) pass all of them; one is enough to claim it.
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
