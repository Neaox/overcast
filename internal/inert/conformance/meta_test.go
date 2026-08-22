package conformance

import (
	"net/http"
	"sort"
	"testing"
)

// expectedNaiveViolations is the exact set of §3 clauses naive_stub.go's
// naiveLogic violates. This is the I0 acceptance gate's evidence: the
// conformance suite exists, and it demonstrably fails against a
// deliberately naive stub — not "fails everything" (a suite that always
// reds is as useless as one that never reds), but exactly this set, which
// this test pins.
//
// Passing (i.e. NOT in this list) and why:
//   - 3.1/create-read, 3.1/update-merge, 3.1/list-stable, 3.1/list-paginate:
//     the naive stub's identifier handling, partial-merge Update, sorted
//     List, and real (if simplistic) pagination are all honest.
//   - 3.5/arn: the ARN template is correct, even though almost nothing else
//     about the record is.
var expectedNaiveViolations = []string{
	"3.1/delete-then-read",
	"3.2/roundtrip-fidelity",
	"3.2/no-fabrication",
	"3.3/not-found",
	"3.3/already-exists",
	"3.3/invalid-parameter",
	"3.3/invalid-token",
	"3.5/timestamps",
	"3.5/idempotency",
	"3.6/verb-default",
}

// TestNaiveStub_ViolatesExpectedClauses is the I0 acceptance gate's
// evidence, run against two protocol families (JSON 1.1 and the AWS Query
// protocol) so "per protocol family" is real: the same clause set fails
// for the same reasons regardless of wire format, because Check operates
// on the logical field map Encode/Decode produce, not on protocol-specific
// bytes.
func TestNaiveStub_ViolatesExpectedClauses(t *testing.T) {
	fixtures := map[string]Fixture{
		"json1.1": NewNaiveJSONFixture(),
		"query":   NewNaiveQueryFixture(),
	}
	for name, f := range fixtures {
		t.Run(name, func(t *testing.T) {
			got := clauseSet(Check(f))
			assertClauseSet(t, got, expectedNaiveViolations)
		})
	}
}

func clauseSet(violations []Violation) []string {
	seen := map[string]bool{}
	for _, v := range violations {
		seen[v.Clause] = true
	}
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

func assertClauseSet(t *testing.T, got, want []string) {
	t.Helper()
	wantSorted := append([]string(nil), want...)
	sort.Strings(wantSorted)

	gotSet := map[string]bool{}
	for _, c := range got {
		gotSet[c] = true
	}
	wantSet := map[string]bool{}
	for _, c := range wantSorted {
		wantSet[c] = true
	}

	for _, c := range wantSorted {
		if !gotSet[c] {
			t.Errorf("expected clause %q to be violated by the naive stub, but Check did not report it", c)
		}
	}
	for _, c := range got {
		if !wantSet[c] {
			t.Errorf("Check reported unexpected clause %q as violated — either the naive stub regressed, or expectedNaiveViolations is stale", c)
		}
	}
}

// TestCheck_PassesAgainstItself is a sanity check that Check is not
// vacuously true: a Fixture whose Handler is wired to reject every
// operation must fail loudly (via panics/violations), not silently report
// zero violations because nothing ran. Exercised implicitly by the two
// naive fixtures above already returning a non-empty violation set with
// specific, stable clause ids — this test only pins that Check on a
// Fixture with no operations configured does not itself return zero
// violations by accident (every clause would fail its own Create call).
func TestCheck_EmptyFixtureFailsLoudly(t *testing.T) {
	f := Fixture{
		Service:  "empty",
		Handler:  http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		Resource: ResourceOps{Create: "Create", Read: "Read", IDField: "Name"},
		Errors:   naiveErrorCodes(),
		Input:    func(InputKind, int) map[string]any { return map[string]any{"Name": "x"} },
		Reset:    func() {},
		Encode: func(op string, fields map[string]any) *http.Request {
			return httpPost("/", nil)
		},
		Decode: func(resp *http.Response) (map[string]any, *WireError) {
			return nil, &WireError{Code: "InternalError", HTTPStatus: 500}
		},
	}
	violations := Check(f)
	if len(violations) == 0 {
		t.Fatal("Check against a fixture whose every call errors returned zero violations")
	}
}
