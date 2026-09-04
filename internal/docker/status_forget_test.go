package docker

// status_forget_test.go — a network the report must stop talking about (#1583).
//
// A recorded NetworkStatus is a statement about a network that exists. Once it
// does not, keeping the last thing Overcast knew turns the report into a
// memory — and a memory here is worse than silence, because the advisory built
// on it keeps telling an operator to run a command they have already run.

import (
	"context"
	"testing"
	"time"
)

// event builds one Docker event. dockerEvent.Actor is an anonymous struct, so
// it cannot be filled in a composite literal from here.
func event(kind, action, name string) *dockerEvent {
	de := &dockerEvent{Type: kind, Action: action, Time: time.Now().Unix()}
	de.Actor.ID = "actor-1"
	de.Actor.Attributes = map[string]string{"name": name}
	return de
}

func TestForgetNetwork_dropsTheEntry(t *testing.T) {
	tr := NewTracker()
	tr.RecordNetworks([]NetworkStatus{
		{Name: "overcast", Internal: false},
		{Name: "overcast-vpc-vpc-1", Internal: true, Drift: "not in the configured state"},
	})

	tr.ForgetNetwork("overcast-vpc-vpc-1")

	got := tr.Snapshot().Networks
	if len(got) != 1 || got[0].Name != "overcast" {
		t.Fatalf("networks = %+v, want only overcast to remain", got)
	}
}

// Order is first-seen and has to survive a removal from the middle: it is
// PlaneSpecs' order, which is what makes the health payload readable.
func TestForgetNetwork_keepsTheOrderOfWhatIsLeft(t *testing.T) {
	tr := NewTracker()
	tr.RecordNetworks([]NetworkStatus{
		{Name: "a"}, {Name: "b"}, {Name: "c"},
	})

	tr.ForgetNetwork("b")

	got := tr.Snapshot().Networks
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "c" {
		t.Fatalf("networks = %+v, want [a c]", got)
	}
}

func TestForgetNetwork_toleratesANameItNeverHeldAndAnEmptyOne(t *testing.T) {
	tr := NewTracker()
	tr.RecordNetworks([]NetworkStatus{{Name: "overcast"}})

	tr.ForgetNetwork("never-recorded")
	tr.ForgetNetwork("")

	if got := tr.Snapshot().Networks; len(got) != 1 {
		t.Fatalf("networks = %+v, want the one recorded network untouched", got)
	}
}

// The path that closes #1583(a): `overcast network reset` runs in another
// process and removes the network before rebuilding it. This daemon sees the
// destroy and stops reporting the drift the reset was run to fix. Without it,
// the operator does exactly what the advisory told them to do, watches it
// succeed, and nothing they can see changes.
func TestWatcher_forgetsANetworkOnDestroy(t *testing.T) {
	tr := NewTracker()
	tr.RecordNetworks([]NetworkStatus{
		{Name: "overcast", Drift: "network is not in the configured state (internal: want false, got true)"},
	})
	w := &Watcher{tracker: tr}

	w.recordTrackerEvent(context.Background(), event("network", "destroy", "overcast"))

	if got := tr.Snapshot().Networks; len(got) != 0 {
		t.Fatalf("networks = %+v, want the destroyed network forgotten", got)
	}
}

// Connect and disconnect say nothing about whether a network matches its spec,
// and a watcher with no verifier — one built by NewWatcher, without a
// Supervisor to hold the resolved specs — has nothing to check a create
// against. None of the three may disturb what the probe recorded.
func TestWatcher_keepsTheRecordOnEveryOtherNetworkEvent(t *testing.T) {
	for _, action := range []string{"create", "connect", "disconnect"} {
		t.Run(action, func(t *testing.T) {
			tr := NewTracker()
			tr.RecordNetworks([]NetworkStatus{{Name: "overcast", Drift: "drifted"}})
			w := &Watcher{tracker: tr}

			w.recordTrackerEvent(context.Background(), event("network", action, "overcast"))

			if got := tr.Snapshot().Networks; len(got) != 1 {
				t.Fatalf("networks = %+v, want the record kept on a %q event", got, action)
			}
		})
	}
}

// A container dying on a network is not the network going away.
func TestWatcher_keepsTheRecordOnAContainerEvent(t *testing.T) {
	tr := NewTracker()
	tr.RecordNetworks([]NetworkStatus{{Name: "overcast", Drift: "drifted"}})
	w := &Watcher{tracker: tr}

	w.recordTrackerEvent(context.Background(), event("container", "destroy", "overcast"))

	if got := tr.Snapshot().Networks; len(got) != 1 {
		t.Fatalf("networks = %+v, want the record kept", got)
	}
}

// The record goes; the decision stays. A rule about what this configuration
// asked for must not be switched off by somebody rebuilding a network — which
// is exactly what `overcast network reset` does, and what the forget above
// exists to notice.
func TestForgetNetwork_keepsTheResolvedDecision(t *testing.T) {
	tr := NewTracker()
	tr.RecordNetworks([]NetworkStatus{{
		Name:     "overcast_control",
		Internal: false,
		Reason:   "OVERCAST_VPC_EGRESS=none, overridden: an internal control plane would sever the Runtime API on this host",
	}})

	tr.ForgetNetwork("overcast_control")

	snap := tr.Snapshot()
	if len(snap.Networks) != 0 {
		t.Fatalf("networks = %+v, want the forgotten network gone", snap.Networks)
	}
	if len(snap.Decisions) != 1 {
		t.Fatalf("decisions = %+v, want the decision kept", snap.Decisions)
	}
	got := snap.Decisions[0]
	if got.Network != "overcast_control" || got.Internal {
		t.Errorf("decision = %+v, want the control plane recorded as routable", got)
	}
	if got.Reason == "" {
		t.Error("decision lost its reason; the advisory needs it to tell a shortfall from an override")
	}
}

// A destroy arriving after the probe recorded the network — the ordering the
// startup race can produce — costs the observed entry and nothing else. The
// decision the advisories read is unaffected either way round, which is what
// keeps that race off the critical path.
func TestForgetNetwork_aLateDestroyDoesNotUndoTheProbeDecision(t *testing.T) {
	tr := NewTracker()
	w := &Watcher{tracker: tr}

	// Probe records; the watcher's destroy for the same rebuild lands after it.
	tr.RecordNetworks([]NetworkStatus{{Name: "overcast", Internal: true, Reason: "OVERCAST_VPC_EGRESS=none"}})
	w.recordTrackerEvent(context.Background(), event("network", "destroy", "overcast"))

	snap := tr.Snapshot()
	if len(snap.Networks) != 0 {
		t.Fatalf("networks = %+v, want the entry dropped by the late destroy", snap.Networks)
	}
	if len(snap.Decisions) != 1 || !snap.Decisions[0].Internal {
		t.Fatalf("decisions = %+v, want the probe's decision intact", snap.Decisions)
	}
}

// Re-recording after a forget puts the network back and updates the decision in
// place rather than appending a second one.
func TestRecordNetworks_updatesTheDecisionInPlace(t *testing.T) {
	tr := NewTracker()
	tr.RecordNetworks([]NetworkStatus{{Name: "overcast_control", Internal: true, Reason: "first"}})
	tr.ForgetNetwork("overcast_control")
	tr.RecordNetworks([]NetworkStatus{{Name: "overcast_control", Internal: false, Reason: "second"}})

	snap := tr.Snapshot()
	if len(snap.Decisions) != 1 {
		t.Fatalf("decisions = %+v, want one entry per network", snap.Decisions)
	}
	if snap.Decisions[0].Reason != "second" || snap.Decisions[0].Internal {
		t.Errorf("decision = %+v, want the later probe's answer", snap.Decisions[0])
	}
}
