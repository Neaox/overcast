package docker

// netspec_unreadable_test.go — what EnsureNetwork does when it cannot read the
// network it is about to vouch for (#1582).
//
// The bug these cover is not that a network went wrong. It is that Overcast
// said one was right without looking. `InspectNetwork` failing for any reason
// other than "no such network" used to fall straight into the create, and
// Docker's create call returns an existing network *unchanged* — so a drifted
// network stayed drifted and `/_overcast/health` reported it clean, with no
// mismatch, no advisory, and the isolation this run *asked* for rather than the
// one the network has. That is #1564's own failure mode, on the error path of
// the code written to close it.

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// drifted is the seeded network every test here starts from: right name, wrong
// isolation, and no spec-hash label — a plane created by an older Overcast.
func drifted(spec ResolvedNetworkSpec) *NetworkInspect {
	info := asCreated(spec)
	info.Internal = !spec.Internal
	delete(info.Labels, LabelSpecHash)
	return info
}

// A single hiccup is not a fact about the network. One retry settles it, and
// the create path is never entered — no create call, and the ordinary
// comparison runs against the network that was really there.
func TestEnsureNetwork_retriesAnInspectThatFailedForSomeOtherReason(t *testing.T) {
	spec := testSpec()
	dc, d := newSpecDaemon(t, asCreated(spec))
	d.failInspects = 1

	status, err := EnsureNetwork(context.Background(), dc, spec, zap.NewNop())
	if err != nil {
		t.Fatalf("EnsureNetwork() = %v, want the retry to settle it", err)
	}
	if !status.OK() {
		t.Errorf("status = %+v, want OK: the network matched once it could be read", status)
	}
	if got := d.createCount(spec.Name); got != 0 {
		t.Errorf("created %d times; a transient inspect failure must not reach the create path", got)
	}
	if got := d.inspectCount(); got < 2 {
		t.Errorf("inspected %d times, want at least 2 — the failure was never retried", got)
	}
}

// The bug itself. The network is drifted *and* has a container on it, so no
// repair is possible and the only correct outcome is to say so. Before #1582
// this returned a clean status: the create resolved the name conflict by
// handing back the drifted network, nothing was compared, and health vouched
// for a state nobody had checked.
func TestEnsureNetwork_doesNotVouchForANetworkItCouldNotRead(t *testing.T) {
	spec := testSpec()
	seed := drifted(spec)
	seed.Containers = map[string]NetworkEndpoint{
		"c1": {Name: "someone-elses-container"},
	}
	dc, d := newSpecDaemon(t, seed)
	// Both the first read and its retry fail; the network is readable again by
	// the time the blind create has happened.
	d.failInspects = 2

	status, err := EnsureNetwork(context.Background(), dc, spec, zap.NewNop())
	if err != nil {
		t.Fatalf("EnsureNetwork() = %v, want a reported problem rather than a refusal to start", err)
	}
	if status.OK() {
		t.Fatalf("status = %+v, want NOT OK — the network is drifted and was never compared", status)
	}
	if len(status.Mismatch) == 0 {
		t.Errorf("status.Mismatch is empty; the re-read after the blind create must run the comparison")
	}
	if status.Internal != seed.Internal {
		t.Errorf("status.Internal = %v, want the live %v — reporting the ask is the #1564 lie",
			status.Internal, seed.Internal)
	}
	if len(status.Attached) == 0 {
		t.Errorf("status.Attached is empty; the container is why this could not be repaired")
	}
	if status.Fix == "" {
		t.Errorf("status.Fix is empty; a reported drift with no way out is half a report")
	}
	if got := d.removeCount(spec.Name); got != 0 {
		t.Errorf("removed %d times; a network with a container on it is never rebuilt underneath it", got)
	}
}

// A drifted network with nothing attached is repaired, exactly as it would have
// been had the first inspect worked. The unreadable path must not cost the
// repair — only the trust.
func TestEnsureNetwork_repairsAfterAnUnreadableInspect(t *testing.T) {
	spec := testSpec()
	dc, d := newSpecDaemon(t, drifted(spec))
	d.failInspects = 2

	status, err := EnsureNetwork(context.Background(), dc, spec, zap.NewNop())
	if err != nil {
		t.Fatalf("EnsureNetwork() = %v", err)
	}
	if !status.OK() {
		t.Errorf("status = %+v, want OK after the repair", status)
	}
	if got := d.removeCount(spec.Name); got != 1 {
		t.Errorf("removed %d times, want 1 — the drifted network was never rebuilt", got)
	}
	if live := d.network(spec.Name); live == nil || live.Internal != spec.Internal {
		t.Errorf("network = %+v, want internal=%v", live, spec.Internal)
	}
}

// When the daemon stops answering reads altogether, there is nothing to compare
// and nothing to claim. The status says so in words, health degrades on it, and
// the fix is the command that will do the comparison on demand.
//
// The create still happens — a daemon with no network at all fails every
// container create with an error naming nothing about networks — but its
// success is not evidence: Docker resolves a name conflict by handing back a
// network it changed nothing about, and this call cannot tell that apart from a
// network it made. So it reports what it knows, which is that it does not know.
func TestEnsureNetwork_reportsANetworkItNeverManagedToRead(t *testing.T) {
	spec := testSpec()
	dc, d := newSpecDaemon(t)
	d.failInspects = 99 // every read, including the one after the create

	status, err := EnsureNetwork(context.Background(), dc, spec, zap.NewNop())
	if err != nil {
		t.Fatalf("EnsureNetwork() = %v, want a degraded report rather than a refusal to start", err)
	}
	if status.OK() {
		t.Fatalf("status = %+v, want NOT OK — nothing about this network was ever established", status)
	}
	if !strings.Contains(status.Drift, "could not be verified") {
		t.Errorf("status.Drift = %q, want it to say the network was not verified", status.Drift)
	}
	if len(status.Mismatch) != 0 {
		t.Errorf("status.Mismatch = %v, want empty — no comparison happened, so there is no field to name",
			status.Mismatch)
	}
	if status.Fix != "overcast network status" {
		t.Errorf("status.Fix = %q, want the command that compares it once the daemon answers", status.Fix)
	}
}

// A network that is genuinely absent is still created and still reported clean.
// The 404 is a fact, and treating it like a failed read would make every first
// run report a problem it does not have.
//
// It also pins that Diff is round-trip stable over a network this code just
// created: the read-back below runs on every create now, so if the spec did not
// compare equal to what it produces, every boot would report drift on a network
// nothing had touched.
func TestEnsureNetwork_aMissingNetworkIsStillJustCreated(t *testing.T) {
	spec := testSpec()
	dc, d := newSpecDaemon(t)

	status, err := EnsureNetwork(context.Background(), dc, spec, zap.NewNop())
	if err != nil {
		t.Fatalf("EnsureNetwork() = %v", err)
	}
	if !status.OK() {
		t.Errorf("status = %+v, want OK: it was created to spec", status)
	}
	if got := d.createCount(spec.Name); got != 1 {
		t.Errorf("created %d times, want 1", got)
	}
	// Two: the 404, and the read-back that verifies the create. Three would
	// mean the 404 had been retried, which is the thing this pins — it is an
	// answer, not a moment, and retrying it only delays the create.
	if got := d.inspectCount(); got != 2 {
		t.Errorf("inspected %d times, want 2 — a 404 is the answer and must not be retried", got)
	}
}

// The other half of the same hole (#1595 review, finding 4). A 404 is a fact
// about the moment it was asked, not about the moment the create landed —
// another process can create the network in between, and Docker resolves the
// name conflict by handing back *their* network unchanged. Returning on the
// strength of a create that succeeded reports it as freshly built to this spec.
//
// The container is what makes the assertion sharp: it means no repair is
// possible, so a clean status could only come from not having looked.
func TestEnsureNetwork_verifiesACreateThatRacedAnotherProcess(t *testing.T) {
	spec := testSpec()
	// Their network: same name, wrong isolation, no spec label.
	theirs := drifted(spec)
	theirs.Containers = map[string]NetworkEndpoint{"c1": {Name: "their-container"}}
	dc, d := newSpecDaemon(t, theirs)
	// It is not there when we look, and is by the time we create.
	d.hideNextInspect = 1

	status, err := EnsureNetwork(context.Background(), dc, spec, zap.NewNop())
	if err != nil {
		t.Fatalf("EnsureNetwork() = %v, want a reported problem rather than a refusal to start", err)
	}
	if status.OK() {
		t.Fatalf("status = %+v, want NOT OK — the create handed back a drifted network nobody compared", status)
	}
	if len(status.Mismatch) == 0 {
		t.Errorf("status.Mismatch is empty; the create was returned on rather than verified")
	}
	if status.Internal != theirs.Internal {
		t.Errorf("status.Internal = %v, want the live %v", status.Internal, theirs.Internal)
	}
	if len(status.Attached) == 0 {
		t.Errorf("status.Attached is empty; their container is why this could not be repaired")
	}
	if got := d.removeCount(spec.Name); got != 0 {
		t.Errorf("removed %d times; a network with a container on it is never rebuilt underneath it", got)
	}
}
