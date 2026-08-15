package cloudformation

// provisioner_readiness_test.go — the wait shared by every asynchronous
// resource, tested once here rather than five times over.
//
// What each resource type supplies to it — its describe call, its status
// vocabulary, and its budget — is tested against that service's own API shape
// in the per-service files (provisioner_elasticache_test.go,
// provisioner_msk_test.go, provisioner_eks_test.go, provisioner_efs_test.go,
// provisioner_lambda_stabilize_test.go). What is tested here is the part none
// of them should have to get right on their own: that an unrecognised status
// keeps the resource waiting, that a documented failure status ends the wait
// immediately with the service's own reason, and that running out of budget
// says how far the resource got.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/clock"
)

// pollDrivenClock is a clock that moves only when something waits on it: every
// After advances it by exactly the interval it was asked for. A stabilization
// wait therefore walks to its deadline in as many polls as the deadline holds
// intervals, spending no real time — which is what lets a fifteen-minute budget
// be tested at all. Everything the wait does not call is the real clock.
type pollDrivenClock struct {
	clock.Clock
	mu  sync.Mutex
	now time.Time
}

func newPollDrivenClock() *pollDrivenClock {
	return &pollDrivenClock{Clock: clock.New(), now: time.Unix(0, 0).UTC()}
}

func (c *pollDrivenClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *pollDrivenClock) Since(t time.Time) time.Duration { return c.Now().Sub(t) }

func (c *pollDrivenClock) Until(t time.Time) time.Duration { return t.Sub(c.Now()) }

func (c *pollDrivenClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	c.mu.Unlock()
	ch := make(chan time.Time, 1)
	ch <- now
	return ch
}

// statusScript is the shape every service fake in these tests answers from: one
// status per describe call, with the last one repeating once the script runs
// out. It is the shape a stabilization wait exists for — a resource that is not
// ready when it is created and becomes ready (or fails) later.
type statusScript struct {
	mu        sync.Mutex
	statuses  []string
	describes int
}

// next returns the status this describe should report and records the call.
func (s *statusScript) next(fallback string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.describes
	s.describes++
	if len(s.statuses) == 0 {
		return fallback
	}
	if i >= len(s.statuses) {
		i = len(s.statuses) - 1
	}
	return s.statuses[i]
}

func (s *statusScript) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.describes
}

// Statuses a service does not document are the ones that must not end a wait.
// Completing a resource over a status nothing here understands is the failure
// the whole path exists to prevent, and the two known sets are small.
func TestStatusVocabulary_classify(t *testing.T) {
	vocab := statusVocabulary{ready: []string{"available"}, failed: []string{"create-failed", "deleting"}}
	cases := []struct {
		status string
		want   readinessOutcome
	}{
		{"available", readinessReady},
		// AWS spells the same vocabulary in both cases across its own APIs —
		// an ElastiCache replication group says "available", a serverless
		// cache says "AVAILABLE" — so the match folds case.
		{"AVAILABLE", readinessReady},
		{"creating", readinessWaiting},
		{"create-failed", readinessFailed},
		{"deleting", readinessFailed},
		{"some-status-aws-added-last-week", readinessWaiting},
		// A service that reports no state cannot be waited on for one.
		{"", readinessReady},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			if got := vocab.classify(tc.status); got != tc.want {
				t.Errorf("classify(%q) = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}

// A resource that reaches a status its service documents as terminal must fail
// the moment it is seen, carrying the service's own account of it — not sit
// there until the budget runs out and blame a timeout for it.
func TestAwaitResourceReady_terminalStatusFailsImmediatelyWithItsReason(t *testing.T) {
	// Given: a resource that fails on its second status check
	clk := newPollDrivenClock()
	calls := 0
	wait := stabilizeWait{
		subject:  "MSK cluster app-msk",
		goal:     "become ACTIVE",
		timeout:  30 * time.Minute,
		statuses: statusVocabulary{ready: []string{"ACTIVE"}, failed: []string{"FAILED"}},
		describe: func(context.Context) (string, string, error) {
			calls++
			if calls == 1 {
				return "CREATING", "", nil
			}
			return "FAILED", "the broker container exited with code 1", nil
		},
	}

	// When: the resource is waited on
	err := awaitResourceReady(context.Background(), clk, wait)

	// Then: it fails on the failure, not on the clock
	if err == nil {
		t.Fatal("expected the wait to fail")
	}
	if !strings.Contains(err.Error(), "exited with code 1") {
		t.Errorf("expected the service's own reason, got %v", err)
	}
	if strings.Contains(err.Error(), "did not become ACTIVE within") {
		t.Errorf("a documented failure was reported as a timeout: %v", err)
	}
	if calls != 2 {
		t.Errorf("describe calls = %d, want 2 — the wait kept polling past a terminal status", calls)
	}
}

// A resource that never settles has to end somewhere, and the message it ends
// with is the only thing an operator has to go on: it must say what the
// resource was waiting for and how far it actually got.
func TestAwaitResourceReady_timeoutSaysWhatItWasWaitingFor(t *testing.T) {
	// Given: a resource stuck in "creating"
	clk := newPollDrivenClock()
	wait := stabilizeWait{
		subject:  "cache cluster appcache",
		goal:     "become available",
		timeout:  2 * time.Second,
		statuses: statusVocabulary{ready: []string{"available"}, failed: []string{"create-failed"}},
		describe: func(context.Context) (string, string, error) { return "creating", "", nil },
	}

	// When: the resource is waited on
	err := awaitResourceReady(context.Background(), clk, wait)

	// Then: the failure names the resource, the goal, and the status it reached
	if err == nil {
		t.Fatal("expected the wait to time out")
	}
	for _, want := range []string{"cache cluster appcache", "become available", "creating", "2s"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("timeout reason %q does not mention %q", err.Error(), want)
		}
	}
}

// The wait ends when the provisioner's context does, so a stuck resource cannot
// outlive a shutdown.
func TestAwaitResourceReady_stopsWithTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	clk := newPollDrivenClock()
	calls := 0
	wait := stabilizeWait{
		subject:  "cache cluster appcache",
		goal:     "become available",
		timeout:  time.Hour,
		statuses: statusVocabulary{ready: []string{"available"}},
		describe: func(context.Context) (string, string, error) {
			calls++
			if calls == 2 {
				cancel()
			}
			return "creating", "", nil
		},
	}

	err := awaitResourceReady(ctx, clk, wait)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// A describe that cannot answer is not a resource that is still working. The
// error travels out rather than being polled over, the same way a vanished RDS
// instance ends its wait.
func TestAwaitResourceReady_describeErrorEndsTheWait(t *testing.T) {
	clk := newPollDrivenClock()
	wait := stabilizeWait{
		subject: "mount target fsmt-1",
		goal:    `reach lifecycle state "available"`,
		timeout: time.Hour,
		describe: func(context.Context) (string, string, error) {
			return "", "", errors.New("mount target fsmt-1 no longer exists")
		},
	}

	err := awaitResourceReady(context.Background(), clk, wait)
	if err == nil || !strings.Contains(err.Error(), "no longer exists") {
		t.Errorf("err = %v, want the describe's own error", err)
	}
}
