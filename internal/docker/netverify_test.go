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
	"strings"
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
	bus        *events.Bus
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

	// Wired exactly as Supervisor.Run wires a watcher, including the bus: these
	// events go in through dispatch, which is where the label gate used to be
	// and where the name gate now is.
	w := &Watcher{tracker: tracker, logger: zap.NewNop(), bus: bus}
	w.verifyNetwork = func(ctx context.Context, name string) {
		sup.VerifyNetwork(ctx, fakeSocket, name)
	}
	w.hasNetworkSpec = func(name string) bool {
		sup.mu.Lock()
		defer sup.mu.Unlock()
		return sup.specFor(fakeSocket, name) != nil
	}
	return &verifyHarness{supervisor: sup, tracker: tracker, watcher: w, daemon: daemon, bus: bus}
}

// deliver feeds one network event through the watcher the way the event stream
// does — through dispatch, not into recordTrackerEvent underneath it. The
// event carries `name` and nothing else, which is every attribute the Engine
// sends for a network.
func (h *verifyHarness) deliver(action, name string) {
	h.watcher.dispatch(context.Background(), event("network", action, name))
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
	spec.Reason = "OVERCAST_CONTROL_PLANE_INTERNAL=false"
	h := newVerifyHarness(t, spec, asCreated(spec))

	// The probe rebuilt the network and recorded that it is fine.
	h.tracker.RecordDecisions([]NetworkDecision{spec.Decision()})
	h.tracker.RecordNetworks([]NetworkStatus{{
		Name: spec.Name, Internal: spec.Internal, SpecHash: spec.SpecHash(), Reason: spec.Reason,
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
	snap := h.tracker.Snapshot()
	if len(snap.Decisions) != 1 {
		t.Fatalf("decisions = %+v, want exactly one per network across the erase and the heal",
			snap.Decisions)
	}
	if snap.Decisions[0].Internal != spec.Internal || snap.Decisions[0].Reason == "" {
		t.Errorf("decision = %+v, want the isolation this run resolved and the reason it gave",
			snap.Decisions[0])
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
// there is no retry here at all, deliberately: see VerifyNetwork on what that
// costs, and why a doubled read on the event goroutine does not buy it back.
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

// ─── The label-free stream, end to end through dispatch ────────────────────

// The regression this whole change exists to fix, pinned where it can actually
// recur: `dispatch` must not gate a network event on a label, because a network
// event carries none. The event here has `name` and nothing else, which is
// every attribute the Engine sends — so anyone re-adding
// `if de.Actor.Attributes[LabelManaged] != "true" { return }` to the network
// branch fails this, rather than passing a suite that only ever called
// recordTrackerEvent underneath it.
func TestDispatch_actsOnANetworkEventThatCarriesNoLabels(t *testing.T) {
	spec := testSpec()
	h := newVerifyHarness(t, spec, asCreated(spec))
	h.tracker.RecordNetworks([]NetworkStatus{{Name: spec.Name, Drift: "drifted"}})

	h.watcher.dispatch(context.Background(), event("network", "destroy", spec.Name))

	if got := h.networks(); len(got) != 0 {
		t.Fatalf("networks = %+v, want the destroyed network forgotten", got)
	}
	if last := h.tracker.Snapshot().LastEvent; last != "network:destroy" {
		t.Errorf("lastEvent = %q, want the network event recorded", last)
	}
}

// The other half of the gate. Nothing labels a network event, so a name this
// process manages nothing about is the only thing separating Overcast's own
// networks from every compose project, devcontainer and CI job on the host —
// and `dispatch` publishes, which is what puts an event on the rolling history
// that GET /_overcast/events replays whether or not anything is subscribed.
//
// Two consequences if this gate is missing, both user-visible: unattributed
// `network:connect` rows on the Events page (a network event carries no
// service or resource id either), pushing real events out of the replay
// window, and `docker.lastEvent` in health reporting somebody else's project.
func TestDispatch_dropsANetworkEventForAnameItManagesNothingAbout(t *testing.T) {
	spec := testSpec()
	h := newVerifyHarness(t, spec, asCreated(spec))

	for _, action := range []string{"create", "connect", "disconnect", "destroy"} {
		h.watcher.dispatch(context.Background(), event("network", action, "someone-elses-compose_default"))
	}

	if last := h.tracker.Snapshot().LastEvent; last != "" {
		t.Errorf("lastEvent = %q, want nothing: none of those networks are Overcast's", last)
	}
	published, cancel := h.bus.SnapshotAndSubscribeAll(func(context.Context, events.Event) {})
	cancel()
	if len(published) != 0 {
		t.Errorf("bus history = %+v, want nothing published for a foreign network", published)
	}
}

// A per-VPC network has no spec here — EC2 resolves those from its own store —
// but it does have a record, and its destroy still has to be acted on: that is
// half of what #1583 was for. The record is the gate.
func TestDispatch_actsOnAVPCNetworkItHasARecordFor(t *testing.T) {
	spec := testSpec()
	h := newVerifyHarness(t, spec, asCreated(spec))
	h.tracker.RecordNetworks([]NetworkStatus{{Name: "ocv-vpc-vpc-1", Internal: true}})

	h.watcher.dispatch(context.Background(), event("network", "destroy", "ocv-vpc-vpc-1"))

	if got := h.networks(); len(got) != 0 {
		t.Fatalf("networks = %+v, want the VPC network forgotten on its destroy", got)
	}
}

// ─── The decision is not an observation ────────────────────────────────────

// A create event for a drifted control plane must not move the decision.
//
// NetworkStatus.Internal is what the network *is* once a mismatch is found —
// deliberately, because reporting the ask beside a drift is #1564's original
// confusion — and Status.Decisions answers a different question: what did this
// configuration resolve, and why. Letting the observation become the decision
// flips `Internal` to true while the reason still says the host would not take
// an isolated plane, and `controlPlaneRoutable` then reads false and switches
// the vpc-egress-not-withheld advisory off. #1595 spent a whole field closing
// that same hole one route over; an event must not reopen it.
func TestVerifyOnCreate_doesNotLetADriftOverwriteTheDecision(t *testing.T) {
	spec := testSpec() // Internal false — the control plane left routable
	spec.Reason = "OVERCAST_VPC_EGRESS=none, overridden: an internal control plane would sever the Runtime API on this host"

	// Drifted the wrong way round: the live network *is* internal, which is
	// exactly the shape that would flip the advisory off if it were believed.
	h := newVerifyHarness(t, spec, drifted(spec))
	h.supervisor.tracker.RecordDecisions([]NetworkDecision{spec.Decision()})

	h.deliver("create", spec.Name)

	snap := h.tracker.Snapshot()
	if len(snap.Decisions) != 1 {
		t.Fatalf("decisions = %+v, want exactly one", snap.Decisions)
	}
	if snap.Decisions[0].Internal {
		t.Error("the decision took the drifted network's isolation; the advisory that reads it is now off " +
			"while the shortfall it reports is unchanged")
	}
	if !strings.Contains(snap.Decisions[0].Reason, "overridden: ") {
		t.Errorf("reason = %q, want the resolved reason kept", snap.Decisions[0].Reason)
	}

	// The observation is still reported — on the network entry, which is where
	// an operator looks for what the network is.
	got := h.networks()
	if len(got) != 1 || !got[0].Internal || got[0].OK() {
		t.Errorf("network = %+v, want the live isolation and the drift reported there", got)
	}
}

// The same rule on the probe path, which is where it was already being broken
// before any of this: a drifted network's status must not become the decision,
// and a network that never had one records none — absence fires no rule, which
// is the right answer for a network whose state nobody can account for.
func TestRecordNetworks_aDriftedStatusIsNotADecision(t *testing.T) {
	tr := NewTracker()
	tr.RecordDecisions([]NetworkDecision{{
		Network: "overcast_control", Internal: false, Reason: "OVERCAST_VPC_EGRESS=none, overridden: host",
	}})

	tr.RecordNetworks([]NetworkStatus{{
		Name: "overcast_control", Internal: true, Reason: "OVERCAST_VPC_EGRESS=none, overridden: host",
		Drift: "network is not in the configured state (internal: want false, got true)",
	}})
	if d := tr.Snapshot().Decisions; len(d) != 1 || d[0].Internal {
		t.Errorf("decisions = %+v, want the resolved answer, not the drifted network's", d)
	}

	tr.RecordNetworks([]NetworkStatus{{Name: "never-decided", Internal: true, Drift: "unverified"}})
	for _, d := range tr.Snapshot().Decisions {
		if d.Network == "never-decided" {
			t.Errorf("decisions = %+v, want no decision invented from an observation", d)
		}
	}

	// A clean status still records one: it is an observation that agrees with
	// the spec by definition, and EC2's per-VPC networks have no other route in.
	tr.RecordNetworks([]NetworkStatus{{Name: "overcast-vpc-vpc-1", Internal: true, Reason: "no gateway"}})
	found := false
	for _, d := range tr.Snapshot().Decisions {
		if d.Network == "overcast-vpc-vpc-1" && d.Internal {
			found = true
		}
	}
	if !found {
		t.Error("a clean status recorded no decision; EC2's per-VPC networks have no other way to report one")
	}
}
