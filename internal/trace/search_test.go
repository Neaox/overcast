package trace

// search_test.go — what the trace list's search box can find.
//
// Search matched request ID, path and service, which is to say the three things
// a reader usually already knows. The fields that say what a request *was* and
// what went wrong with it — the operation, the AWS error it answered with, the
// host it was addressed to — were not searchable, so the way to find "the
// RunTask that returned AccessDenied" was to page through the list by eye.
//
// This is the cheap half of trace search: every field here is a short string
// already held on the recorder, so matching them costs one read lock and no
// scanning. The expensive half — hop bodies, log entries, hop errors — is
// deliberately not here; see docs/plans/trace-deep-search.md.

import (
	"net/http"
	"testing"
	"time"
)

// searchable builds a trace with the full set of fields search looks at, so a
// test can name one and be sure the others are not what matched.
func searchable(b *Buffer, spec traceSpec, operation, errCode, errMessage string) {
	rec := NewRecorder(spec.RequestID, spec.Timestamp, spec.Method, spec.Path, "localhost", "", http.Header{})
	rec.SetServiceInfo(spec.Service, operation, "us-east-1")
	rec.SetMeta("", "", "", errCode, errMessage)
	b.Add(rec)
}

func TestBuffer_searchFindsTheOperation(t *testing.T) {
	// Given: two traces whose paths and services are identical — which is the
	// normal case for the RPC protocols, where every request is POST / and only
	// the operation tells them apart.
	buf := NewBuffer(10)
	searchable(buf, traceSpec{RequestID: "r1", Method: "POST", Path: "/", Service: "ecs", Timestamp: time.Now()}, "RunTask", "", "")
	searchable(buf, traceSpec{RequestID: "r2", Method: "POST", Path: "/", Service: "ecs", Timestamp: time.Now()}, "DescribeClusters", "", "")

	// When: the operation is searched for
	entries, _ := buf.ListSummaries(ListFilter{Search: "runtask", Limit: 10})

	// Then: only that one comes back, case-insensitively
	if got := requestIDs(entries); len(got) != 1 || got[0] != "r1" {
		t.Errorf("search for the operation = %v, want [r1]", got)
	}
}

func TestBuffer_searchFindsTheAWSError(t *testing.T) {
	// Given: one trace that failed and one that did not
	buf := NewBuffer(10)
	searchable(buf, traceSpec{RequestID: "denied", Method: "POST", Path: "/", Service: "s3", Timestamp: time.Now()},
		"PutObject", "AccessDenied", "Access Denied for bucket assets")
	searchable(buf, traceSpec{RequestID: "fine", Method: "POST", Path: "/", Service: "s3", Timestamp: time.Now()},
		"PutObject", "", "")

	// Then: the error code finds it
	if got := requestIDs(mustList(t, buf, "accessdenied")); len(got) != 1 || got[0] != "denied" {
		t.Errorf("search for the error code = %v, want [denied]", got)
	}

	// And: so does a phrase from the message, which is what someone pastes in
	// from a stack trace or a CDK failure
	if got := requestIDs(mustList(t, buf, "bucket assets")); len(got) != 1 || got[0] != "denied" {
		t.Errorf("search for the error message = %v, want [denied]", got)
	}
}

func TestBuffer_searchStillFindsWhatItAlwaysDid(t *testing.T) {
	// The three original fields keep working — this is a widening, not a
	// replacement, and each is the fastest route to a trace when you have it.
	buf := NewBuffer(10)
	searchable(buf, traceSpec{RequestID: "abc-123", Method: "GET", Path: "/2015-03-31/functions", Service: "lambda", Timestamp: time.Now()}, "Invoke", "", "")
	searchable(buf, traceSpec{RequestID: "xyz-789", Method: "GET", Path: "/other", Service: "sqs", Timestamp: time.Now()}, "SendMessage", "", "")

	for _, tc := range []struct{ name, query, want string }{
		{"request id", "abc-1", "abc-123"},
		{"path", "functions", "abc-123"},
		{"service", "sqs", "xyz-789"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := requestIDs(mustList(t, buf, tc.query)); len(got) != 1 || got[0] != tc.want {
				t.Errorf("search %q = %v, want [%s]", tc.query, got, tc.want)
			}
		})
	}
}

func TestBuffer_searchMatchesNothingItShouldNot(t *testing.T) {
	// A query that appears in no searchable field returns nothing rather than
	// everything — the failure mode of a filter that silently stops applying.
	buf := NewBuffer(10)
	searchable(buf, traceSpec{RequestID: "r1", Method: "POST", Path: "/", Service: "ecs", Timestamp: time.Now()}, "RunTask", "", "")

	if entries, _ := buf.ListSummaries(ListFilter{Search: "no-such-thing", Limit: 10}); len(entries) != 0 {
		t.Errorf("search for an absent term returned %d entries, want 0", len(entries))
	}
}

// The body of a hop is not searchable here, and a test says so: someone adding
// deep search should be changing this expectation deliberately, not
// discovering it.
func TestBuffer_searchDoesNotReachHopBodies(t *testing.T) {
	buf := NewBuffer(10)
	rec := NewRecorder("r1", time.Now(), "POST", "/", "localhost", "", http.Header{})
	rec.SetServiceInfo("ecs", "RunTask", "us-east-1")
	rec.AddHop(Hop{Service: "ecr", Operation: "DescribeImages", ResponseBody: []byte(`{"__type":"ImageNotFoundException"}`)})
	buf.Add(rec)

	if entries, _ := buf.ListSummaries(ListFilter{Search: "ImageNotFoundException", Limit: 10}); len(entries) != 0 {
		t.Errorf("hop bodies are matched by the cheap search path; that is the deep search's job (%d entries)", len(entries))
	}
}

func mustList(t *testing.T, b *Buffer, query string) []Summary {
	t.Helper()
	entries, _ := b.ListSummaries(ListFilter{Search: query, Limit: 10})
	return entries
}
