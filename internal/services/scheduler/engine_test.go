package scheduler

// Engine coverage: what one tick does with the schedules it finds due, and in
// particular that those schedules are independent of one another. A schedule
// whose target is slow, wedged, or retrying must not hold up every other
// schedule in the emulator — the engine used to fire inline and serially on the
// tick goroutine, so it did.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// seedSchedule writes an ENABLED rate(1 minute) schedule that has never fired,
// so it is due on the very next tick.
func seedSchedule(tb testing.TB, s *Service, region, name string, target scheduleTarget) {
	tb.Helper()
	raw, err := json.Marshal(Schedule{
		Name: name, GroupName: defaultGroup, State: "ENABLED",
		ScheduleExpression: "rate(1 minute)", Target: target,
	})
	if err != nil {
		tb.Fatalf("marshal schedule %s: %v", name, err)
	}
	key := s.scheduleKey(region, defaultGroup, name)
	if err := s.store.Set(context.Background(), nsSchedules, key, string(raw)); err != nil {
		tb.Fatalf("seed schedule %s: %v", name, err)
	}
}

// sqsTarget returns a target naming an SQS queue. The recording router answers
// every delivery, so the queue never has to exist.
func sqsTarget(queue string) scheduleTarget {
	return scheduleTarget{Arn: "arn:aws:sqs:us-east-1:000000000000:" + queue}
}

// deliveredTo reports whether the router has recorded a delivery whose body
// names queue.
func deliveredTo(router *recordingRouter, queue string) bool {
	for _, c := range router.recorded() {
		if strings.Contains(c.Body, `"QueueUrl":"`+queue+`"`) {
			return true
		}
	}
	return false
}

// waitFor polls cond until it holds or the timeout passes.
func waitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestTick_oneSlowTargetDoesNotStallTheOtherSchedules(t *testing.T) {
	// Given: two due schedules, the first of which has a target that blocks
	// indefinitely. Store keys are scanned in ascending order, so the blocking
	// schedule is the one the engine reaches first.
	var releaseOnce sync.Once
	release := make(chan struct{})
	releaseSlow := func() { releaseOnce.Do(func() { close(release) }) }

	var reachedOnce sync.Once
	reached := make(chan struct{})

	router := &recordingRouter{}
	router.status = func(c recordedCall) int {
		if strings.Contains(c.Body, `"QueueUrl":"slow-queue"`) {
			reachedOnce.Do(func() { close(reached) })
			<-release
		}
		return http.StatusOK
	}

	s, _ := newFiringService(t, router)
	t.Cleanup(releaseSlow)

	seedSchedule(t, s, "us-east-1", "a-slow", sqsTarget("slow-queue"))
	seedSchedule(t, s, "us-east-1", "b-fast", sqsTarget("fast-queue"))

	// When: the engine ticks
	ticked := make(chan struct{})
	go func() {
		defer close(ticked)
		s.tick()
	}()

	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("the slow target was never reached")
	}

	// Then: the second schedule is delivered while the first is still in flight
	if !waitFor(5*time.Second, func() bool { return deliveredTo(router, "fast-queue") }) {
		t.Fatal("the second schedule was not delivered while the first target was still blocked — the engine fires serially")
	}

	releaseSlow()
	select {
	case <-ticked:
	case <-time.After(5 * time.Second):
		t.Fatal("tick did not return")
	}
}

func TestTick_doesNotQueueAFiringBehindAnInFlightOne(t *testing.T) {
	// Given: one due schedule whose target blocks
	var releaseOnce sync.Once
	release := make(chan struct{})
	releaseSlow := func() { releaseOnce.Do(func() { close(release) }) }

	var reachedOnce sync.Once
	reached := make(chan struct{})

	router := &recordingRouter{}
	router.status = func(recordedCall) int {
		reachedOnce.Do(func() { close(reached) })
		<-release
		return http.StatusOK
	}

	s, _ := newFiringService(t, router)
	t.Cleanup(releaseSlow)
	seedSchedule(t, s, "us-east-1", "s1", sqsTarget("slow-queue"))

	// When: the engine ticks again and again while that firing is still in
	// flight — as it would once a second against a wedged target
	s.tick()
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("the target was never reached")
	}
	for i := 0; i < 5; i++ {
		s.tick()
	}
	time.Sleep(50 * time.Millisecond)

	// Then: the schedule has exactly one firing outstanding. Firing stays
	// ordered per schedule because a schedule is never in flight twice.
	if got := len(router.recorded()); got != 1 {
		t.Fatalf("made %d deliveries for one schedule, want 1: a tick queued a firing behind an in-flight one", got)
	}
}

func TestTick_recordsTheFireTimeBeforeDelivering(t *testing.T) {
	// Given: one due rate(1 minute) schedule whose target answers at once
	router := &recordingRouter{}
	s, _ := newFiringService(t, router)
	seedSchedule(t, s, "us-east-1", "s1", sqsTarget("q"))
	key := s.scheduleKey("us-east-1", defaultGroup, "s1")

	// When: the engine ticks, the firing completes, and it ticks again
	s.tick()
	if !waitFor(5*time.Second, func() bool { return deliveredTo(router, "q") }) {
		t.Fatal("the schedule was never delivered")
	}
	if !waitFor(5*time.Second, func() bool {
		return !s.getLastFire(context.Background(), key).IsZero()
	}) {
		t.Fatal("the last-fire time was never recorded")
	}
	time.Sleep(50 * time.Millisecond) // let the worker finish and release the schedule
	s.tick()
	time.Sleep(50 * time.Millisecond)

	// Then: the cadence held — a minute has not passed, so it fired once
	if got := len(router.recorded()); got != 1 {
		t.Fatalf("made %d deliveries, want 1: the last-fire time did not hold the cadence", got)
	}
}
