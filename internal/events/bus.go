package events

import (
	"context"
	"sync"
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
// Bus is safe for concurrent use.
type Bus struct {
	mu          sync.RWMutex
	subscribers map[Type][]HandlerFunc
	workCh      chan workItem
	stopCh      chan struct{}
	wg          sync.WaitGroup
}

// NewBus returns an initialised Bus with its worker pool running.
// Call Stop to shut down the workers.
func NewBus() *Bus {
	b := &Bus{
		subscribers: make(map[Type][]HandlerFunc),
		workCh:      make(chan workItem, workQueueSize),
		stopCh:      make(chan struct{}),
	}
	b.wg.Add(workerCount)
	for range workerCount {
		go b.worker()
	}
	return b
}

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
// wildcard (All) subscribers. Each subscriber invocation is enqueued onto the
// worker pool's buffered channel. Publish returns immediately unless the
// queue is completely full, in which case it blocks briefly until a slot
// opens — no events are ever dropped.
func (b *Bus) Publish(ctx context.Context, e Event) {
	b.mu.RLock()
	typed := make([]HandlerFunc, len(b.subscribers[e.Type]))
	copy(typed, b.subscribers[e.Type])
	wild := make([]HandlerFunc, len(b.subscribers[All]))
	copy(wild, b.subscribers[All])
	b.mu.RUnlock()

	for _, h := range typed {
		b.workCh <- workItem{ctx: ctx, e: e, h: h}
	}
	for _, h := range wild {
		b.workCh <- workItem{ctx: ctx, e: e, h: h}
	}
}
