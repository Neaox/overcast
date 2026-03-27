package events

import (
	"context"
	"sync"
)

// HandlerFunc is the signature for event subscribers.
// Implementations must be safe for concurrent use and must not block —
// they run in a goroutine spawned by the bus.
type HandlerFunc func(ctx context.Context, e Event)

// Bus is a simple pub/sub event bus.
//
// Publish is non-blocking: each call spawns a goroutine per registered
// subscriber and returns immediately, so the calling HTTP handler is never
// delayed by downstream delivery. Subscribers receive a copy of the
// request context so they respect cancellation and deadlines.
//
// Bus is safe for concurrent use.
type Bus struct {
	mu          sync.RWMutex
	subscribers map[Type][]HandlerFunc
}

// NewBus returns an initialised Bus ready for use.
func NewBus() *Bus {
	return &Bus{
		subscribers: make(map[Type][]HandlerFunc),
	}
}

// Subscribe registers h to be called for every event of type t.
// The returned cancel function removes the subscription. It is safe to call
// cancel more than once.
func (b *Bus) Subscribe(t Type, h HandlerFunc) (cancel func()) {
	b.mu.Lock()
	b.subscribers[t] = append(b.subscribers[t], h)
	idx := len(b.subscribers[t]) - 1
	b.mu.Unlock()

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

// Publish dispatches e to all subscribers registered for e.Type and to any
// wildcard (All) subscribers. Each subscriber runs in its own goroutine.
// Publish returns before any subscriber completes.
func (b *Bus) Publish(ctx context.Context, e Event) {
	b.mu.RLock()
	typed := make([]HandlerFunc, len(b.subscribers[e.Type]))
	copy(typed, b.subscribers[e.Type])
	wild := make([]HandlerFunc, len(b.subscribers[All]))
	copy(wild, b.subscribers[All])
	b.mu.RUnlock()

	for _, h := range typed {
		h := h
		go h(ctx, e)
	}
	for _, h := range wild {
		h := h
		go h(ctx, e)
	}
}
