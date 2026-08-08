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
// agents) can run side by side.
package main

import (
	"context"
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
)

// --start-overcast modes.
const (
	startOvercastAuto   = "auto"
	startOvercastAlways = "always"
	startOvercastNever  = "never"
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
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
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
func overcastEnv(apiPort, uiPort int) []string {
	return []string{
		"OVERCAST_PORT=" + strconv.Itoa(apiPort),
		"OVERCAST_UI_PORT=" + strconv.Itoa(uiPort),
		"OVERCAST_STATE=memory",
	}
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
	// Image is the container image used when no binary is found.
	Image string
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
	// How it was started, for log lines: "binary" or "docker".
	Mode string
	// Stop terminates the instance. Safe to call more than once.
	Stop func()
}

// startOvercast brings up a throwaway instance on free ports and waits for it
// to answer /_health. It prefers a locally built binary (fast, and picks up
// uncommitted changes) and falls back to a container.
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

	apiPort, uiPort := 0, 0
	var err error
	if opts.WithUI {
		apiPort, uiPort, err = freePortPair(opts.PortBase)
	} else {
		apiPort, err = freePort(opts.PortBase)
	}
	if err != nil {
		return nil, err
	}

	if bin := findOvercastBinary(opts.Bin); bin != "" {
		return startOvercastBinary(ctx, bin, apiPort, uiPort, opts, logf)
	}
	if _, err := exec.LookPath("docker"); err == nil {
		return startOvercastContainer(ctx, apiPort, uiPort, opts, logf)
	}
	return nil, fmt.Errorf(
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
	bin string,
	apiPort, uiPort int,
	opts overcastOptions,
	logf func(string, ...any),
) (*managedOvercast, error) {
	endpoint := fmt.Sprintf("http://%s:%d", opts.Host, apiPort)
	logf("starting Overcast (%s) on %s", filepath.Base(bin), endpoint)

	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	cmd := exec.CommandContext(runCtx, bin, "serve")
	cmd.Env = append(os.Environ(), overcastEnv(apiPort, uiPort)...)
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
	oc := &managedOvercast{Endpoint: endpoint, Mode: "binary", Stop: stop}
	if uiPort != 0 {
		oc.UIURL = fmt.Sprintf("http://%s:%d", opts.Host, uiPort)
	}
	return oc, nil
}

func startOvercastContainer(
	ctx context.Context,
	apiPort, uiPort int,
	opts overcastOptions,
	logf func(string, ...any),
) (*managedOvercast, error) {
	endpoint := fmt.Sprintf("http://%s:%d", opts.Host, apiPort)
	logf("starting Overcast (%s) on %s", opts.Image, endpoint)

	argv := []string{
		"run", "-d", "--rm",
		"-p", fmt.Sprintf("%d:%d", apiPort, reservedAPIPort),
		"-e", "OVERCAST_STATE=memory",
		"-e", "OVERCAST_LOG_LEVEL=" + opts.LogLevel,
	}
	if uiPort != 0 {
		argv = append(argv, "-p", fmt.Sprintf("%d:%d", uiPort, reservedUIPort))
	}
	argv = append(argv, opts.Image)

	// MSYS_NO_PATHCONV stops Git Bash mangling the -p arguments into paths.
	cmd := exec.CommandContext(ctx, "docker", argv...)
	cmd.Env = append(os.Environ(), "MSYS_NO_PATHCONV=1")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("docker run %s: %w", opts.Image, err)
	}
	containerID := strings.TrimSpace(string(out))
	if containerID == "" {
		return nil, fmt.Errorf("docker run %s: no container id returned", opts.Image)
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
	oc := &managedOvercast{Endpoint: endpoint, Mode: "docker", Stop: stop}
	if uiPort != 0 {
		oc.UIURL = fmt.Sprintf("http://%s:%d", opts.Host, uiPort)
	}
	return oc, nil
}

// waitForHealth polls /_health until the instance answers or the timeout
// expires.
func waitForHealth(ctx context.Context, endpoint string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	url := strings.TrimSuffix(endpoint, "/") + "/_health"
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
