package dynamodb

import (
	"context"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/internal/state"
)

// The settle callback for a TTL transition must only ever settle the
// transition it was armed for. The window closes at the very instant a new
// UpdateTimeToLive becomes acceptable, so the old transition's settle and the
// new update can run at the same moment; before #1868 a settle that had read
// the table just before the update wrote the old specification back over it,
// deadline cleared, and the table a caller had just disabled read ENABLED.
//
// Two guards close that, and each has a test here. The deadline guard is
// deterministic: a settle armed for one deadline finds the table re-armed for
// another and leaves it alone. The lock is exercised by holding it while the
// scheduled settle comes due, which pins that the settle's read-modify-write
// and UpdateTimeToLive's cannot interleave.

func TestSettleTTLTransition_ignoresARecordReArmedForAnotherTransition(t *testing.T) {
	// Given: a table whose enable has settled, and a disable now in flight
	store := state.NewMemoryStore()
	mock := newTTLTestClock()
	svc := newTTLTestService(t, store, mock)
	mustCreateTable(t, svc, "ttl-stale-settle")
	mustUpdateTTL(t, svc, "ttl-stale-settle", true, "expiresAt")
	enableDeadline := ttlDeadlineOf(t, svc, "ttl-stale-settle")
	svc.handler.ttlSched.AdvanceAndSettle(mock, ttlTransitionDuration+time.Second)
	mustUpdateTTL(t, svc, "ttl-stale-settle", false, "expiresAt")
	disableDeadline := ttlDeadlineOf(t, svc, "ttl-stale-settle")
	if disableDeadline == enableDeadline {
		t.Fatalf("test setup: the disable re-used the enable's deadline %d", disableDeadline)
	}

	// When: a settle armed for the enable runs late, after the disable
	// has been accepted
	svc.handler.settleTTLTransition(context.Background(), "ttl-stale-settle", enableDeadline)

	// Then: the disable is untouched — still in flight, attribute intact,
	// deadline as the disable set it
	if got := ttlStatusOf(t, svc, "ttl-stale-settle"); got != ttlStatusDisabling {
		t.Errorf("status after the stale settle = %q, want %q", got, ttlStatusDisabling)
	}
	if got := ttlDeadlineOf(t, svc, "ttl-stale-settle"); got != disableDeadline {
		t.Errorf("TTLTransitionAt after the stale settle = %d, want the disable's %d", got, disableDeadline)
	}

	// And: the disable's own settle still completes it
	svc.handler.ttlSched.AdvanceAndSettle(mock, ttlTransitionDuration+time.Second)
	if got := ttlStatusOf(t, svc, "ttl-stale-settle"); got != ttlStatusDisabled {
		t.Errorf("status after the disable settled = %q, want %q", got, ttlStatusDisabled)
	}
}

func TestSettleTTLTransition_waitsForTheTableLock(t *testing.T) {
	// Given: a table whose enable is in flight, and the table's TTL lock held
	// as UpdateTimeToLive holds it across its read and write
	store := state.NewMemoryStore()
	mock := newTTLTestClock()
	svc := newTTLTestService(t, store, mock)
	mustCreateTable(t, svc, "ttl-locked-settle")
	mustUpdateTTL(t, svc, "ttl-locked-settle", true, "expiresAt")
	deadline := ttlDeadlineOf(t, svc, "ttl-locked-settle")
	unlock := svc.handler.ttlLocks.Lock(ttlLockKey("us-east-1", "ttl-locked-settle"))

	// When: the window closes, so the scheduled settle fires and finds the
	// lock held. A bare Add is deliberate here: the settle must be left
	// running on its own goroutine, which is exactly the shape of the race.
	mock.Add(ttlTransitionDuration + time.Second)

	// Then: the settle has written nothing while the lock is held, however
	// long it waits
	time.Sleep(100 * time.Millisecond)
	if got := ttlDeadlineOf(t, svc, "ttl-locked-settle"); got != deadline {
		t.Fatalf("TTLTransitionAt changed to %d under the lock, want %d untouched", got, deadline)
	}

	// And: it completes once the lock is released
	unlock()
	settledBy := time.Now().Add(5 * time.Second)
	for ttlDeadlineOf(t, svc, "ttl-locked-settle") != 0 {
		if time.Now().After(settledBy) {
			t.Fatal("the settle did not complete after the lock was released")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := ttlStatusOf(t, svc, "ttl-locked-settle"); got != ttlStatusEnabled {
		t.Errorf("status after the settle = %q, want %q", got, ttlStatusEnabled)
	}
}

func ttlDeadlineOf(t *testing.T, svc *Service, table string) int64 {
	t.Helper()
	rec, aerr := svc.handler.store.getTable(context.Background(), table)
	if aerr != nil {
		t.Fatalf("getTable(%q): %v", table, aerr)
	}
	return rec.TTLTransitionAt
}
