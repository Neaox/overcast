package router

import (
	"runtime"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

const leakMsg = "possible goroutine leak: goroutines blocked past age threshold"

// newTestMonitor returns a monitor configured for fast test execution.
// ageThreshold=0 disables the age filter so all goroutines are counted — we
// can't manufacture 15-minute-old goroutines in a test. countGate and
// oldCountThreshold are set low so any Go test process trivially exceeds them.
// stwGate=1 so the STW snap fires immediately on the first elevated check.
func newTestMonitor(core zapcore.Core) *goroutineMonitor {
	mon := newGoroutineMonitor(zap.New(core))
	mon.checkInterval = 30 * time.Millisecond
	mon.countGate = 2         // any Go process has far more than 2 goroutines
	mon.stwGate = 1           // fire on first elevated check (no consecutive delay in tests)
	mon.ageThreshold = 0      // count all goroutines regardless of blocking duration
	mon.oldCountThreshold = 1 // fire as soon as countGate is crossed
	return mon
}

// waitForLog polls until pred returns true or the deadline expires.
func waitForLog(t *testing.T, pred func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		if pred() {
			return
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for expected log entry")
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// TestGoroutineMonitor_FiresWhenAboveThreshold verifies that the monitor emits
// a warning when the "old" goroutine count meets oldCountThreshold.
// With ageThreshold=0 every goroutine is counted, and any Go test process runs
// far more than oldCountThreshold=1.
func TestGoroutineMonitor_FiresWhenAboveThreshold(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	mon := newTestMonitor(core)
	mon.cooldown = time.Hour

	mon.Start()
	defer mon.Stop()

	waitForLog(t, func() bool {
		return logs.FilterMessage(leakMsg).Len() > 0
	}, 3*time.Second)

	entry := logs.FilterMessage(leakMsg).All()[0]

	if entry.ContextMap()["old_goroutines"] == nil {
		t.Error("expected old_goroutines field in log entry")
	}
	stacks, ok := entry.ContextMap()["stacks"]
	if !ok || stacks == "" {
		t.Error("expected non-empty stacks field in log entry")
	}
}

// TestGoroutineMonitor_SilentBelowThreshold verifies that no warning fires
// when the goroutine count stays below countGate.
func TestGoroutineMonitor_SilentBelowThreshold(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	mon := newTestMonitor(core)
	mon.countGate = 1_000_000 // impossibly high — never crossed
	mon.cooldown = time.Hour

	mon.Start()
	defer mon.Stop()

	// Wait for several check ticks.
	time.Sleep(mon.checkInterval * 4)

	if logs.Len() > 0 {
		t.Fatalf("expected no log entries, got %d: %v", logs.Len(), logs.All())
	}
}

// TestGoroutineMonitor_CooldownPreventsRepeatDump verifies that a second full
// stack dump is not emitted within the cooldown period; only the summary log fires.
func TestGoroutineMonitor_CooldownPreventsRepeatDump(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	mon := newTestMonitor(core)
	mon.cooldown = time.Hour // prevent a second full dump

	mon.Start()
	defer mon.Stop()

	// Wait for the first full dump.
	waitForLog(t, func() bool {
		return logs.FilterMessage(leakMsg).Len() > 0
	}, 3*time.Second)

	firstDumpCount := logs.FilterMessage(leakMsg).Len()

	// Wait several check intervals — cooldown should suppress further full dumps.
	time.Sleep(mon.checkInterval * 6)

	if got := logs.FilterMessage(leakMsg).Len(); got != firstDumpCount {
		t.Fatalf("cooldown not working: expected %d full dump(s), got %d", firstDumpCount, got)
	}

	// The "still present" summary should appear at least once during cooldown.
	if logs.FilterMessage("goroutine leak still present").Len() == 0 {
		t.Error("expected at least one 'still present' summary log during cooldown")
	}
}

// TestGoroutineMonitor_TransientSpikeClearsBeforeCheck verifies that goroutines
// released before the next check tick do not trigger a warning, since the
// age-based approach only fires when goroutines are present at check time.
func TestGoroutineMonitor_TransientSpikeClearsBeforeCheck(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	mon := newTestMonitor(core)
	// Use a long checkInterval so released goroutines are long gone by the time
	// the monitor checks. oldCountThreshold is set high so only the spike would
	// trigger — not background goroutines.
	mon.checkInterval = 300 * time.Millisecond
	mon.oldCountThreshold = runtime.NumGoroutine() + 5
	mon.cooldown = time.Hour

	mon.Start()
	defer mon.Stop()

	// Brief spike: spawn goroutines then release well before the first tick.
	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() { <-done }()
	}
	time.Sleep(20 * time.Millisecond) // hold briefly, then release
	close(done)

	// Wait past the first tick — the goroutines have already cleared.
	time.Sleep(mon.checkInterval + 50*time.Millisecond)

	if logs.FilterMessage(leakMsg).Len() > 0 {
		t.Error("monitor fired for transient spike that cleared before the check tick")
	}
}

// TestGoroutineMonitor_STWGateRequiresConsecutiveChecks verifies that the STW
// stack snap (and therefore any warning) is not triggered on the first elevated
// check when stwGate > 1 — a single burst must persist across multiple ticks.
func TestGoroutineMonitor_STWGateRequiresConsecutiveChecks(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	mon := newTestMonitor(core)
	mon.stwGate = 3 // require 3 consecutive elevated checks
	mon.cooldown = time.Hour

	mon.Start()
	defer mon.Stop()

	// After 1 tick the counter is 1 — below stwGate=3, no dump yet.
	time.Sleep(mon.checkInterval*1 + 10*time.Millisecond)
	if logs.FilterMessage(leakMsg).Len() > 0 {
		t.Error("monitor fired before stwGate consecutive checks were reached")
	}

	// After 3 ticks the counter reaches stwGate — dump should fire.
	waitForLog(t, func() bool {
		return logs.FilterMessage(leakMsg).Len() > 0
	}, time.Duration(mon.stwGate+1)*mon.checkInterval+200*time.Millisecond)
}

// TestGoroutineMonitor_STWGateResetsOnDrop verifies that the consecutive counter
// resets when the count drops below countGate, so a second spike must earn its
// own consecutive checks before causing a STW snap.
func TestGoroutineMonitor_STWGateResetsOnDrop(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	mon := newTestMonitor(core)
	mon.stwGate = 3
	mon.countGate = runtime.NumGoroutine() + 5 // start above baseline so we can control it
	mon.checkInterval = 40 * time.Millisecond
	mon.cooldown = time.Hour

	mon.Start()
	defer mon.Stop()

	// Spike: push count above gate for 2 ticks (not enough to fire).
	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() { <-done }()
	}
	time.Sleep(mon.checkInterval*2 + 10*time.Millisecond)

	// Drop below gate: counter should reset.
	close(done)
	time.Sleep(mon.checkInterval * 2)

	if logs.FilterMessage(leakMsg).Len() > 0 {
		t.Error("monitor fired despite resetting between spikes (stwGate not reset on drop)")
	}
}

// TestCountOldGoroutines_AgeFiltering unit-tests the age parsing logic
// directly against synthetic stack text.
func TestCountOldGoroutines_AgeFiltering(t *testing.T) {
	// Build a synthetic stack dump with goroutines at various ages.
	dump := `goroutine 1 [running]:
main.main()
	/main.go:1

goroutine 2 [select]:
runtime.gopark()
	/runtime.go:1

goroutine 3 [IO wait, 5 minutes]:
net.(*conn).Read()
	/net.go:1

goroutine 4 [IO wait, 20 minutes]:
net.(*conn).Read()
	/net.go:1

goroutine 5 [chan receive, 1 minute]:
some.func()
	/x.go:1

goroutine 6 [select, 2 hours]:
long.running()
	/x.go:1
`

	tests := []struct {
		minAge time.Duration
		want   int
	}{
		{0, 6},                // ageThreshold=0: count all goroutine headers
		{1 * time.Minute, 4},  // >= 1 min: goroutines 3, 4, 5, 6
		{5 * time.Minute, 3},  // >= 5 min: goroutines 3, 4, 6
		{15 * time.Minute, 2}, // >= 15 min: goroutines 4, 6
		{3 * time.Hour, 0},    // >= 3 hours: none
	}

	for _, tt := range tests {
		got := countOldGoroutines(dump, tt.minAge)
		if got != tt.want {
			t.Errorf("countOldGoroutines(dump, %v) = %d, want %d", tt.minAge, got, tt.want)
		}
	}
}
