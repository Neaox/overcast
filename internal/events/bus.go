package events

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"sync"
	"time"

	"github.com/Neaox/overcast/internal/clock"
)

const (
	// workerCount is the fixed number of goroutines that process event
	// deliveries. This bounds goroutine growth regardless of publish rate.
	workerCount = 16

	// workQueueSize is the capacity of the buffered work channel. Events
	// beyond this buffer will block Publish briefly until a worker is free,
	// but no events are dropped.
	workQueueSize = 4096
)

// workItem pairs one handler invocation with its context and event.
type workItem struct {
	ctx context.Context
	e   Event
	h   HandlerFunc
}

// HandlerFunc is the signature for event subscribers.
// Implementations must be safe for concurrent use and should complete
// promptly — they run on a shared worker pool.
type HandlerFunc func(ctx context.Context, e Event)

// Bus is a pub/sub event bus backed by a fixed-size worker pool.
//
// Publish enqueues one work item per subscriber onto a buffered channel.
// A fixed pool of worker goroutines drains the channel and executes
// handlers. This guarantees bounded goroutine count while ensuring every
// event is eventually delivered (no drops).
//
// Bus also owns a rolling History of recently published events (see
// history.go) so that newly-connecting consumers — the SSE /_overcast/events
// endpoint in particular — can replay recent activity instead of starting
// from a blank slate. See SnapshotAndSubscribeAll.
//
// Bus is safe for concurrent use.
type Bus struct {
	mu          sync.RWMutex
	subscribers map[Type][]HandlerFunc
	workCh      chan workItem
	stopCh      chan struct{}
	wg          sync.WaitGroup
	history     *History
	clk         clock.Clock

	// seq is the last sequence number handed out by Publish, guarded by mu.
	seq uint64
	// runID identifies this Bus instance for the lifetime of the process.
	// Immutable after construction, so it needs no lock.
	runID string
}

// NewBus returns an initialised Bus with its worker pool running.
// Call Stop to shut down the workers.
func NewBus() *Bus {
	return NewBusWithClock(clock.New())
}

// NewBusWithClock is NewBus with an injectable clock, for tests that need
// deterministic event timestamps (see Publish's zero-Time stamping).
func NewBusWithClock(clk clock.Clock) *Bus {
	b := &Bus{
		subscribers: make(map[Type][]HandlerFunc),
		workCh:      make(chan workItem, workQueueSize),
		stopCh:      make(chan struct{}),
		history:     NewHistory(HistoryCapacity),
		clk:         clk,
		runID:       newRunID(),
	}
	b.wg.Add(workerCount)
	for range workerCount {
		go b.worker()
	}
	return b
}

// newRunID returns a short, process-unique identifier for one Bus instance.
// Randomness beats a timestamp here only because it survives a restart
// inside the same second; nothing about this is security-sensitive, and a
// failed read falls back to the clock rather than refusing to start.
func newRunID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}

// RunID identifies this Bus instance. Event sequence numbers are only
// meaningful within one run, so a consumer that persists a resume point
// must record the RunID alongside it and discard the pair when it changes —
// see Event.Seq.
func (b *Bus) RunID() string { return b.runID }

// Stop shuts down the worker pool after draining all queued work items.
func (b *Bus) Stop() {
	close(b.stopCh)
	b.wg.Wait()
}

// worker processes work items from the shared channel until stopCh is closed
// and the channel is drained.
func (b *Bus) worker() {
	defer b.wg.Done()
	for {
		select {
		case item := <-b.workCh:
			item.h(item.ctx, item.e)
		case <-b.stopCh:
			// Drain remaining items before exiting.
			for {
				select {
				case item := <-b.workCh:
					item.h(item.ctx, item.e)
				default:
					return
				}
			}
		}
	}
}

// Subscribe registers h to be called for every event of type t.
// The returned cancel function removes the subscription. It is safe to call
// cancel more than once.
func (b *Bus) Subscribe(t Type, h HandlerFunc) (cancel func()) {
	b.mu.Lock()
	cancel = b.subscribeLocked(t, h)
	b.mu.Unlock()
	return cancel
}

// subscribeLocked appends h to subscribers[t] and returns a cancel function
// that removes it. Caller must hold b.mu (for writing) across the call that
// reads idx, but the returned cancel closure re-acquires the lock itself.
func (b *Bus) subscribeLocked(t Type, h HandlerFunc) (cancel func()) {
	b.subscribers[t] = append(b.subscribers[t], h)
	idx := len(b.subscribers[t]) - 1

	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			handlers := b.subscribers[t]
			if idx >= len(handlers) {
				return
			}
			// Replace with last element and shrink — O(1), order-independent.
			last := len(handlers) - 1
			handlers[idx] = handlers[last]
			handlers[last] = nil
			b.subscribers[t] = handlers[:last]
		})
	}
}

// SubscribeAll registers h to be called for every event, regardless of type.
// It is equivalent to Subscribe(All, h) and is provided for readability.
func (b *Bus) SubscribeAll(h HandlerFunc) (cancel func()) {
	return b.Subscribe(All, h)
}

// SnapshotAndSubscribeAll atomically returns a chronological snapshot of the
// buffered event History and registers h as a wildcard (All) subscriber.
//
// The snapshot and the subscription are taken under the same lock that
// Publish holds around its own sequence-assign + history-append +
// subscriber-copy section, so there is no gap or duplicate at the
// connection boundary: any Publish call that is fully serialized before this
// call is reflected in the returned snapshot only, and any Publish call
// serialized after it is delivered to h only — never both, never neither.
//
// This is the primitive the SSE /_overcast/events endpoint uses to implement
// "replay history, then tail live" without a race window.
func (b *Bus) SnapshotAndSubscribeAll(h HandlerFunc) (snapshot []Event, cancel func()) {
	b.mu.Lock()
	snapshot = b.history.Snapshot()
	cancel = b.subscribeLocked(All, h)
	b.mu.Unlock()
	return snapshot, cancel
}

// Publish dispatches e to all subscribers registered for e.Type and to any
// wildcard (All) subscribers. Each subscriber invocation is enqueued onto the
// worker pool's buffered channel. Publish returns immediately unless the
// queue is completely full, in which case it blocks briefly until a slot
// opens — no events are ever dropped.
//
// The event is stamped with its sequence number and appended to the rolling
// History buffer under the same locked section that reads the subscriber
// lists, which is what gives SnapshotAndSubscribeAll its atomicity guarantee
// (see its doc comment).
//
// That section takes the write lock rather than a read lock, so publishes
// serialize against each other. This is what makes Event.Seq usable as a
// resume point: assigning the number outside the lock would let two
// concurrent publishes land in history in the opposite order to their
// numbers, and a client resuming from "everything up to N" would then be
// sent an event it already had. The serialized work is a counter increment
// and two slice copies — the History append underneath already took an
// exclusive lock of its own — and handler dispatch stays outside it.
func (b *Bus) Publish(ctx context.Context, e Event) {
	// Zero-Time guard: most publishers stamp their service clock, but a
	// publisher that omits Time must never produce a zero timestamp in the
	// history buffer or the live stream (rendered as 00:00:00.000 by the
	// Events page). Stamping here, once, covers every publisher — past and
	// future — without requiring 30+ call sites to remember.
	if e.Time.IsZero() {
		e.Time = b.clk.Now()
	}
	// Best-effort ResourceARN guard: mirrors the zero-Time stamping above —
	// if the caller didn't set ResourceARN explicitly, derive it from the
	// payload when the payload type knows how (see arnCarrier). This lets
	// individual publish call sites populate an ARN field on their existing
	// payload struct (e.g. ResourcePayload.ARN) without also having to
	// thread it through to the Event envelope by hand.
	if e.ResourceARN == "" {
		if carrier, ok := e.Payload.(arnCarrier); ok {
			e.ResourceARN = carrier.arnFromPayload()
		}
	}
	b.mu.Lock()
	b.seq++
	e.Seq = b.seq
	b.history.Append(e)
	typed := make([]HandlerFunc, len(b.subscribers[e.Type]))
	copy(typed, b.subscribers[e.Type])
	wild := make([]HandlerFunc, len(b.subscribers[All]))
	copy(wild, b.subscribers[All])
	b.mu.Unlock()

	for _, h := range typed {
		b.workCh <- workItem{ctx: ctx, e: e, h: h}
	}
	for _, h := range wild {
		b.workCh <- workItem{ctx: ctx, e: e, h: h}
	}
}

// FindEventsByRequestID returns all buffered events whose
// RequestReceived payload matches the given request ID.
func (b *Bus) FindEventsByRequestID(requestID string) []Event {
	return b.history.FindByRequestID(requestID)
}
