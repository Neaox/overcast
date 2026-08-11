package helpers

import (
	"context"
	"net"
	"os/exec"
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
func DockerLoginOrSkip(t *testing.T, proxy, user, password string) {
	t.Helper()
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
