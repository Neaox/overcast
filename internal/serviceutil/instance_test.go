package serviceutil_test

import (
	"context"
	"errors"
	"testing"

	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

const testNS = "test:instance"

// The identity is stable for as long as the store holding it is, which is what
// lets a sweep recognise its own resources after a restart — and what stops it
// recognising anything else's.
func TestInstanceIdentity_stableWithinAStore(t *testing.T) {
	ctx := context.Background()
	st := state.NewMemoryStore()

	first := serviceutil.InstanceIdentity(ctx, st, testNS)
	if first == "" {
		t.Fatal("expected an identity to be minted")
	}
	if again := serviceutil.InstanceIdentity(ctx, st, testNS); again != first {
		t.Fatalf("identity changed within one store: %q then %q", first, again)
	}
	// A separate store is a separate sweep domain — a memory-backed restart,
	// or another instance with its own data directory.
	if other := serviceutil.InstanceIdentity(ctx, state.NewMemoryStore(), testNS); other == first {
		t.Fatal("separate stores must not yield the same identity")
	}
}

// failingStore fails every read. The embedded nil interface would panic on any
// other method, which is the point: nothing else should be reached.
type failingStore struct{ state.Store }

func (failingStore) Get(context.Context, string, string) (string, bool, error) {
	return "", false, errors.New("store unavailable")
}

// A store that cannot answer means ownership cannot be established. Callers
// read "" as "sweep nothing" — a store being briefly unavailable is not
// evidence that anything on the Docker daemon is litter.
func TestInstanceIdentity_unavailableStoreYieldsNoIdentity(t *testing.T) {
	if got := serviceutil.InstanceIdentity(context.Background(), failingStore{}, testNS); got != "" {
		t.Fatalf("expected no identity from a failing store, got %q", got)
	}
	if got := serviceutil.InstanceIdentity(context.Background(), nil, testNS); got != "" {
		t.Fatalf("expected no identity from a nil store, got %q", got)
	}
}

// ─── InstanceDomain ───────────────────────────────────────────────────────────

// countingStore counts reads so the memoization can be observed. Every service
// resolves the domain on its container-creation path, so a lookup per container
// would put a store round-trip in front of every task, cache and cold start.
type countingStore struct {
	state.Store
	reads int
}

func (c *countingStore) Get(ctx context.Context, ns, key string) (string, bool, error) {
	c.reads++
	return c.Store.Get(ctx, ns, key)
}

func TestInstanceDomain_resolvesOnceAndAgreesWithInstanceIdentity(t *testing.T) {
	ctx := context.Background()
	st := &countingStore{Store: state.NewMemoryStore()}
	d := serviceutil.NewInstanceDomain(st, testNS)

	first := d.Resolve(ctx)
	if first == "" {
		t.Fatal("expected an identity to be minted")
	}
	readsAfterFirst := st.reads
	if again := d.Resolve(ctx); again != first {
		t.Fatalf("memoized identity changed: %q then %q", first, again)
	}
	if st.reads != readsAfterFirst {
		t.Errorf("Resolve hit the store again: %d reads then %d", readsAfterFirst, st.reads)
	}
	// The label the creation path stamps must be the value the sweep compares
	// against, or a service sweeps away its own containers — or none of them.
	if got := serviceutil.InstanceIdentity(ctx, st, testNS); got != first {
		t.Errorf("domain %q disagrees with the identity in the store %q", first, got)
	}
}

// A failed resolution is not cached. A store briefly unavailable at startup
// must not leave the service unable to sweep anything until it is restarted.
func TestInstanceDomain_doesNotCacheAFailedResolution(t *testing.T) {
	ctx := context.Background()
	flaky := &flakyStore{Store: state.NewMemoryStore(), failNext: true}
	d := serviceutil.NewInstanceDomain(flaky, testNS)

	if got := d.Resolve(ctx); got != "" {
		t.Fatalf("expected no identity while the store was failing, got %q", got)
	}
	if got := d.Resolve(ctx); got == "" {
		t.Fatal("a transient store failure disabled sweeping for the life of the process")
	}
}

// flakyStore fails the first read, then behaves.
type flakyStore struct {
	state.Store
	failNext bool
}

func (f *flakyStore) Get(ctx context.Context, ns, key string) (string, bool, error) {
	if f.failNext {
		f.failNext = false
		return "", false, errors.New("store unavailable")
	}
	return f.Store.Get(ctx, ns, key)
}

// A nil domain is what a service that never wired one has. It must resolve to
// "" rather than panic, because "" is the answer the sweeps already treat as
// "prove nothing, remove nothing".
func TestInstanceDomain_nilResolvesToEmpty(t *testing.T) {
	var d *serviceutil.InstanceDomain
	if got := d.Resolve(context.Background()); got != "" {
		t.Fatalf("expected %q from a nil domain, got %q", "", got)
	}
}

// ── Matching a container to the record that owns it ──────────────────────────

// The label settles ownership wherever it is present, in both directions.
func TestContainerIsOurs_labelSettlesOwnership(t *testing.T) {
	ctx := context.Background()
	d := serviceutil.NewInstanceDomain(state.NewMemoryStore(), testNS)
	mine := d.Resolve(ctx)

	if !d.ContainerIsOurs(ctx, "c1", mine, "recorded-elsewhere") {
		t.Error("a container labelled with this instance's identity was not claimed")
	}
	// Even when the record names it: another Overcast's container is never
	// ours, and a record that names it is a record pointing at the wrong thing.
	if d.ContainerIsOurs(ctx, "c1", "another-overcast", "c1") {
		t.Error("a container labelled as another instance's was claimed")
	}
}

// Without a label the record's own note of the container ID decides, which is
// what keeps containers created before the label in service.
func TestContainerIsOurs_unlabelledFallsBackToTheRecord(t *testing.T) {
	ctx := context.Background()
	d := serviceutil.NewInstanceDomain(state.NewMemoryStore(), testNS)

	if !d.ContainerIsOurs(ctx, "c1", "", "c1") {
		t.Error("an unlabelled container the record names was not claimed")
	}
	if d.ContainerIsOurs(ctx, "c1", "", "c2") {
		t.Error("an unlabelled container the record does not name was claimed")
	}
	// A record tracking several containers — an ECS task — claims any of them.
	if !d.ContainerIsOurs(ctx, "c2", "", "c1", "c2", "c3") {
		t.Error("a container among those the record names was not claimed")
	}
	// A record tracking none yet claims nothing, rather than everything.
	if d.ContainerIsOurs(ctx, "c1", "") {
		t.Error("a record naming no container claimed one anyway")
	}
	// An empty container ID is not evidence of anything, even against a record
	// that has yet to write one down.
	if d.ContainerIsOurs(ctx, "", "", "") {
		t.Error("an empty container ID matched an empty recorded ID")
	}
}

// A nil domain resolves to "", which sends every match to the record. That is
// the same rule an unlabelled container gets, and deliberately not the sweep's
// "claim nothing": a service that never wired a domain must still manage the
// containers its own records name.
func TestContainerIsOurs_nilDomainStillTrustsTheRecord(t *testing.T) {
	var d *serviceutil.InstanceDomain
	if !d.ContainerIsOurs(context.Background(), "c1", "irrelevant", "c1") {
		t.Error("a nil domain refused a container its own record names")
	}
	if d.ContainerIsOurs(context.Background(), "c1", "irrelevant", "c2") {
		t.Error("a nil domain claimed a container no record names")
	}
}

// OwnContainer picks this instance's out of every container carrying one
// resource ID, whatever order the daemon listed them in.
func TestOwnContainer_picksOursFromACollision(t *testing.T) {
	ctx := context.Background()
	d := serviceutil.NewInstanceDomain(state.NewMemoryStore(), testNS)
	mine := d.Resolve(ctx)

	ours := &docker.ContainerSummary{ID: "ours", Labels: map[string]string{docker.LabelInstance: mine}}
	theirs := &docker.ContainerSummary{ID: "theirs", Labels: map[string]string{docker.LabelInstance: "another-overcast"}}

	// Listed last is the case the old single-entry index got wrong.
	if got := d.OwnContainer(ctx, []*docker.ContainerSummary{ours, theirs}, "ours"); got != ours {
		t.Errorf("a neighbour's container listed last shadowed ours: got %v", got)
	}
	if got := d.OwnContainer(ctx, []*docker.ContainerSummary{theirs, ours}, "ours"); got != ours {
		t.Errorf("a neighbour's container listed first shadowed ours: got %v", got)
	}
	// Only a neighbour's is no container of ours — not a fallback to it.
	if got := d.OwnContainer(ctx, []*docker.ContainerSummary{theirs}, "ours"); got != nil {
		t.Errorf("adopted a neighbour's container when ours was absent: got %v", got)
	}
	if got := d.OwnContainer(ctx, nil, "ours"); got != nil {
		t.Errorf("found a container in an empty listing: got %v", got)
	}
}
