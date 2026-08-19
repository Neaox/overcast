package events

import (
	"fmt"
	"testing"
	"time"
)

// --- classification table -----------------------------------------------

func TestIsNoise(t *testing.T) {
	cases := []struct {
		name string
		typ  Type
		want bool
	}{
		{"request telemetry is noise", RequestReceived, true},
		{"s3 object created is signal", S3ObjectCreated, false},
		{"sqs message sent is signal", SQSMessageSent, false},
		{"service error is signal", ServiceError, false},
		{"docker container health status is signal", DockerContainerHealthStatus, false},
		{"lambda instance acquired is signal", LambdaInstanceAcquired, false},
		{"wildcard All is signal", All, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsNoise(tc.typ); got != tc.want {
				t.Errorf("IsNoise(%q) = %v, want %v", tc.typ, got, tc.want)
			}
		})
	}
}

// --- buffer capacity + eviction order -------------------------------------

func TestHistory_RespectsCapacity(t *testing.T) {
	h := NewHistory(5)
	for i := 0; i < 20; i++ {
		h.Append(Event{Type: S3ObjectCreated, Source: "s3", Time: time.Now()})
	}
	if got := h.Len(); got != 5 {
		t.Fatalf("Len() = %d, want 5", got)
	}
	if got := len(h.Snapshot()); got != 5 {
		t.Fatalf("len(Snapshot()) = %d, want 5", got)
	}
}

// TestHistory_EvictsNoiseBeforeSignal is the core two-tier eviction
// contract: when the buffer is full, a noise-class event (RequestReceived)
// must be evicted before any signal-class event, even though the signal
// event arrived first (i.e. is chronologically older).
func TestHistory_EvictsNoiseBeforeSignal(t *testing.T) {
	h := NewHistory(3)

	// Oldest first: signal, noise, noise.
	h.Append(Event{Type: S3BucketCreated, Source: "s3", Payload: tag("signal-1")})
	h.Append(Event{Type: RequestReceived, Source: "request", Payload: tag("noise-1")})
	h.Append(Event{Type: RequestReceived, Source: "request", Payload: tag("noise-2")})

	// Buffer is now full (3/3). Appending a new signal event must evict the
	// OLDEST NOISE event (noise-1), not the oldest event overall (signal-1).
	h.Append(Event{Type: S3BucketDeleted, Source: "s3", Payload: tag("signal-2")})

	got := tagsOf(h.Snapshot())
	want := []string{"signal-1", "noise-2", "signal-2"}
	assertTagsEqual(t, got, want)
}

// TestHistory_EvictsOldestOverallWhenNoNoiseRemains verifies the fallback
// tier: once every noise event has already been evicted, further evictions
// fall back to strict oldest-first regardless of class.
func TestHistory_EvictsOldestOverallWhenNoNoiseRemains(t *testing.T) {
	h := NewHistory(2)

	h.Append(Event{Type: S3BucketCreated, Payload: tag("signal-1")})
	h.Append(Event{Type: S3BucketDeleted, Payload: tag("signal-2")})
	// No noise present — must evict oldest overall (signal-1).
	h.Append(Event{Type: SQSQueueCreated, Payload: tag("signal-3")})

	got := tagsOf(h.Snapshot())
	want := []string{"signal-2", "signal-3"}
	assertTagsEqual(t, got, want)
}

// TestHistory_MultipleNoiseEvictedOldestFirst confirms that among several
// buffered noise events, eviction proceeds oldest-noise-first rather than
// arbitrarily.
func TestHistory_MultipleNoiseEvictedOldestFirst(t *testing.T) {
	h := NewHistory(4)

	h.Append(Event{Type: RequestReceived, Payload: tag("noise-1")})
	h.Append(Event{Type: S3BucketCreated, Payload: tag("signal-1")})
	h.Append(Event{Type: RequestReceived, Payload: tag("noise-2")})
	h.Append(Event{Type: RequestReceived, Payload: tag("noise-3")})

	// Full (4/4). Two more signal events arrive; each append should evict
	// the oldest remaining noise event (noise-1, then noise-2).
	h.Append(Event{Type: S3BucketDeleted, Payload: tag("signal-2")})
	h.Append(Event{Type: SQSQueueCreated, Payload: tag("signal-3")})

	got := tagsOf(h.Snapshot())
	want := []string{"signal-1", "noise-3", "signal-2", "signal-3"}
	assertTagsEqual(t, got, want)
}

func TestHistory_SnapshotIsChronological(t *testing.T) {
	h := NewHistory(10)
	for i := 0; i < 5; i++ {
		h.Append(Event{Type: S3ObjectCreated, Payload: tag(fmt.Sprintf("e-%d", i))})
	}
	got := tagsOf(h.Snapshot())
	want := []string{"e-0", "e-1", "e-2", "e-3", "e-4"}
	assertTagsEqual(t, got, want)
}

func TestHistory_ZeroCapacityIsNoOp(t *testing.T) {
	h := NewHistory(0)
	h.Append(Event{Type: S3ObjectCreated})
	if got := h.Len(); got != 0 {
		t.Fatalf("Len() = %d, want 0", got)
	}
	if got := h.Snapshot(); len(got) != 0 {
		t.Fatalf("Snapshot() = %v, want empty", got)
	}
}

// TestHistory_RedactsLambdaTriggerEvent verifies the large-payload
// mitigation: LambdaInstancePayload.TriggerEvent must not be retained in
// history (it can carry up to the Lambda sync-invoke payload limit and is
// republished on every instance lifecycle transition).
func TestHistory_RedactsLambdaTriggerEvent(t *testing.T) {
	h := NewHistory(10)
	big := make([]byte, 4096)
	for i := range big {
		big[i] = 'x'
	}
	h.Append(Event{
		Type: LambdaInstanceAcquired,
		Payload: LambdaInstancePayload{
			InstanceID:   "abc",
			FunctionName: "fn",
			TriggerEvent: big,
		},
	})

	snap := h.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected 1 event, got %d", len(snap))
	}
	p, ok := snap[0].Payload.(LambdaInstancePayload)
	if !ok {
		t.Fatalf("payload type = %T, want LambdaInstancePayload", snap[0].Payload)
	}
	if p.TriggerEvent != nil {
		t.Errorf("TriggerEvent = %d bytes, want nil (redacted)", len(p.TriggerEvent))
	}
	if p.InstanceID != "abc" || p.FunctionName != "fn" {
		t.Errorf("other payload fields should survive redaction: got %+v", p)
	}
}

// --- helpers ---------------------------------------------------------------

type tagPayload struct{ Tag string }

func tag(s string) tagPayload { return tagPayload{Tag: s} }

func tagsOf(events []Event) []string {
	out := make([]string, len(events))
	for i, e := range events {
		p, ok := e.Payload.(tagPayload)
		if !ok {
			out[i] = fmt.Sprintf("<no tag: %T>", e.Payload)
			continue
		}
		out[i] = p.Tag
	}
	return out
}

func assertTagsEqual(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v (len %d), want %v (len %d)", got, len(got), want, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v (mismatch at index %d)", got, want, i)
		}
	}
}

// --- request attribution ---------------------------------------------------

// TestHistory_FindByRequestID_ReturnsEverythingTheRequestCaused is the whole
// point of the envelope-level RequestID: one PutObject answers with the S3
// write, the notification it fired and the queue send behind it — not just the
// request:Received row summarising the call. Before the envelope carried the
// ID, only that one row matched, and a trace's Events tab showed a request
// that apparently did nothing.
func TestHistory_FindByRequestID_ReturnsEverythingTheRequestCaused(t *testing.T) {
	h := NewHistory(50)
	const target = "req-target"

	h.Append(Event{Type: RequestReceived, Source: "request", RequestID: target,
		Payload: RequestPayload{Method: "PUT", Path: "/bucket/key", RequestID: target}})
	h.Append(Event{Type: S3ObjectCreated, Source: "s3", RequestID: target,
		Payload: S3ObjectPayload{Bucket: "bucket", Key: "key"}})
	h.Append(Event{Type: SQSMessageSent, Source: "sqs", RequestID: target,
		Payload: ResourcePayload{Name: "q"}})
	// Another request's work, and background work with no request at all.
	h.Append(Event{Type: S3ObjectCreated, Source: "s3", RequestID: "req-other",
		Payload: S3ObjectPayload{Bucket: "other", Key: "k"}})
	h.Append(Event{Type: DockerContainerDied, Source: "docker",
		Payload: DockerContainerPayload{ContainerID: "abc"}})

	got := h.FindByRequestID(target)
	if len(got) != 3 {
		t.Fatalf("FindByRequestID returned %d events, want 3", len(got))
	}
	wantTypes := []Type{RequestReceived, S3ObjectCreated, SQSMessageSent}
	for i, want := range wantTypes {
		if got[i].Type != want {
			t.Errorf("event %d type = %q, want %q (chronological order)", i, got[i].Type, want)
		}
		if got[i].RequestID != target {
			t.Errorf("event %d RequestID = %q, want %q", i, got[i].RequestID, target)
		}
	}
}

// TestHistory_FindByRequestID_EmptyMatchesNothing verifies the empty id is not
// treated as a wildcard. Every background event carries no request ID, so
// matching on "" would return most of the buffer — the opposite of a filter,
// and reachable from a URL (/_overcast/events/request/) if the guard is lost.
func TestHistory_FindByRequestID_EmptyMatchesNothing(t *testing.T) {
	h := NewHistory(10)
	h.Append(Event{Type: S3ObjectCreated, Source: "s3"})
	h.Append(Event{Type: SQSMessageSent, Source: "sqs"})

	if got := h.FindByRequestID(""); len(got) != 0 {
		t.Errorf("FindByRequestID(\"\") returned %d events, want 0", len(got))
	}
}
