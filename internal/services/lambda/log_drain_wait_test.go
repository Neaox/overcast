package lambda

import (
	"context"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/clock"
)

// waitForLogDrain is the last chance a dying container's output has. It runs
// once per teardown — after a crash, after a timeout, and on Close — and logCtx
// is cancelled moments later, which closes the Docker log stream and drops
// whatever was still in the pipe. What the drain gives up on is gone from
// CloudWatch Logs for good, not merely missing from an X-Amz-Log-Result tail.
//
// These tests drive it on the same mock clock, and through the same pipeline
// helpers, as the tail waits in log_tail_wait_test.go.

// runLogDrain runs waitForLogDrain against a mock clock the test owns and
// returns how long the drain lasted in mock time. onTick runs before every step
// of the clock, which is where a test puts whatever Docker is about to do.
//
// As with the tail waits, mock time only moves here, so the drain cannot race
// ahead of a delivery the test has not made yet.
func runLogDrain(t *testing.T, mock *clock.Mock, ci *containerInstance, grace time.Duration, onTick func()) time.Duration {
	t.Helper()

	start := mock.Now()
	advanceUntilDone(t, mock, 0, func() {
		ci.waitForLogDrain(context.Background(), grace)
	}, onTick)
	return mock.Now().Sub(start)
}

// TestWaitForLogDrain_readerThatHasNotAskedDockerIsNotSilence is #873's defect
// on the path where it costs the most.
//
// "Docker has handed over nothing" is the only evidence the emulator has that a
// container printed nothing, and it is worth nothing at all while the reader has
// no question outstanding with Docker. A container that dies early is precisely
// the case where the reader is likeliest to still be short of its first Read —
// opening the log stream is a Docker round trip that races container start — and
// the output at stake is the stack trace that says why it died.
func TestWaitForLogDrain_readerThatHasNotAskedDockerIsNotSilence(t *testing.T) {
	// Given: a container that has never produced a byte, whose reader has its
	// stream open but has not yet reached its first Read on it.
	mock := clock.NewMock()
	mock.Set(time.Unix(1_700_000_000, 0))
	ci := newTailWaitInstance(mock)
	readerWorks(ci)

	// When: the container dies, and the reader gets to Docker 40 ms later — well
	// past the 25 ms grace — to find what it printed on the way out.
	start := mock.Now()
	arrived := false
	elapsed := runLogDrain(t, mock, ci, logDrainFirstReadGrace, func() {
		if !arrived && mock.Now().Sub(start) >= 40*time.Millisecond {
			readerParks(ci)
			deliverBytes(ci)
			arrived = true
		}
	})

	// Then: the drain was still there for it. Had it given up on the grace, the
	// teardown that follows would have cancelled logCtx with those bytes still in
	// Docker's pipe, and nothing downstream would ever have read them.
	if !arrived || elapsed < 40*time.Millisecond {
		t.Errorf("drain returned after %v, before the reader had put a single question to Docker — the dying container's output is lost when logCtx is cancelled", elapsed)
	}
}

// TestWaitForLogDrain_containerThatNeverLoggedGivesUpAtTheGrace pins the cost
// side of that wait. A container that really did print nothing looks the same
// from the outside however long we wait, so the grace has to end somewhere: at
// firstReadGrace, on the 2 s deadline that is only there for a genuinely stuck
// pipeline.
func TestWaitForLogDrain_containerThatNeverLoggedGivesUpAtTheGrace(t *testing.T) {
	// Given: a dying container whose reader is parked on Docker — the question
	// has been asked — and has been told nothing across its whole life.
	mock := clock.NewMock()
	mock.Set(time.Unix(1_700_000_000, 0))
	ci := newTailWaitInstance(mock)

	// When: the drain runs with nothing ever delivered.
	elapsed := runLogDrain(t, mock, ci, logDrainFirstReadGrace, nil)

	// Then: teardown moves on at the grace rather than the deadline.
	if elapsed > 40*time.Millisecond {
		t.Errorf("silent container held teardown for %v, expected to give up around the %v grace", elapsed, logDrainFirstReadGrace)
	}
}

// TestWaitForLogDrain_zeroGraceDoesNotWaitOnTheReader pins Close's end of the
// bargain. Pool eviction closes stale instances on the acquire path
// (InstancePool.takeWarm), so anything Close spends here is charged to the next
// cold start. A caller that grants no grace is declining the question, not
// asking it slowly: the reader's state is not consulted at all.
func TestWaitForLogDrain_zeroGraceDoesNotWaitOnTheReader(t *testing.T) {
	// Given: a container that never logged, being closed while its reader is
	// between Reads — the state the crash path holds on for.
	mock := clock.NewMock()
	mock.Set(time.Unix(1_700_000_000, 0))
	ci := newTailWaitInstance(mock)
	readerWorks(ci)

	// When: Close drains it with no grace at all.
	elapsed := runLogDrain(t, mock, ci, 0, nil)

	// Then: it returns on its first tick.
	if elapsed > 10*time.Millisecond {
		t.Errorf("Close's drain took %v — a caller that granted no grace paid for one anyway", elapsed)
	}
}
