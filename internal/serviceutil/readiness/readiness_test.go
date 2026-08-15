package readiness_test

// The policy half on its own: what a Watch does with each of the four outcomes
// a probe can report, and when. No sockets and no sleeping — the probe is a
// function the test writes and every delay is a mock clock the test advances.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/lifecycle"
	"github.com/Neaox/overcast/internal/serviceutil/readiness"
)

var testPolicy = readiness.Policy{
	FirstDelay: 1 * time.Second,
	Interval:   2 * time.Second,
	Budget:     10 * time.Second,
}

// settled records what a Watch did to its record, under a lock because the
// scheduler runs the callbacks on goroutines of its own.
type settled struct {
	mu     sync.Mutex
	ready  int
	failed int
	reason string
}

func (s *settled) onReady(context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ready++
}

func (s *settled) onFailed(_ context.Context, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failed++
	s.reason = reason
}

func (s *settled) read() (ready, failed int, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready, s.failed, s.reason
}

// start wires a Watch around probe and returns the recorder and a function that
// advances one interval at a time, settling each attempt as it fires.
func start(t *testing.T, probe func(context.Context) readiness.Result) (*settled, func(steps int)) {
	t.Helper()
	clk := clock.NewMock()
	sched := lifecycle.NewScheduler(clk)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		sched.Stop(ctx)
	})

	got := &settled{}
	readiness.Watch{
		Scheduler:  sched,
		Clock:      clk,
		Region:     "us-east-1",
		ResourceID: "res-1",
		Transition: "ready",
		Subject:    "the widget at 127.0.0.1:1234",
		Policy:     testPolicy,
		Probe:      probe,
		OnReady:    got.onReady,
		OnFailed:   got.onFailed,
	}.Start()

	return got, func(steps int) {
		for range steps {
			sched.AdvanceAndSettle(clk, testPolicy.Interval)
		}
	}
}

func TestReadyOnTheFirstAttempt(t *testing.T) {
	got, advance := start(t, func(context.Context) readiness.Result {
		return readiness.Answered()
	})
	advance(1)

	ready, failed, _ := got.read()
	if ready != 1 || failed != 0 {
		t.Fatalf("ready=%d failed=%d, want the first attempt to settle ready", ready, failed)
	}
}

func TestExhaustedBudgetFailsWithTheLastAttemptsEvidence(t *testing.T) {
	var attempts int
	got, advance := start(t, func(context.Context) readiness.Result {
		attempts++
		return readiness.Retry(errors.New("connection refused"))
	})
	advance(10) // 20s of mock time against a 10s budget

	ready, failed, reason := got.read()
	if ready != 0 {
		t.Fatalf("ready=%d, want a resource that never answered to settle failed", ready)
	}
	if failed != 1 {
		t.Fatalf("failed=%d, want exactly one terminal settle", failed)
	}
	if !strings.Contains(reason, "the widget at 127.0.0.1:1234") {
		t.Errorf("reason = %q, want it to name the subject", reason)
	}
	if !strings.Contains(reason, testPolicy.Budget.String()) {
		t.Errorf("reason = %q, want it to name the budget", reason)
	}
	if !strings.Contains(reason, "connection refused") {
		t.Errorf("reason = %q, want it to carry what the last attempt saw", reason)
	}
	// A budget, not an attempt count: 1s + 2s per attempt over 10s is six
	// attempts, and the seventh is the one that finds the budget spent.
	if attempts < 5 || attempts > 8 {
		t.Errorf("attempts = %d, want roughly Budget/Interval before giving up", attempts)
	}
}

func TestDoomedSettlesImmediatelyWithoutSpendingTheBudget(t *testing.T) {
	var attempts int
	got, advance := start(t, func(context.Context) readiness.Result {
		attempts++
		return readiness.Doomed("the container exited with code 1")
	})
	advance(10)

	ready, failed, reason := got.read()
	if ready != 0 || failed != 1 {
		t.Fatalf("ready=%d failed=%d, want one terminal settle", ready, failed)
	}
	if reason != "the container exited with code 1" {
		t.Errorf("reason = %q, want the probe's own words verbatim", reason)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1: waiting out a budget only delays saying the same thing", attempts)
	}
}

func TestGoneSettlesNothingAndStops(t *testing.T) {
	var attempts int
	got, advance := start(t, func(context.Context) readiness.Result {
		attempts++
		return readiness.Abandoned()
	})
	advance(10)

	ready, failed, _ := got.read()
	if ready != 0 || failed != 0 {
		t.Fatalf("ready=%d failed=%d, want neither: the record moved on and is not this watch's to settle",
			ready, failed)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1: a watch that has been abandoned must not keep polling", attempts)
	}
}

func TestAnAnswerOnALaterAttemptStillSettlesReady(t *testing.T) {
	var attempts int
	got, advance := start(t, func(context.Context) readiness.Result {
		attempts++
		if attempts < 3 {
			return readiness.Retry(errors.New("not yet"))
		}
		return readiness.Answered()
	})
	advance(10)

	ready, failed, _ := got.read()
	if ready != 1 || failed != 0 {
		t.Fatalf("ready=%d failed=%d, want ready once the third attempt answered", ready, failed)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want the watch to stop as soon as it had an answer", attempts)
	}
}

func TestExhaustionReasonWithoutEvidenceStillNamesSubjectAndBudget(t *testing.T) {
	got, advance := start(t, func(context.Context) readiness.Result {
		return readiness.Retry(nil)
	})
	advance(10)

	_, failed, reason := got.read()
	if failed != 1 {
		t.Fatalf("failed=%d, want one terminal settle", failed)
	}
	want := "the widget at 127.0.0.1:1234 did not become ready within 10s"
	if reason != want {
		t.Errorf("reason = %q, want %q", reason, want)
	}
}
