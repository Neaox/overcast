package helpers

// golden.go implements the wire-byte golden test harness described in
// docs/plans/wire-byte-goldens.md. A golden test captures the exact HTTP
// response (status, an allowlisted header subset, and the raw body) for one
// deterministic AWS operation and compares it byte-for-byte against a
// checked-in fixture.
//
// Recording vs asserting is controlled by a single flag:
//
//	go test ./tests/integration/<svc>/ -run TestGolden -record   # write goldens
//	go test ./tests/integration/<svc>/ -run TestGolden            # assert goldens
//
// The harness does not stand up a separate "legacy" server: the golden file
// always captures whatever HTTP path currently serves the request. Today
// that is the legacy JSON/XML dispatch path for every hybrid service (native
// protocol traffic never reaches the typed dispatcher). When a later P2
// delegation change makes the legacy handler a decode-shim to the typed
// implementation — or removes the legacy handler entirely — the exact same
// test, run without -record, either keeps passing (byte-identical output,
// migration proven safe) or fails loudly (a real regression to fix before
// merging). Re-recording is only appropriate for a deliberate, reviewed
// behaviour change.
//
// Determinism is the caller's responsibility: use helpers.WithMockClock()
// (stopped at the Unix epoch) so every timestamp field is reproducible, and
// only exercise operations whose output contains no UUIDs, host:port pairs,
// or other per-run randomness. Operations that can't be made deterministic
// this way (message-plane sends/receives, anything minting a UUID) are
// excluded per-service with a documented reason — see the golden_test.go
// file in each covered service package.
import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// recordGoldens is the -record flag. Pass it to (re)write golden fixtures
// from the current response; omit it to assert against the committed files.
//
// Flags are process-global in Go's testing package, so this is safe to
// declare once here and share across every golden_test.go in every service
// package — `go test ./tests/integration/... -record` records everywhere in
// one pass, and a scoped `go test ./tests/integration/sqs/ -record` records
// only that package's goldens.
var recordGoldens = flag.Bool("record", false, "write wire-byte golden fixtures instead of asserting against them")

// GoldenHeaders lists the only response headers a golden fixture ever
// captures or compares. Request-ID headers (x-amzn-requestid,
// x-amz-request-id) are deliberately excluded — they are unique per request
// by design (see internal/middleware/requestid.go) and would make every
// golden fail on every run even with a mock clock.
var GoldenHeaders = []string{"Content-Type"}

// goldenFixture is the on-disk JSON shape of a golden file. Body is stored
// as a Go string (not raw JSON) so that byte-for-byte comparison — including
// whitespace, key order, and any XML/JSON serialization quirks — survives
// the round trip through this wrapper format untouched.
type goldenFixture struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// NormalizeFunc redacts expected non-determinism (e.g. an httptest-assigned
// host:port embedded in a response body) from raw response bytes before the
// golden comparison. Most operations need none — pass nil. Each service
// defines its own normalize functions, named and justified next to the
// golden test that uses them (docs/plans/wire-byte-goldens.md §4.4).
type NormalizeFunc func(body []byte) []byte

// GoldenTest asserts (or, under -record, writes) a wire-byte golden fixture
// for one AWS operation response.
//
// dir is the golden directory for the service, conventionally
// "goldens" (relative to the test package — Go runs tests with the package
// directory as the working directory) so fixtures land in
// tests/integration/<service>/goldens/. action is the AWS operation name and
// doubles as the fixture's filename (<action>.json).
//
// resp is the *http.Response already produced by calling the operation
// against a helpers.TestServer (typically via the service's existing
// <svc>Call test helper) — GoldenTest does not perform the HTTP round trip
// itself, it only captures/compares the result. The caller retains
// ownership of resp and should not also call resp.Body.Close() before
// passing it in; GoldenTest reads and closes the body.
//
// normalize, if non-nil, is applied to the raw body bytes before recording
// and before comparison, so both sides of the comparison go through the
// same redaction.
//
// Usage:
//
//	resp := ssmCall(t, srv, "DescribeParameters", nil)
//	helpers.GoldenTest(t, "goldens", "DescribeParameters", resp, nil)
func GoldenTest(t *testing.T, dir, action string, resp *http.Response, normalize NormalizeFunc) {
	t.Helper()

	bodyBytes, err := io.ReadAll(resp.Body)
	resp.Body.Close() //nolint:errcheck
	if err != nil {
		t.Fatalf("golden %s: read response body: %v", action, err)
	}
	if normalize != nil {
		bodyBytes = normalize(bodyBytes)
	}

	headers := map[string]string{}
	for _, h := range GoldenHeaders {
		if v := resp.Header.Get(h); v != "" {
			headers[h] = v
		}
	}

	got := goldenFixture{
		Status:  resp.StatusCode,
		Headers: headers,
		Body:    string(bodyBytes),
	}

	path := filepath.Join(dir, action+".json")

	if *recordGoldens {
		writeGolden(t, path, got)
		t.Logf("golden %s: recorded %s", action, path)
		return
	}

	want := readGolden(t, action, path)

	if got.Status != want.Status {
		t.Errorf("golden %s: status = %d, want %d", action, got.Status, want.Status)
	}
	for k, wantV := range want.Headers {
		if gotV := got.Headers[k]; gotV != wantV {
			t.Errorf("golden %s: header %s = %q, want %q", action, k, gotV, wantV)
		}
	}
	if got.Body != want.Body {
		t.Errorf("golden %s: body mismatch (byte-for-byte compare)\n got:  %s\nwant: %s", action, got.Body, want.Body)
	}
}

// writeGolden marshals fx as indented, non-HTML-escaped JSON (so bodies
// containing "<", ">", "&" stay human-legible in review diffs) and writes it
// to path, creating the parent directory if needed.
func writeGolden(t *testing.T, path string, fx goldenFixture) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("golden: mkdir %s: %v", filepath.Dir(path), err)
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(fx); err != nil {
		t.Fatalf("golden: marshal %s: %v", path, err)
	}

	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("golden: write %s: %v", path, err)
	}
}

// readGolden reads and decodes the fixture at path, failing with a message
// that points at -record when the file doesn't exist yet.
func readGolden(t *testing.T, action, path string) goldenFixture {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden %s: read %s: %v (run with -record to create it)", action, path, err)
	}
	var fx goldenFixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("golden %s: unmarshal %s: %v", action, path, err)
	}
	return fx
}
