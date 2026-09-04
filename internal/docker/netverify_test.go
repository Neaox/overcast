package docker

// netverify_test.go — the re-verification a managed network gets on its Docker
// `create` event, and the window it closes.
//
// Two issues meet here, and the second is why the first is worth having:
//
//   - #1599: `overcast network reset` repairs a drifted network, the daemon
//     sees the destroy and forgets it, and health then reports *nothing* about
//     the network the operator was warned about. Absence means "nothing to say"
//     everywhere in this code, so the answer they most want — it is fine now —
//     is the one answer the endpoint cannot give.
//   - #1601: a destroy delivered after the probe recorded the rebuilt network
//     erases the entry the probe just wrote. The create behind it, on the same
//     stream and handled on the same goroutine, puts one back.
//
// The verification is read-only, and half of these tests exist to pin that: a
// repair on an event would land between `overcast network reset`'s remove and
// its create, and the CLI's confirmed-removal check would then fail a working
// command with "still present after removal".

import (
	"context"
	"testing"

	"github.com/overcast-sh/overcast/internal/events"
	"go.uber.org/zap"
)

// fakeSocket is the socket path the fake daemon is registered under. The
// Supervisor keys clients and specs by it, and the watcher's callback binds it,
// so its only requirement is that both halves agree.
const fakeSocket = "/var/run/docker-test.sock"

// verifyHarness is a Supervisor holding one daemon's client and one resolved
// spec — Probe's output, without a Probe — plus the Watcher wired to it exactly
// as Supervisor.Run wires one.
type verifyHarness struct {
	supervisor *Supervisor
	tracker    *Tracker
	watcher    *Watcher
	daemon     *specDaemon
}

func newVerifyHarness(t *testing.T, spec ResolvedNetworkSpec, seed ...*NetworkInspect) *verifyHarness {
	t.Helper()
	dc, daemon := newSpecDaemon(t, seed...)

	bus := events.NewBus()
	t.Cleanup(bus.Stop)
	tracker := NewTracker()
	sup := NewSupervisorWithTracker(bus, zap.NewNop(), tracker)
	sup.clients[fakeSocket] = dc
	sup.specs[fakeSocket] = []ResolvedNetworkSpec{spec}

	w := &Watcher{tracker: tracker, logger: zap.NewNop()}
	w.verifyNetwork = func(ctx context.Context, name string) {
		sup.VerifyNetwork(ctx, fakeSocket, name)
	}
	return &verifyHarness{supervisor: sup, tracker: tracker, watcher: w, daemon: daemon}
}

// deliver feeds one network event through the watcher, as the event stream
// would.
func (h *verifyHarness) deliver(action, name string) {
	h.watcher.recordTrackerEvent(context.Background(), event("network", action, name))
}

// networks returns what health would report.
func (h *verifyHarness) networks() []NetworkStatus { return h.tracker.Snapshot().Networks }

// replace models another process rebuilding a network — `overcast network
// reset` runs outside this daemon — by changing what the fake holds without
// going through the client under test. The fake's created and removed ledgers
// then record only what the code under test did, which is what makes the
// "never repairs" assertions mean anything. A nil info removes it.
func (d *specDaemon) replace(name string, info *NetworkInspect) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if info == nil {
		delete(d.networks, name)
		return
	}
	d.networks[name] = info
}

// The seeded drift these tests start from is netspec_unreadable_test.go's
// `drifted`: this instance's network, nothing attached, wrong isolation and no
// spec-hash label — a plane an older Overcast created.

// ─── #1599: a repaired network reports ok rather than nothing ───────────────

// The sequence the issue is about, end to end. The operator is told a network
// is not in its configured state, runs the command the advisory names, and it
// works. Before this, the destroy dropped the entry and the create put nothing
// back, so health went from a mismatch to silence — and silence is what this
// code means by "no Docker at all".
func TestVerifyOnCreate_aRepairedNetworkReturnsToOK(t *testing.T) {
	spec := testSpec()

	// Given: a drifted network the startup probe could not repair, because
	// containers are on it — the population `overcast network reset` exists
	// for, and the one the advisory names.
	held := drifted(spec)
	held.Containers = map[string]NetworkEndpoint{"c1": {Name: "some-container"}}
	h := newVerifyHarness(t, spec, held)

	status, err := EnsureNetwork(context.Background(), h.supervisor.clients[fakeSocket], spec, zap.NewNop())
	if err != nil {
		t.Fatalf("EnsureNetwork: %v", err)
	}
	if status.OK() {
		t.Fatalf("status = %+v, want the drift the operator is about to reset", status)
	}
	h.tracker.RecordNetworks([]NetworkStatus{status})

	// When: `overcast network reset` removes it and rebuilds it to spec, in
	// another process, and both events reach this daemon in order.
	h.daemon.replace(spec.Name, nil)
	h.deliver("destroy", spec.Name)
	h.daemon.replace(spec.Name, asCreated(spec))
	h.deliver("create", spec.Name)

	// Then: health says the network is fine, rather than saying nothing.
	got := h.networks()
	if len(got) != 1 {
		t.Fatalf("networks = %+v, want the repaired network reported", got)
	}
	if !got[0].OK() {
		t.Fatalf("network = %+v, want OK after the rebuild", got[0])
	}
	if got[0].Internal != spec.Internal || got[0].SpecHash != spec.SpecHash() {
		t.Errorf("network = %+v, want the spec's isolation and hash", got[0])
	}
}

// The create is verified, not trusted. A network rebuilt by hand into a state
// nothing asked for must be reported as drifted, with the live isolation rather
// than the one this run asked for — reporting the ask beside a drift is the
// original #1564 confusion.
func TestVerifyOnCreate_reportsADriftItFindsAndRepairsNothing(t *testing.T) {
	spec := testSpec()
	h := newVerifyHarness(t, spec, drifted(spec))

	h.deliver("create", spec.Name)

	got := h.networks()
	if len(got) != 1 {
		t.Fatalf("networks = %+v, want the created network reported", got)
	}
	if got[0].OK() {
		t.Fatalf("network = %+v, want the drift reported", got[0])
	}
	if got[0].Internal != !spec.Internal {
		t.Errorf("internal = %v, want the live value, not the one this run asked for", got[0].Internal)
	}
	if got[0].Fix != "overcast network reset "+spec.Name {
		t.Errorf("fix = %q, want the command that rebuilds it", got[0].Fix)
	}

	// And nothing was done about it. This is the constraint the whole design
	// turns on: the drifted network here has nothing attached, so EnsureNetwork
	// would rebuild it on sight. From an event it must not.
	if n := h.daemon.removeCount(spec.Name); n != 0 {
		t.Errorf("removes = %d, want 0: verification on an event never repairs", n)
	}
	if n := h.daemon.createCount(spec.Name); n != 0 {
		t.Errorf("creates = %d, want 0: verification on an event never creates", n)
	}
}

// The reason it must not repair, as its own test. Between `overcast network
// reset`'s remove and its create the network is absent; a create issued from
// here would win that race, and the CLI's own confirmed-removal check would
// then abort a command that was working with "still present after removal".
func TestVerifyOnCreate_createsNothingDuringAReset(t *testing.T) {
	spec := testSpec()
	h := newVerifyHarness(t, spec, drifted(spec))
	h.tracker.RecordNetworks([]NetworkStatus{{Name: spec.Name, Drift: "drifted"}})

	// The reset has removed it and has not created it back yet.
	h.daemon.replace(spec.Name, nil)
	h.deliver("destroy", spec.Name)
	// A create event for it arrives while the name is still free — a stale
	// event, or the CLI's create seen before its network is readable.
	h.deliver("create", spec.Name)

	if n := h.daemon.createCount(spec.Name); n != 0 {
		t.Fatalf("creates = %d, want 0 — a create from here fails the reset that is mid-flight", n)
	}
	if h.daemon.network(spec.Name) != nil {
		t.Fatal("the network is back; the reset's removal check would now abort")
	}
	if got := h.networks(); len(got) != 0 {
		t.Errorf("networks = %+v, want nothing recorded for a network that cannot be read", got)
	}
}

// Only networks this process resolved a spec for. A per-VPC network's spec
// lives in EC2's store, not in PlaneSpecs, and EC2 reports those through the
// same tracker on its own schedule; a network under a name nothing here manages
// is not ours to describe at all.
func TestVerifyOnCreate_ignoresANetworkItHasNoSpecFor(t *testing.T) {
	spec := testSpec()
	h := newVerifyHarness(t, spec, asCreated(spec))
	h.daemon.replace("overcast-vpc-vpc-1", &NetworkInspect{
		ID: "net-vpc", Name: "overcast-vpc-vpc-1", Driver: DefaultNetworkDriver, Scope: "local",
	})

	h.deliver("create", "overcast-vpc-vpc-1")

	if got := h.networks(); len(got) != 0 {
		t.Fatalf("networks = %+v, want nothing recorded for a network with no spec here", got)
	}
}

// ─── #1601: the late destroy heals itself ──────────────────────────────────

// The window the issue describes: EnsureNetwork rebuilds a drifted network, the
// probe records the result, and the `destroy` from that rebuild is delivered
// *after* the record and erases it.
//
// It heals rather than being prevented. The two events are one stream and are
// handled on one goroutine in order, so the create that follows the destroy
// re-reads the network and records it again — which costs one inspect and no
// change to what a NetworkReporter has to write, where stamping a generation on
// every record would widen an interface two packages implement and consume.
func TestVerifyOnCreate_healsAnEraseByALateDestroy(t *testing.T) {
	spec := testSpec()
	h := newVerifyHarness(t, spec, asCreated(spec))

	// The probe rebuilt the network and recorded that it is fine.
	h.tracker.RecordNetworks([]NetworkStatus{{
		Name: spec.Name, Internal: spec.Internal, SpecHash: spec.SpecHash(),
		Reason: "OVERCAST_CONTROL_PLANE_INTERNAL=false",
	}})

	// The rebuild's own destroy arrives late and takes the entry with it.
	h.deliver("destroy", spec.Name)
	if got := h.networks(); len(got) != 0 {
		t.Fatalf("networks = %+v, want the entry erased by the late destroy", got)
	}

	// The create behind it puts back a network that was read, not remembered.
	h.deliver("create", spec.Name)

	got := h.networks()
	if len(got) != 1 || !got[0].OK() {
		t.Fatalf("networks = %+v, want the erased entry restored as OK", got)
	}
	if snap := h.tracker.Snapshot(); len(snap.Decisions) != 1 {
		t.Errorf("decisions = %+v, want exactly one per network across the erase and the heal",
			snap.Decisions)
	}
}

// ─── VerifyNetwork on its own ──────────────────────────────────────────────

// A network that cannot be read is not a network that is wrong. EnsureNetwork
// reports an unreadable network as drifted, because it is about to hand the
// daemon to services that will use it; this runs opportunistically off an
// event, and degrading health from a read that failed would make it worse than
// the silence it replaces.
func TestVerifyNetwork_recordsNothingWhenTheNetworkCannotBeRead(t *testing.T) {
	spec := testSpec()
	dc, daemon := newSpecDaemon(t, asCreated(spec))

	daemon.failInspects = 1
	if _, ok := VerifyNetwork(context.Background(), dc, spec, zap.NewNop()); ok {
		t.Error("VerifyNetwork reported a status for a network it could not read")
	}

	daemon.replace(spec.Name, nil)
	if _, ok := VerifyNetwork(context.Background(), dc, spec, zap.NewNop()); ok {
		t.Error("VerifyNetwork reported a status for a network that is gone")
	}
	if n := daemon.createCount(spec.Name); n != 0 {
		t.Errorf("creates = %d, want 0: a missing network is not this function's to make", n)
	}
}

// One inspect, and it is not retried. The retry in inspectForVerify buys the
// startup path a settled answer before it hands the daemon to services; here
// the next event or the next probe is the retry, and a doubled read on the
// event goroutine buys nothing.
func TestVerifyNetwork_readsTheNetworkExactlyOnce(t *testing.T) {
	spec := testSpec()
	dc, daemon := newSpecDaemon(t, asCreated(spec))

	if _, ok := VerifyNetwork(context.Background(), dc, spec, zap.NewNop()); !ok {
		t.Fatal("VerifyNetwork reported nothing for a network in its configured state")
	}
	if n := daemon.inspectCount(); n != 1 {
		t.Errorf("inspects = %d, want exactly 1", n)
	}
}

// A network belonging to another Overcast instance is reported as theirs and
// left alone here exactly as it is on the startup path — the comparison is the
// same function, and this pins that the split did not lose the ownership check.
func TestVerifyNetwork_keepsTheOwnershipCheck(t *testing.T) {
	spec := testSpec()
	theirs := asCreated(spec)
	theirs.Internal = !spec.Internal
	theirs.Labels[LabelInstance] = "instance-b"

	dc, daemon := newSpecDaemon(t, theirs)
	status, ok := VerifyNetwork(context.Background(), dc, spec, zap.NewNop())
	if !ok {
		t.Fatal("VerifyNetwork reported nothing for a readable network")
	}
	if status.Owner != "instance-b" {
		t.Errorf("owner = %q, want the instance that owns it", status.Owner)
	}
	if n := daemon.removeCount(spec.Name); n != 0 {
		t.Errorf("removes = %d, want 0", n)
	}
}
