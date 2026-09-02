package router

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// localstack_compat_test.go — the rest of LocalStack's operational namespace.
//
// Like health_compat_test.go, every test goes through the real router.New()
// wiring rather than calling a handler directly: routing is the whole claim.
// Before these routes existed each path matched the S3 catch-all and answered
// an XML NoSuchBucket, which is a 404 with the wrong story attached.

// TestLocalStackInitAliasIsByteIdentical is the test that keeps "alias" from
// becoming "approximation". The two paths share one handler value today, so
// the bodies cannot differ — and this fails the moment someone gives the
// compatibility path its own translation layer without noticing that
// LocalStack's contract and Overcast's were already the same.
func TestLocalStackInitAliasIsByteIdentical(t *testing.T) {
	srv := newHealthCompatServer(t)

	for _, paths := range [][2]string{
		{"/_overcast/init", "/_localstack/init"},
		{"/_overcast/init/ready", "/_localstack/init/ready"},
	} {
		t.Run(paths[1], func(t *testing.T) {
			canonicalStatus, canonical := getRaw(t, srv.URL+paths[0])
			aliasStatus, alias := getRaw(t, srv.URL+paths[1])

			if canonicalStatus != http.StatusOK || aliasStatus != http.StatusOK {
				t.Fatalf("status: canonical %d, alias %d — both must be 200", canonicalStatus, aliasStatus)
			}
			if canonical != alias {
				t.Fatalf("bodies differ.\n canonical %s: %s\n     alias %s: %s",
					paths[0], canonical, paths[1], alias)
			}
		})
	}
}

// TestLocalStackInitAliasShape pins the field names LocalStack's own callers
// parse. Sharing a handler makes the two paths agree with each other; only
// this pins what they agree on.
func TestLocalStackInitAliasShape(t *testing.T) {
	srv := newHealthCompatServer(t)

	status, body, raw := getJSON(t, srv.URL+"/_localstack/init")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, raw)
	}

	completed, ok := body["completed"].(map[string]any)
	if !ok {
		t.Fatalf("completed is %T, want an object keyed by stage: %s", body["completed"], raw)
	}
	// LocalStack's stage vocabulary, which Overcast's init hooks already use.
	// A caller polling for readiness reads exactly this key.
	for _, stage := range []string{"BOOT", "START", "READY", "SHUTDOWN"} {
		if _, present := completed[stage]; !present {
			t.Errorf("completed is missing stage %q: %s", stage, raw)
		}
	}
	if _, present := body["scripts"]; !present {
		t.Errorf("body has no scripts array: %s", raw)
	}
}

func TestLocalStackInitAliasRejectsUnknownStage(t *testing.T) {
	srv := newHealthCompatServer(t)

	status, _, raw := getJSON(t, srv.URL+"/_localstack/init/nonsense")
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 — an unknown stage is a bad request, not a missing bucket (body: %s)",
			status, raw)
	}
}

// TestLocalStackResetAlias covers the one path here that mutates: a POST, on
// the same handler as /_overcast/reset.
func TestLocalStackResetAlias(t *testing.T) {
	srv := newHealthCompatServer(t)

	resp, err := http.Post(srv.URL+"/_localstack/state/reset", "", nil)
	if err != nil {
		t.Fatalf("POST /_localstack/state/reset: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", resp.StatusCode, raw)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body is not JSON: %s", raw)
	}
	if body["status"] != "reset" {
		t.Fatalf("body = %s, want Overcast's own {\"status\":\"reset\"}", raw)
	}
}

// TestLocalStackResetAliasIsPOSTOnly: a GET must not silently do nothing and
// answer 200. It falls to the namespace's 404, which names where to go.
func TestLocalStackResetAliasIsPOSTOnly(t *testing.T) {
	srv := newHealthCompatServer(t)

	status, _, raw := getJSON(t, srv.URL+"/_localstack/state/reset")
	if status != http.StatusNotFound {
		t.Fatalf("GET status = %d, want 404 — reset is a POST (body: %s)", status, raw)
	}
	if strings.Contains(raw, "NoSuchBucket") {
		t.Fatalf("a GET fell through to the S3 catch-all: %s", raw)
	}
}

// TestLocalStackEndpointMapNamesOnlyUnservedPaths: the 404's "here is where it
// went" map must not name a path Overcast actually answers. Such an entry
// could never be reached, so it could only ever go stale — and it would read,
// to anyone maintaining this, as though the path were unserved.
func TestLocalStackEndpointMapNamesOnlyUnservedPaths(t *testing.T) {
	srv := newHealthCompatServer(t)

	for endpoint := range localStackEndpointMap {
		status, _, _ := getJSON(t, srv.URL+"/_localstack/"+endpoint)
		if status != http.StatusNotFound {
			t.Errorf("/_localstack/%s answers %d — it is served, so remove it from localStackEndpointMap",
				endpoint, status)
		}
	}
}

// TestLocalStackServedPathsAreNotClaimedByTheWildcard guards the routing
// precedence the registration relies on: chi must prefer each static path over
// /_localstack/*. Cheap to assert, and the failure it catches (every alias
// silently 404ing) would otherwise only show up in someone's compose file.
func TestLocalStackServedPathsAreNotClaimedByTheWildcard(t *testing.T) {
	srv := newHealthCompatServer(t)

	for _, path := range []string{"/_localstack/health", "/_localstack/init", "/_localstack/init/ready"} {
		status, _, raw := getJSON(t, srv.URL+path)
		if status != http.StatusOK {
			t.Errorf("%s = %d, want 200 — the wildcard claimed it (body: %s)", path, status, raw)
		}
	}
}

// getRaw returns the status and the exact response body, for the comparisons
// that are about bytes rather than fields.
func getRaw(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return resp.StatusCode, string(body)
}
