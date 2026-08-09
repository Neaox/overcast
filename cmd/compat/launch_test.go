package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// listenOn binds a throwaway listener inside the scanner's own window and
// returns its port. An ephemeral :0 port won't do: the Windows dynamic range
// runs to 65535, so it can land at or above portScanLimit, where freePort has
// no room left to scan above it and the tests built on this fixture flake.
// The listener is closed when the test ends. Loopback only: a wildcard bind
// makes Windows Firewall prompt for the test binary.
func listenOn(t *testing.T) int {
	t.Helper()
	for port := defaultPortBase; port < portScanLimit; port++ {
		if isReservedPort(port) {
			continue
		}
		ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			continue // held by someone else — keep scanning
		}
		t.Cleanup(func() { _ = ln.Close() })
		return port
	}
	t.Fatal("no bindable port in the scan window")
	return 0
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

func TestFreePortPairRefusesReservedPortsInEitherRole(t *testing.T) {
	// Given: scan bases that put a reserved port in the API slot, the UI slot,
	// or both
	// When: an API/UI port pair is chosen
	// Then: neither role gets 4566 or 4567. The guard is easy to write per
	// role — comparing the API port against 4566 and the UI port against 4567
	// only — and that shape hands the UI the user's API port for a base of
	// 4565. isReservedPort is role-blind, which is what keeps that bug out of
	// here; this test is what would catch it coming back.
	for _, base := range []int{4565, reservedAPIPort, reservedUIPort} {
		api, ui, err := freePortPair(base)
		if err != nil {
			t.Fatalf("freePortPair(%d): %v", base, err)
		}
		if isReservedPort(api) {
			t.Errorf("freePortPair(%d) gave the API role reserved port %d", base, api)
		}
		if isReservedPort(ui) {
			t.Errorf("freePortPair(%d) gave the UI role reserved port %d", base, ui)
		}
	}
}

func TestDockerRunArgsPublishesToLoopbackOnly(t *testing.T) {
	// Given: a managed container instance with its own web UI
	// When: the docker argv is built with no bridge gateway to add
	// Then: every mapping is bound to 127.0.0.1. A bare "<host>:<container>"
	// mapping binds every interface, which puts an unauthenticated emulator on
	// whatever network the machine is attached to.
	argv := dockerRunArgs(4570, 4571, "ghcr.io/neaox/overcast:alpha", "warn", "")
	want := []string{
		"127.0.0.1:4570:" + strconv.Itoa(reservedAPIPort),
		"127.0.0.1:4571:" + strconv.Itoa(reservedUIPort),
	}
	if got := publishMappings(argv); !slices.Equal(got, want) {
		t.Errorf("dockerRunArgs published %v, want %v", got, want)
	}
}

func TestDockerRunArgsOmitsUIPublishWhenDisabled(t *testing.T) {
	// Given: a managed instance whose own web UI is disabled (the default)
	// When: the docker argv is built
	// Then: only the API port is published, so nothing can bind the user's 4567
	argv := dockerRunArgs(4570, 0, "img", "warn", "")
	want := []string{"127.0.0.1:4570:" + strconv.Itoa(reservedAPIPort)}
	if got := publishMappings(argv); !slices.Equal(got, want) {
		t.Errorf("dockerRunArgs published %v, want %v", got, want)
	}
}

func TestDockerRunArgsAddsBridgeGatewayPublish(t *testing.T) {
	// Given: a Docker bridge gateway address
	// When: the docker argv is built
	// Then: each port is published on loopback *and* on that gateway, which is
	// what "host.docker.internal:host-gateway" resolves to on native Linux —
	// compat/suites/rust-sdk/run.sh reaches the emulator that way, and a
	// loopback-only publish is invisible to it.
	argv := dockerRunArgs(4570, 4571, "img", "warn", "172.17.0.1")
	want := []string{
		"127.0.0.1:4570:" + strconv.Itoa(reservedAPIPort),
		"172.17.0.1:4570:" + strconv.Itoa(reservedAPIPort),
		"127.0.0.1:4571:" + strconv.Itoa(reservedUIPort),
		"172.17.0.1:4571:" + strconv.Itoa(reservedUIPort),
	}
	if got := publishMappings(argv); !slices.Equal(got, want) {
		t.Errorf("dockerRunArgs published %v, want %v", got, want)
	}
}

func TestDockerRunArgsCarriesImageAndEnvironment(t *testing.T) {
	// Given: an image and a log level
	// When: the docker argv is built
	// Then: the image is the last argument (so nothing is mistaken for a flag)
	// and the instance is told to keep its state in memory
	argv := dockerRunArgs(4570, 0, "ghcr.io/neaox/overcast:alpha", "debug", "")
	if last := argv[len(argv)-1]; last != "ghcr.io/neaox/overcast:alpha" {
		t.Errorf("dockerRunArgs ended with %q, want the image", last)
	}
	joined := strings.Join(argv, " ")
	for _, want := range []string{"OVERCAST_STATE=memory", "OVERCAST_LOG_LEVEL=debug"} {
		if !strings.Contains(joined, want) {
			t.Errorf("dockerRunArgs argv %v is missing %q", argv, want)
		}
	}
}

// publishMappings returns the value of every -p argument, in order.
func publishMappings(argv []string) []string {
	var out []string
	for i, arg := range argv {
		if arg == "-p" && i+1 < len(argv) {
			out = append(out, argv[i+1])
		}
	}
	return out
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
	ln, err := net.Listen("tcp", "127.0.0.1:0")
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
	env := overcastEnv(4570, 0, bindHosts(""))
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
	env := overcastEnv(4570, 4571, bindHosts(""))
	if !containsEnv(env, "OVERCAST_UI_PORT=4571") {
		t.Errorf("overcastEnv did not carry the requested UI port: %v", env)
	}
}

func TestOvercastEnvBindsLoopbackOnly(t *testing.T) {
	// Given: a managed native instance with no bridge gateway to add
	// When: its environment is built
	// Then: it is told to listen on 127.0.0.1 and nothing else. This is the
	// binary path's half of what TestDockerRunArgsPublishesToLoopbackOnly
	// pins for the container path, and it is the half that matters most:
	// findOvercastBinary means the binary is what compat picks when one is
	// built, so an unset OVERCAST_HOST — the emulator's own default is
	// 0.0.0.0 — would put an unauthenticated instance on whatever network
	// the machine is attached to, in the *common* case rather than the
	// fallback one.
	env := overcastEnv(4570, 0, bindHosts(""))
	if !containsEnv(env, "OVERCAST_HOST=127.0.0.1") {
		t.Errorf("overcastEnv did not pin the bind address to loopback: %v", env)
	}
}

func TestOvercastEnvAddsBridgeGatewayBind(t *testing.T) {
	// Given: a Docker bridge gateway address
	// When: the environment is built
	// Then: the instance listens on loopback *and* on that gateway, loopback
	// first. Same address set, same order, and for the same reason as the
	// container path's -p mappings: it is what
	// "host.docker.internal:host-gateway" resolves to on native Linux, and
	// compat/suites/rust-sdk/run.sh reaches the emulator that way.
	env := overcastEnv(4570, 0, bindHosts("172.17.0.1"))
	if !containsEnv(env, "OVERCAST_HOST=127.0.0.1,172.17.0.1") {
		t.Errorf("overcastEnv did not add the bridge gateway: %v", env)
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
