package router

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/state"
)

// health_compat_test.go — the two health URLs that are not Overcast's own.
//
// Every test here goes through the real router.New() wiring rather than
// calling a handler directly, because routing is the whole claim: before these
// routes existed both paths matched the S3 catch-all and answered a 404 with
// an XML NoSuchBucket body. A handler-level test would pass with the routes
// unregistered and prove nothing.

func newHealthCompatServer(t *testing.T) *httptest.Server {
	t.Helper()
	cfg := &config.Config{
		Host:      "127.0.0.1",
		Port:      0,
		Region:    "us-east-1",
		AccountID: "000000000000",
		State:     config.StateBackendMemory,
		LogLevel:  "error",
		Version:   "test-version",

		ShutdownTimeout: 0,
		SigV4Validate:   false,
	}
	handler, preShutdown, cleanup, _ := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())
	srv := httptest.NewServer(handler)
	t.Cleanup(func() {
		preShutdown()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		cleanup(ctx)
		srv.Close()
	})
	return srv
}

func getJSON(t *testing.T, url string) (int, map[string]any, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	var decoded map[string]any
	_ = json.Unmarshal(raw, &decoded)
	return resp.StatusCode, decoded, string(raw)
}

// TestLegacyHealthPath_isServedAsAnAliasOfOvercastHealth pins the URL
// Overcast's own health endpoint used before #927. The body must be the same
// one, not merely a 200: `overcast status`, the compose healthcheck and the
// Testcontainers module all read fields out of it.
func TestLegacyHealthPath_isServedAsAnAliasOfOvercastHealth(t *testing.T) {
	srv := newHealthCompatServer(t)

	// Given: the canonical endpoint's answer.
	canonicalStatus, canonical, _ := getJSON(t, srv.URL+"/_overcast/health")
	if canonicalStatus != http.StatusOK {
		t.Fatalf("GET /_overcast/health: status = %d, want 200", canonicalStatus)
	}

	// When: the same server is asked at the pre-#927 path.
	status, alias, body := getJSON(t, srv.URL+"/_health")

	// Then: it answers, rather than falling through to the S3 catch-all.
	if status != http.StatusOK {
		t.Fatalf("GET /_health: status = %d, want 200 (body: %s)", status, body)
	}
	// And: with Overcast's own health body — same shape, same fields. The
	// timestamp differs between the two calls, so compare everything else.
	for _, field := range []string{"status", "version", "services", "serviceTiers", "storage"} {
		if _, ok := alias[field]; !ok {
			t.Errorf("GET /_health: response has no %q field; body: %s", field, body)
		}
	}
	if alias["version"] != canonical["version"] {
		t.Errorf("GET /_health: version = %v, want %v", alias["version"], canonical["version"])
	}
	if alias["status"] != canonical["status"] {
		t.Errorf("GET /_health: status field = %v, want %v", alias["status"], canonical["status"])
	}
}

// TestLocalStackHealthPath_answersInLocalStacksShape covers the URL a compose
// healthcheck or Testcontainers wait strategy carried over from LocalStack
// polls. Those callers parse the body: the LocalStack CLI renders the
// `services` map, so a 200 with a body they cannot read is only half a fix.
func TestLocalStackHealthPath_answersInLocalStacksShape(t *testing.T) {
	srv := newHealthCompatServer(t)

	status, body, raw := getJSON(t, srv.URL+"/_localstack/health")
	if status != http.StatusOK {
		t.Fatalf("GET /_localstack/health: status = %d, want 200 (body: %s)", status, raw)
	}

	services, ok := body["services"].(map[string]any)
	if !ok {
		t.Fatalf("GET /_localstack/health: no services map; body: %s", raw)
	}
	if len(services) == 0 {
		t.Fatalf("GET /_localstack/health: services map is empty; body: %s", raw)
	}
	// A service Overcast enables by default, reported in LocalStack's own
	// vocabulary — anything else and `localstack status services` prints a
	// blank column against it.
	if got := services["s3"]; got != localStackServiceStatus {
		t.Errorf("services[s3] = %v, want %q; body: %s", got, localStackServiceStatus, raw)
	}
	if body["edition"] == "" || body["edition"] == nil {
		t.Errorf("GET /_localstack/health: edition is absent; body: %s", raw)
	}
	if body["version"] != "test-version" {
		t.Errorf("GET /_localstack/health: version = %v, want the configured version; body: %s", body["version"], raw)
	}
	// Overcast's own marker, so a LocalStack-shaped body is never mistaken for
	// LocalStack itself.
	if body["emulator"] != "overcast" {
		t.Errorf("GET /_localstack/health: emulator = %v, want \"overcast\"; body: %s", body["emulator"], raw)
	}
}

// TestLocalStackNamespace_404sAsItselfNotAsS3 is the reason the wildcard route
// exists. Every /_localstack/* path used to answer S3's NoSuchBucket, which
// tells the reader nothing about what is wrong — and is the shape that made a
// misconfigured healthcheck so hard to recognise in the first place.
//
// The example is /_localstack/info: a path with a mapping but no Overcast
// endpoint that could answer it, which is exactly what this 404 is for.
// /_localstack/state/reset used to stand here and no longer can — it is served
// now (see localstack_compat.go), which is a better outcome and a worse
// example.
func TestLocalStackNamespace_404sAsItselfNotAsS3(t *testing.T) {
	srv := newHealthCompatServer(t)

	status, body, raw := getJSON(t, srv.URL+"/_localstack/info")
	if status != http.StatusNotFound {
		t.Fatalf("GET /_localstack/info: status = %d, want 404 (body: %s)", status, raw)
	}
	message, _ := body["message"].(string)
	if message == "" {
		t.Fatalf("GET /_localstack/info: no message; body: %s", raw)
	}
	// The 404 has to name the endpoint that replaces it, which is the whole
	// difference between this and the catch-all.
	if want := "/_overcast/debug/config"; !strings.Contains(message, want) {
		t.Errorf("GET /_localstack/info: message %q does not name %q", message, want)
	}
	if raw == "" || strings.Contains(raw, "NoSuchBucket") || strings.Contains(raw, "NoSuchKey") {
		t.Errorf("GET /_localstack/info answered as S3: %s", raw)
	}
}

// TestLocalStackNamespace_unknownPathStillPointsAtTheNamespace covers a path
// with no mapping of its own: it must still say where Overcast's endpoints
// live rather than fall back to an S3 error.
func TestLocalStackNamespace_unknownPathStillPointsAtTheNamespace(t *testing.T) {
	srv := newHealthCompatServer(t)

	status, body, raw := getJSON(t, srv.URL+"/_localstack/something-we-never-had")
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body: %s)", status, raw)
	}
	message, _ := body["message"].(string)
	if !strings.Contains(message, "/_overcast/") {
		t.Errorf("message %q does not point at the /_overcast/ namespace", message)
	}
}
