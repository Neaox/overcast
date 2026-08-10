package helpers

import (
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/docker"
)

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
