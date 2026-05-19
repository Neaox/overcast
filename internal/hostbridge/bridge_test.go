package hostbridge

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"

	"github.com/Neaox/overcast/internal/hostbridge/mdns"
)

// fakePublisher records every call it receives so tests can assert the
// exact sequence of Publish / Unpublish operations the bridge performed.
type fakePublisher struct {
	mu        sync.Mutex
	published []mdns.Record
	withdrawn []mdns.Record
	closed    bool

	// publishErr, unpublishErr let individual tests force a failure.
	publishErr   error
	unpublishErr error
}

func (f *fakePublisher) Publish(_ context.Context, r mdns.Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.publishErr != nil {
		return f.publishErr
	}
	f.published = append(f.published, r)
	return nil
}

func (f *fakePublisher) Unpublish(_ context.Context, r mdns.Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.unpublishErr != nil {
		return f.unpublishErr
	}
	f.withdrawn = append(f.withdrawn, r)
	return nil
}

func (f *fakePublisher) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakePublisher) snapshot() ([]mdns.Record, []mdns.Record, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pub := append([]mdns.Record(nil), f.published...)
	unp := append([]mdns.Record(nil), f.withdrawn...)
	return pub, unp, f.closed
}

// chanSource is a Source backed by a caller-controlled channel.
type chanSource struct {
	ch       chan Event
	watchErr error
}

func newChanSource() *chanSource {
	return &chanSource{ch: make(chan Event, 16)}
}

func (c *chanSource) Watch(_ context.Context) (<-chan Event, error) {
	if c.watchErr != nil {
		return nil, c.watchErr
	}
	return c.ch, nil
}

func rec(host, ip string) mdns.Record {
	return mdns.Record{Hostname: host, IP: net.ParseIP(ip)}
}

// Given a fresh bridge, When an Added event arrives, Then the publisher
// sees exactly one Publish call and the active set reflects it.
func TestBridge_Add_Publishes(t *testing.T) {
	t.Parallel()

	pub := &fakePublisher{}
	src := newChanSource()
	b := New(pub, src, zaptest.NewLogger(t))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := runAsync(t, b, ctx)

	src.ch <- Event{Type: EventAdded, Record: rec("api.myapp.local", "127.0.0.1")}
	waitFor(t, func() bool {
		p, _, _ := pub.snapshot()
		return len(p) == 1
	})

	cancel()
	<-done

	p, _, closed := pub.snapshot()
	if len(p) != 1 || p[0].Hostname != "api.myapp.local" {
		t.Fatalf("unexpected Publish calls: %#v", p)
	}
	if !closed {
		t.Fatalf("publisher was not closed on shutdown")
	}
}

// Given an existing advertisement, When the same hostname is added with a
// different IP, Then the bridge withdraws the old record and publishes the
// new one (replace semantics).
func TestBridge_Add_ReplaceDifferentIP(t *testing.T) {
	t.Parallel()

	pub := &fakePublisher{}
	src := newChanSource()
	b := New(pub, src, zaptest.NewLogger(t))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runAsync(t, b, ctx)

	src.ch <- Event{Type: EventAdded, Record: rec("api.myapp.local", "127.0.0.1")}
	src.ch <- Event{Type: EventAdded, Record: rec("api.myapp.local", "10.0.0.1")}

	waitFor(t, func() bool {
		p, u, _ := pub.snapshot()
		return len(p) == 2 && len(u) >= 1
	})

	cancel()
	<-done

	p, _, _ := pub.snapshot()
	if len(p) != 2 {
		t.Fatalf("expected 2 publishes, got %d", len(p))
	}
	if !p[1].IP.Equal(net.ParseIP("10.0.0.1")) {
		t.Fatalf("second publish should be 10.0.0.1, got %v", p[1].IP)
	}
}

// Given an existing advertisement, When the same record is added again
// verbatim, Then the publisher is not called a second time.
func TestBridge_Add_IdempotentSameIP(t *testing.T) {
	t.Parallel()

	pub := &fakePublisher{}
	src := newChanSource()
	b := New(pub, src, zaptest.NewLogger(t))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runAsync(t, b, ctx)

	src.ch <- Event{Type: EventAdded, Record: rec("api.myapp.local", "127.0.0.1")}
	waitFor(t, func() bool { p, _, _ := pub.snapshot(); return len(p) == 1 })
	src.ch <- Event{Type: EventAdded, Record: rec("api.myapp.local", "127.0.0.1")}

	// Give the bridge a moment to (not) act on the duplicate.
	time.Sleep(20 * time.Millisecond)

	cancel()
	<-done

	p, _, _ := pub.snapshot()
	if len(p) != 1 {
		t.Fatalf("expected 1 publish for idempotent add, got %d: %#v", len(p), p)
	}
}

// Given an active record, When a Removed event arrives, Then the bridge
// withdraws it and drops it from the active set.
func TestBridge_Remove_Withdraws(t *testing.T) {
	t.Parallel()

	pub := &fakePublisher{}
	src := newChanSource()
	b := New(pub, src, zaptest.NewLogger(t))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runAsync(t, b, ctx)

	src.ch <- Event{Type: EventAdded, Record: rec("api.myapp.local", "127.0.0.1")}
	waitFor(t, func() bool { p, _, _ := pub.snapshot(); return len(p) == 1 })
	src.ch <- Event{Type: EventRemoved, Record: rec("api.myapp.local", "127.0.0.1")}
	waitFor(t, func() bool { _, u, _ := pub.snapshot(); return len(u) == 1 })

	if got := len(b.Active()); got != 0 {
		t.Fatalf("expected empty active set after remove, got %d", got)
	}

	cancel()
	<-done
}

// Given no record was ever added, When a Removed event arrives, Then the
// bridge silently ignores it.
func TestBridge_Remove_Unknown_NoOp(t *testing.T) {
	t.Parallel()

	pub := &fakePublisher{}
	src := newChanSource()
	b := New(pub, src, zaptest.NewLogger(t))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runAsync(t, b, ctx)

	src.ch <- Event{Type: EventRemoved, Record: rec("ghost.myapp.local", "127.0.0.1")}
	time.Sleep(20 * time.Millisecond)

	cancel()
	<-done

	_, u, _ := pub.snapshot()
	if len(u) != 0 {
		t.Fatalf("expected no unpublish calls for unknown remove, got %d", len(u))
	}
}

// Given several active records, When the context is cancelled, Then Run
// returns and every active record is withdrawn and the publisher is closed.
func TestBridge_ShutdownWithdrawsEverything(t *testing.T) {
	t.Parallel()

	pub := &fakePublisher{}
	src := newChanSource()
	b := New(pub, src, zaptest.NewLogger(t))

	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(t, b, ctx)

	src.ch <- Event{Type: EventAdded, Record: rec("a.myapp.local", "127.0.0.1")}
	src.ch <- Event{Type: EventAdded, Record: rec("b.myapp.local", "127.0.0.1")}
	waitFor(t, func() bool { p, _, _ := pub.snapshot(); return len(p) == 2 })

	cancel()
	<-done

	_, u, closed := pub.snapshot()
	if len(u) != 2 {
		t.Fatalf("expected 2 withdrawals on shutdown, got %d", len(u))
	}
	if !closed {
		t.Fatalf("publisher was not closed on shutdown")
	}
}

// Given the source reports an error from Watch, When Run is called, Then
// Run returns a wrapped error without publishing anything.
func TestBridge_SourceWatchError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("source boom")
	pub := &fakePublisher{}
	src := &chanSource{watchErr: sentinel}
	b := New(pub, src, zap.NewNop())

	err := b.Run(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected wrapped sentinel, got %v", err)
	}
}

// runAsync starts b.Run in a goroutine and returns a channel that is
// closed when Run returns. The test owns the context and cancels it to
// trigger shutdown.
func runAsync(t *testing.T, b *Bridge, ctx context.Context) <-chan struct{} {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = b.Run(ctx)
	}()
	return done
}

// waitFor spins on cond for up to 2 seconds, failing the test on timeout.
// It exists because the bridge processes events asynchronously, so tests
// cannot assume a publish has landed the instant they sent the event.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("condition not met within 2s")
}
