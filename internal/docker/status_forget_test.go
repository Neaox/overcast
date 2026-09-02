package docker

// status_forget_test.go — a network the report must stop talking about (#1583).
//
// A recorded NetworkStatus is a statement about a network that exists. Once it
// does not, keeping the last thing Overcast knew turns the report into a
// memory — and a memory here is worse than silence, because the advisory built
// on it keeps telling an operator to run a command they have already run.

import (
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

	w.recordTrackerEvent(event("network", "destroy", "overcast"))

	if got := tr.Snapshot().Networks; len(got) != 0 {
		t.Fatalf("networks = %+v, want the destroyed network forgotten", got)
	}
}

// Only the destroy. A create says a network exists, not that it matches — this
// goroutine has no spec to compare against — and acting on one would race the
// command that is mid-rebuild. Connect and disconnect say nothing about state
// at all.
func TestWatcher_keepsTheRecordOnEveryOtherNetworkEvent(t *testing.T) {
	for _, action := range []string{"create", "connect", "disconnect"} {
		t.Run(action, func(t *testing.T) {
			tr := NewTracker()
			tr.RecordNetworks([]NetworkStatus{{Name: "overcast", Drift: "drifted"}})
			w := &Watcher{tracker: tr}

			w.recordTrackerEvent(event("network", action, "overcast"))

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

	w.recordTrackerEvent(event("container", "destroy", "overcast"))

	if got := tr.Snapshot().Networks; len(got) != 1 {
		t.Fatalf("networks = %+v, want the record kept", got)
	}
}
