package docker

// registry_test.go — the daemon-vantage registry probe and the taxonomy that
// reads its answers. The probe exists because push, pull and login are all
// performed by the daemon, so only the daemon can say whether a registry
// address works; the taxonomy exists because "the registry answered and
// refused" and "the registry could not be reached" deserve opposite verdicts
// everywhere they are consumed.

import (
	"context"
	"encoding/base64"
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

func TestDistributionInspectWithAuth_offersTheCredentials(t *testing.T) {
	// Given: a daemon that records the auth header the probe carried.
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("X-Registry-Auth")
		_, _ = w.Write([]byte(`{"Descriptor":{}}`))
	}))
	defer srv.Close()
	c := NewClient("tcp://"+strings.TrimPrefix(srv.URL, "http://"), zap.NewNop())

	// When: a reference is inspected with credentials.
	err := c.DistributionInspectWithAuth(context.Background(), "localhost:4510/probe:none",
		&RegistryAuth{Username: "AWS", Password: "s3cret", ServerAddress: "localhost:4510"})
	if err != nil {
		t.Fatalf("DistributionInspectWithAuth: %v", err)
	}

	// Then: the daemon was given them to present to the registry — without
	// which its answer says only that something is listening.
	if gotAuth == "" {
		t.Fatal("probe carried no X-Registry-Auth")
	}
	raw, decErr := base64.URLEncoding.DecodeString(gotAuth)
	if decErr != nil {
		t.Fatalf("decode X-Registry-Auth: %v", decErr)
	}
	if !strings.Contains(string(raw), `"password":"s3cret"`) {
		t.Errorf("auth payload = %s, want the supplied password", raw)
	}
}

func TestDistributionInspectWithAuth_anonymousWhenNoCredentials(t *testing.T) {
	// Given: a daemon recording whether the header was present at all.
	var hadAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadAuth = r.Header["X-Registry-Auth"]
		_, _ = w.Write([]byte(`{"Descriptor":{}}`))
	}))
	defer srv.Close()
	c := NewClient("tcp://"+strings.TrimPrefix(srv.URL, "http://"), zap.NewNop())

	// When: the anonymous form is used.
	if err := c.DistributionInspect(context.Background(), "localhost:4510/probe:none"); err != nil {
		t.Fatalf("DistributionInspect: %v", err)
	}

	// Then: no empty credential header is sent — a public registry must see an
	// ordinary anonymous request.
	if hadAuth {
		t.Error("anonymous inspect sent an X-Registry-Auth header")
	}
}

func TestRegistryRejectedCredentials_taxonomy(t *testing.T) {
	// A rejection is two facts at once: something is listening, and it is not
	// ours. Both halves are load-bearing, so both directions are pinned.
	rejected := []string{
		"distribution inspect x: status 401: unauthorized: authentication required",
		"distribution inspect x: status 500: no basic auth credentials",
	}
	for _, msg := range rejected {
		if !RegistryRejectedCredentials(errors.New(msg)) {
			t.Errorf("%q should read as a credential rejection", msg)
		}
	}
	// A registry that let us in and simply has no such repository is ours.
	// Reading this as a rejection would make our own registry unidentifiable.
	accepted := []string{
		"distribution inspect x: status 404: manifest unknown: manifest unknown",
		"distribution inspect x: status 500: dial tcp [::1]:32768: connect: connection refused",
	}
	for _, msg := range accepted {
		if RegistryRejectedCredentials(errors.New(msg)) {
			t.Errorf("%q must not read as a credential rejection", msg)
		}
	}
	if RegistryRejectedCredentials(nil) {
		t.Error("nil error misclassified")
	}
}
