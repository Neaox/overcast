package events

import (
	"container/list"
	"sync"
)

// HistoryCapacity is the maximum number of events retained in the rolling
// history buffer that newly-connecting event-stream clients (the web UI's
// Events page) replay on connect.
//
// This is a compile-time constant rather than a config/env knob: Overcast
// is explicitly not a production dependency or a durability guarantee (see
// AGENTS.md "Non-goals"), and 10,000 events is comfortably enough to cover
// "what happened in the last few minutes of a debugging session" without
// the unbounded memory growth an env-tunable cap would invite.
const HistoryCapacity = 10_000

// historyEntry is one node in the buffer's chronological ordering.
type historyEntry struct {
	event Event
	noise bool
}

// History is a fixed-capacity, thread-safe buffer of published events with
// two-tier eviction: when full, the oldest NOISE-class event (see IsNoise)
// is evicted first; only once no noise event remains in the buffer does the
// oldest event overall get evicted. This keeps high-frequency, low-signal
// traffic (request telemetry) from crowding out the state-change events a
// developer debugging yesterday's incident actually wants to see.
//
// Internally, events are kept in a doubly-linked list in arrival order
// (order), plus a second list of *list.Element pointers into order for the
// noise-only subsequence (noiseQ). Both eviction and insertion are O(1):
// evicting the oldest noise event is noiseQ.Front() followed by an O(1)
// list.Remove using the stored element pointer — no scan required.
//
// History is safe for concurrent use on its own, but a caller that needs
// snapshot+subscribe atomicity (no gap, no duplicate event at the
// connection boundary) must coordinate externally — see
// Bus.SnapshotAndSubscribeAll, which holds Bus.mu across both the snapshot
// and the subscription so no Publish can interleave.
type History struct {
	mu       sync.Mutex
	order    *list.List // Value: *historyEntry: oldest at Front, newest at Back
	noiseQ   *list.List // Value: *list.Element (into order): oldest noise at Front
	capacity int
}

// NewHistory returns an empty History with the given capacity. A capacity
// of 0 disables buffering: Append becomes a no-op and Snapshot always
// returns an empty slice.
func NewHistory(capacity int) *History {
	return &History{
		order:    list.New(),
		noiseQ:   list.New(),
		capacity: capacity,
	}
}

// Append adds e to the buffer. If the payload carries data that could be
// large enough to make a 10,000-event buffer memory-unsafe, a redacted copy
// is stored instead (see sanitizeForHistory) — the live SSE stream is
// unaffected, only what gets retained for replay.
//
// When the buffer is at capacity, one event is evicted first per the
// two-tier policy described on History.
func (h *History) Append(e Event) {
	if h.capacity <= 0 {
		return
	}
	e = sanitizeForHistory(e)

	h.mu.Lock()
	defer h.mu.Unlock()

	for h.order.Len() >= h.capacity {
		h.evictLocked()
	}

	entry := &historyEntry{event: e, noise: IsNoise(e.Type)}
	elem := h.order.PushBack(entry)
	if entry.noise {
		h.noiseQ.PushBack(elem)
	}
}

// evictLocked removes exactly one event per the two-tier eviction policy.
// Caller must hold h.mu.
func (h *History) evictLocked() {
	if front := h.noiseQ.Front(); front != nil {
		elem, _ := front.Value.(*list.Element)
		h.noiseQ.Remove(front)
		if elem != nil {
			h.order.Remove(elem)
		}
		return
	}
	if front := h.order.Front(); front != nil {
		h.order.Remove(front)
	}
}

// Snapshot returns a copy of all buffered events in chronological
// (oldest-first) order. The returned slice is safe to use without further
// locking — it shares no state with the buffer.
func (h *History) Snapshot() []Event {
	h.mu.Lock()
	defer h.mu.Unlock()

	out := make([]Event, 0, h.order.Len())
	for el := h.order.Front(); el != nil; el = el.Next() {
		out = append(out, el.Value.(*historyEntry).event)
	}
	return out
}

// Len returns the number of events currently buffered.
func (h *History) Len() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.order.Len()
}

// FindByRequestID returns all buffered events whose RequestReceived
// payload carries the given request ID, in chronological order.
func (h *History) FindByRequestID(requestID string) []Event {
	h.mu.Lock()
	defer h.mu.Unlock()

	var out []Event
	for el := h.order.Front(); el != nil; el = el.Next() {
		e := el.Value.(*historyEntry).event
		if p, ok := e.Payload.(RequestPayload); ok && p.RequestID == requestID {
			out = append(out, e)
		}
	}
	return out
}

// sanitizeForHistory returns a copy of e with known-large payload fields
// stripped so the history buffer's memory footprint stays bounded
// regardless of what individual events carry.
//
// LambdaInstancePayload.TriggerEvent holds the raw invocation payload
// bytes — up to the Lambda synchronous-invoke limit (6 MB) — and is
// republished on every instance lifecycle transition (acquired, ready,
// initializing, released, evicted). Keeping that in a 10,000-entry buffer
// could dominate memory, so it is omitted from history entirely rather
// than truncated (truncating a byte slice that is later base64-encoded by
// json.Marshal would silently corrupt it instead of just losing detail).
// The live SSE stream still carries the full TriggerEvent; only the
// replay-on-connect history entry is redacted.
func sanitizeForHistory(e Event) Event {
	if p, ok := e.Payload.(LambdaInstancePayload); ok && len(p.TriggerEvent) > 0 {
		p.TriggerEvent = nil
		e.Payload = p
	}
	return e
}
