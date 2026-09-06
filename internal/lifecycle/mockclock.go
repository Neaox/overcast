package lifecycle

import (
	"slices"
	"sync"
	"time"

	"github.com/benbjohnson/clock"
)

// mockclock.go — settling every Scheduler a mock clock drives, for tests that
// only have the clock.
//
// Scheduler.AdvanceAndSettle is the deterministic way to advance a mock clock
// past a transition, but it needs the Scheduler, and an integration test has
// only the *clock.Mock it handed to router.New: the Schedulers are built deep
// inside the services and never surface. Without a way to reach them such a
// test can do nothing but mock.Add and read on the next line, which is the race
// tests/AGENTS.md § "Mock clocks" describes — the mock runs each AfterFunc
// callback on a goroutine of its own and sleeps one millisecond before
// returning, so the read wins on an idle machine and loses on a loaded one.
// That is what failed TestStopDBInstance_success in CI with the instance still
// "stopping".
//
// So a Scheduler built on a mock clock records itself against that clock here,
// and AdvanceAndSettleAll settles the lot. Only mock clocks are indexed: a
// production Scheduler holds a real clock, takes the type assertion in
// NewScheduler and stores nothing, so this costs a running emulator one failed
// type assertion per service and no memory at all.

var (
	mockRegistryMu sync.Mutex
	mockRegistry   = map[*clock.Mock][]*Scheduler{}
)

// registerMockScheduler indexes s against the mock clock driving it.
func registerMockScheduler(mock *clock.Mock, s *Scheduler) {
	mockRegistryMu.Lock()
	defer mockRegistryMu.Unlock()
	mockRegistry[mock] = append(mockRegistry[mock], s)
}

// schedulersFor returns a snapshot of the Schedulers built on mock.
func schedulersFor(mock *clock.Mock) []*Scheduler {
	mockRegistryMu.Lock()
	defer mockRegistryMu.Unlock()
	return slices.Clone(mockRegistry[mock])
}

// AdvanceAndSettleAll advances mock by d and returns only once every
// transition that came due on every Scheduler built with that clock has run to
// completion. It is Scheduler.AdvanceAndSettle for a caller that holds the
// clock rather than the Scheduler, and carries the same definition of settled:
// transitions a callback schedules while this runs are not waited for, even if
// they fall due within d.
//
// A zero d settles what is already due without moving time. A Scheduler
// created after this call begins is not covered — build the server first, then
// advance its clock.
func AdvanceAndSettleAll(mock *clock.Mock, d time.Duration) {
	scheds := schedulersFor(mock)
	target := mock.Now().Add(d)

	due := make([]chan struct{}, 0, len(scheds))
	for _, s := range scheds {
		due = append(due, s.dueBy(target)...)
	}

	mock.Add(d)

	for _, done := range due {
		<-done
	}
}

// Forget drops the Schedulers indexed against mock. A test server calls it
// when it shuts down, so a package that builds thousands of servers does not
// retain every Scheduler any of them made until the test binary exits.
//
// Calling it while the clock is still in use is safe but pointless: a
// Scheduler built afterwards indexes itself again.
func Forget(mock *clock.Mock) {
	mockRegistryMu.Lock()
	defer mockRegistryMu.Unlock()
	delete(mockRegistry, mock)
}
