package events

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/clock"
)

type idPayload struct{ ID int }

// TestBus_Publish_PopulatesResourceARNFromPayload verifies that Bus.Publish
// derives Event.ResourceARN from a payload implementing arnCarrier (see
// event.go) when the caller doesn't set ResourceARN explicitly. This is what
// lets most publish call sites get resourceArn on the wire for free just by
// populating the ARN field on their existing ResourcePayload/LambdaFunctionPayload.
func TestBus_Publish_PopulatesResourceARNFromPayload(t *testing.T) {
	bus := NewBus()
	defer bus.Stop()

	const wantARN = "arn:aws:sqs:us-east-1:000000000000:my-queue"
	bus.Publish(context.Background(), Event{
		Type:    SQSQueueCreated,
		Source:  "sqs",
		Payload: ResourcePayload{Name: "my-queue", ARN: wantARN},
	})

	snapshot, cancel := bus.SnapshotAndSubscribeAll(func(context.Context, Event) {})
	defer cancel()
	if len(snapshot) != 1 {
		t.Fatalf("snapshot len = %d, want 1", len(snapshot))
	}
	if got := snapshot[0].ResourceARN; got != wantARN {
		t.Errorf("ResourceARN = %q, want %q", got, wantARN)
	}
}

// TestBus_Publish_LeavesResourceARNEmptyWithoutCarrier verifies that
// ResourceARN stays empty (never a zero-value guess) when the payload type
// doesn't implement arnCarrier, or the ARN is genuinely unknown.
func TestBus_Publish_LeavesResourceARNEmptyWithoutCarrier(t *testing.T) {
	bus := NewBus()
	defer bus.Stop()

	bus.Publish(context.Background(), Event{Type: S3ObjectCreated, Payload: idPayload{ID: 1}})
	bus.Publish(context.Background(), Event{Type: SQSQueueCreated, Payload: ResourcePayload{Name: "no-arn-known"}})

	snapshot, cancel := bus.SnapshotAndSubscribeAll(func(context.Context, Event) {})
	defer cancel()
	if len(snapshot) != 2 {
		t.Fatalf("snapshot len = %d, want 2", len(snapshot))
	}
	for i, e := range snapshot {
		if e.ResourceARN != "" {
			t.Errorf("snapshot[%d].ResourceARN = %q, want empty", i, e.ResourceARN)
		}
	}
}

// TestBus_Publish_RespectsExplicitResourceARN verifies that a caller-supplied
// ResourceARN is never overwritten by the payload-derived value.
func TestBus_Publish_RespectsExplicitResourceARN(t *testing.T) {
	bus := NewBus()
	defer bus.Stop()

	const explicit = "arn:aws:sqs:us-east-1:000000000000:explicit-override"
	bus.Publish(context.Background(), Event{
		Type:        SQSQueueCreated,
		Payload:     ResourcePayload{Name: "q", ARN: "arn:aws:sqs:us-east-1:000000000000:q"},
		ResourceARN: explicit,
	})

	snapshot, cancel := bus.SnapshotAndSubscribeAll(func(context.Context, Event) {})
	defer cancel()
	if len(snapshot) != 1 {
		t.Fatalf("snapshot len = %d, want 1", len(snapshot))
	}
	if got := snapshot[0].ResourceARN; got != explicit {
		t.Errorf("ResourceARN = %q, want %q (explicit value must win)", got, explicit)
	}
}

// TestS3ObjectPayload_ArnFromPayload verifies the cheap-to-construct S3
// object ARN derivation (no account/region component, per AWS's S3 ARN
// format quirk — see protocol.ARN).
func TestS3ObjectPayload_ArnFromPayload(t *testing.T) {
	cases := []struct {
		name string
		p    S3ObjectPayload
		want string
	}{
		{"bucket+key", S3ObjectPayload{Bucket: "my-bucket", Key: "path/to/obj.txt"}, "arn:aws:s3:::my-bucket/path/to/obj.txt"},
		{"bucket only", S3ObjectPayload{Bucket: "my-bucket"}, "arn:aws:s3:::my-bucket"},
		{"no bucket", S3ObjectPayload{Key: "x"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.p.arnFromPayload(); got != tc.want {
				t.Errorf("arnFromPayload() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCFNResourcePayload_ArnFromPayload verifies PhysicalID is only surfaced
// as ResourceARN when it actually looks like an ARN — CloudFormation
// physical IDs are frequently bare names (e.g. S3 bucket names), and
// treating those as ARNs would be actively misleading downstream.
func TestCFNResourcePayload_ArnFromPayload(t *testing.T) {
	cases := []struct {
		name string
		p    CFNResourcePayload
		want string
	}{
		{"arn physical id", CFNResourcePayload{PhysicalID: "arn:aws:sqs:us-east-1:000000000000:q"}, "arn:aws:sqs:us-east-1:000000000000:q"},
		{"bare name physical id", CFNResourcePayload{PhysicalID: "my-bucket"}, ""},
		{"empty", CFNResourcePayload{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.p.arnFromPayload(); got != tc.want {
				t.Errorf("arnFromPayload() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBus_SnapshotAndSubscribeAll_Deterministic exercises the simple,
// non-racy case: events published strictly before the call must appear in
// the snapshot; events published strictly after must arrive only via the
// live subscription, never duplicated into a later snapshot.
func TestBus_SnapshotAndSubscribeAll_Deterministic(t *testing.T) {
	bus := NewBus()
	defer bus.Stop()

	for i := 0; i < 3; i++ {
		bus.Publish(context.Background(), Event{
			Type:    S3ObjectCreated,
			Payload: idPayload{ID: i},
		})
	}
	// Give the (unsubscribed) publishes a moment to be appended to history
	// — no subscribers exist yet, so this is just the history-append side
	// effect of Publish, which happens synchronously under the bus lock.

	var (
		mu   sync.Mutex
		live []int
	)
	snapshot, cancel := bus.SnapshotAndSubscribeAll(func(_ context.Context, e Event) {
		mu.Lock()
		live = append(live, e.Payload.(idPayload).ID)
		mu.Unlock()
	})
	defer cancel()

	if len(snapshot) != 3 {
		t.Fatalf("snapshot len = %d, want 3", len(snapshot))
	}
	for i, e := range snapshot {
		if got := e.Payload.(idPayload).ID; got != i {
			t.Errorf("snapshot[%d].ID = %d, want %d", i, got, i)
		}
	}

	bus.Publish(context.Background(), Event{Type: S3ObjectCreated, Payload: idPayload{ID: 3}})
	bus.Publish(context.Background(), Event{Type: S3ObjectCreated, Payload: idPayload{ID: 4}})

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(live)
		mu.Unlock()
		if n >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// The bus fans a Publish call out across a fixed worker pool (see
	// bus.go), so two separate Publish calls are not guaranteed to be
	// delivered to a subscriber in call order — only that each is delivered
	// exactly once. Assert set membership, not order.
	mu.Lock()
	defer mu.Unlock()
	if len(live) != 2 {
		t.Fatalf("live = %v, want exactly 2 events", live)
	}
	seen := map[int]bool{live[0]: true, live[1]: true}
	if !seen[3] || !seen[4] {
		t.Fatalf("live = %v, want {3, 4} in any order", live)
	}
}

// TestBus_SnapshotAndSubscribeAll_NoGapNoDuplicate is the atomicity
// contract for connect-time replay: every event published concurrently
// with a SnapshotAndSubscribeAll call must appear exactly once across the
// returned snapshot and the events subsequently delivered to the new
// subscriber — never zero times (a gap) and never twice (a duplicate).
//
// This directly protects the SSE endpoint's "replay history, then tail
// live" behaviour (internal/router/events.go): without the atomicity
// SnapshotAndSubscribeAll provides, a client connecting mid-publish could
// either miss an event (gap) or see it twice (once via replay, once live).
func TestBus_SnapshotAndSubscribeAll_NoGapNoDuplicate(t *testing.T) {
	const n = 200
	bus := NewBus()
	defer bus.Stop()

	var (
		liveMu sync.Mutex
		live   = make(map[int]int)
	)

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(id int) {
			defer wg.Done()
			bus.Publish(context.Background(), Event{
				Type:    S3ObjectCreated,
				Source:  "s3",
				Payload: idPayload{ID: id},
			})
		}(i)
	}

	// Race the snapshot+subscribe call against the concurrent publishes
	// above — this is the boundary under test.
	snapshot, cancel := bus.SnapshotAndSubscribeAll(func(_ context.Context, e Event) {
		p := e.Payload.(idPayload)
		liveMu.Lock()
		live[p.ID]++
		liveMu.Unlock()
	})
	defer cancel()

	wg.Wait() // all n Publish calls have returned (enqueued or delivered)

	// Worker goroutines drain the queue asynchronously; poll for quiescence
	// instead of assuming delivery completed the instant Publish returned.
	total := map[int]int{}
	deadline := time.Now().Add(2 * time.Second)
	for {
		for k := range total {
			delete(total, k)
		}
		for _, e := range snapshot {
			total[e.Payload.(idPayload).ID]++
		}
		liveMu.Lock()
		for id, c := range live {
			total[id] += c
		}
		liveMu.Unlock()

		sum := 0
		for _, c := range total {
			sum += c
		}
		if sum >= n || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	for id := 0; id < n; id++ {
		if got := total[id]; got != 1 {
			t.Errorf("event id %d delivered %d times (snapshot+live), want exactly 1", id, got)
		}
	}
}

// TestPublish_StampsZeroTime pins the zero-Time guard: a publisher that
// omits Time gets the bus clock's now; one that sets Time keeps it.
func TestPublish_StampsZeroTime(t *testing.T) {
	clk := clock.NewMock()
	clk.Set(time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC))
	b := NewBusWithClock(clk)
	defer b.Stop()

	b.Publish(context.Background(), Event{Type: S3BucketCreated, Source: "s3"})
	explicit := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	b.Publish(context.Background(), Event{Type: SQSQueueCreated, Source: "sqs", Time: explicit})

	snap, unsub := b.SnapshotAndSubscribeAll(func(context.Context, Event) {})
	defer unsub()
	if len(snap) != 2 {
		t.Fatalf("expected 2 events in history, got %d", len(snap))
	}
	if !snap[0].Time.Equal(clk.Now()) {
		t.Fatalf("zero-Time event not stamped: got %v want %v", snap[0].Time, clk.Now())
	}
	if !snap[1].Time.Equal(explicit) {
		t.Fatalf("explicit Time overwritten: got %v want %v", snap[1].Time, explicit)
	}
}
