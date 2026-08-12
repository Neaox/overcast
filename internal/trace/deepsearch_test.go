package trace

// deepsearch_test.go — finding a trace by something it said, rather than by
// something it is.
//
// The cheap search (search_test.go) matches short scalar fields and stops
// there, because reaching further means scanning up to MaxHopBodyBytes per
// trace. This is the further reach: hop bodies, hop errors and log entries,
// scanned a budget at a time so no single call can run away with the process.
//
// See docs/plans/trace-deep-search.md.

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// deepTrace registers a trace carrying one hop and one log line, which between
// them cover the fields worth searching.
// deepTrace builds a trace carrying the things a deep search scans. The body
// goes on the trace's own response rather than on a hop: a hop retains no
// bodies, because the call it records is a trace in its own right and is
// scanned as itself.
func deepTrace(b *Buffer, id string, ts time.Time, logMessage, hopError string, body []byte) *Recorder {
	rec := NewRecorder(id, ts, http.MethodPost, "/", "localhost", "", http.Header{})
	rec.SetServiceInfo("cloudformation", "CreateStack", "us-east-1")
	if body != nil {
		rec.SetResponse(http.Header{}, body, 400, 1<<20, false)
	}
	if hopError != "" {
		rec.AddHop(Hop{
			Service:        "ecr",
			Operation:      "DescribeImages",
			ResponseStatus: 400,
			Error:          hopError,
		})
	}
	if logMessage != "" {
		rec.AddLog(LogEntry{Level: "warn", Message: logMessage, Timestamp: ts})
	}
	b.Add(rec)
	return rec
}

// matchedIDs is the assertion shorthand: which traces a deep search found.
func matchedIDs(matches []Match) []string {
	ids := make([]string, 0, len(matches))
	for _, m := range matches {
		ids = append(ids, m.RequestID)
	}
	return ids
}

// searchAll drives the cursor to exhaustion, as the UI does, and returns
// everything found. It fails the test rather than looping forever if the
// cursor stops advancing — a scan that cannot finish is the defect most likely
// to hide here.
func searchAll(t *testing.T, b *Buffer, query string) []Match {
	t.Helper()
	var all []Match
	filter := DeepFilter{Query: query}
	for i := 0; ; i++ {
		if i > 100 {
			t.Fatal("deep search did not terminate: the cursor is not advancing")
		}
		result := b.DeepSearch(context.Background(), filter)
		all = append(all, result.Matches...)
		if result.Done {
			return all
		}
		if result.NextCursor == filter.Cursor {
			t.Fatalf("cursor did not advance past %q", result.NextCursor)
		}
		filter.Cursor = result.NextCursor
	}
}

// The message this whole feature was built for. It reaches the ring buffer as a
// log line on the trace of the request that provisioned the stack, and nothing
// in the cheap search could see it.
func TestDeepSearch_findsALogMessage(t *testing.T) {
	buf := NewBuffer(10)
	base := time.Now()
	deepTrace(buf, "flushed", base, "cfn: terminal stack state not yet persisted", "", nil)
	deepTrace(buf, "quiet", base.Add(time.Second), "cfn: stack provisioned", "", nil)

	matches := searchAll(t, buf, "not yet persisted")

	if got := matchedIDs(matches); len(got) != 1 || got[0] != "flushed" {
		t.Fatalf("deep search = %v, want [flushed]", got)
	}
	m := matches[0]
	if m.Field != MatchLog {
		t.Errorf("Field = %q, want %q", m.Field, MatchLog)
	}
	if !strings.Contains(m.Label, "warn") {
		t.Errorf("Label = %q, want it to name the log level", m.Label)
	}
	// The excerpt has to carry the surrounding text, or the reader learns only
	// that an answer exists somewhere.
	if !strings.Contains(m.Before+m.Text+m.After, "terminal stack state") {
		t.Errorf("excerpt = %q|%q|%q, want the surrounding line", m.Before, m.Text, m.After)
	}
	if m.Text != "not yet persisted" {
		t.Errorf("Text = %q, want the matched span itself", m.Text)
	}
}

// The flagship case for deep search: an ECS pull failure whose explanation is
// an ECR call's response body. That body used to be copied onto the calling
// trace's hop and matched there. It is no longer copied — the ECR call is a
// trace of its own — so the match now lands on that trace, as its own response
// body. The reader gets the same answer, attributed to the request that
// actually produced it, and the hops tab links back to the caller.
func TestDeepSearch_findsAnInternalCallsResponseBody(t *testing.T) {
	buf := NewBuffer(10)
	now := time.Now()

	// The ECR call, as it is really recorded: its own request.
	callee := NewRecorder("ecr-1", now, http.MethodPost, "/", "localhost", "", http.Header{})
	callee.SetServiceInfo("ecr", "DescribeImages", "us-east-1")
	callee.SetResponse(http.Header{},
		[]byte(`{"__type":"ImageNotFoundException","message":"The image tag does not exist"}`),
		400, 1<<20, false)
	buf.Add(callee)

	// The caller, whose hop names it.
	caller := NewRecorder("pull-404", now.Add(-time.Second), http.MethodPost, "/", "localhost", "", http.Header{})
	caller.SetServiceInfo("cloudformation", "CreateStack", "us-east-1")
	caller.AddHop(Hop{Service: "ecr", Operation: "DescribeImages", RequestID: "ecr-1", ResponseStatus: 400})
	buf.Add(caller)

	matches := searchAll(t, buf, "imagenotfoundexception")

	// Found once, on the trace that owns the bytes — not twice.
	if got := matchedIDs(matches); len(got) != 1 || got[0] != "ecr-1" {
		t.Fatalf("deep search = %v, want [ecr-1]", got)
	}
	m := matches[0]
	if m.Field != MatchResponseBody {
		t.Errorf("Field = %q, want %q", m.Field, MatchResponseBody)
	}
	if !strings.Contains(m.Label, "ecr") || !strings.Contains(m.Label, "DescribeImages") {
		t.Errorf("Label = %q, want it to name the matched trace's service and operation", m.Label)
	}
}

func TestDeepSearch_findsAHopError(t *testing.T) {
	buf := NewBuffer(10)
	deepTrace(buf, "refused", time.Now(), "", "HTTP 501: not emulated", nil)

	matches := searchAll(t, buf, "not emulated")
	if got := matchedIDs(matches); len(got) != 1 || got[0] != "refused" {
		t.Fatalf("deep search = %v, want [refused]", got)
	}
	if matches[0].Field != MatchHopError {
		t.Errorf("Field = %q, want %q", matches[0].Field, MatchHopError)
	}
}

// Case-insensitive, like the cheap search — someone pasting a message from a
// console is not going to reproduce its capitalisation.
func TestDeepSearch_isCaseInsensitive(t *testing.T) {
	buf := NewBuffer(10)
	deepTrace(buf, "r1", time.Now(), "Context Deadline Exceeded", "", nil)

	if got := matchedIDs(searchAll(t, buf, "context deadline")); len(got) != 1 {
		t.Errorf("lowercase query against mixed-case text = %v, want [r1]", got)
	}
}

// One match per trace is what a list wants: the row is the trace, and ten
// matches inside one body would bury every other trace that also matched.
func TestDeepSearch_reportsATraceOnce(t *testing.T) {
	buf := NewBuffer(10)
	deepTrace(buf, "repeats", time.Now(), "flush flush flush", "flush", []byte("flush flush"))

	matches := searchAll(t, buf, "flush")
	if len(matches) != 1 {
		t.Fatalf("got %d matches for one trace, want 1: %+v", len(matches), matches)
	}
}

// The budget is what keeps one call bounded on a buffer full of deploy-sized
// traces. Exceeding it must pause the scan, not truncate it: the cursor comes
// back and the next call resumes.
func TestDeepSearch_resumesAcrossBudgets(t *testing.T) {
	buf := NewBuffer(200)
	base := time.Now()
	body := make([]byte, 256*1024)
	for i := range body {
		body[i] = 'x'
	}
	for i := 0; i < 60; i++ {
		deepTrace(buf, "t"+strconv.Itoa(i), base.Add(time.Duration(i)*time.Millisecond), "", "", body)
	}
	// One trace, at the far end of the scan, holds the answer — so a scan that
	// stops at its budget and does not resume will report "no matches".
	deepTrace(buf, "needle", base.Add(-time.Hour), "the buried message", "", nil)

	// 1 MiB of budget against 15 MiB of bodies: a single call cannot finish.
	filter := DeepFilter{Query: "buried", Budget: 1 << 20}
	first := buf.DeepSearch(context.Background(), filter)
	if first.Done {
		t.Fatal("one call covered 15 MiB against a 1 MiB budget; the budget is not being enforced")
	}
	if first.NextCursor == "" {
		t.Fatal("a paused scan returned no cursor, so it cannot be resumed")
	}
	if first.Remaining == 0 {
		t.Error("Remaining is 0 on a paused scan, so a caller can show no progress")
	}

	// Driving the cursor to exhaustion still finds it.
	var found []Match
	for i := 0; !first.Done; i++ {
		if i > 200 {
			t.Fatal("resumed scan did not terminate")
		}
		found = append(found, first.Matches...)
		filter.Cursor = first.NextCursor
		first = buf.DeepSearch(context.Background(), filter)
	}
	found = append(found, first.Matches...)

	if got := matchedIDs(found); len(got) != 1 || got[0] != "needle" {
		t.Errorf("resumed search = %v, want [needle]", got)
	}
}

// A client that goes away must stop the work, not merely stop reading it.
func TestDeepSearch_stopsWhenTheCallerCancels(t *testing.T) {
	buf := NewBuffer(200)
	base := time.Now()
	body := make([]byte, 128*1024)
	for i := 0; i < 100; i++ {
		deepTrace(buf, "t"+strconv.Itoa(i), base.Add(time.Duration(i)*time.Millisecond), "", "", body)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := buf.DeepSearch(ctx, DeepFilter{Query: "nothing-here"})
	if result.Scanned > 1 {
		t.Errorf("scanned %d traces after cancellation, want it to stop at once", result.Scanned)
	}
	if !result.Cancelled {
		t.Error("result does not report that it was cancelled, so a caller cannot tell a stopped scan from a finished one")
	}
}

// A body that is not text — CBOR, protobuf, a gzipped payload — still matches,
// but its excerpt would be mojibake, so it says where it hit instead.
func TestDeepSearch_doesNotRenderBinaryAsText(t *testing.T) {
	buf := NewBuffer(10)
	body := append([]byte{0x00, 0xff, 0xfe, 0x01}, []byte("SecretMarker")...)
	body = append(body, 0x00, 0xff)
	deepTrace(buf, "binary", time.Now(), "", "", body)

	matches := searchAll(t, buf, "secretmarker")
	if len(matches) != 1 {
		t.Fatalf("got %d matches, want 1", len(matches))
	}
	if !matches[0].Binary {
		t.Error("a match inside a non-text body is not flagged binary")
	}
	if matches[0].Before != "" || matches[0].After != "" {
		t.Errorf("binary match carries an excerpt (%q|%q); it should carry an offset instead",
			matches[0].Before, matches[0].After)
	}
	if matches[0].Offset != 4 {
		t.Errorf("Offset = %d, want 4", matches[0].Offset)
	}
}

// Internal traces are a fifth of the ring and the UI hides them; scanning their
// bodies by default spends the budget on what nobody asked to see.
func TestDeepSearch_skipsInternalTracesUnlessAsked(t *testing.T) {
	buf := NewBuffer(10)
	rec := NewRecorder("poll", time.Now(), http.MethodGet, "/_overcast/debug/traces", "localhost", "", http.Header{})
	rec.AddLog(LogEntry{Level: "info", Message: "a distinctive marker"})
	buf.Add(rec)

	if got := matchedIDs(searchAll(t, buf, "distinctive marker")); len(got) != 0 {
		t.Errorf("internal trace matched by default: %v", got)
	}

	filter := DeepFilter{Query: "distinctive marker", IncludeInternal: true}
	if result := buf.DeepSearch(context.Background(), filter); len(result.Matches) != 1 {
		t.Errorf("internal trace not matched when asked for: %d matches", len(result.Matches))
	}
}

// Newest first, like every other view of the buffer.
func TestDeepSearch_returnsNewestFirst(t *testing.T) {
	buf := NewBuffer(10)
	base := time.Now()
	deepTrace(buf, "older", base, "shared marker", "", nil)
	deepTrace(buf, "newer", base.Add(time.Minute), "shared marker", "", nil)

	if got := matchedIDs(searchAll(t, buf, "shared marker")); len(got) != 2 || got[0] != "newer" {
		t.Errorf("deep search order = %v, want [newer older]", got)
	}
}

// Two traces recorded in the same instant must not make the cursor stall or
// skip: the scan orders by timestamp and breaks ties by request ID.
func TestDeepSearch_handlesIdenticalTimestamps(t *testing.T) {
	buf := NewBuffer(10)
	ts := time.Now()
	for i := 0; i < 5; i++ {
		deepTrace(buf, "tie-"+strconv.Itoa(i), ts, "shared marker", "", nil)
	}

	if got := matchedIDs(searchAll(t, buf, "shared marker")); len(got) != 5 {
		t.Errorf("found %d of 5 traces sharing a timestamp: %v", len(got), got)
	}
}
