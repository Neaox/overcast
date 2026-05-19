package domainregistry

import (
	"context"
	"sync"
	"testing"
	"time"
)

func rec(name, source string) Record { return Record{Name: name, Source: source} }

// drain reads up to n events from ch, failing the test if they don't
// arrive within 2 seconds.
func drain(t *testing.T, ch <-chan Event, n int) []Event {
	t.Helper()
	out := make([]Event, 0, n)
	deadline := time.After(2 * time.Second)
	for len(out) < n {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("channel closed after %d events, expected %d", len(out), n)
			}
			out = append(out, ev)
		case <-deadline:
			t.Fatalf("timed out after %d events, expected %d", len(out), n)
		}
	}
	return out
}

// Given an empty registry with an active watcher, When Put is called,
// Then the watcher sees exactly one EventAdded.
func TestRegistry_Put_EmitsAdded(t *testing.T) {
	t.Parallel()
	r := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := r.Watch(ctx)
	r.Put(rec("api.myapp.local", "apigateway.v1"))

	events := drain(t, ch, 1)
	if events[0].Type != EventAdded || events[0].Record.Name != "api.myapp.local" {
		t.Fatalf("unexpected event: %#v", events[0])
	}
}

// Given a record already in the registry, When Put is called with a
// byte-identical record, Then no event is emitted (idempotence).
func TestRegistry_Put_IdenticalIsNoOp(t *testing.T) {
	t.Parallel()
	r := New()
	r.Put(rec("api.myapp.local", "apigateway.v1"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := r.Watch(ctx)
	drain(t, ch, 1) // snapshot

	r.Put(rec("api.myapp.local", "apigateway.v1"))

	select {
	case ev := <-ch:
		t.Fatalf("expected no event for identical Put, got %#v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

// Given a record already in the registry, When Put is called with the
// same name but a different source, Then EventAdded is emitted (replace).
func TestRegistry_Put_ReplaceDifferentSource(t *testing.T) {
	t.Parallel()
	r := New()
	r.Put(rec("api.myapp.local", "apigateway.v1"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := r.Watch(ctx)
	drain(t, ch, 1) // snapshot

	r.Put(rec("api.myapp.local", "cloudfront"))
	events := drain(t, ch, 1)
	if events[0].Type != EventAdded || events[0].Record.Source != "cloudfront" {
		t.Fatalf("expected EventAdded with cloudfront source, got %#v", events[0])
	}
}

// Given an active record, When Delete is called, Then EventRemoved is
// emitted and Snapshot reflects the removal.
func TestRegistry_Delete_EmitsRemoved(t *testing.T) {
	t.Parallel()
	r := New()
	r.Put(rec("api.myapp.local", "apigateway.v1"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := r.Watch(ctx)
	drain(t, ch, 1) // snapshot

	r.Delete("api.myapp.local")
	events := drain(t, ch, 1)
	if events[0].Type != EventRemoved {
		t.Fatalf("expected EventRemoved, got %#v", events[0])
	}
	if got := len(r.Snapshot()); got != 0 {
		t.Fatalf("expected empty snapshot after delete, got %d", got)
	}
}

// Given an empty registry, When Delete is called on an unknown name,
// Then no event is emitted and no panic occurs.
func TestRegistry_Delete_Unknown_NoOp(t *testing.T) {
	t.Parallel()
	r := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := r.Watch(ctx)

	r.Delete("ghost.myapp.local")
	select {
	case ev := <-ch:
		t.Fatalf("expected no event for unknown delete, got %#v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

// Given records already in the registry, When Watch is called, Then the
// new watcher receives every existing record as EventAdded before any
// live updates.
func TestRegistry_Watch_ReplaysSnapshot(t *testing.T) {
	t.Parallel()
	r := New()
	r.Put(rec("a.myapp.local", "apigateway.v1"))
	r.Put(rec("b.myapp.local", "apigateway.v1"))
	r.Put(rec("c.myapp.local", "cloudfront"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := r.Watch(ctx)

	events := drain(t, ch, 3)
	seen := map[string]bool{}
	for _, ev := range events {
		if ev.Type != EventAdded {
			t.Fatalf("snapshot events must be EventAdded, got %v", ev.Type)
		}
		seen[ev.Record.Name] = true
	}
	for _, n := range []string{"a.myapp.local", "b.myapp.local", "c.myapp.local"} {
		if !seen[n] {
			t.Fatalf("snapshot missing %q", n)
		}
	}
}

// Given an active watcher, When its context is cancelled, Then the
// channel is closed and the registry drops the subscription.
func TestRegistry_Watch_CancelClosesChannel(t *testing.T) {
	t.Parallel()
	r := New()
	ctx, cancel := context.WithCancel(context.Background())
	ch := r.Watch(ctx)

	cancel()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // closed — success
			}
		case <-deadline:
			t.Fatalf("channel was not closed after context cancel")
		}
	}
}

// Given several concurrent watchers, When Put is called, Then every
// watcher receives the event exactly once.
func TestRegistry_MultipleWatchers_AllReceive(t *testing.T) {
	t.Parallel()
	r := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const n = 5
	chans := make([]<-chan Event, n)
	for i := range chans {
		chans[i] = r.Watch(ctx)
	}

	r.Put(rec("api.myapp.local", "apigateway.v1"))

	var wg sync.WaitGroup
	wg.Add(n)
	for _, ch := range chans {
		go func(ch <-chan Event) {
			defer wg.Done()
			drain(t, ch, 1)
		}(ch)
	}
	wg.Wait()
}

// Given a watcher that never reads, When many Puts arrive, Then writers
// are not blocked and events are silently dropped for that watcher.
func TestRegistry_SlowWatcher_DoesntBlockWrites(t *testing.T) {
	t.Parallel()
	r := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = r.Watch(ctx) // never read

	done := make(chan struct{})
	go func() {
		for i := 0; i < 10_000; i++ {
			r.Put(rec("api-"+intS(i)+".myapp.local", "apigateway.v1"))
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("writer blocked on slow watcher after 5s")
	}
}

// intS is a tiny helper to avoid pulling strconv into the test file for
// a single use. It handles positive ints only, which is all the test needs.
func intS(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
