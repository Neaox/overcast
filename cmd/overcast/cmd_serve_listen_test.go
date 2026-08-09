package main

import (
	"net"
	"strings"
	"testing"
)

// freeLoopbackAddr returns a loopback address that was free a moment ago.
func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return addr
}

func TestListenAllBindsEveryAddress(t *testing.T) {
	// Given: two addresses, as OVERCAST_HOST produces when it names more than
	// one — loopback plus a second address the same server should answer on
	// When: they are bound
	// Then: both are listening, and the first is first, since callers read
	// lns[0] as "the" address (the bridge proxy, the startup banner)
	addrs := []string{freeLoopbackAddr(t), freeLoopbackAddr(t)}
	lns, err := listenAll(addrs)
	if err != nil {
		t.Fatalf("listenAll: %v", err)
	}
	t.Cleanup(func() {
		for _, ln := range lns {
			_ = ln.Close()
		}
	})
	if len(lns) != len(addrs) {
		t.Fatalf("listenAll returned %d listeners, want %d", len(lns), len(addrs))
	}
	for i, ln := range lns {
		if got := ln.Addr().String(); got != addrs[i] {
			t.Errorf("listener %d is on %q, want %q", i, got, addrs[i])
		}
	}
}

func TestListenAllReleasesOpenedListenersOnFailure(t *testing.T) {
	// Given: a second address that is already taken
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer taken.Close() //nolint:errcheck
	first := freeLoopbackAddr(t)

	// When: both are bound
	lns, err := listenAll([]string{first, taken.Addr().String()})

	// Then: it fails, naming the address that could not be bound…
	if err == nil {
		for _, ln := range lns {
			_ = ln.Close()
		}
		t.Fatal("listenAll succeeded on an address already in use")
	}
	if !strings.Contains(err.Error(), taken.Addr().String()) {
		t.Errorf("error %q does not name the address that failed", err)
	}

	// …and the one it did bind is released rather than held by a daemon that
	// is about to exit, which would fail the next start for a stale reason.
	reclaimed, err := net.Listen("tcp", first)
	if err != nil {
		t.Fatalf("first address %s still held after a failed listenAll: %v", first, err)
	}
	_ = reclaimed.Close()
}

func TestListenAllOnASingleAddress(t *testing.T) {
	// Given: one address, which is what every configuration but the compat
	// launcher's produces
	// When: it is bound
	// Then: one listener, on that address — the pre-list behaviour, unchanged
	addr := freeLoopbackAddr(t)
	lns, err := listenAll([]string{addr})
	if err != nil {
		t.Fatalf("listenAll: %v", err)
	}
	t.Cleanup(func() { _ = lns[0].Close() })
	if len(lns) != 1 {
		t.Fatalf("listenAll returned %d listeners, want 1", len(lns))
	}
	if got := lns[0].Addr().String(); got != addr {
		t.Errorf("listener is on %q, want %q", got, addr)
	}
}
