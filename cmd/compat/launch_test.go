package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
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
	argv := dockerRunArgs(4570, 4571, "ghcr.io/neaox/overcast:alpha", "warn", "", "")
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
	argv := dockerRunArgs(4570, 0, "img", "warn", "", "")
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
	argv := dockerRunArgs(4570, 4571, "img", "warn", "172.17.0.1", "")
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
	argv := dockerRunArgs(4570, 0, "ghcr.io/neaox/overcast:alpha", "debug", "", "")
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

func TestChooseOvercastArtifactPrefersARequestedImageOverALocalBinary(t *testing.T) {
	// Given: the caller named an image, and a binary is also sitting in bin/
	// When: the artifact is chosen
	// Then: the image wins. This is issue #801: binary discovery used to run
	// first unconditionally, so testing a release candidate with
	// --overcast-image silently ran yesterday's bin/overcast.exe instead and
	// the run looked exactly like one that had tested the image.
	got, err := chooseOvercastArtifact(artifactRequest{
		Image:           "ghcr.io/neaox/overcast:0.0.1-alpha.33-rc.1",
		ImageRequested:  true,
		FoundBin:        `F:\dev\overcast\bin\overcast.exe`,
		DockerAvailable: true,
	})
	if err != nil {
		t.Fatalf("chooseOvercastArtifact: %v", err)
	}
	if got.Mode != overcastModeDocker {
		t.Fatalf("chose %s %q, want the requested container image", got.Mode, got.Ref)
	}
	if got.Ref != "ghcr.io/neaox/overcast:0.0.1-alpha.33-rc.1" {
		t.Errorf("chose image %q, want the one that was named", got.Ref)
	}
	// And: the binary that was passed over is named, so a reader can tell this
	// run apart from one where no binary existed at all.
	if !strings.Contains(got.Ignored, `bin\overcast.exe`) {
		t.Errorf("Ignored = %q, want it to name the local binary that was passed over", got.Ignored)
	}
}

func TestChooseOvercastArtifactUsesALocalBinaryWhenNoImageWasNamed(t *testing.T) {
	// Given: no image flag, and a binary in bin/
	// When: the artifact is chosen
	// Then: the binary is used, exactly as before — the default image is a
	// fallback and must not start outranking a local build.
	got, err := chooseOvercastArtifact(artifactRequest{
		Image:           defaultOvercastImage,
		FoundBin:        "/repo/bin/overcast",
		DockerAvailable: true,
	})
	if err != nil {
		t.Fatalf("chooseOvercastArtifact: %v", err)
	}
	if got.Mode != overcastModeBinary || got.Ref != "/repo/bin/overcast" {
		t.Fatalf("chose %s %q, want the local binary", got.Mode, got.Ref)
	}
	if got.Ignored != "" {
		t.Errorf("Ignored = %q, want nothing reported as passed over", got.Ignored)
	}
}

func TestChooseOvercastArtifactUsesARequestedImageWithNoBinaryPresent(t *testing.T) {
	// Given: an image was named and nothing is built locally
	// When: the artifact is chosen
	// Then: the image is used, and the reason says the caller asked for it
	// rather than that nothing else was available — the two are different runs
	// and the log should not conflate them.
	got, err := chooseOvercastArtifact(artifactRequest{
		Image:           "ghcr.io/neaox/overcast:0.0.1-alpha.32",
		ImageRequested:  true,
		DockerAvailable: true,
	})
	if err != nil {
		t.Fatalf("chooseOvercastArtifact: %v", err)
	}
	if got.Mode != overcastModeDocker || got.Ref != "ghcr.io/neaox/overcast:0.0.1-alpha.32" {
		t.Fatalf("chose %s %q, want the requested image", got.Mode, got.Ref)
	}
	if !strings.Contains(got.Reason, "--overcast-image") {
		t.Errorf("Reason = %q, want it to credit the flag", got.Reason)
	}
	if got.Ignored != "" {
		t.Errorf("Ignored = %q, want nothing reported as passed over", got.Ignored)
	}
}

func TestChooseOvercastArtifactFallsBackToTheDefaultImage(t *testing.T) {
	// Given: nothing built locally and no image named
	// When: the artifact is chosen
	// Then: the default image is used — the pre-existing fallback
	got, err := chooseOvercastArtifact(artifactRequest{
		Image:           defaultOvercastImage,
		DockerAvailable: true,
	})
	if err != nil {
		t.Fatalf("chooseOvercastArtifact: %v", err)
	}
	if got.Mode != overcastModeDocker || got.Ref != defaultOvercastImage {
		t.Fatalf("chose %s %q, want the default image", got.Mode, got.Ref)
	}
}

func TestChooseOvercastArtifactRefusesARequestedImageWithoutDocker(t *testing.T) {
	// Given: an image was named on a machine with no Docker, but a binary is
	// available
	// When: the artifact is chosen
	// Then: it fails rather than running the binary. Falling back here would
	// be the bug in issue #801 wearing a different hat: the caller named the
	// bits to test, so a run that cannot test them is not a run.
	_, err := chooseOvercastArtifact(artifactRequest{
		Image:          "ghcr.io/neaox/overcast:0.0.1-alpha.33-rc.1",
		ImageRequested: true,
		FoundBin:       "/repo/bin/overcast",
	})
	if err == nil {
		t.Fatal("chooseOvercastArtifact returned no error, want a refusal to substitute the binary")
	}
	if !strings.Contains(err.Error(), "--overcast-image") {
		t.Errorf("error %q does not name the flag it could not honour", err)
	}
}

func TestChooseOvercastArtifactHonoursARequestedBinary(t *testing.T) {
	// Given: --overcast-bin naming a binary that exists
	// When: the artifact is chosen
	// Then: it is used, and the reason credits the flag rather than discovery
	got, err := chooseOvercastArtifact(artifactRequest{
		Bin:             "/tmp/overcast",
		BinRequested:    true,
		Image:           defaultOvercastImage,
		FoundBin:        "/tmp/overcast",
		DockerAvailable: true,
	})
	if err != nil {
		t.Fatalf("chooseOvercastArtifact: %v", err)
	}
	if got.Mode != overcastModeBinary || got.Ref != "/tmp/overcast" {
		t.Fatalf("chose %s %q, want the requested binary", got.Mode, got.Ref)
	}
	if !strings.Contains(got.Reason, "--overcast-bin") {
		t.Errorf("Reason = %q, want it to credit the flag", got.Reason)
	}
}

func TestChooseOvercastArtifactRefusesAMissingRequestedBinary(t *testing.T) {
	// Given: --overcast-bin pointing at a path that does not exist
	// When: the artifact is chosen
	// Then: it fails naming the path, rather than quietly starting a container
	// instead — the mirror image of #801, and just as easy to mistake for a
	// run that did what it was told.
	_, err := chooseOvercastArtifact(artifactRequest{
		Bin:             "/nope/overcast",
		BinRequested:    true,
		Image:           defaultOvercastImage,
		DockerAvailable: true,
	})
	if err == nil {
		t.Fatal("chooseOvercastArtifact returned no error for a --overcast-bin path that does not exist")
	}
	if !strings.Contains(err.Error(), "/nope/overcast") {
		t.Errorf("error %q does not name the path it could not use", err)
	}
}

func TestChooseOvercastArtifactRefusesTwoNamedArtifacts(t *testing.T) {
	// Given: the caller named both a binary and an image
	// When: the artifact is chosen
	// Then: it fails naming both. There is no principled winner, and silently
	// picking one would rebuild #801 facing the other way.
	_, err := chooseOvercastArtifact(artifactRequest{
		Bin:             "/tmp/overcast",
		BinRequested:    true,
		Image:           "ghcr.io/neaox/overcast:0.0.1-alpha.32",
		ImageRequested:  true,
		FoundBin:        "/tmp/overcast",
		DockerAvailable: true,
	})
	if err == nil {
		t.Fatal("chooseOvercastArtifact returned no error for two named artifacts")
	}
	for _, want := range []string{"/tmp/overcast", "ghcr.io/neaox/overcast:0.0.1-alpha.32"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

func TestChooseOvercastArtifactFailsWithNothingToRun(t *testing.T) {
	// Given: no binary, no Docker, and nothing named
	// When: the artifact is chosen
	// Then: the existing dead-end error is returned, telling the caller the
	// three ways out
	_, err := chooseOvercastArtifact(artifactRequest{Image: defaultOvercastImage})
	if err == nil {
		t.Fatal("chooseOvercastArtifact returned no error with nothing to run")
	}
	if !strings.Contains(err.Error(), "no way to start Overcast") {
		t.Errorf("error %q is not the dead-end message", err)
	}
}

func TestOvercastArtifactDescribeNamesTheArtifact(t *testing.T) {
	// Given: each kind of chosen artifact
	// When: it is described for the log
	// Then: the phrase carries the exact reference, not just the mode. "docker"
	// alone was all a run printed before #801, and it is the same word whether
	// the image was the one you asked for or the compiled-in default.
	cases := []struct {
		artifact overcastArtifact
		want     string
	}{
		{overcastArtifact{Mode: overcastModeDocker, Ref: "ghcr.io/neaox/overcast:alpha"}, "container image ghcr.io/neaox/overcast:alpha"},
		{overcastArtifact{Mode: overcastModeBinary, Ref: "/repo/bin/overcast"}, "binary /repo/bin/overcast"},
	}
	for _, tc := range cases {
		if got := tc.artifact.Describe(); got != tc.want {
			t.Errorf("Describe() = %q, want %q", got, tc.want)
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
	// built, so an unset OVERCAST_LISTEN — the emulator's own default is
	// 0.0.0.0 — would put an unauthenticated instance on whatever network
	// the machine is attached to, in the *common* case rather than the
	// fallback one.
	env := overcastEnv(4570, 0, bindHosts(""))
	if !containsEnv(env, "OVERCAST_LISTEN=127.0.0.1") {
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
	if !containsEnv(env, "OVERCAST_LISTEN=127.0.0.1,172.17.0.1") {
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
	// Then: it returns as soon as /_overcast/health answers 200, not on the first try
	var polls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_overcast/health" {
			t.Errorf("polled %q, want /_overcast/health", r.URL.Path)
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

// ---------------------------------------------------------------------------
// The Docker socket a managed container needs to run Lambda (issue #867)
// ---------------------------------------------------------------------------

func TestDockerRunArgsMountsTheDockerSocket(t *testing.T) {
	// Given: a host socket to mount
	// When: the docker argv is built
	// Then: it is bind-mounted at the path the emulator looks for, and nothing
	// else is done about it — no --group-add, no chgrp. The image's entrypoint
	// joins the socket's group itself, and compat/docker-compose.yml records
	// what changing the socket from outside cost the last time it was tried.
	argv := dockerRunArgs(4570, 0, "img", "warn", "", "/var/run/docker.sock")
	if !slices.Contains(argv, "-v") {
		t.Fatalf("dockerRunArgs argv %v has no bind mount", argv)
	}
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "-v /var/run/docker.sock:"+dockerSocketPath) {
		t.Errorf("dockerRunArgs argv %v does not mount the socket at %s", argv, dockerSocketPath)
	}
	if strings.Contains(joined, "--group-add") {
		t.Errorf("dockerRunArgs argv %v adds a group; the image's entrypoint derives it", argv)
	}
	if last := argv[len(argv)-1]; last != "img" {
		t.Errorf("dockerRunArgs ended with %q, want the image last", last)
	}
}

func TestDockerRunArgsOmitsTheMountWhenThereIsNoSocket(t *testing.T) {
	// Given: no socket to mount
	// When: the docker argv is built
	// Then: there is no -v at all, rather than a mount of the empty string
	argv := dockerRunArgs(4570, 0, "img", "warn", "", "")
	if slices.Contains(argv, "-v") {
		t.Errorf("dockerRunArgs argv %v mounts something with no socket configured", argv)
	}
}

func TestResolveDockerSocketRefusesWhenTheFlagIsOff(t *testing.T) {
	// Given: --mount-docker-socket=false
	// When: the socket is resolved
	// Then: nothing is mounted, and the reason names the flag — so the banner
	// says the caller asked for this, not that the machine cannot manage it
	socket, whyNot := resolveDockerSocket(false, "/var/run/docker.sock")
	if socket != "" {
		t.Errorf("resolveDockerSocket mounted %q with the flag off", socket)
	}
	if !strings.Contains(whyNot, "--mount-docker-socket") {
		t.Errorf("reason %q does not name the flag", whyNot)
	}
}

func TestResolveDockerSocketDefaultsToTheStandardPath(t *testing.T) {
	// Given: no explicit path
	// When: the socket is resolved on a platform where the daemon's filesystem
	// is not this one, so there is nothing to check the path against
	// Then: the standard path is used and the mount is attempted; whether it
	// worked is settled afterwards by asking the instance
	if runtime.GOOS == "linux" {
		t.Skip("Linux checks that the path exists; covered by the tests below")
	}
	socket, whyNot := resolveDockerSocket(true, "")
	if socket != dockerSocketPath {
		t.Errorf("resolveDockerSocket(true, %q) = %q, %q; want %q", "", socket, whyNot, dockerSocketPath)
	}
}

func TestResolveDockerSocketRefusesAMissingPathOnLinux(t *testing.T) {
	// Given: a socket path that does not exist, on the one platform where the
	// daemon shares this filesystem and so the absence is conclusive
	// When: the socket is resolved
	// Then: nothing is mounted. Docker would not have failed — it would have
	// created an empty directory at that path, on the host as well as in the
	// container, leaving a stray behind and a Lambda failure that names
	// nothing.
	if runtime.GOOS != "linux" {
		t.Skip("the existence check is Linux-only; see resolveDockerSocket")
	}
	missing := filepath.Join(t.TempDir(), "docker.sock")
	socket, whyNot := resolveDockerSocket(true, missing)
	if socket != "" {
		t.Errorf("resolveDockerSocket mounted %q, which does not exist", socket)
	}
	if !strings.Contains(whyNot, missing) || !strings.Contains(whyNot, "COMPAT_DOCKER_SOCK") {
		t.Errorf("reason %q should name the path and how to change it", whyNot)
	}
}

func TestResolveDockerSocketRefusesANonSocketOnLinux(t *testing.T) {
	// Given: a path that exists but is a plain file
	// When: the socket is resolved
	// Then: it is refused rather than mounted — a regular file at the target
	// is the same dead end as a directory
	if runtime.GOOS != "linux" {
		t.Skip("the existence check is Linux-only; see resolveDockerSocket")
	}
	path := filepath.Join(t.TempDir(), "docker.sock")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	socket, whyNot := resolveDockerSocket(true, path)
	if socket != "" {
		t.Errorf("resolveDockerSocket mounted %q, which is not a socket", socket)
	}
	if !strings.Contains(whyNot, "not a socket") {
		t.Errorf("reason %q should say the path is not a socket", whyNot)
	}
}

func TestResolveDockerSocketAcceptsARealSocketOnLinux(t *testing.T) {
	// Given: an actual Unix socket
	// When: it is resolved
	// Then: it is mounted, with no reason to report
	if runtime.GOOS != "linux" {
		t.Skip("the existence check is Linux-only; see resolveDockerSocket")
	}
	path := filepath.Join(t.TempDir(), "docker.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen on %s: %v", path, err)
	}
	defer func() { _ = ln.Close() }()

	socket, whyNot := resolveDockerSocket(true, path)
	if socket != path || whyNot != "" {
		t.Errorf("resolveDockerSocket(%q) = %q, %q; want it mounted", path, socket, whyNot)
	}
}

// healthWithDocker serves /_overcast/health with a fixed body.
func healthWithDocker(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_overcast/health" {
			t.Errorf("polled %q, want /_overcast/health", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDockerReportedWaitsForTheProbeToFinish(t *testing.T) {
	// Given: an instance whose Docker probe has not run yet, which it reports
	// as available=false with no services at all
	// When: the launcher asks
	// Then: it keeps asking rather than reading the flag once and calling it a
	// no. The emulator's probe retries for about fifteen seconds before it has
	// an answer, and every poll until then looks exactly like a failure.
	var polls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&polls, 1) < 3 {
			fmt.Fprint(w, `{"docker":{"available":false,"services":[]}}`)
			return
		}
		fmt.Fprint(w, `{"docker":{"available":true,"services":[{"service":"lambda","connected":true}]}}`)
	}))
	defer srv.Close()

	if got := dockerReported(context.Background(), srv.URL, 10*time.Second); got != dockerStateAvailable {
		t.Fatalf("dockerReported = %v after %d polls, want available", got, atomic.LoadInt32(&polls))
	}
}

func TestDockerReportedIsUnavailableOnceTheProbeHasRun(t *testing.T) {
	// Given: an instance that has probed and found nothing
	// When: the launcher asks
	// Then: that is a definite no — which is what the skip decision turns on,
	// so it must not be reachable before the probe has actually reported
	srv := healthWithDocker(t,
		`{"docker":{"available":false,"services":[{"service":"lambda","connected":false}]}}`)
	if got := dockerReported(context.Background(), srv.URL, 10*time.Second); got != dockerStateUnavailable {
		t.Fatalf("dockerReported = %v, want unavailable", got)
	}
}

func TestDockerReportedIsUnknownWithoutADockerSection(t *testing.T) {
	// Given: an instance with no Docker-backed service configured at all, so
	// /_overcast/health omits the section
	// When: the launcher asks
	// Then: the answer is "unknown", and it comes back at once — there is
	// nothing to report and nothing to skip, and polling for an answer that
	// cannot arrive would stall every such run for the full timeout
	srv := healthWithDocker(t, `{"status":"ok"}`)
	start := time.Now()
	if got := dockerReported(context.Background(), srv.URL, 10*time.Second); got != dockerStateUnknown {
		t.Fatalf("dockerReported = %v, want unknown", got)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("dockerReported waited %s for an answer that cannot change", elapsed)
	}
}

func TestDockerReportedGivesUpAtTheDeadline(t *testing.T) {
	// Given: an instance that never finishes probing
	// When: the launcher asks
	// Then: it stops at the deadline with "unknown" rather than hanging, and
	// unknown is acted on by doing nothing in either direction
	srv := healthWithDocker(t, `{"docker":{"available":false,"services":[]}}`)
	if got := dockerReported(context.Background(), srv.URL, 300*time.Millisecond); got != dockerStateUnknown {
		t.Fatalf("dockerReported = %v, want unknown", got)
	}
}

func TestDockerBlameHoldsTheEnvironmentResponsibleForAnUnmountedSocket(t *testing.T) {
	// Given: compat could not mount a socket
	// When: the instance reports no Docker
	// Then: the machine is the reason, which is what licenses a skip — and the
	// reason carries the detail, so the banner says which machine problem
	noDocker, environmental := dockerBlame("", "/var/run/docker.sock does not exist on this host", false)
	if !environmental {
		t.Error("an unmounted socket should be blamed on the environment")
	}
	if !strings.Contains(noDocker, "does not exist on this host") {
		t.Errorf("reason %q drops the detail of why nothing was mounted", noDocker)
	}
}

func TestDockerBlameHoldsTheEnvironmentResponsibleWithNoDaemonAtAll(t *testing.T) {
	// Given: an instance on a machine with no daemon running
	// When: it reports no Docker
	// Then: environmental again — there was never anything for it to reach
	noDocker, environmental := dockerBlame("", "", false)
	if !environmental {
		t.Error("no daemon on the machine should be blamed on the environment")
	}
	if noDocker == "" {
		t.Error("dockerBlame gave no reason")
	}
}

func TestDockerBlameHoldsTheInstanceResponsibleWhenTheSocketWasMounted(t *testing.T) {
	// Given: compat mounted the socket and the host daemon is up
	// When: the instance still says it has no Docker
	// Then: this is NOT environmental. Compat gave it everything it needed, so
	// the Docker-dependent tests must be allowed to run and fail: skipping
	// here is the green-over-a-stub blindspot compat/AGENTS.md was written
	// about.
	noDocker, environmental := dockerBlame("/var/run/docker.sock", "", true)
	if environmental {
		t.Error("a mounted socket plus a live daemon is not an environment failure")
	}
	if !strings.Contains(noDocker, "/var/run/docker.sock") {
		t.Errorf("reason %q should name the socket that was mounted", noDocker)
	}
}

func TestDockerBlameHoldsTheInstanceResponsibleOnTheBinaryPath(t *testing.T) {
	// Given: a native binary instance — nothing to mount — on a machine whose
	// daemon is up and which it therefore already shares
	// When: it reports no Docker
	// Then: not environmental either, by the same reasoning as the mount case
	noDocker, environmental := dockerBlame("", "", true)
	if environmental {
		t.Error("a live host daemon the instance shares is not an environment failure")
	}
	if noDocker == "" {
		t.Error("dockerBlame gave no reason")
	}
}

func TestImageRefIsPullableRefreshesAMovingRegistryTag(t *testing.T) {
	// Given: the compiled-in default, a moving tag on a registry
	// When: the reference is classified
	// Then: it is pullable. `docker run` only fetches an image it does not
	// already have, so without this the tag resolves to whatever copy the
	// machine cached — which is how a run against ":alpha" silently exercised
	// a months-old build whose health path had since been renamed, and failed
	// the health gate with nothing but a timeout to go on.
	for _, ref := range []string{
		defaultOvercastImage,
		"ghcr.io/neaox/overcast:0.0.1-alpha.35-rc.1902",
		"localhost:5000/overcast:dev",
		"docker.io/library/alpine:3",
	} {
		pullable, why := imageRefIsPullable(ref)
		if !pullable {
			t.Errorf("imageRefIsPullable(%q) = false (%s), want it refreshed", ref, why)
		}
	}
}

func TestImageRefIsPullableSkipsLocallyBuiltTags(t *testing.T) {
	// Given: the tags this repo builds by hand — `docker build -t overcast:dev .`
	// When: they are classified
	// Then: no pull is attempted. They exist in no registry, so pulling can
	// only fail, and a failed pull that prints a warning about a locally built
	// image is noise the caller cannot act on.
	for _, ref := range []string{"overcast:dev", "overcast:compat-hygiene", "compat-overcast"} {
		pullable, why := imageRefIsPullable(ref)
		if pullable {
			t.Errorf("imageRefIsPullable(%q) = true, want it left alone as a local build", ref)
		}
		if why == "" {
			t.Errorf("imageRefIsPullable(%q) gave no reason for skipping", ref)
		}
	}
}

func TestImageRefIsPullableSkipsDigestPinnedReferences(t *testing.T) {
	// Given: a digest-pinned reference
	// When: it is classified
	// Then: no pull. A digest names exactly one image for all time, so a
	// cached copy cannot be stale and `docker run` already fetches it if it is
	// missing.
	ref := "ghcr.io/neaox/overcast@sha256:0d7bcf259da3777a423aaae3f94ddd3f28f87d19daf1dfc52757598921950e94"
	pullable, why := imageRefIsPullable(ref)
	if pullable {
		t.Errorf("imageRefIsPullable(%q) = true, want a digest treated as immutable", ref)
	}
	if !strings.Contains(why, "digest") {
		t.Errorf("reason %q should say the reference is digest-pinned", why)
	}
}

func TestImageSkewNoteExplainsFailuresFromTestsNewerThanTheImage(t *testing.T) {
	// Given: the default published image, built four commits before HEAD
	// When: the skew is described
	// Then: it says how far behind and what that does to the run. The suites
	// come from the working tree while the image is whatever was last
	// released, so a test merged after the release fails against it and looks
	// exactly like a regression — this is `cli/opensearch-tags` failing on
	// alpha.35 because the tests (#977) and the fix under them (#970) both
	// landed after the image was built (PR #979).
	note := imageSkewNote(defaultOvercastImage, "0bfc845926a1b48ed3a68a0c1677aed0c8738dc5", 4)
	if note == "" {
		t.Fatal("imageSkewNote said nothing about an image behind the checkout")
	}
	if !strings.Contains(note, "0bfc8459") {
		t.Errorf("note %q should name the commit the image was built from", note)
	}
	if !strings.Contains(note, "4") {
		t.Errorf("note %q should say how far behind the image is", note)
	}
	if !strings.Contains(note, "--overcast-image") {
		t.Errorf("note %q should say how to test the working tree instead", note)
	}
}

func TestImageSkewNoteIsSilentWhenTheImageMatchesTheCheckout(t *testing.T) {
	// Given: an image built from HEAD, or a revision git here cannot place
	// (an unfetched commit counts as 0 rather than guessing)
	// When: the skew is described
	// Then: nothing is said. A warning that fires on a correct setup is a
	// warning people learn to scroll past.
	if note := imageSkewNote(defaultOvercastImage, "0bfc8459", 0); note != "" {
		t.Errorf("imageSkewNote = %q, want silence when the image is level with the checkout", note)
	}
	if note := imageSkewNote(defaultOvercastImage, "", 4); note != "" {
		t.Errorf("imageSkewNote = %q, want silence when the image carries no revision label", note)
	}
}

func TestPullFallbackKeepsGoingWhenACachedCopyExists(t *testing.T) {
	// Given: the pull failed — offline, or the registry is down — but the
	// machine has the image cached
	// When: the fallback is decided
	// Then: the run continues rather than dying on a network blip, and the
	// warning says the copy is cached and names its digest, so a caller who
	// then hits odd behaviour can tell it is not the tag they think it is.
	warning, fatal := pullFallback("sha256:0d7bcf25", fmt.Errorf("dial tcp: no route to host"), defaultOvercastImage)
	if fatal != nil {
		t.Fatalf("pullFallback returned fatal %v, want the cached copy used", fatal)
	}
	if !strings.Contains(warning, "sha256:0d7bcf25") {
		t.Errorf("warning %q should name the cached digest being used instead", warning)
	}
	if !strings.Contains(warning, defaultOvercastImage) {
		t.Errorf("warning %q should name the image whose pull failed", warning)
	}
}

func TestPullFallbackFailsWhenNothingIsCached(t *testing.T) {
	// Given: the pull failed and there is no local copy to fall back to
	// When: the fallback is decided
	// Then: it is fatal, and the message carries the pull error — `docker run`
	// would fail moments later with a worse one.
	warning, fatal := pullFallback("", fmt.Errorf("manifest unknown"), defaultOvercastImage)
	if fatal == nil {
		t.Fatal("pullFallback returned no error, want a failure with nothing to run")
	}
	if !strings.Contains(fatal.Error(), "manifest unknown") {
		t.Errorf("error %q should carry the underlying pull failure", fatal)
	}
	if warning != "" {
		t.Errorf("warning %q should be empty when the outcome is fatal", warning)
	}
}
