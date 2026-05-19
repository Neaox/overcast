// Package domainregistry tracks the set of user-registered custom domain
// names that map to overcast resources (API Gateway custom domains today,
// CloudFront alias names tomorrow) and broadcasts Added / Removed events
// to subscribed watchers.
//
// The registry is the single source of truth consumed by the
// /_internal/domains/watch SSE endpoint, which in turn drives the
// `overcast dev` host CLI's mDNS publishing and local trust store.
//
// Design notes:
//
//   - The registry is a *state of the world*, not a firehose. New
//     subscribers receive a snapshot of every currently-active record as
//     EventAdded events before live updates begin — atomically, under
//     the same lock, so no Put/Delete can race with subscription.
//
//   - Per-subscriber channels are buffered with headroom for the initial
//     snapshot plus a small live backlog. A slow subscriber that fills
//     its channel causes subsequent events to be dropped rather than
//     blocking writers, which matches the "not a security boundary, not
//     a production dependency" posture of overcast: the CLI reconnects
//     and re-snapshots on its own.
//
//   - No async worker: Put and Delete are synchronous and broadcast
//     directly under the registry lock. This keeps event ordering
//     strictly consistent with the call order, which matters for tests
//     and for the rename semantics the bridge relies on.
package domainregistry

import (
	"context"
	"sync"
)

// EventType distinguishes registrations from de-registrations.
type EventType int

const (
	// EventAdded means a record has been registered or replaces an
	// existing record with the same Name.
	EventAdded EventType = iota
	// EventRemoved means a previously-registered record has been withdrawn.
	EventRemoved
)

// String returns a human-readable name for logging.
func (t EventType) String() string {
	switch t {
	case EventAdded:
		return "added"
	case EventRemoved:
		return "removed"
	default:
		return "unknown"
	}
}

// Record is a single custom-domain registration. Name is the deduplication
// key; Source identifies the emulator subsystem that registered it so the
// host CLI can present a readable origin in logs and `overcast status`.
type Record struct {
	// Name is the fully-qualified custom domain name, e.g. "api.myapp.local".
	Name string
	// Source identifies the owning subsystem, e.g. "apigateway.v1",
	// "apigateway.v2", "cloudfront".
	Source string
}

// Event is a single change notification delivered to every active watcher.
type Event struct {
	Type   EventType
	Record Record
}

// Registry is a thread-safe set of Records with snapshot-on-subscribe
// semantics. The zero value is not usable — call New.
type Registry struct {
	mu       sync.Mutex
	active   map[string]Record
	watchers map[uint64]chan Event
	nextID   uint64
}

// New returns an empty Registry ready for use.
func New() *Registry {
	return &Registry{
		active:   make(map[string]Record),
		watchers: make(map[uint64]chan Event),
	}
}

// Put inserts or replaces a record and broadcasts an EventAdded to every
// watcher. If the record is byte-identical to the existing entry the call
// is a no-op and no event is emitted, so idempotent writes from repeated
// CreateDomainName calls do not spam subscribers.
func (r *Registry) Put(rec Record) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if prev, ok := r.active[rec.Name]; ok && prev == rec {
		return
	}
	r.active[rec.Name] = rec
	r.broadcast(Event{Type: EventAdded, Record: rec})
}

// Delete withdraws a record by Name and broadcasts an EventRemoved to
// every watcher. Deleting an unknown Name is a no-op: no event is emitted
// and no error is returned.
func (r *Registry) Delete(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.active[name]
	if !ok {
		return
	}
	delete(r.active, name)
	r.broadcast(Event{Type: EventRemoved, Record: rec})
}

// Snapshot returns a copy of the currently-active records. Intended for
// tests and the `overcast status` subcommand.
func (r *Registry) Snapshot() []Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Record, 0, len(r.active))
	for _, rec := range r.active {
		out = append(out, rec)
	}
	return out
}

// Watch returns a channel of Events. The channel first replays every
// currently-active record as EventAdded, then streams live changes. When
// ctx is cancelled the channel is closed and the watcher is removed from
// the registry.
//
// The returned channel is buffered to hold the entire initial snapshot
// plus a fixed live backlog, so slow consumers never block writers: if
// the backlog overflows, events are dropped for that consumer and the
// caller is expected to reconnect.
func (r *Registry) Watch(ctx context.Context) <-chan Event {
	r.mu.Lock()
	// Headroom = current state + live backlog. 256 is generous for a
	// dev-time tool; real load is a handful of events per minute.
	const liveBacklog = 256
	ch := make(chan Event, len(r.active)+liveBacklog)
	id := r.nextID
	r.nextID++
	r.watchers[id] = ch
	for _, rec := range r.active {
		ch <- Event{Type: EventAdded, Record: rec}
	}
	r.mu.Unlock()

	go func() {
		<-ctx.Done()
		r.unsubscribe(id)
	}()
	return ch
}

// unsubscribe removes a watcher and closes its channel. Safe to call
// multiple times for the same id; the second call is a no-op.
func (r *Registry) unsubscribe(id uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch, ok := r.watchers[id]
	if !ok {
		return
	}
	delete(r.watchers, id)
	close(ch)
}

// broadcast delivers ev to every watcher. Must be called with r.mu held.
// Writes are non-blocking: if a watcher's buffer is full the event is
// dropped for that watcher and the registry continues. This prevents a
// stuck subscriber from freezing the emulator's control plane.
func (r *Registry) broadcast(ev Event) {
	for _, ch := range r.watchers {
		select {
		case ch <- ev:
		default:
			// Slow watcher — drop. The `overcast dev` CLI is expected
			// to reconnect and take a fresh snapshot on detecting gaps.
		}
	}
}
