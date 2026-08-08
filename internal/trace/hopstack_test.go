package trace

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func newHopRecorder() *Recorder {
	return NewRecorder("req-1", time.Unix(0, 0), http.MethodPost, "/", "localhost", "", http.Header{})
}

// A CDK deploy dispatches hundreds of hops through one trace. Capturing a
// multi-KiB goroutine stack for every one of them makes the biggest traces the
// most expensive, so the capture is budgeted per trace.
func TestAddHop_stackBudgetIsBounded(t *testing.T) {
	// Given: a trace recording far more hops than the stack budget
	rec := newHopRecorder()

	// When: MaxHopStacks+50 successful hops are added
	for i := 0; i < MaxHopStacks+50; i++ {
		rec.AddHop(Hop{Service: "sqs", Operation: "CreateQueue", ResponseStatus: 200})
	}

	// Then: only the first MaxHopStacks carry a stack, and they are the first
	// ones — the budget is spent in arrival order, not at random
	hops := rec.Entry().Hops
	if len(hops) != MaxHopStacks+50 {
		t.Fatalf("hops = %d, want %d", len(hops), MaxHopStacks+50)
	}
	withStack := 0
	for i, h := range hops {
		if h.Stack == "" {
			continue
		}
		withStack++
		if i >= MaxHopStacks {
			t.Errorf("hop %d carries a stack, past the budget of %d", i, MaxHopStacks)
		}
		if !strings.Contains(h.Stack, "goroutine") {
			t.Errorf("hop %d stack does not look like a goroutine stack: %q", i, h.Stack)
		}
	}
	if withStack != MaxHopStacks {
		t.Errorf("hops with a stack = %d, want %d", withStack, MaxHopStacks)
	}
}

// A failure late in a long deploy is exactly the hop worth a stack, so failed
// hops draw on their own budget rather than competing with the successes
// ahead of them.
func TestAddHop_failedHopDrawsOnItsOwnBudget(t *testing.T) {
	// Given: a trace that has already exhausted the successful-hop budget
	rec := newHopRecorder()
	for i := 0; i < MaxHopStacks*3; i++ {
		rec.AddHop(Hop{Service: "sqs", Operation: "CreateQueue", ResponseStatus: 200})
	}

	// When: a hop fails
	id := rec.AddHop(Hop{Service: "sqs", Operation: "CreateQueue", ResponseStatus: 400, Error: "AccessDenied"})

	// Then: it still carries a stack
	hops := rec.Entry().Hops
	last := hops[len(hops)-1]
	if last.ID != id {
		t.Fatalf("last hop ID = %q, want %q", last.ID, id)
	}
	if last.Stack == "" {
		t.Error("failed hop has no stack despite the separate failure budget")
	}
}

// A caller that already knows the stack it wants recorded keeps it, and spends
// no budget doing so.
func TestAddHop_preservesCallerSuppliedStack(t *testing.T) {
	// Given: a trace with an exhausted successful-hop budget
	rec := newHopRecorder()
	for i := 0; i < MaxHopStacks; i++ {
		rec.AddHop(Hop{Service: "sqs", ResponseStatus: 200})
	}

	// When: a hop arrives with its own stack
	rec.AddHop(Hop{Service: "sqs", ResponseStatus: 200, Stack: "caller-supplied"})

	// Then: that stack is what is stored
	hops := rec.Entry().Hops
	if got := hops[len(hops)-1].Stack; got != "caller-supplied" {
		t.Errorf("Stack = %q, want %q", got, "caller-supplied")
	}
}

// Both response-writer wrappers in the chain (Logger's and RequestEvents')
// capture at WriteHeader time and they nest, so the outermost used to win —
// the wrapper furthest from the handler, and one frame deeper for it. First
// call wins instead, and later calls capture nothing.
func TestCaptureStackOnce_firstCallWins(t *testing.T) {
	// Given: a recorder whose stack has been captured from an inner frame
	rec := newHopRecorder()
	innerFrame(rec)
	first := rec.Entry().Stack
	if first == "" {
		t.Fatal("no stack captured")
	}
	if !strings.Contains(first, "innerFrame") {
		t.Fatalf("stack does not name the capturing frame: %q", first)
	}

	// When: an outer wrapper captures again
	rec.CaptureStackOnce()

	// Then: the first stack is still the one recorded
	if got := rec.Entry().Stack; got != first {
		t.Error("second CaptureStackOnce overwrote the first stack")
	}
}

func TestCaptureStackOnce_nilRecorder(t *testing.T) {
	// Given/When/Then: a nil recorder (debug off) is a no-op, not a panic
	var rec *Recorder
	rec.CaptureStackOnce()
}

//go:noinline
func innerFrame(rec *Recorder) { rec.CaptureStackOnce() }
