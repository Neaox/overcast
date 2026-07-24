package helpers

import (
	"slices"
	"testing"
)

// PageFetcher fetches a single page of a paginated List/Describe operation.
// token is "" for the first page. Implementations should call t.Fatal on
// transport/decode failures (they run inside PaginationContractTest's walk,
// so t.Helper() + t.Fatal produces a useful failure line). nextToken must be
// "" when there are no more pages — the walk stops there.
type PageFetcher[T any] func(t *testing.T, token string) (items []T, nextToken string)

// InvalidTokenProbe drives one request against the same operation using a
// deliberately garbled/out-of-range continuation token and reports the HTTP
// status code and AWS error code/type the service returned. Implementations
// should NOT call t.Fatal/t.Error themselves — PaginationContractTest
// compares the returned values against the expected ones so failures report
// with full context (both the wanted and the observed response).
type InvalidTokenProbe func(t *testing.T) (statusCode int, errorCode string)

// PaginationContractOptions configures PaginationContractTest.
type PaginationContractOptions struct {
	// WantInvalidTokenStatus is the HTTP status code expected when Probe is
	// invoked with a garbage/out-of-range token (e.g. http.StatusBadRequest).
	WantInvalidTokenStatus int
	// WantInvalidTokenErrorCode is the AWS error code/type expected in the
	// invalid-token response (e.g. "ValidationError" for CloudFormation,
	// "InvalidArgument" for CloudFront, "InvalidNextToken" for SSM).
	WantInvalidTokenErrorCode string
	// MaxPages bounds the walk so an operation whose NextToken never
	// terminates (a G1-class bug) fails the test instead of looping
	// forever. Defaults to 1000 pages.
	MaxPages int
}

// PaginationContractTest walks every page of a paginated operation (via
// fetch, starting from an empty token) until NextToken comes back empty,
// then asserts the pagination-plan's contract in one place:
//
//   - exactly-once (+ order when known): if wantIDs is non-nil, the
//     concatenated item IDs across every page must match it exactly, in
//     sequence — any duplicate, dropped, or reordered item fails here. If
//     wantIDs is nil (the operation's IDs are server-generated and its
//     exact response order can't be predicted without duplicating the
//     store's internals — e.g. random UUIDs), only exactly-once
//     (no duplicate ID anywhere in the walk) is checked; callers that need
//     an order assertion in that case should inspect the returned items
//     themselves (e.g. checking a documented monotonic field).
//   - terminal condition: the walk reaches an empty NextToken within
//     opts.MaxPages; a NextToken that repeats without changing also fails
//     immediately rather than spinning.
//   - invalid-token error: probe (when non-nil) is called once with a
//     garbled/out-of-range token, and its result must match
//     opts.WantInvalidTokenStatus / WantInvalidTokenErrorCode — i.e. the
//     service must return the documented AWS error instead of silently
//     restarting the walk from page 1 (docs/plans/pagination-plan.md, H1/G3).
//
// idOf extracts a comparable identifier from each item T (e.g. an event ID,
// a distribution ID, a parameter name+version composite) for the
// exactly-once/order comparison. When non-nil, wantIDs must already be in
// the operation's documented response order (e.g. reverse-chronological for
// DescribeStackEvents) — PaginationContractTest does not reorder anything.
//
// Returns every item collected across the walk, in response order, so
// callers can run additional operation-specific assertions afterward.
func PaginationContractTest[T any](
	t *testing.T,
	wantIDs []string,
	idOf func(T) string,
	fetch PageFetcher[T],
	probe InvalidTokenProbe,
	opts PaginationContractOptions,
) []T {
	t.Helper()

	maxPages := opts.MaxPages
	if maxPages <= 0 {
		maxPages = 1000
	}

	var all []T
	token := ""
	for pages := 0; ; pages++ {
		if pages >= maxPages {
			t.Fatalf("pagination did not terminate within %d pages (possible infinite/non-terminating NextToken loop)", maxPages)
		}
		items, next := fetch(t, token)
		all = append(all, items...)
		if next == "" {
			break
		}
		if next == token {
			t.Fatalf("NextToken repeated (%q) without terminating — pagination is stuck", next)
		}
		token = next
	}

	gotIDs := make([]string, len(all))
	for i, item := range all {
		gotIDs[i] = idOf(item)
	}
	if wantIDs != nil {
		if !slices.Equal(gotIDs, wantIDs) {
			t.Fatalf("pagination walk violated the exactly-once/order contract:\n got:  %v\n want: %v", gotIDs, wantIDs)
		}
	} else {
		seen := make(map[string]bool, len(gotIDs))
		for _, id := range gotIDs {
			if seen[id] {
				t.Fatalf("pagination walk returned duplicate item %q (exactly-once violated); full sequence: %v", id, gotIDs)
			}
			seen[id] = true
		}
	}

	if probe != nil {
		status, code := probe(t)
		if status != opts.WantInvalidTokenStatus {
			t.Errorf("invalid-token probe: expected HTTP %d, got %d", opts.WantInvalidTokenStatus, status)
		}
		if code != opts.WantInvalidTokenErrorCode {
			t.Errorf("invalid-token probe: expected error code %q, got %q", opts.WantInvalidTokenErrorCode, code)
		}
	}

	return all
}
