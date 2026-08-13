package router_test

// aws_error_forwarding_ledger_test.go — every ResponseWriter wrapper must
// forward RecordAWSError.
//
// protocol.recordAWSError finds the recorder by type-asserting the
// ResponseWriter the handler holds. That handler's writer is whatever the
// innermost wrapper happens to be, and the chain is assembled from two
// independent places: the middleware order in internal/router/router.go, and
// whatever a service wraps for its own reasons. A wrapper that does not
// forward silently truncates the chain — the assertion still succeeds, on some
// outer writer that nothing reads, so no error is raised and no test on either
// type alone can see it.
//
// It has now happened twice, in two different layers:
//
//   - middleware.RequestEvents builds a second responseWriter around Logger's,
//     so every service lost its recorded error (#964).
//   - dynamodb's crc32ResponseWriter and cloudfront's responseRecorder wrap
//     again inside that, so those two services lost it even after the first
//     fix.
//
// The cost is not only the trace list's search. The request log's
// aws_error_code field comes from the same value, and internal/trace's
// retention keeps a trace when an AWS error code is set even if the status is
// 2xx — so a swallowed error is also a failure that stops being retained.
//
// A behaviour test per wrapper cannot close this: it covers the wrappers
// somebody remembered to list, which is the failure mode itself. So the ledger
// below scans the shipping source, in the same shape as the request-ID ledger
// next to it.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// embedsWriter is how a wrapper is declared: an embedded http.ResponseWriter in
// a struct, which is what makes the outer type satisfy the interface while
// answering for only the methods it overrides.
const embedsWriter = "\thttp.ResponseWriter\n"

// forwardsAWSError is the method that keeps the chain intact.
const forwardsAWSError = "RecordAWSError"

// scannedRoots are the trees a request's ResponseWriter can be wrapped in.
// Anything outside them is not in the serving path.
var scannedRoots = []string{
	filepath.Join("..", "..", "..", "internal", "services"),
	filepath.Join("..", "..", "..", "internal", "middleware"),
	filepath.Join("..", "..", "..", "internal", "router"),
}

// wrappersOutsideTheErrorPath are files that embed http.ResponseWriter but can
// never carry an AWS error, so forwarding would be dead code. Each entry says
// why.
//
// Adding a file here is a claim that no protocol error writer can ever run
// behind this wrapper. If that is not true, the fix is the forwarding method,
// not an entry in this map.
var wrappersOutsideTheErrorPath = map[string]string{}

func TestResponseWriterWrappers_forwardTheRecordedAWSError(t *testing.T) {
	// Given: every shipping file that wraps a ResponseWriter.
	var offenders []string
	seen := map[string]bool{}

	for _, root := range scannedRoots {
		if _, err := os.Stat(root); err != nil {
			t.Fatalf("cannot find %s from the test's working directory: %v", root, err)
		}
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			source, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			text := string(source)
			if !strings.Contains(text, embedsWriter) {
				return nil
			}
			rel := filepath.ToSlash(strings.TrimPrefix(filepath.ToSlash(path), "../../../"))
			seen[rel] = true
			if _, allowed := wrappersOutsideTheErrorPath[rel]; allowed {
				return nil
			}
			// When: each is checked for the forwarding method.
			if !strings.Contains(text, forwardsAWSError) {
				offenders = append(offenders, rel)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	sort.Strings(offenders)

	// Then: none is missing it.
	if len(offenders) > 0 {
		t.Errorf("these files wrap http.ResponseWriter without forwarding RecordAWSError, so an AWS error written behind them never reaches the trace, the request log, or retention:\n  %s\n"+
			"Add a RecordAWSError method that records or forwards to the wrapped writer.\n"+
			"If no protocol error writer can ever run behind the wrapper, add it to wrappersOutsideTheErrorPath with the reason.",
			strings.Join(offenders, "\n  "))
	}

	// And: no exception outlives the wrapper it describes.
	var stale []string
	for rel := range wrappersOutsideTheErrorPath {
		if !seen[rel] {
			stale = append(stale, rel)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("wrappersOutsideTheErrorPath has entries that no longer wrap a ResponseWriter: %s", strings.Join(stale, ", "))
	}
}
