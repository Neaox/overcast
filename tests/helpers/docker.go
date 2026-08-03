package helpers

import (
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/docker"
)

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
// answering that the image cannot be had.
//
// Deliberately conservative: an error that does not match one of these stays a
// failure, because the cost of wrongly skipping is silent lost coverage while
// the cost of wrongly failing is one visibly red run.
func RegistryUnreachable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())

	// A refusal from a registry that did answer is never environmental, and
	// some of these carry wording that would otherwise match below.
	for _, genuine := range []string{
		"manifest unknown",
		"manifest for",
		"not found",
		"unauthorized",
		"authentication required",
		"denied",
		"no such image",
		"invalid reference",
	} {
		if strings.Contains(msg, genuine) {
			return false
		}
	}

	for _, environmental := range []string{
		"context deadline exceeded",
		"client.timeout exceeded",
		"i/o timeout",
		"tls handshake timeout",
		"no such host",
		"connection refused",
		"connection reset",
		"network is unreachable",
		"temporary failure in name resolution",
		"toomanyrequests",
		"too many requests",
		"rate limit",
		"503 service unavailable",
		"502 bad gateway",
	} {
		if strings.Contains(msg, environmental) {
			return true
		}
	}
	return false
}
