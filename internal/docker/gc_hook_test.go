package docker

// gc_hook_test.go — the before-remove hook, which exists so that whatever a
// container is holding that is worth keeping is taken while the container is
// still there to be read.
//
// Removal is the point of no return: ScheduleRemove is non-blocking and the
// remove loop runs on its own goroutine, so a caller has nowhere in its own
// control flow that provably precedes the removal. The ordering therefore has
// to be owned here, and these pin the two halves of it — the hook runs before
// the removal, and it runs against a container that has finished writing rather
// than one still shutting down.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

// hookDaemon records what was asked of it, in order, so a test can assert
// against the sequence rather than only the outcome.
type hookDaemon struct {
	srv *httptest.Server

	mu     sync.Mutex
	events []string
	// failDeletes is the number of removal attempts to reject with a
	// retryable error before letting one through.
	failDeletes int
}

func newHookDaemon(t *testing.T, failDeletes int) *hookDaemon {
	t.Helper()
	d := &hookDaemon{failDeletes: failDeletes}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/stop"):
			d.record("stop")
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && strings.Contains(p, "/containers/"):
			d.mu.Lock()
			retry := d.failDeletes > 0
			if retry {
				d.failDeletes--
			}
			d.events = append(d.events, "remove")
			d.mu.Unlock()
			if retry {
				// Not a not-found, so the loop retries rather than giving up.
				http.Error(w, `{"message":"daemon busy"}`, http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func (d *hookDaemon) record(what string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, what)
}

func (d *hookDaemon) seen() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.events...)
}

func (d *hookDaemon) count(what string) int {
	n := 0
	for _, e := range d.seen() {
		if e == what {
			n++
		}
	}
	return n
}

// newHookGC returns a GC over d with fn registered and its remove loop running.
func newHookGC(t *testing.T, d *hookDaemon, fn func(string)) *GC {
	t.Helper()
	gc := NewGC(NewClient("tcp://"+d.srv.Listener.Addr().String(), zap.NewNop()),
		zap.NewNop(), false, fixedDomain(thisInstance))
	gc.SetBeforeRemove(fn)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	gc.StartRemoveLoop(ctx)
	return gc
}

// waitForEvent blocks until what has been recorded, so the assertions do not
// race the loop they are about.
func waitForEvent(t *testing.T, d *hookDaemon, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if d.count(what) > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q; saw %v", what, d.seen())
}

// The hook's whole purpose is to run while the container is still readable, and
// the stop before it is what makes what it reads final. StopNow and
// ScheduleRemove are independent and both non-blocking, so the remove loop
// routinely reaches a container whose stop is still in flight; without the stop
// here the hook reads a container mid-shutdown and keeps the lines from before
// it started dying.
func TestBeforeRemoveHookRunsBetweenStopAndRemove(t *testing.T) {
	// Given: a GC whose before-remove hook records when it ran
	d := newHookDaemon(t, 0)
	gc := newHookGC(t, d, func(string) { d.record("hook") })

	// When: a container is scheduled for removal
	gc.ScheduleRemove("c1")
	waitForEvent(t, d, "remove")

	// Then: it was stopped, then read, then removed
	want := []string{"stop", "hook", "remove"}
	got := d.seen()
	if len(got) != len(want) {
		t.Fatalf("daemon saw %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("daemon saw %v; want %v", got, want)
		}
	}
}

// A removal that Docker rejects is retried, and the hook is not part of what is
// being retried: it already ran against a container that was certainly present,
// and running it again would read the same container later and no better. For
// ECS's hook, whose capture keeps the first success, a re-run is wasted work on
// the GC's only loop.
func TestBeforeRemoveHookRunsOncePerContainer(t *testing.T) {
	// Given: a daemon that rejects the first removal attempt
	d := newHookDaemon(t, 1)
	var hookCalls int
	var mu sync.Mutex
	gc := newHookGC(t, d, func(string) {
		mu.Lock()
		hookCalls++
		mu.Unlock()
	})

	// When: a container is scheduled and the removal has to be retried
	gc.ScheduleRemove("c1")
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && d.count("remove") < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	if d.count("remove") < 2 {
		t.Fatalf("removal was not retried; daemon saw %v", d.seen())
	}

	// Then: the hook ran once, for the container, not once per attempt
	mu.Lock()
	defer mu.Unlock()
	if hookCalls != 1 {
		t.Fatalf("hook ran %d times across %d removal attempts; want 1", hookCalls, d.count("remove"))
	}
}

// A GC with no hook is left exactly as it was: the remove loop force-removes
// without a stop of its own, because every caller that schedules a removal has
// already asked for one and there is nothing to order against.
func TestNoHookMeansNoExtraStop(t *testing.T) {
	// Given: a GC with no before-remove hook
	d := newHookDaemon(t, 0)
	gc := NewGC(NewClient("tcp://"+d.srv.Listener.Addr().String(), zap.NewNop()),
		zap.NewNop(), false, fixedDomain(thisInstance))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	gc.StartRemoveLoop(ctx)

	// When: a container is scheduled for removal
	gc.ScheduleRemove("c1")
	waitForEvent(t, d, "remove")

	// Then: the daemon was asked to remove it and nothing else
	if got := d.seen(); len(got) != 1 || got[0] != "remove" {
		t.Fatalf("daemon saw %v; want just the remove", got)
	}
}
