package events

import (
	"context"
	"sync"
	"testing"
	"time"
)

type idPayload struct{ ID int }

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
