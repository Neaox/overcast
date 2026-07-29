package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// listenOn binds a throwaway listener and returns its port. The listener is
// closed when the test ends.
func listenOn(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("atoi: %v", err)
	}
	return port
}

func TestIsReservedPort(t *testing.T) {
	// Given: the ports AGENTS.md reserves for the developer's own instance
	// When: each is classified
	// Then: only 4566 and 4567 are reserved
	cases := map[int]bool{
		4565: false,
		4566: true,
		4567: true,
		4568: false,
		4570: false,
		7777: false,
	}
	for port, want := range cases {
		if got := isReservedPort(port); got != want {
			t.Errorf("isReservedPort(%d) = %v, want %v", port, got, want)
		}
	}
}

func TestFreePortNeverReturnsReservedPorts(t *testing.T) {
	// Given: a scan base that lands directly on the reserved API port
	// When: a free port is chosen
	// Then: neither reserved port is handed out, even though the scan
	// started on one — 4566/4567 belong to the user's own instance.
	port, err := freePort(reservedAPIPort)
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	if isReservedPort(port) {
		t.Fatalf("freePort(%d) = %d, which is reserved", reservedAPIPort, port)
	}
	if port < reservedAPIPort {
		t.Fatalf("freePort(%d) = %d, want a port at or above the base", reservedAPIPort, port)
	}
}

func TestFreePortSkipsBusyPorts(t *testing.T) {
	// Given: a port that is already bound
	// When: a free port is chosen starting from it
	// Then: the busy port is skipped
	busy := listenOn(t)
	port, err := freePort(busy)
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	if port == busy {
		t.Fatalf("freePort(%d) returned the busy port", busy)
	}
	if port < busy {
		t.Fatalf("freePort(%d) = %d, want a port above the busy one", busy, port)
	}
}

func TestFreePortPairIsDistinctAndFree(t *testing.T) {
	// Given: a default scan base
	// When: an API/UI port pair is chosen
	// Then: the two differ and neither is reserved
	api, ui, err := freePortPair(defaultPortBase)
	if err != nil {
		t.Fatalf("freePortPair: %v", err)
	}
	if api == ui {
		t.Fatalf("freePortPair returned the same port twice: %d", api)
	}
	if isReservedPort(api) || isReservedPort(ui) {
		t.Fatalf("freePortPair returned a reserved port: api=%d ui=%d", api, ui)
	}
}

func TestResolveListenAddrKeepsRequestedPortWhenFree(t *testing.T) {
	// Given: a listen address whose port nothing is holding
	// When: the address is resolved
	// Then: it is returned unchanged — an explicit port is honoured
	want := fmt.Sprintf(":%d", freeEphemeral(t))
	addr, err := resolveListenAddr(want)
	if err != nil {
		t.Fatalf("resolveListenAddr: %v", err)
	}
	if addr != want {
		t.Fatalf("resolveListenAddr(%q) = %q, want it unchanged", want, addr)
	}
}

func TestResolveListenAddrNormalisesBarePort(t *testing.T) {
	// Given: a bare port number, as accepted by --port
	// When: the address is resolved
	// Then: it comes back in :port form
	addr, err := resolveListenAddr(strconv.Itoa(freeEphemeral(t)))
	if err != nil {
		t.Fatalf("resolveListenAddr: %v", err)
	}
	if !strings.HasPrefix(addr, ":") {
		t.Fatalf("resolveListenAddr returned %q, want a :port form", addr)
	}
}

// freeEphemeral returns a port that was free a moment ago.
func freeEphemeral(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	_ = ln.Close()
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("atoi: %v", err)
	}
	return port
}

func TestResolveListenAddrMovesOffBusyPort(t *testing.T) {
	// Given: a dashboard port that another process already holds
	// When: the listen address is resolved
	// Then: a different, free port is chosen rather than failing to bind
	busy := listenOn(t)
	addr, err := resolveListenAddr(fmt.Sprintf(":%d", busy))
	if err != nil {
		t.Fatalf("resolveListenAddr: %v", err)
	}
	if addr == fmt.Sprintf(":%d", busy) {
		t.Fatalf("resolveListenAddr kept the busy port %d", busy)
	}
}

func TestResolveListenAddrRefusesReservedPorts(t *testing.T) {
	// Given: a dashboard port that collides with the user's web UI
	// When: the listen address is resolved
	// Then: a non-reserved port is chosen instead
	addr, err := resolveListenAddr(":4567")
	if err != nil {
		t.Fatalf("resolveListenAddr: %v", err)
	}
	if addr == ":4567" || addr == ":4566" {
		t.Fatalf("resolveListenAddr returned reserved address %q", addr)
	}
}

func TestShouldStartOvercast(t *testing.T) {
	// Given: the three --start-overcast modes
	// When: combined with whether the caller pinned an endpoint
	// Then: "auto" defers to an explicit endpoint, the others are absolute
	cases := []struct {
		mode           string
		endpointPinned bool
		want           bool
	}{
		{startOvercastAuto, false, true},
		{startOvercastAuto, true, false},
		{startOvercastAlways, true, true},
		{startOvercastAlways, false, true},
		{startOvercastNever, false, false},
		{startOvercastNever, true, false},
	}
	for _, tc := range cases {
		got := shouldStartOvercast(tc.mode, tc.endpointPinned)
		if got != tc.want {
			t.Errorf("shouldStartOvercast(%q, %v) = %v, want %v",
				tc.mode, tc.endpointPinned, got, tc.want)
		}
	}
}

func TestOvercastEnvDisablesWebUIByDefault(t *testing.T) {
	// Given: a managed instance on a chosen API port with no UI requested
	// When: its environment is built
	// Then: the API port is set and the emulator's own web UI is disabled,
	// so it cannot bind the user's reserved 4567.
	env := overcastEnv(4570, 0)
	want := map[string]string{
		"OVERCAST_PORT":    "4570",
		"OVERCAST_UI_PORT": "0",
	}
	for key, value := range want {
		if !containsEnv(env, key+"="+value) {
			t.Errorf("overcastEnv missing %s=%s, got %v", key, value, env)
		}
	}
}

func TestOvercastEnvUsesRequestedUIPort(t *testing.T) {
	// Given: a caller that asked for the emulator's web UI
	// When: the environment is built
	// Then: the requested UI port is passed through
	env := overcastEnv(4570, 4571)
	if !containsEnv(env, "OVERCAST_UI_PORT=4571") {
		t.Errorf("overcastEnv did not carry the requested UI port: %v", env)
	}
}

func containsEnv(env []string, want string) bool {
	for _, kv := range env {
		if kv == want {
			return true
		}
	}
	return false
}

func TestWaitForHealthReturnsOnceHealthy(t *testing.T) {
	// Given: an instance that is unhealthy for its first few polls
	// When: the launcher waits for it
	// Then: it returns as soon as /_health answers 200, not on the first try
	var polls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_health" {
			t.Errorf("polled %q, want /_health", r.URL.Path)
		}
		if atomic.AddInt32(&polls, 1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := waitForHealth(context.Background(), srv.URL, 10*time.Second); err != nil {
		t.Fatalf("waitForHealth: %v", err)
	}
	if got := atomic.LoadInt32(&polls); got < 3 {
		t.Fatalf("waitForHealth returned after %d polls, want at least 3", got)
	}
}

func TestWaitForHealthTimesOut(t *testing.T) {
	// Given: an instance that never becomes healthy
	// When: the launcher waits for it
	// Then: it gives up with an error naming the endpoint, rather than hanging
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := waitForHealth(context.Background(), srv.URL, 500*time.Millisecond)
	if err == nil {
		t.Fatal("waitForHealth returned nil, want a timeout error")
	}
	if !strings.Contains(err.Error(), srv.URL) {
		t.Errorf("error %q does not name the endpoint %q", err, srv.URL)
	}
}

func TestWaitForHealthStopsOnCancel(t *testing.T) {
	// Given: a cancelled context
	// When: the launcher waits
	// Then: it returns promptly instead of polling out the full timeout
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForHealth(ctx, srv.URL, time.Minute); err == nil {
		t.Fatal("waitForHealth returned nil for a cancelled context")
	}
}

func TestBrowserCommandIsNonEmpty(t *testing.T) {
	// Given: a dashboard URL
	// When: the platform's open command is built
	// Then: an executable and the URL are present
	argv := browserCommand("http://localhost:7777")
	if len(argv) == 0 {
		t.Fatal("browserCommand returned no argv")
	}
	if !strings.Contains(strings.Join(argv, " "), "http://localhost:7777") {
		t.Fatalf("browserCommand argv does not mention the URL: %v", argv)
	}
}
