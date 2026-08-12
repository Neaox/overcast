package helpers

import (
	"context"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/docker"
)

// ReserveTCPPort returns a host port that was free a moment ago, for a test
// that must name a fixed port before the thing that will listen on it exists —
// WithECRRegistryPort is the case this was written for.
//
// The listener is closed before returning, so this is a hint rather than a
// reservation: another process can take the port in between. That is tolerable
// here because the caller's fallback is benign (the ECR registry falls back to
// an ephemeral port and the test skips), and because the point is a port no
// *other test* will claim, which a kernel-assigned one reliably is.
func ReserveTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a TCP port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("release the reserved port: %v", err)
	}
	return port
}

// DockerLoginOrSkip authenticates the Docker CLI against a registry, skipping
// the test when the daemon will not talk plain HTTP to it.
//
// That refusal is daemon configuration a test cannot change and says nothing
// about Overcast, so it is a gate rather than a failure. Any other login error
// is real and fails.
//
// The matching logout is not tidiness — it is the difference between a suite
// that cleans up after itself and one that degrades the machine it runs on.
// `docker login` records the credential per registry address, in two places at
// once: an `auths` key in ~/.docker/config.json, and an entry in the platform
// credential store named by `credsStore` (Windows Credential Manager, the macOS
// keychain, libsecret). Every registry here is `localhost:<ephemeral port>`, so
// each run of each test mints an address that will never be seen again and
// leaves an entry behind for it — permanently, accumulating across every run by
// every contributor.
//
// That has a hard ceiling. A saturated Windows credential store fails the next
// login with "Not enough memory resources are available to process this
// command", which reads as a machine problem rather than as this suite's litter
// and takes every Docker-gated test down with it. It was found at ~200 entries
// on a development machine.
//
// So the test logs out of what it logged in to. `docker logout` is the
// supported way to remove both records and normalises the scheme, so the same
// proxyEndpoint string the login was given matches what was stored.
//
// It is an improvement rather than a cure, and the shape of what is left
// matters. Measured on Windows with Docker Desktop: a single test leaks
// nothing, and a serial run of the ECR and ECS suites leaks nothing, where
// before this every login leaked. Run concurrently the cleanup often does not
// take — around three entries survived running those two packages together,
// and around fifteen survived a full `go test ./...` on a loaded machine. The
// credential helper is a separate process mutating one shared store, and the
// logout is best-effort with a bounded timeout, so under contention it races or
// gives up. Unbounded growth is now bounded growth, which is worth having and
// is not the same as fixed.
//
// Two things were tried and rejected, so they are not re-attempted: isolating
// DOCKER_CONFIG to a temp directory (with no config.json the CLI *detects* the
// platform helper rather than falling back to a file store, and Docker Desktop
// maintains ~/.docker/config.json itself regardless), and reading the residue
// as a shared-file race in config.json (it is the helper, not the file). If
// this needs to be airtight, the promising direction is forcing the file store
// with an explicit empty `credHelpers` entry for the registry, which removes
// the shared store from the picture entirely — untested here for want of a
// live registry to log in to.
func DockerLoginOrSkip(t *testing.T, proxy, user, password string) {
	t.Helper()
	// Registered before the login so a partial one — the credential stored, the
	// command still reporting failure — is cleaned up too. Logging out of a
	// registry that was never logged in to is a no-op, and this runs on a fresh
	// context because t's is already cancelled by cleanup time.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "docker", "logout", proxy).Run()
	})
	cmd := exec.CommandContext(t.Context(), "docker", "login", proxy, "-u", user, "--password-stdin")
	cmd.Stdin = strings.NewReader(password + "\n")
	out, err := cmd.CombinedOutput()
	if err == nil {
		return
	}
	msg := string(out)
	if strings.Contains(msg, "server gave HTTP response to HTTPS client") ||
		strings.Contains(msg, "https://") ||
		strings.Contains(msg, "context deadline exceeded") {
		t.Skipf("docker daemon will not talk plain HTTP to %s: %s", proxy, strings.TrimSpace(msg))
	}
	t.Fatalf("docker login %s: %v\n%s", proxy, err, out)
}

// removeTestNetworks deletes the Docker networks a test server minted for
// itself, best-effort: a daemon that never answered created none, and one that
// still holds a container on a network refuses to remove it, which the next
// run's sweep of exited containers resolves. Neither is worth failing a test
// that has already made its assertions.
func removeTestNetworks(names []string) {
	dc := docker.NewClient(TestDockerSocket(), zap.NewNop())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, name := range names {
		if name == "" {
			continue
		}
		_ = dc.RemoveNetwork(ctx, name)
	}
}

// removeTestRegistryVolume deletes the storage volume a fixed-port registry
// claim created for this test server, best-effort and for the same reasons
// removeTestNetworks is.
//
// The volume is the point of the fixed-port claim in production — a restart
// there is meant to find the last run's images — and exactly the wrong thing to
// keep in a suite, where every server takes a fresh reserved port. Left alone
// the suite accumulates one volume per Docker-gated registry test, per run,
// each with a port in its name that nothing will ever claim again.
//
// Force, because the registry container may still be on its way out: the
// server's own cleanup removes it and Docker's removal is asynchronous, so an
// unforced delete races the container's disappearance and loses.
func removeTestRegistryVolume(port int) {
	if port <= 0 {
		return
	}
	dc := docker.NewClient(TestDockerSocket(), zap.NewNop())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = dc.RemoveVolume(ctx, "overcast-ecr-registry-data-"+strconv.Itoa(port), true)
}

// SkipWithoutDocker calls t.Skip if Docker is not reachable. Use at the top of
// tests that need a running daemon (ECS, Lambda, RDS, ElastiCache, MSK, EFS).
func SkipWithoutDocker(t *testing.T) {
	t.Helper()
	dc := docker.NewClient("", nil)
	if err := dc.Ping(t.Context()); err != nil {
		t.Skipf("skipping: Docker is not available (%v)", err)
	}
}

// Docker-dependent tests that need an image from a public registry have two
// distinct ways to go wrong, and they deserve opposite verdicts.
//
// A registry that cannot be reached — Docker Hub timing out, DNS failing, or
// the anonymous pull-rate limit rejecting a shared CI egress IP — says nothing
// about Overcast. Failing the build for it teaches people to re-run the gate
// instead of reading it, which is exactly what the compat flake policy exists
// to prevent. That is an environmental gate, like the absence of a daemon.
//
// A registry that answers and refuses — unknown manifest, unauthorized, no
// such image — is a real problem with the test or the emulator, and must fail.

// PullOrSkip fetches image so a test can use it, skipping the test when the
// registry is unreachable and failing it when the registry answers with a
// refusal. Pulling up front also warms the daemon's cache for the emulator,
// whose own pull then resolves locally.
func PullOrSkip(t *testing.T, dc *docker.Client, image string) {
	t.Helper()

	// One retry: the transient failures here are timeouts and rate limits,
	// both of which frequently clear within seconds. More than one retry just
	// makes a real outage take longer to report.
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			time.Sleep(3 * time.Second)
		}
		if err = dc.PullImage(t.Context(), image); err == nil {
			return
		}
		if !RegistryUnreachable(err) {
			t.Fatalf("pull %s: %v", image, err)
		}
	}
	t.Skipf("skipping: the registry serving %s could not be reached, which is an "+
		"environmental failure rather than an Overcast one: %v", image, err)
}

// RegistryUnreachable reports whether err is the registry being unavailable —
// a transport failure or a pull-rate rejection — rather than the registry
// answering that the image cannot be had. The taxonomy lives in the docker
// package, which uses the same distinction for the ECR registry's own startup
// probe; this wrapper keeps the name test files already use.
func RegistryUnreachable(err error) bool {
	return docker.RegistryUnreachable(err)
}
