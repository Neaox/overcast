// endpoint_fallback_test.go pins the shape of the queue URL minted when a
// request carries no dialable origin — the fallback branch of
// (*sqs.Handler).queueURLBase, taken for callers on a real AWS hostname,
// internal callers, and background work.
//
// This is the second half of the addressing story in endpoint_addressing_test.go.
// That file covers "echo the caller's origin"; this one covers "when there is no
// caller origin to echo, use the configured external one" — and specifically
// that the result is a base a client can actually dial.
//
// See docs/plans/host-routing-precedence.md for why an unrepresentative harness
// default is the failure mode worth guarding here.
package sqs_test

import (
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// TestQueueURL_awsHostFallbackIsDialable asserts the AWS-hostname fallback mints
// a queue URL on the origin clients actually reach Overcast on: the configured
// hostname (OVERCAST_HOSTNAME) at the port the server is listening on.
//
// The existing TestQueueURL_awsHostFallsBackToConfiguredOrigin cannot catch this,
// because it builds its expectation from srv.Config.ExternalBaseURL() — the same
// call the handler makes. Both sides agree on any value, correct or not.
//
// They currently agree on "http://localhost:0". Config.ExternalBaseURL() formats
// cfg.Port verbatim, and the harness leaves Port at 0 because httptest assigns
// the real port after the config is built. Port 0 is not a port: nothing can
// dial it, and an SQS client handed such a URL fails outright, since AWS SDKs
// resolve the SQS endpoint from the QueueUrl rather than from client config
// (see internal/middleware/clientendpoint.go).
//
// So every assertion in the suite that is anchored to Config.ExternalBaseURL()
// — this fallback, the CloudFormation queue-URL checks, SNS, ECR, AppSync — is
// pinned to a base no real deployment produces.
func TestQueueURL_awsHostFallbackIsDialable(t *testing.T) {
	// Given: an emulator whose external hostname is configured (the harness
	// default, "localhost") and which is listening on a real port.
	srv := helpers.NewTestServer(t)

	// When: a queue is created by a caller arriving on a real AWS hostname, so
	// there is no dialable origin to echo back and the fallback is taken.
	got := createQueueFromHost(t, srv, "sqs.us-east-1.amazonaws.com", "aws-host-dialable")

	// Then: the queue URL is built on the configured hostname at the port the
	// server actually bound — a base a client on this machine can dial.
	want := srv.ExternalBase() + "/000000000000/aws-host-dialable"
	if got != want {
		t.Errorf("QueueUrl = %q, want %q", got, want)
	}

	// And, stated independently of the expected string so the reason a failure
	// matters is legible from the output alone: the URL carries a usable port.
	if strings.Contains(got, ":0/") || strings.HasSuffix(got, ":0") {
		t.Errorf("QueueUrl %q carries port 0 — no client can dial it; "+
			"Config.ExternalBaseURL() formats cfg.Port, which the harness never "+
			"updates to the port httptest bound", got)
	}
}
