package docker

// registry_test.go — the daemon-vantage registry probe and the taxonomy that
// reads its answers. The probe exists because push, pull and login are all
// performed by the daemon, so only the daemon can say whether a registry
// address works; the taxonomy exists because "the registry answered and
// refused" and "the registry could not be reached" deserve opposite verdicts
// everywhere they are consumed.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestDistributionInspect_asksTheDaemonNotTheRegistry(t *testing.T) {
	// Given: a fake daemon whose /distribution endpoint records the ref.
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"Descriptor":{}}`))
	}))
	defer srv.Close()
	c := NewClient("tcp://"+strings.TrimPrefix(srv.URL, "http://"), zap.NewNop())

	// When: a reference is inspected.
	if err := c.DistributionInspect(context.Background(), "localhost:4510/probe:none"); err != nil {
		t.Fatalf("DistributionInspect: %v", err)
	}

	// Then: the request went to the daemon's distribution endpoint — the
	// daemon does the dialing, which is the entire point of the probe.
	if !strings.HasPrefix(gotPath, "/v1.45/distribution/") {
		t.Errorf("path = %q, want a /distribution inspect", gotPath)
	}
}

func TestDistributionInspect_carriesTheDaemonsRefusal(t *testing.T) {
	// Given: a daemon that reports the registry answered with a refusal.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"unauthorized: authentication required"}`))
	}))
	defer srv.Close()
	c := NewClient("tcp://"+strings.TrimPrefix(srv.URL, "http://"), zap.NewNop())

	// When: a reference is inspected.
	err := c.DistributionInspect(context.Background(), "localhost:4510/probe:none")

	// Then: the error carries the daemon's message, and the taxonomy reads it
	// as an answer rather than an unreachable registry.
	if err == nil {
		t.Fatal("expected an error")
	}
	if RegistryUnreachable(err) {
		t.Errorf("an authorization refusal proves the path; it must not read as unreachable: %v", err)
	}
}

func TestRegistryUnreachable_taxonomy(t *testing.T) {
	// The classification decides opposite verdicts — a test skip vs a test
	// failure, a startup warning vs silence — so both directions are pinned.
	unreachable := []string{
		"distribution inspect x: status 500: dial tcp 127.0.0.1:4510: connect: connection refused",
		"pull image x: Client.Timeout exceeded while awaiting headers",
		"lookup registry.example.com: no such host",
		"read tcp: connection reset by peer",
	}
	for _, msg := range unreachable {
		if !RegistryUnreachable(errors.New(msg)) {
			t.Errorf("%q should read as unreachable", msg)
		}
	}
	answered := []string{
		"manifest unknown: manifest unknown",
		"unauthorized: authentication required",
		"pull access denied, repository does not exist or may require authorization: authorization failed: no basic auth credentials",
		"repository not found",
	}
	for _, msg := range answered {
		if RegistryUnreachable(errors.New(msg)) {
			t.Errorf("%q is the registry answering; it must not read as unreachable", msg)
		}
	}
	if RegistryUnreachable(nil) {
		t.Error("nil error misclassified")
	}
}
