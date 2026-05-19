package mdns

import (
	"context"
	"net"
	"os/exec"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestRecordKey verifies the dedup key is the hostname so that two records
// with the same hostname but different IPs compare equal (replace semantics).
func TestRecordKey(t *testing.T) {
	a := Record{Hostname: "api.myapp.local", IP: net.IPv4(127, 0, 0, 1)}
	b := Record{Hostname: "api.myapp.local", IP: net.IPv4(10, 0, 0, 1)}
	if a.Key() != b.Key() {
		t.Fatalf("records with the same hostname but different IPs must share a key: %q vs %q", a.Key(), b.Key())
	}
}

// fakeCmdFactory returns a factory that spawns `sleep` processes — long-
// lived enough that procPublisher behaves as it would with a real dns-sd
// or avahi-publish subprocess, but with zero platform dependencies.
func fakeCmdFactory(t *testing.T) cmdFactory {
	t.Helper()
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep not available on this host")
	}
	return func(ctx context.Context, _ Record) *exec.Cmd {
		return exec.CommandContext(ctx, "sleep", "30")
	}
}

// Given an empty publisher, When Publish is called, Then the record is
// tracked as active and the subprocess is running.
func TestProcPublisher_Publish_TracksActive(t *testing.T) {
	t.Parallel()
	p := newProcPublisher(zap.NewNop(), fakeCmdFactory(t))
	t.Cleanup(func() { _ = p.Close() })

	r := Record{Hostname: "api.myapp.local", IP: net.IPv4(127, 0, 0, 1)}
	if err := p.Publish(context.Background(), r); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	p.mu.Lock()
	_, ok := p.active[r.Key()]
	p.mu.Unlock()
	if !ok {
		t.Fatalf("record not found in active set after Publish")
	}
}

// Given an already-published record, When Publish is called again with a
// different IP, Then the old subprocess is killed and the new one replaces it.
func TestProcPublisher_Publish_Replace(t *testing.T) {
	t.Parallel()
	p := newProcPublisher(zap.NewNop(), fakeCmdFactory(t))
	t.Cleanup(func() { _ = p.Close() })

	r1 := Record{Hostname: "api.myapp.local", IP: net.IPv4(127, 0, 0, 1)}
	r2 := Record{Hostname: "api.myapp.local", IP: net.IPv4(10, 0, 0, 1)}

	if err := p.Publish(context.Background(), r1); err != nil {
		t.Fatalf("Publish r1: %v", err)
	}
	p.mu.Lock()
	first := p.active[r1.Key()].cmd
	p.mu.Unlock()

	if err := p.Publish(context.Background(), r2); err != nil {
		t.Fatalf("Publish r2: %v", err)
	}
	p.mu.Lock()
	second := p.active[r2.Key()].cmd
	p.mu.Unlock()

	if first == second {
		t.Fatalf("expected a new subprocess on replace, got the same *exec.Cmd")
	}
	// Old process must have exited (its ctx was cancelled).
	waitDone := make(chan struct{})
	go func() { _ = first.Wait(); close(waitDone) }()
	select {
	case <-waitDone:
	case <-time.After(2 * time.Second):
		t.Fatalf("previous subprocess still running after replace")
	}
}

// Given a published record, When Unpublish is called, Then the record is
// removed from the active set.
func TestProcPublisher_Unpublish_Removes(t *testing.T) {
	t.Parallel()
	p := newProcPublisher(zap.NewNop(), fakeCmdFactory(t))
	t.Cleanup(func() { _ = p.Close() })

	r := Record{Hostname: "api.myapp.local", IP: net.IPv4(127, 0, 0, 1)}
	if err := p.Publish(context.Background(), r); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := p.Unpublish(context.Background(), r); err != nil {
		t.Fatalf("Unpublish: %v", err)
	}
	p.mu.Lock()
	_, ok := p.active[r.Key()]
	p.mu.Unlock()
	if ok {
		t.Fatalf("record still in active set after Unpublish")
	}
}

// Given an empty publisher, When Unpublish is called for an unknown record,
// Then no error is returned.
func TestProcPublisher_Unpublish_Unknown_NoOp(t *testing.T) {
	t.Parallel()
	p := newProcPublisher(zap.NewNop(), fakeCmdFactory(t))
	t.Cleanup(func() { _ = p.Close() })

	r := Record{Hostname: "ghost.myapp.local", IP: net.IPv4(127, 0, 0, 1)}
	if err := p.Unpublish(context.Background(), r); err != nil {
		t.Fatalf("Unpublish unknown: %v", err)
	}
}

// Given multiple active records, When Close is called, Then all
// subprocesses are torn down and the publisher rejects further Publish.
func TestProcPublisher_Close_TearsDownAll(t *testing.T) {
	t.Parallel()
	p := newProcPublisher(zap.NewNop(), fakeCmdFactory(t))

	records := []Record{
		{Hostname: "a.myapp.local", IP: net.IPv4(127, 0, 0, 1)},
		{Hostname: "b.myapp.local", IP: net.IPv4(127, 0, 0, 1)},
		{Hostname: "c.myapp.local", IP: net.IPv4(127, 0, 0, 1)},
	}
	for _, r := range records {
		if err := p.Publish(context.Background(), r); err != nil {
			t.Fatalf("Publish %s: %v", r.Hostname, err)
		}
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(p.active) != 0 {
		t.Fatalf("expected empty active set after Close, got %d", len(p.active))
	}
	if err := p.Publish(context.Background(), records[0]); err == nil {
		t.Fatalf("expected Publish to fail after Close")
	}
	// Double-close is a no-op.
	if err := p.Close(); err != nil {
		t.Fatalf("double Close: %v", err)
	}
}
