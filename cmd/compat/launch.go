// cmd/compat/launch.go — everything the compat CLI needs to stand up its own
// environment: choosing ports that are safe to bind, starting a throwaway
// Overcast instance (native binary or container), waiting for it to become
// healthy, running the Vite dev server for the dashboard UI, and opening a
// browser. This is the cross-platform replacement for the logic that used to
// live in compat/run.sh and compat/dev.sh — those are now thin wrappers, so
// the behaviour is identical on Windows, macOS, and Linux.
//
// Port policy: 4566 (API) and 4567 (web UI) belong to the developer's own
// Overcast instance — see AGENTS.md § Reserved ports. Nothing started from
// here may bind either, even when the scan base or an explicit flag would land
// on them. Every port is chosen by probing, so two compat sessions (or two
// agents) can run side by side. Ports are bound on loopback, never on every
// interface, whether the instance is a container (published with -p) or a
// native binary (told where to listen with OVERCAST_LISTEN) — see
// loopbackHost, bindHosts and dockerBridgeGateway.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	// reservedAPIPort and reservedUIPort are the developer's own instance.
	reservedAPIPort = 4566
	reservedUIPort  = 4567

	// defaultPortBase is where port scans start, matching
	// scripts/run-test-instance.sh.
	defaultPortBase = 4570

	// portScanLimit bounds the upward scan so a saturated machine fails with a
	// message instead of spinning to 65535.
	portScanLimit = 65000

	// defaultOvercastImage is the container used when no native binary is
	// available. Matches scripts/run-test-instance.sh.
	defaultOvercastImage = "ghcr.io/neaox/overcast:alpha"

	// dockerSocketPath is where a Unix Docker socket lives, on the host and
	// inside the container both. The container side is not configurable: it is
	// the emulator's own compiled-in default (internal/config), so mounting
	// anywhere else would need the instance reconfigured to match.
	dockerSocketPath = "/var/run/docker.sock"

	// dockerReadyTimeout bounds the wait for a managed instance to say whether
	// it found a Docker daemon. The emulator's probe retries five times with a
	// 2s dial timeout and a growing backoff (internal/docker.Probe), so a
	// definite "no" takes about fifteen seconds to arrive; a "yes" arrives
	// almost immediately. Only the "no" path spends the whole budget, and it
	// buys the difference between one accurate line at startup and five
	// Lambda suites failing later for a reason none of them can name.
	dockerReadyTimeout = 30 * time.Second

	// imagePullTimeout bounds refreshing a moving tag. Generous because it is
	// paid once on a cold machine pulling ~90MB, and skipped entirely when the
	// registry already agrees with what is cached.
	imagePullTimeout = 5 * time.Minute

	// loopbackHost is where a managed instance listens — published there for a
	// container, bound there for a native binary — and the address every port
	// probe binds. Both paths default to every interface if left alone: a bare
	// "<host>:<container>" mapping publishes on all of them, and the emulator's
	// own OVERCAST_LISTEN default is 0.0.0.0. Either puts an unauthenticated
	// emulator on whatever network the machine is attached to, and trips a
	// Windows Firewall prompt. Nothing that talks to a compat instance is
	// off-box.
	loopbackHost = "127.0.0.1"
)

// --start-overcast modes.
const (
	startOvercastAuto   = "auto"
	startOvercastAlways = "always"
	startOvercastNever  = "never"
)

// How a managed instance was started, reported in log lines and the ready
// banner.
const (
	overcastModeBinary = "binary"
	overcastModeDocker = "docker"
)

// repoRoot walks up from the working directory looking for go.mod, so compat
// can be launched from anywhere in the tree — the wrappers, a Makefile in
// compat/, or an editor's run button — and still find bin/ and compat/ui.
func repoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}

// uiProjectDir is the dashboard UI's Vite project.
func uiProjectDir() string { return filepath.Join(repoRoot(), "compat", "ui") }

// uiDistDir is the dashboard UI's build output.
func uiDistDir() string { return filepath.Join(uiProjectDir(), "dist") }

// isReservedPort reports whether a port belongs to the developer's own
// Overcast instance and must never be bound by compat.
func isReservedPort(port int) bool {
	return port == reservedAPIPort || port == reservedUIPort
}

// portFree reports whether a TCP port can be bound. The probe is loopback
// only: a wildcard bind would make Windows Firewall prompt for every binary
// that scans for a port, and a port held on another interface but free on
// loopback is vanishingly rare on a dev machine. It binds and immediately
// releases, so there is a small race between the probe and the eventual
// listener — acceptable for a dev harness, and far better than assuming a
// fixed port is available.
func portFree(port int) bool {
	return portFreeOn(loopbackHost, port)
}

// portFreeOn reports whether a TCP port can be bound on one specific address.
// Only the bridge-gateway publish needs an address other than loopback; see
// dockerBridgeGateway.
func portFreeOn(host string, port int) bool {
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// freePort returns the first bindable port at or above base, skipping the
// reserved pair.
func freePort(base int) (int, error) {
	if base <= 0 {
		base = defaultPortBase
	}
	for port := base; port < portScanLimit; port++ {
		if isReservedPort(port) {
			continue
		}
		if portFree(port) {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free port found at or above %d", base)
}

// freePortPair returns two distinct free ports for an instance that also
// serves its own web UI.
func freePortPair(base int) (int, int, error) {
	api, err := freePort(base)
	if err != nil {
		return 0, 0, err
	}
	ui, err := freePort(api + 1)
	if err != nil {
		return 0, 0, err
	}
	return api, ui, nil
}

// resolveListenAddr normalises a --port value (":7777" or "7777") and moves
// off the requested port when it is busy or reserved, so a second dashboard
// starts instead of failing to bind.
func resolveListenAddr(addr string) (string, error) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(addr), ":")
	port, err := strconv.Atoi(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid listen address %q: want a port like :7777", addr)
	}
	if !isReservedPort(port) && portFree(port) {
		return ":" + strconv.Itoa(port), nil
	}
	next, err := freePort(port + 1)
	if err != nil {
		return "", err
	}
	return ":" + strconv.Itoa(next), nil
}

// shouldStartOvercast decides whether the CLI manages its own emulator.
// "auto" (the default) starts one unless the caller pinned an endpoint, which
// is how CI and the compose file target an instance they started themselves.
func shouldStartOvercast(mode string, endpointPinned bool) bool {
	switch mode {
	case startOvercastAlways:
		return true
	case startOvercastNever:
		return false
	default:
		return !endpointPinned
	}
}

// overcastEnv builds the environment for a managed native instance. A UI port
// of 0 disables the emulator's own web UI, which is what compat wants: it
// keeps the instance from binding the reserved 4567.
//
// hosts is where the instance listens. It is the binary path's counterpart to
// the container path's -p mappings (see dockerRunArgs): without it the
// emulator takes its own 0.0.0.0 default and a compat run puts an
// unauthenticated instance on whatever network the machine is attached to —
// and the binary path is the one compat prefers, so that is the common case,
// not the fallback.
func overcastEnv(apiPort, uiPort int, hosts []string) []string {
	return []string{
		"OVERCAST_PORT=" + strconv.Itoa(apiPort),
		"OVERCAST_UI_PORT=" + strconv.Itoa(uiPort),
		"OVERCAST_LISTEN=" + strings.Join(hosts, ","),
		"OVERCAST_STATE=memory",
	}
}

// bindHosts returns the addresses a managed native instance listens on:
// loopback, plus the Docker bridge gateway when there is one. It is the same
// address set publishArgs maps for a container, arrived at the same way — see
// dockerBridgeGateway for why the gateway is needed, when it is empty, and why
// it is never loopback itself.
func bindHosts(gateway string) []string {
	hosts := []string{loopbackHost}
	if gateway != "" {
		hosts = append(hosts, gateway)
	}
	return hosts
}

// overcastOptions configures a managed Overcast instance.
type overcastOptions struct {
	// Host is the hostname the suites address the instance by. Defaults to
	// "localhost"; set it to a wildcard-resolving name such as
	// localhost.overcast.sh when a suite needs virtual-host-style addressing
	// (S3 bucket subdomains) to reach the emulator.
	Host string
	// Bin is an explicit path to the overcast binary. Empty searches
	// bin/overcast then PATH.
	Bin string
	// BinRequested says the caller named Bin, rather than it being empty.
	BinRequested bool
	// Image is the container image to run.
	Image string
	// ImageRequested says the caller named Image, rather than it coming from
	// defaultOvercastImage. It is the difference between "run this" and "run
	// something if there is nothing better", and chooseOvercastArtifact turns
	// on it.
	ImageRequested bool
	// MountDockerSocket bind-mounts the host's Docker socket into a managed
	// container, which is what lets the emulator inside start containers of
	// its own — Lambda execution environments above all. It has no effect on
	// the binary path, where the instance is already on the host and uses the
	// host's daemon directly.
	MountDockerSocket bool
	// DockerSocket is the host path of that socket. Empty means
	// dockerSocketPath; COMPAT_DOCKER_SOCK overrides it, spelled the same way
	// as in compat/docker-compose.yml.
	DockerSocket string
	// PortBase is where the port scan starts.
	PortBase int
	// WithUI serves the emulator's own web UI on a second free port.
	// Compat does not need it; it is here for eyeballing state during a run.
	WithUI bool
	// Timeout bounds the wait for the health endpoint.
	Timeout time.Duration
	// LogLevel is passed through to the instance.
	LogLevel string
	// Logf receives progress lines.
	Logf func(format string, args ...any)
}

// managedOvercast is a running instance owned by this process.
type managedOvercast struct {
	// Endpoint is the API base URL, e.g. "http://localhost:4570".
	Endpoint string
	// UIURL is the emulator's own web UI, empty when disabled.
	UIURL string
	// Artifact is what it was started from, and why. It replaces a bare
	// "binary"/"docker" mode string so the ready banner can name the exact
	// binary or image — the mode alone is the same word whether the image was
	// the release candidate the caller asked for or the compiled-in default.
	Artifact overcastArtifact
	// NoDocker is empty when the instance reported a reachable Docker daemon,
	// and otherwise says in one sentence why it has none. It is read from the
	// instance's own /_overcast/health rather than inferred, so it is the same answer
	// the Lambda tests are about to get.
	NoDocker string
	// DockerIsEnvironmental says the machine, not the emulator, is why
	// NoDocker is set: there was no socket to mount, or no daemon on this host
	// at all. It is the difference between a test that cannot run here and a
	// test that should have run and did not — see dockerVerdict.
	DockerIsEnvironmental bool
	// Stop terminates the instance. Safe to call more than once.
	Stop func()
}

// startOvercast brings up a throwaway instance on free ports and waits for it
// to answer /_overcast/health. What it runs is decided by chooseOvercastArtifact: an
// artifact the caller named, otherwise a locally built binary (fast, and picks
// up uncommitted changes), otherwise the default container image.
func startOvercast(ctx context.Context, opts overcastOptions) (*managedOvercast, error) {
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 60 * time.Second
	}
	if opts.Image == "" {
		opts.Image = defaultOvercastImage
	}
	if opts.LogLevel == "" {
		opts.LogLevel = "warn"
	}
	if opts.Host == "" {
		opts.Host = "localhost"
	}
	if opts.DockerSocket == "" {
		opts.DockerSocket = dockerSocketPath
	}

	// Decide what to run before touching any ports, so a caller who named an
	// artifact that cannot be used hears about it immediately.
	dockerAvailable := false
	if _, err := exec.LookPath("docker"); err == nil {
		dockerAvailable = true
	}
	artifact, err := chooseOvercastArtifact(artifactRequest{
		Bin:             opts.Bin,
		BinRequested:    opts.BinRequested,
		Image:           opts.Image,
		ImageRequested:  opts.ImageRequested,
		FoundBin:        findOvercastBinary(opts.Bin),
		DockerAvailable: dockerAvailable,
	})
	if err != nil {
		return nil, err
	}
	// Say which artifact won and why, before anything is started. A run that
	// was asked for an image and used a binary instead must be impossible to
	// mistake for one that used the image (issue #801), and the mode alone —
	// "docker", "binary" — never said that.
	logf("using the %s — %s", artifact.Describe(), artifact.Reason)
	if artifact.Ignored != "" {
		logf("%s", artifact.Ignored)
	}

	// Decide the socket before the ports too, and say so before anything is
	// started: a run that cannot invoke a Lambda should announce that at the
	// top, not twenty minutes in through five suites' worth of failures that
	// read like the emulator is broken (issue #867).
	socket, socketWhyNot := "", ""
	if artifact.Mode == overcastModeDocker {
		socket, socketWhyNot = resolveDockerSocket(opts.MountDockerSocket, opts.DockerSocket)
		if socket != "" {
			logf("mounting the Docker socket %s, so the instance can run Lambda and ECS containers", socket)
		} else {
			logf("NOT mounting the Docker socket: %s", socketWhyNot)
		}
	}

	apiPort, uiPort := 0, 0
	if opts.WithUI {
		apiPort, uiPort, err = freePortPair(opts.PortBase)
	} else {
		apiPort, err = freePort(opts.PortBase)
	}
	if err != nil {
		return nil, err
	}

	var oc *managedOvercast
	if artifact.Mode == overcastModeBinary {
		oc, err = startOvercastBinary(ctx, artifact, apiPort, uiPort, opts, logf)
	} else {
		oc, err = startOvercastContainer(ctx, artifact, apiPort, uiPort, socket, opts, logf)
	}
	if err != nil {
		return nil, err
	}
	oc.NoDocker, oc.DockerIsEnvironmental = dockerVerdict(ctx, oc.Endpoint, socket, socketWhyNot, logf)
	return oc, nil
}

// resolveDockerSocket decides what to bind-mount into a managed container,
// returning either a host path or the reason there is not one. Both are
// caller-facing: the reason is printed, and it is also what tells a machine
// that cannot run containers apart from an emulator that will not.
//
// The existence check is Linux-only on purpose. There, the daemon shares this
// filesystem, so a missing path means a missing socket — and bind-mounting a
// source that does not exist does not fail, it silently creates an empty
// *directory*, on the host as well as in the container. The container then has
// a directory where its socket should be and the emulator reports Docker
// unavailable, which is the confusing failure this function exists to replace,
// plus a root-owned stray left at /var/run/docker.sock that a later daemon
// start would trip over.
//
// On Windows and macOS the same check would be wrong: Docker Desktop resolves
// a bind source inside its own Linux VM, where /var/run/docker.sock exists
// whatever the host filesystem looks like. So the mount is attempted
// unconditionally there and judged afterwards, by asking the instance — see
// dockerVerdict, which is the authority on every platform anyway.
func resolveDockerSocket(mount bool, path string) (socket, whyNot string) {
	if !mount {
		return "", "--mount-docker-socket=false"
	}
	if path == "" {
		path = dockerSocketPath
	}
	if runtime.GOOS == "linux" {
		info, err := os.Stat(path)
		switch {
		case err != nil:
			return "", fmt.Sprintf("%s does not exist on this host "+
				"(set COMPAT_DOCKER_SOCK if the socket is somewhere else)", path)
		case info.Mode()&os.ModeSocket == 0:
			return "", fmt.Sprintf("%s is not a socket "+
				"(set COMPAT_DOCKER_SOCK if the socket is somewhere else)", path)
		}
	}
	return path, ""
}

// instanceDocker is the part of /_overcast/health that says whether the instance found
// a Docker daemon. Services is empty until the emulator's probe has finished,
// which is what tells "not yet" apart from "no" — see internal/docker.Tracker.
type instanceDocker struct {
	Docker *struct {
		Available bool `json:"available"`
		// Only the length of Services is read — the emulator publishes one
		// entry per Docker-backed service once it has probed, and none before
		// — so the elements are deliberately empty.
		Services []struct{} `json:"services"`
	} `json:"docker"`
}

// dockerVerdict asks a freshly started instance whether it can reach a Docker
// daemon, and returns an empty reason when it can.
//
// The second return value is the load-bearing one: whether the *machine* is
// why. Compat acts on that by skipping the Docker-dependent tests, and it must
// only ever do so on evidence, because a skip that is really a product failure
// is the blindspot compat/AGENTS.md § "Docker-dependent tests are first-class"
// was written about — a green run over an emulator answering from its stub.
//
// So the line is drawn at what compat itself did:
//
//   - There was no socket to mount, or no daemon on this host at all. The
//     environment cannot run these tests. Skipping is the honest report.
//   - The socket was mounted, or the host daemon is up and the instance is a
//     native binary sharing it — and the instance still says no. Compat gave
//     it everything it needed, so this is the emulator's answer and the tests
//     must be allowed to fail on it. Naming the cause here is what stops that
//     failure being mistaken for a broken Lambda.
func dockerVerdict(
	ctx context.Context,
	endpoint, socket, socketWhyNot string,
	logf func(string, ...any),
) (noDocker string, environmental bool) {
	switch dockerReported(ctx, endpoint, dockerReadyTimeout) {
	case dockerStateAvailable:
		return "", false
	case dockerStateUnknown:
		// The instance never said. Guessing either way would be worse than
		// saying so: an unwarranted skip hides a real failure, an unwarranted
		// failure blames the emulator for this machine.
		logf("could not tell whether the instance has Docker (no answer from /_overcast/health within %s); "+
			"Docker-dependent tests will run and report whatever they find", dockerReadyTimeout)
		return "", false
	}

	// The host probe is only asked for when the answer can still change the
	// verdict: with nothing mounted, the environment is already the reason.
	if socketWhyNot != "" {
		return dockerBlame(socket, socketWhyNot, false)
	}
	return dockerBlame(socket, socketWhyNot, hostDockerDaemonReachable(ctx))
}

// dockerBlame is the whole of the decision dockerVerdict documents, with the
// evidence already gathered: what compat mounted (or why it did not), and
// whether this machine has a daemon at all.
func dockerBlame(socket, socketWhyNot string, hostHasDaemon bool) (noDocker string, environmental bool) {
	if socketWhyNot != "" {
		return "the Docker socket was not mounted: " + socketWhyNot, true
	}
	if !hostHasDaemon {
		return "there is no reachable Docker daemon on this machine", true
	}
	if socket != "" {
		return fmt.Sprintf(
			"the instance reports no Docker daemon even though %s was mounted at %s — "+
				"check the socket's permissions, and the image's entrypoint",
			socket, dockerSocketPath), false
	}
	return "the instance reports no Docker daemon, though this machine has one", false
}

// dockerState is what an instance says about its own Docker access.
type dockerState int

const (
	// dockerStateUnknown means no answer arrived before the deadline.
	dockerStateUnknown dockerState = iota
	dockerStateAvailable
	dockerStateUnavailable
)

// dockerReported polls /_overcast/health until the instance's Docker probe has
// finished. A probe that has not run yet reports available=false with no
// services, which is indistinguishable from a real "no" at a single glance and
// is why this waits for the services list rather than reading the flag once.
func dockerReported(ctx context.Context, endpoint string, timeout time.Duration) dockerState {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	url := strings.TrimSuffix(endpoint, "/") + "/_overcast/health"
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return dockerStateUnknown
		}
		resp, err := client.Do(req)
		if err == nil {
			var health instanceDocker
			decodeErr := json.NewDecoder(resp.Body).Decode(&health)
			_ = resp.Body.Close()
			switch {
			case decodeErr != nil:
			case health.Docker == nil:
				// No Docker-backed service is configured at all, so there is
				// nothing to report and nothing to skip.
				return dockerStateUnknown
			case health.Docker.Available:
				return dockerStateAvailable
			case len(health.Docker.Services) > 0:
				return dockerStateUnavailable
			}
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			return dockerStateUnknown
		}
		select {
		case <-ctx.Done():
			return dockerStateUnknown
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// hostDockerDaemonReachable reports whether this machine has a Docker daemon
// answering. `docker version` talks to the daemon — unlike `docker info`'s
// client half, or a LookPath, both of which succeed with the daemon stopped.
func hostDockerDaemonReachable(ctx context.Context) bool {
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, "docker", "version", "--format", "{{.Server.Os}}")
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

// artifactRequest separates what the caller asked for from what this machine
// happens to have lying around. The Requested flags are the whole point: a
// value that came from the compiled-in default is nobody's request, so it must
// not outrank anything. See imageRequested/binRequested in main.go for how
// they are decided.
type artifactRequest struct {
	// Bin is --overcast-bin, and BinRequested says the caller named it.
	Bin          string
	BinRequested bool
	// Image is --overcast-image, and ImageRequested says the caller named it
	// rather than inheriting defaultOvercastImage.
	Image          string
	ImageRequested bool
	// FoundBin is what findOvercastBinary turned up, empty when nothing did.
	FoundBin string
	// DockerAvailable reports whether a container can be started at all.
	DockerAvailable bool
}

// overcastArtifact is the thing a managed instance will be started from,
// together with why it was chosen and what that passed over.
type overcastArtifact struct {
	// Mode is overcastModeBinary or overcastModeDocker.
	Mode string
	// Ref is the binary path or the container image reference.
	Ref string
	// Reason says, in caller-facing words, why this artifact won.
	Reason string
	// Ignored is a full sentence naming something the choice passes over,
	// empty when nothing was. Nothing the caller *asked* for ever lands here —
	// a request that cannot be honoured is an error, not a note.
	Ignored string
}

// Describe names the artifact in one phrase for a log line.
func (a overcastArtifact) Describe() string {
	if a.Mode == overcastModeDocker {
		return "container image " + a.Ref
	}
	return "binary " + a.Ref
}

// chooseOvercastArtifact decides what a managed instance is started from.
//
// The rule is that a named artifact wins over a discovered one. Discovery is a
// convenience — it exists so that `go run ./cmd/compat` picks up the binary you
// just built — and a convenience must never quietly outrank an instruction.
// It used to: binary discovery ran first unconditionally, so an explicitly
// requested --overcast-image was ignored whenever any bin/overcast existed,
// which is how a release candidate got "compat-tested" against a day-old local
// build (issue #801). The whole failure was silent-by-construction, hence both
// the precedence here and the Reason/Ignored strings the caller prints.
//
// Naming two different artifacts is refused rather than resolved: there is no
// principled winner between --overcast-bin and --overcast-image, and picking
// one would rebuild the same trap facing the other way.
func chooseOvercastArtifact(req artifactRequest) (overcastArtifact, error) {
	if req.BinRequested && req.ImageRequested {
		return overcastArtifact{}, fmt.Errorf(
			"--overcast-bin %s and --overcast-image %s both name what to test; pass one, not both",
			req.Bin, req.Image)
	}

	if req.BinRequested {
		if req.FoundBin == "" {
			return overcastArtifact{}, fmt.Errorf(
				"--overcast-bin %s: no such file — build it or fix the path; "+
					"compat will not run something else in its place", req.Bin)
		}
		return overcastArtifact{
			Mode:   overcastModeBinary,
			Ref:    req.FoundBin,
			Reason: "--overcast-bin names it",
		}, nil
	}

	if req.ImageRequested {
		if !req.DockerAvailable {
			return overcastArtifact{}, fmt.Errorf(
				"--overcast-image %s needs Docker, which is not on PATH — install it, "+
					"or drop the flag to run a local binary instead", req.Image)
		}
		artifact := overcastArtifact{
			Mode:   overcastModeDocker,
			Ref:    req.Image,
			Reason: "--overcast-image names it, and a named image outranks any local binary",
		}
		if req.FoundBin != "" {
			artifact.Ignored = fmt.Sprintf(
				"NOT using the local binary %s: --overcast-image was given, so the image is what gets tested",
				req.FoundBin)
		}
		return artifact, nil
	}

	if req.FoundBin != "" {
		return overcastArtifact{
			Mode:   overcastModeBinary,
			Ref:    req.FoundBin,
			Reason: "no image was named and this is what the search found",
		}, nil
	}
	if req.DockerAvailable {
		return overcastArtifact{
			Mode:   overcastModeDocker,
			Ref:    req.Image,
			Reason: "no local binary was found and no image was named, so this is the default",
		}, nil
	}
	return overcastArtifact{}, fmt.Errorf(
		"no way to start Overcast: build it first (task build / go build -o bin/overcast ./cmd/overcast), " +
			"install Docker, or point --endpoint at an instance you are already running")
}

// findOvercastBinary locates a built binary: the explicit path, then
// bin/overcast in the repo, then PATH.
func findOvercastBinary(explicit string) string {
	if explicit != "" {
		if _, err := os.Stat(explicit); err == nil {
			return explicit
		}
		return ""
	}
	names := []string{"overcast"}
	if runtime.GOOS == "windows" {
		names = []string{"overcast.exe", "overcast"}
	}
	for _, name := range names {
		candidate := filepath.Join(repoRoot(), "bin", name)
		if _, err := os.Stat(candidate); err == nil {
			abs, err := filepath.Abs(candidate)
			if err == nil {
				return abs
			}
			return candidate
		}
	}
	if path, err := exec.LookPath("overcast"); err == nil {
		return path
	}
	return ""
}

func startOvercastBinary(
	ctx context.Context,
	artifact overcastArtifact,
	apiPort, uiPort int,
	opts overcastOptions,
	logf func(string, ...any),
) (*managedOvercast, error) {
	bin := artifact.Ref
	endpoint := fmt.Sprintf("http://%s:%d", opts.Host, apiPort)
	logf("starting Overcast (%s) on %s", filepath.Base(bin), endpoint)

	hosts := bindHosts(dockerBridgeGateway(ctx, apiPort, uiPort))
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	cmd := exec.CommandContext(runCtx, bin, "serve")
	cmd.Env = append(os.Environ(), overcastEnv(apiPort, uiPort, hosts)...)
	cmd.Env = append(cmd.Env, "OVERCAST_LOG_LEVEL="+opts.LogLevel)
	cmd.Stdout = os.Stderr // keep stdout clean for --format json
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start %s: %w", bin, err)
	}

	stopped := make(chan struct{})
	stop := func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
		}
	}
	go func() {
		_ = cmd.Wait()
		close(stopped)
	}()

	if err := waitForHealth(ctx, endpoint, opts.Timeout); err != nil {
		stop()
		return nil, err
	}
	oc := &managedOvercast{Endpoint: endpoint, Artifact: artifact, Stop: stop}
	if uiPort != 0 {
		oc.UIURL = fmt.Sprintf("http://%s:%d", opts.Host, uiPort)
	}
	return oc, nil
}

// dockerRunArgs builds the argv for a managed emulator container. Ports are
// published on loopback (see loopbackHost) and, when gateway is non-empty, on
// that address as well.
//
// socket, when non-empty, is the host Docker socket to bind-mount. Lambda and
// ECS start containers of their own through it, so without it the emulator
// inside can only answer from its metadata-only path and every Lambda invoke
// fails — which is what an --overcast-image run did until issue #867.
//
// The mount is a plain bind, exactly as compat/docker-compose.yml and
// scripts/run-test-instance.sh --mount-docker-socket do it, and deliberately
// nothing else: no --group-add, no chgrp. The image's entrypoint reads the
// socket's gid at its own start and joins that group itself. The compose file
// carries the scar from the alternative — a chgrp of the shared socket, landing
// after the emulator had already derived the gid, revoked its access and failed
// nineteen Lambda tests nowhere near the cause.
func dockerRunArgs(apiPort, uiPort int, image, logLevel, gateway, socket string) []string {
	argv := []string{"run", "-d", "--rm"}
	argv = append(argv, publishArgs(apiPort, reservedAPIPort, gateway)...)
	if uiPort != 0 {
		argv = append(argv, publishArgs(uiPort, reservedUIPort, gateway)...)
	}
	if socket != "" {
		argv = append(argv, "-v", socket+":"+dockerSocketPath)
	}
	argv = append(argv,
		"-e", "OVERCAST_STATE=memory",
		"-e", "OVERCAST_LOG_LEVEL="+logLevel,
	)
	// The image goes last so nothing that follows can be read as a docker flag.
	return append(argv, image)
}

// publishArgs returns the -p arguments mapping one container port onto a host
// port, on loopback and optionally on a second address.
func publishArgs(hostPort, containerPort int, extraHost string) []string {
	argv := []string{"-p", fmt.Sprintf("%s:%d:%d", loopbackHost, hostPort, containerPort)}
	if extraHost != "" {
		argv = append(argv, "-p", fmt.Sprintf("%s:%d:%d", extraHost, hostPort, containerPort))
	}
	return argv
}

// dockerBridgeGateway returns the default bridge network's gateway address —
// the host address a sibling container reaches by Docker's "host-gateway"
// alias on native Linux. compat/suites/rust-sdk/run.sh rewrites a loopback
// endpoint to host.docker.internal and maps that name to host-gateway, so a
// managed instance that is on loopback alone is invisible to it: the packet
// arrives on the bridge, where nothing is listening. Covering the gateway too
// keeps that suite working without putting the instance on any network the
// machine is attached to — the bridge address is host-local, reachable from
// this machine's containers and from nowhere else.
//
// Both managed paths use it, and mean the same thing by it: publishArgs adds a
// -p mapping on it, bindHosts adds it to OVERCAST_LISTEN.
//
// It is not a rust-sdk-only accommodation. internal/containerendpoint hands
// every container Overcast starts itself — Lambda, ECS — an /etc/hosts entry
// pointing at Docker's "host-gateway" whenever Overcast is on the host, and on
// Linux that resolves to this same default-bridge address whatever network the
// container is on. So loopback plus this gateway is the set that keeps a
// managed instance reachable from everything that is supposed to reach it.
//
// Empty off Linux, where Docker Desktop routes host.docker.internal to the
// host itself rather than over the bridge, so loopback is enough. Empty too
// whenever the address cannot be determined or is already in use on one of the
// ports, since a bind that cannot be made fails the whole run — the suite that
// needed it then reports honestly instead.
//
// That last check is also what tells this machine's own Docker apart from a
// daemon somewhere else. On WSL2 with Docker Desktop, `docker network inspect`
// answers with the gateway inside Desktop's VM, an address this kernel has on
// no interface: the probe cannot bind it, so it is dropped rather than
// configured and left unreachable. Nothing here reads uname or the daemon's
// self-description — the evidence is whether the address exists locally.
func dockerBridgeGateway(ctx context.Context, ports ...int) string {
	if runtime.GOOS != "linux" {
		return ""
	}
	cmd := exec.CommandContext(ctx, "docker", "network", "inspect", "bridge",
		"--format", "{{(index .IPAM.Config 0).Gateway}}")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	gateway := strings.TrimSpace(string(out))
	if ip := net.ParseIP(gateway); ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
		return ""
	}
	for _, port := range ports {
		if port != 0 && !portFreeOn(gateway, port) {
			return ""
		}
	}
	return gateway
}

// imageRefIsPullable reports whether refreshing ref against its registry is a
// meaningful thing to do before running it, and when it is not, why not in
// caller-facing words.
//
// `docker run` fetches an image only when it is absent, so a moving tag like
// :alpha resolves to whatever copy the machine cached, for as long as that copy
// survives. That is issue #801's silent substitution one layer down: the log
// names the tag the caller asked for while the bits under it are arbitrarily
// old. It cost a run against a cached :alpha old enough that the health path
// had been renamed underneath it (`/_health` → `/_overcast/health`), which
// surfaced only as "did not become healthy within 1m0s" — the emulator was up
// and answering the whole time (PR #979).
//
// Two kinds of reference are deliberately left alone. A digest names one image
// for all time, so a cached copy of it cannot be stale. An unqualified name —
// no registry host in the first path segment — is how this repo names its own
// local builds (`docker build -t overcast:dev .`, `compat-overcast`), and those
// exist in no registry, so a pull could only ever fail.
func imageRefIsPullable(ref string) (bool, string) {
	if strings.Contains(ref, "@") {
		return false, "the reference is digest-pinned, so the cached copy cannot be stale"
	}
	host, _, hasSlash := strings.Cut(ref, "/")
	if !hasSlash || (!strings.ContainsAny(host, ".:") && host != "localhost") {
		return false, "the name carries no registry host, so it is a local build rather than a registry tag"
	}
	return true, ""
}

// pullFallback decides what happens when `docker pull` failed: carry on with
// the cached copy, or stop.
//
// A failed pull is not on its own a reason to abandon the run — compat on a
// train, or through a registry outage, should still work off what the machine
// already has. But it has to say so, and name the digest it fell back to:
// falling back silently is the same trap the pull exists to close.
func pullFallback(localDigest string, pullErr error, image string) (string, error) {
	if localDigest == "" {
		return "", fmt.Errorf(
			"could not pull %s, and there is no copy cached on this machine to run instead: %w",
			image, pullErr)
	}
	return fmt.Sprintf(
		"could not refresh %s (%v) — running the cached copy %s instead, which may be older than the tag",
		image, pullErr, localDigest), nil
}

// dockerImageInfo reports what a locally present image actually is: the repo
// digest — or the image ID, for a local build that was never pushed anywhere —
// and the commit it was built from. Both are empty when the machine does not
// have the image, which is the same answer as "nothing to fall back to".
func dockerImageInfo(ctx context.Context, image string) (digest, revision string) {
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	const format = `{{if .RepoDigests}}{{index .RepoDigests 0}}{{else}}{{.Id}}{{end}}` +
		"\x1f" + `{{index .Config.Labels "org.opencontainers.image.revision"}}`
	cmd := exec.CommandContext(probeCtx, "docker", "image", "inspect", image, "--format", format) //nolint:gosec
	out, err := cmd.Output()
	if err != nil {
		return "", ""
	}
	digest, revision, _ = strings.Cut(strings.TrimSpace(string(out)), "\x1f")
	if revision == "<no value>" {
		revision = ""
	}
	return digest, revision
}

// commitsAheadOf counts commits from rev to HEAD in this checkout, and returns
// 0 whenever git cannot answer — outside a checkout, or when rev has not been
// fetched. Zero means "say nothing", which is the right default for a note
// whose only job is to explain a surprising result.
func commitsAheadOf(ctx context.Context, rev string) int {
	if rev == "" {
		return 0
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, "git", "-C", repoRoot(), //nolint:gosec
		"rev-list", "--count", rev+"..HEAD")
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0
	}
	return n
}

// imageSkewNote explains, in one line, that the image is older than the suites
// about to be run against it.
//
// The default image is the last published release; the suites are whatever is
// in the working tree. Every commit that merges a new compat test between one
// release and the next widens that gap, and the failure it produces is
// indistinguishable from a regression — same suite, same group, same red. The
// note exists so the reader does not go looking for a bug in the emulator that
// is really a bug in what they are testing it with.
func imageSkewNote(image, imageRevision string, commitsAhead int) string {
	if imageRevision == "" || commitsAhead <= 0 {
		return ""
	}
	short := imageRevision
	if len(short) > 8 {
		short = short[:8]
	}
	return fmt.Sprintf(
		"%s was built from %s, %d commit(s) behind this checkout — compat suites come from the working tree, "+
			"so any test merged since then fails against it and looks like a regression; "+
			"build this tree and pass --overcast-image to test your own changes",
		image, short, commitsAhead)
}

// refreshImage brings a moving tag up to date so the run tests the image the
// caller named rather than a cached ancestor of it. See imageRefIsPullable for
// why this is not left to `docker run`.
func refreshImage(ctx context.Context, image string, logf func(string, ...any)) error {
	pullable, why := imageRefIsPullable(image)
	if !pullable {
		logf("not refreshing %s: %s", image, why)
		return nil
	}
	pullCtx, cancel := context.WithTimeout(ctx, imagePullTimeout)
	defer cancel()
	cmd := exec.CommandContext(pullCtx, "docker", "pull", "--quiet", image) //nolint:gosec
	cmd.Env = append(os.Environ(), "MSYS_NO_PATHCONV=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(string(out))
	if detail == "" {
		detail = err.Error()
	}
	cached, _ := dockerImageInfo(ctx, image)
	warning, fatal := pullFallback(cached, errors.New(detail), image)
	if fatal != nil {
		return fatal
	}
	logf("%s", warning)
	return nil
}

func startOvercastContainer(
	ctx context.Context,
	artifact overcastArtifact,
	apiPort, uiPort int,
	socket string,
	opts overcastOptions,
	logf func(string, ...any),
) (*managedOvercast, error) {
	image := artifact.Ref
	endpoint := fmt.Sprintf("http://%s:%d", opts.Host, apiPort)

	if err := refreshImage(ctx, image, logf); err != nil {
		return nil, err
	}
	// Say what is about to run, not just what it is called. A tag is a label
	// the caller chose; the digest is the only thing that identifies the bits,
	// and it is what makes a "this passed locally" claim checkable against the
	// digest in a release PR's RC comment.
	digest, revision := dockerImageInfo(ctx, image)
	if digest != "" {
		logf("resolved %s to %s", image, digest)
	}
	if note := imageSkewNote(image, revision, commitsAheadOf(ctx, revision)); note != "" {
		logf("%s", note)
	}

	logf("starting Overcast (%s) on %s", image, endpoint)

	gateway := dockerBridgeGateway(ctx, apiPort, uiPort)
	argv := dockerRunArgs(apiPort, uiPort, image, opts.LogLevel, gateway, socket)

	// MSYS_NO_PATHCONV stops Git Bash mangling the -p and -v arguments into
	// paths.
	cmd := exec.CommandContext(ctx, "docker", argv...)
	cmd.Env = append(os.Environ(), "MSYS_NO_PATHCONV=1")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("docker run %s: %w", image, err)
	}
	containerID := strings.TrimSpace(string(out))
	if containerID == "" {
		return nil, fmt.Errorf("docker run %s: no container id returned", image)
	}

	stop := func() {
		// Detached from ctx: the container must still be stopped when the run
		// was cancelled by Ctrl+C.
		stopCmd := exec.Command("docker", "stop", "-t", "3", containerID) //nolint:gosec
		_ = stopCmd.Run()
	}

	if err := waitForHealth(ctx, endpoint, opts.Timeout); err != nil {
		stop()
		return nil, err
	}
	oc := &managedOvercast{Endpoint: endpoint, Artifact: artifact, Stop: stop}
	if uiPort != 0 {
		oc.UIURL = fmt.Sprintf("http://%s:%d", opts.Host, uiPort)
	}
	return oc, nil
}

// waitForHealth polls /_overcast/health until the instance answers or the timeout
// expires.
func waitForHealth(ctx context.Context, endpoint string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	url := strings.TrimSuffix(endpoint, "/") + "/_overcast/health"
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return fmt.Errorf("health check %s: %w", url, err)
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("Overcast at %s did not become healthy within %s", endpoint, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// installUIDeps installs a UI project's dependencies. It runs even when
// node_modules already exists: npm is a no-op when the tree is current, and a
// tree installed on another platform — a repo shared between a Windows host
// and a Linux container is the common case — resolves to native binaries this
// machine cannot load. Reinstalling repairs that; skipping it produces a
// MODULE_NOT_FOUND from deep inside the bundler instead.
func installUIDeps(ctx context.Context, npm, dir string, logf func(format string, args ...any)) error {
	logf("installing UI dependencies…")
	install := exec.CommandContext(ctx, npm, "install", "--silent", "--no-audit", "--no-fund")
	install.Dir = dir
	install.Stdout = os.Stderr
	install.Stderr = os.Stderr
	if err := install.Run(); err != nil {
		return fmt.Errorf("npm install in %s: %w", dir, err)
	}
	return nil
}

// buildDashboardUI installs the dashboard UI's dependencies and runs its
// production build. Used by --build-ui so the stable (non-HMR) dashboard is a
// single command on every platform.
func buildDashboardUI(ctx context.Context, dir string, logf func(format string, args ...any)) error {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	npm, err := exec.LookPath("npm")
	if err != nil {
		return fmt.Errorf("npm not found on PATH — needed to build the dashboard UI")
	}
	if err := installUIDeps(ctx, npm, dir, logf); err != nil {
		return err
	}
	logf("building the dashboard UI…")
	build := exec.CommandContext(ctx, npm, "run", "build", "--silent")
	build.Dir = dir
	build.Stdout = os.Stderr
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return fmt.Errorf("npm run build in %s: %w", dir, err)
	}
	// The committed dist/.gitkeep that keeps `//go:embed all:ui/dist` resolving
	// is restored by the build itself — see the keep-dist-placeholder plugin in
	// compat/ui/vite.config.ts, which covers every way the UI gets built.
	return nil
}

// viteOptions configures the dashboard UI dev server.
type viteOptions struct {
	// Dir is the UI project directory, e.g. "compat/ui".
	Dir string
	// CompatURL is the compat server the Vite proxy forwards API calls to.
	CompatURL string
	// PortBase is where the scan for the Vite port starts.
	PortBase int
	// Timeout bounds the wait for the dev server to answer.
	Timeout time.Duration
	// Logf receives progress lines.
	Logf func(format string, args ...any)
}

// startViteDev installs dependencies and runs the Vite dev server on a free
// port, returning its URL. Used by --ui-dev / --dev for hot reloading.
func startViteDev(ctx context.Context, opts viteOptions) (string, func(), error) {
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 90 * time.Second
	}
	npm, err := exec.LookPath("npm")
	if err != nil {
		return "", nil, fmt.Errorf("npm not found on PATH — needed for the hot-reloading UI (use --serve without --ui-dev for the embedded UI)")
	}
	if err := installUIDeps(ctx, npm, opts.Dir, logf); err != nil {
		return "", nil, err
	}

	port, err := freePort(opts.PortBase)
	if err != nil {
		return "", nil, err
	}
	url := fmt.Sprintf("http://localhost:%d", port)
	logf("starting Vite dev server on %s", url)

	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	cmd := exec.CommandContext(runCtx, npm, "run", "dev", "--silent", "--",
		"--port", strconv.Itoa(port), "--strictPort")
	cmd.Dir = opts.Dir
	// The Vite config reads COMPAT_SERVER_URL to point its API proxy at the
	// compat server, whose port is also chosen at runtime.
	cmd.Env = append(os.Environ(), "COMPAT_SERVER_URL="+opts.CompatURL)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return "", nil, fmt.Errorf("start vite: %w", err)
	}

	stopped := make(chan struct{})
	stop := func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
		}
	}
	go func() {
		_ = cmd.Wait()
		close(stopped)
	}()

	if err := waitForHTTP(ctx, url, opts.Timeout); err != nil {
		stop()
		return "", nil, err
	}
	return url, stop, nil
}

// waitForHTTP polls a URL until it answers anything at all.
func waitForHTTP(ctx context.Context, url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not start within %s", url, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// browserCommand returns the platform command that opens a URL.
func browserCommand(url string) []string {
	switch runtime.GOOS {
	case "windows":
		// rundll32 avoids cmd.exe's quoting rules around & and ? in URLs.
		return []string{"rundll32", "url.dll,FileProtocolHandler", url}
	case "darwin":
		return []string{"open", url}
	default:
		return []string{"xdg-open", url}
	}
}

// openBrowser opens a URL, ignoring failures — a headless machine is not an
// error, the URL is always printed too.
func openBrowser(url string) {
	argv := browserCommand(url)
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec
	_ = cmd.Start()
}
