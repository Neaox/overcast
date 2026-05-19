// Package hostbridge wires a stream of domain-registration events from the
// overcast emulator to the host machine's mDNS responder and local trust
// store. It is the implementation behind the `overcast dev` subcommand.
//
// The bridge is deliberately small: it owns an active set of published
// records, consumes Events from a Source, and translates them into calls
// on an mdns.Publisher. Everything platform-specific lives behind the
// mdns.Publisher and trust.Store interfaces — this file has no OS knowledge.
package hostbridge

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/hostbridge/mdns"
)

// EventType distinguishes a domain registration from a de-registration.
type EventType int

const (
	// EventAdded indicates a domain has been registered with the emulator
	// and should be advertised on the host.
	EventAdded EventType = iota
	// EventRemoved indicates a domain has been de-registered and should
	// be withdrawn from the host advertisement.
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

// Event is a single domain registration change delivered by a Source.
type Event struct {
	Type   EventType
	Record mdns.Record
}

// Source is the upstream feed of domain events. The overcast host CLI
// implementation of Source tails the emulator's /_internal/domains/watch
// SSE endpoint; tests can supply an in-memory channel instead.
//
// Watch must return a channel that is closed when the underlying feed
// terminates (either cleanly or due to an error). Implementations should
// honour ctx cancellation to unblock the caller promptly.
type Source interface {
	Watch(ctx context.Context) (<-chan Event, error)
}

// Bridge consumes Events from a Source and drives an mdns.Publisher,
// maintaining an internal set of currently-published records so that
// Close cleanly withdraws every advertisement on shutdown.
type Bridge struct {
	pub mdns.Publisher
	src Source
	log *zap.Logger

	mu     sync.Mutex
	active map[string]mdns.Record // keyed by Record.Key()
}

// New constructs a Bridge. All three arguments are required; passing nil
// for any of them is a programming error.
func New(pub mdns.Publisher, src Source, log *zap.Logger) *Bridge {
	if pub == nil || src == nil || log == nil {
		panic("hostbridge.New: pub, src and log must all be non-nil")
	}
	return &Bridge{
		pub:    pub,
		src:    src,
		log:    log,
		active: make(map[string]mdns.Record),
	}
}

// Run drives the bridge until ctx is cancelled or the Source's channel is
// closed. It always withdraws any records it has published before returning,
// even on error, so callers can simply defer-cancel their context.
//
// Run returns nil when the source channel closes cleanly, ctx.Err() when
// the caller cancels, and a wrapped error if the source or publisher fails.
func (b *Bridge) Run(ctx context.Context) error {
	events, err := b.src.Watch(ctx)
	if err != nil {
		return fmt.Errorf("hostbridge: start source watch: %w", err)
	}

	defer b.withdrawAll()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-events:
			if !ok {
				return nil
			}
			b.handle(ctx, ev)
		}
	}
}

// handle applies a single Event to the active set. Errors from the
// publisher are logged but do not terminate the bridge: a transient DNS-SD
// failure should not take down the whole host CLI.
func (b *Bridge) handle(ctx context.Context, ev Event) {
	switch ev.Type {
	case EventAdded:
		b.add(ctx, ev.Record)
	case EventRemoved:
		b.remove(ctx, ev.Record)
	default:
		b.log.Warn("hostbridge: unknown event type",
			zap.Int("type", int(ev.Type)),
			zap.String("hostname", ev.Record.Hostname),
		)
	}
}

// add publishes a record, replacing any prior advertisement for the same
// hostname. Replace semantics let the emulator update a custom domain's
// target IP without the client having to send an explicit Removed first.
func (b *Bridge) add(ctx context.Context, r mdns.Record) {
	b.mu.Lock()
	prev, existed := b.active[r.Key()]
	b.active[r.Key()] = r
	b.mu.Unlock()

	if existed && prev.IP.Equal(r.IP) {
		// No-op: an identical advertisement is already live.
		return
	}
	if existed {
		// Best-effort withdrawal of the stale record before republishing.
		if err := b.pub.Unpublish(ctx, prev); err != nil {
			b.log.Warn("hostbridge: withdraw prior record failed",
				zap.String("hostname", prev.Hostname),
				zap.Error(err),
			)
		}
	}
	if err := b.pub.Publish(ctx, r); err != nil {
		b.log.Error("hostbridge: publish failed",
			zap.String("hostname", r.Hostname),
			zap.Stringer("ip", r.IP),
			zap.Error(err),
		)
		return
	}
	b.log.Info("hostbridge: published",
		zap.String("hostname", r.Hostname),
		zap.Stringer("ip", r.IP),
	)
}

// remove withdraws a record if it is currently in the active set. Removing
// a record that was never added is silently ignored, which matches the
// "idempotent unpublish" contract on mdns.Publisher.
func (b *Bridge) remove(ctx context.Context, r mdns.Record) {
	b.mu.Lock()
	cur, ok := b.active[r.Key()]
	if ok {
		delete(b.active, r.Key())
	}
	b.mu.Unlock()

	if !ok {
		return
	}
	if err := b.pub.Unpublish(ctx, cur); err != nil {
		b.log.Warn("hostbridge: unpublish failed",
			zap.String("hostname", cur.Hostname),
			zap.Error(err),
		)
		return
	}
	b.log.Info("hostbridge: withdrawn", zap.String("hostname", cur.Hostname))
}

// withdrawAll unpublishes every record in the active set. It is called
// from Run's defer so that a cancelled context or a source EOF always
// leaves the host responder in a clean state.
func (b *Bridge) withdrawAll() {
	b.mu.Lock()
	snapshot := make([]mdns.Record, 0, len(b.active))
	for _, r := range b.active {
		snapshot = append(snapshot, r)
	}
	b.active = make(map[string]mdns.Record)
	b.mu.Unlock()

	if len(snapshot) == 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var errs []error
	for _, r := range snapshot {
		if err := b.pub.Unpublish(ctx, r); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", r.Hostname, err))
		}
	}
	if err := b.pub.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close publisher: %w", err))
	}
	if len(errs) > 0 {
		b.log.Warn("hostbridge: shutdown had errors", zap.Error(errors.Join(errs...)))
	}
}

// Active returns a snapshot of the currently-published records. Intended
// for tests and for the `overcast status` subcommand.
func (b *Bridge) Active() []mdns.Record {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]mdns.Record, 0, len(b.active))
	for _, r := range b.active {
		out = append(out, r)
	}
	return out
}
