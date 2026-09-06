package lifecycle

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/internal/clock"
)

// TestMockAdd_returnsBeforeTheCallbackHasFinished is the defect the settle
// helpers exist for, pinned deterministically rather than by load: mock.Add
// runs each AfterFunc on a goroutine of its own and sleeps a single
// millisecond, so a test that advances the clock and reads the result on the
// next line is racing the transition. That is what left
// TestStopDBInstance_success reading "stopping" on a loaded CI runner.
func TestMockAdd_returnsBeforeTheCallbackHasFinished(t *testing.T) {
	// Given a transition whose callback cannot finish until the test says so
	mock := clock.NewMock()
	defer Forget(mock)

	s := NewScheduler(mock)

	release := make(chan struct{})
	var finished atomic.Bool
	s.After("held", 1*time.Second, func() {
		<-release
		finished.Store(true)
	})

	// When the clock is advanced with a bare Add
	mock.Add(1 * time.Second)

	// Then Add has already returned with the transition still outstanding —
	// the callback is provably blocked, so this cannot pass by being quick
	if finished.Load() {
		t.Fatal("the callback finished while it was still blocked")
	}

	// And settling after it is released does wait for it
	close(release)
	AdvanceAndSettleAll(mock, 0)
	if !finished.Load() {
		t.Fatal("AdvanceAndSettleAll returned while the callback was still running")
	}
}

func TestAdvanceAndSettleAll_waitsForEverySchedulerOnTheClock(t *testing.T) {
	// Given two Schedulers sharing one mock clock — an integration test's
	// server builds one per service — each with a callback that takes longer
	// than the millisecond a bare mock.Add yields for
	mock := clock.NewMock()
	defer Forget(mock)

	first, second := NewScheduler(mock), NewScheduler(mock)

	var firstFired, secondFired atomic.Bool
	first.After("first", 1*time.Second, func() {
		time.Sleep(50 * time.Millisecond)
		firstFired.Store(true)
	})
	second.After("second", 1*time.Second, func() {
		time.Sleep(50 * time.Millisecond)
		secondFired.Store(true)
	})

	// When the clock is advanced through both and settled
	AdvanceAndSettleAll(mock, 1*time.Second)

	// Then both callbacks have finished, not merely started
	if !firstFired.Load() || !secondFired.Load() {
		t.Fatalf("AdvanceAndSettleAll returned early: first=%v second=%v",
			firstFired.Load(), secondFired.Load())
	}
}

func TestAdvanceAndSettleAll_leavesATransitionThatIsNotYetDue(t *testing.T) {
	// Given one transition inside the advance and one well beyond it
	mock := clock.NewMock()
	defer Forget(mock)

	s := NewScheduler(mock)

	var due, later atomic.Bool
	s.After("due", 1*time.Second, func() { due.Store(true) })
	s.After("later", 1*time.Hour, func() { later.Store(true) })

	// When the clock is advanced past the first only
	AdvanceAndSettleAll(mock, 2*time.Second)

	// Then it settled the one that came due and did not wait for the other
	if !due.Load() {
		t.Fatal("expected the due transition to have run")
	}
	if later.Load() {
		t.Fatal("expected the later transition not to have run")
	}
}

func TestAdvanceAndSettleAll_ignoresSchedulersOnAnotherClock(t *testing.T) {
	// Given two mock clocks, each with its own Scheduler — two test servers
	mine, theirs := clock.NewMock(), clock.NewMock()
	defer Forget(mine)
	defer Forget(theirs)

	NewScheduler(mine)
	other := NewScheduler(theirs)

	var otherFired atomic.Bool
	other.After("theirs", 1*time.Second, func() { otherFired.Store(true) })

	// When only my clock is advanced
	AdvanceAndSettleAll(mine, 1*time.Hour)

	// Then the other server's transition is untouched — its clock never moved
	if otherFired.Load() {
		t.Fatal("advancing one mock clock ran a transition scheduled on another")
	}
	if other.PendingCount() != 1 {
		t.Fatalf("expected the other scheduler to still hold its transition, got %d pending", other.PendingCount())
	}
}

func TestAdvanceAndSettleAll_zeroDurationSettlesWhatIsAlreadyDue(t *testing.T) {
	// Given a zero-delay transition, which a mock clock leaves pending
	mock := clock.NewMock()
	defer Forget(mock)

	s := NewScheduler(mock)

	var fired atomic.Bool
	s.After("zero", 0, func() {
		time.Sleep(50 * time.Millisecond)
		fired.Store(true)
	})
	at := mock.Now()

	// When the clock is settled without being moved
	AdvanceAndSettleAll(mock, 0)

	// Then the callback has finished, and the clock has not moved
	if !fired.Load() {
		t.Fatal("AdvanceAndSettleAll returned while the callback was still running")
	}
	if got := mock.Now(); !got.Equal(at) {
		t.Fatalf("a zero advance moved the clock from %v to %v", at, got)
	}
}

func TestForget_dropsTheClocksSchedulers(t *testing.T) {
	// Given a Scheduler indexed against a mock clock
	mock := clock.NewMock()
	NewScheduler(mock)

	if got := len(schedulersFor(mock)); got != 1 {
		t.Fatalf("expected the scheduler to be indexed, got %d", got)
	}

	// When the clock is forgotten — what a test server does on shutdown
	Forget(mock)

	// Then nothing is retained for it
	if got := len(schedulersFor(mock)); got != 0 {
		t.Fatalf("expected 0 schedulers after Forget, got %d", got)
	}
}

func TestNewScheduler_realClockIsNotIndexed(t *testing.T) {
	// Given a production Scheduler on a real clock
	before := registrySize()
	NewScheduler(clock.New())

	// Then nothing was recorded for it
	if got := registrySize(); got != before {
		t.Fatalf("a real-clock Scheduler was indexed: registry went from %d to %d", before, got)
	}
}

func registrySize() int {
	mockRegistryMu.Lock()
	defer mockRegistryMu.Unlock()
	return len(mockRegistry)
}
