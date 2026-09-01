package main

import (
	"net"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/overcast-sh/overcast/internal/config"
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
	// Given: two addresses, as OVERCAST_LISTEN produces when it names more than
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

// TestLogListenResolution_namesTheAddressAndTheVariable verifies the startup
// log line (#870 §2 / #761's decision-free item) names the resolved bind
// address(es) and points at OVERCAST_LISTEN as the way to change it — the
// bind-address analogue of logStoreMode's storage-mode line.
func TestLogListenResolution_namesTheAddressAndTheVariable(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)
	cfg := &config.Config{Host: "127.0.0.1", Port: 4566, Hosts: []string{"127.0.0.1", "172.17.0.1"}}

	logListenResolution(logger, cfg)

	entries := logs.FilterMessageSnippet("set OVERCAST_LISTEN to change this").All()
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 listen-resolution log line, got %d", len(entries))
	}
	entry := entries[0]
	if !strings.Contains(entry.Message, "127.0.0.1:4566") || !strings.Contains(entry.Message, "172.17.0.1:4566") {
		t.Errorf("log message %q does not name both bound addresses", entry.Message)
	}
	got, _ := entry.ContextMap()["addrs"].([]interface{})
	if len(got) != 2 {
		t.Errorf("addrs field: expected 2 entries, got %v", got)
	}
}

// TestLogListenResolution_namesTheReasonWhenDefaulted verifies the #761
// extension: when the bind address was defaulted rather than set explicitly,
// the log line also says why — containerised or native — since that default
// is now environment-dependent.
func TestLogListenResolution_namesTheReasonWhenDefaulted(t *testing.T) {
	for _, tc := range []struct {
		name   string
		signal string
		reason string
	}{
		{name: "containerised", signal: "containerised", reason: "containerised (OVERCAST_DATA_DIR_SOURCE=image)"},
		{name: "native", signal: "native", reason: "native — loopback only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.DebugLevel)
			logger := zap.New(core)
			cfg := &config.Config{
				Host: "127.0.0.1", Port: 4566, Hosts: []string{"127.0.0.1"},
				ListenSource:     config.ListenSourceAuto,
				ListenAutoSignal: tc.signal,
				ListenAutoReason: tc.reason,
			}

			logListenResolution(logger, cfg)

			entries := logs.FilterMessageSnippet("set OVERCAST_LISTEN to change this").All()
			if len(entries) != 1 {
				t.Fatalf("expected exactly 1 listen-resolution log line, got %d", len(entries))
			}
			entry := entries[0]
			if !strings.Contains(entry.Message, tc.reason) {
				t.Errorf("log message %q does not name the reason %q", entry.Message, tc.reason)
			}
			if got := entry.ContextMap()["listenAutoSignal"]; got != tc.signal {
				t.Errorf("listenAutoSignal field: expected %q, got %v", tc.signal, got)
			}
		})
	}
}

// TestLogListenResolution_explicitOmitsTheReason verifies an explicitly
// configured bind address gets the plain message, with no "(default: ...)"
// clause and no listenAutoSignal field — there is nothing to explain when
// the operator set it themselves.
func TestLogListenResolution_explicitOmitsTheReason(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)
	cfg := &config.Config{
		Host: "127.0.0.1", Port: 4566, Hosts: []string{"127.0.0.1"},
		ListenSource: config.ListenSourceExplicit,
	}

	logListenResolution(logger, cfg)

	entries := logs.FilterMessageSnippet("set OVERCAST_LISTEN to change this").All()
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 listen-resolution log line, got %d", len(entries))
	}
	entry := entries[0]
	if strings.Contains(entry.Message, "default:") {
		t.Errorf("log message %q should not explain a default when the value was explicit", entry.Message)
	}
	if _, ok := entry.ContextMap()["listenAutoSignal"]; ok {
		t.Errorf("listenAutoSignal field should be absent when explicit, got %v", entry.ContextMap())
	}
}

// TestPublishedPortMismatchWarning covers the environment-preflight "ports"
// instance (deploy-failure-diagnosis.md W4): the message resolvePublishedPort
// logs when Overcast's container remaps its API port. It must name both
// ports, say what containerendpoint's rewriting already covers, and name the
// one thing it cannot (a value compared rather than dialed, e.g. a Cognito
// token's iss) with a concrete fix.
func TestPublishedPortMismatchWarning(t *testing.T) {
	msg := publishedPortMismatchWarning(4566, 4580)

	for _, want := range []string{"4566", "4580", "publish 1:1", "Cognito"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message = %q, expected it to mention %q", msg, want)
		}
	}
}
