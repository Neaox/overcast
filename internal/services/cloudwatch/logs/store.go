package logs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const (
	nsLogGroups = "logs:groups"
	nsStreams   = "logs:streams" // key: <groupName>/<streamName>

	// nsEvents is the legacy generic-kv namespace event blobs used to live
	// under. Event storage moved to the dedicated logs_events SQL table
	// (storage-plan.md Phase 2 item 2.3, see event_backend.go); this constant
	// now exists only so migrations.go's one-time blob-to-row conversion can
	// find and delete the old rows. Nothing in the runtime read/write path
	// below touches this namespace anymore.
	nsEvents = "logs:events"
)

// LogGroup represents a stored CloudWatch Logs log group.
type LogGroup struct {
	Name            string            `json:"name"`
	ARN             string            `json:"arn"`
	CreationTime    int64             `json:"creation_time"` // epoch millis
	RetentionInDays int               `json:"retention_in_days,omitempty"`
	Tags            map[string]string `json:"tags,omitempty"`
}

// LogStream represents a stored CloudWatch Logs log stream.
type LogStream struct {
	Name                string `json:"name"`
	ARN                 string `json:"arn"`
	CreationTime        int64  `json:"creation_time"`         // epoch millis
	FirstEventTimestamp int64  `json:"first_event_timestamp"` // epoch millis, 0 if no events
	LastEventTimestamp  int64  `json:"last_event_timestamp"`  // epoch millis, 0 if no events
	LastIngestionTime   int64  `json:"last_ingestion_time"`   // epoch millis, 0 if no events
	UploadSequenceToken string `json:"upload_sequence_token"`
}

// LogEvent represents a single log event within a stream.
type LogEvent struct {
	Timestamp     int64  `json:"timestamp"` // epoch millis (from caller)
	Message       string `json:"message"`
	IngestionTime int64  `json:"ingestion_time"` // epoch millis (set by server)
}

// logsStore wraps state.Store with CloudWatch Logs-specific helpers.
//
// Log group/stream metadata (small, finite, TierHot) still goes through
// state.Store's generic kv path unchanged. Event storage is different: it's
// unbounded and high-frequency, so it goes through a dedicated eventBackend
// (event_backend.go) backed by the logs_events SQL table (or an in-memory
// map in memory-mode) instead of a JSON blob per stream.
//
// Event storage keeps a hybrid model, but the cache's role has changed from
// the previous design: per-stream in-memory eventCaches now hold ONLY
// unflushed events (a write buffer), not the stream's full history — the
// backend is the source of truth for everything already persisted. This
// keeps the valuable part of the old design (coalescing bursty writers like
// Lambda's log batcher into one write per debounce window) while dropping
// its two worst properties: rewriting the entire stream's JSON on every
// flush, and holding a stream's entire history resident in memory forever.
//
// getEvents merges the backend's persisted, already-sorted events with the
// cache's small sorted buffer of not-yet-flushed events (a linear merge, not
// a re-sort — see mergeEventsSorted). On graceful shutdown, Stop
// synchronously flushes every dirty cache so nothing is lost.
type logsStore struct {
	store         state.Store
	backend       eventBackend
	clk           clock.Clock
	defaultRegion string

	// streamCaches is a sync.Map of region-scoped event-key → *eventCache.
	// sync.Map fits the access pattern (write-once, read-many for the cache
	// pointer; no iteration required outside Stop) and avoids a single
	// coarse lock across unrelated streams.
	streamCaches sync.Map

	// flushBg is the context for background flush operations. Cancelled by
	// Stop so any in-flight debounce timers exit promptly.
	flushBg       context.Context
	flushBgCancel context.CancelFunc

	// flushWG tracks scheduled debounce goroutines so Stop can wait for them.
	flushWG sync.WaitGroup

	stopped atomic.Bool // set true once Stop has been called
}

// eventCache is the per-stream write buffer for events not yet flushed to
// the backend. Loaded lazily on first append/read; mutations are coalesced
// via a debounced flush goroutine, same shape as the previous design, but
// buffer only ever holds the events accumulated since the last successful
// flush — never the stream's full history.
type eventCache struct {
	mu sync.Mutex

	region string
	group  string
	stream string

	buffer []LogEvent // unflushed events, sorted ascending by Timestamp
	lastTS int64      // Timestamp of buffer's last event; monotonic fast-path

	// dirty is true when buffer has been mutated since the last successful
	// backend flush. Cleared by flushLocked after a successful append.
	dirty bool

	// flushScheduled is true when a debounced flush goroutine is pending or
	// running. Prevents unbounded goroutine creation under append bursts.
	flushScheduled bool
}

func newLogsStore(store state.Store, clk clock.Clock, defaultRegion string) *logsStore {
	bg, cancel := context.WithCancel(context.Background())
	// Resolve past any state.NamespacedStore wrapping before probing for
	// SQLiteDBProvider — an unrelated OVERCAST_STATE_<SVC> override on some
	// other service would otherwise wrap store in a type that satisfies
	// neither SQLiteDBProvider nor ReadyAwaiter, silently downgrading Logs
	// events to the in-memory-only backend even though Logs itself was never
	// routed away from SQLite. See internal/state's state.Unwrap doc comment
	// and the equivalent DynamoDB fix in internal/services/dynamodb/service.go.
	backendStore := state.Unwrap(store, serviceName)
	s := &logsStore{
		store:         store,
		backend:       newEventBackendFor(backendStore),
		clk:           clk,
		defaultRegion: defaultRegion,
		flushBg:       bg,
		flushBgCancel: cancel,
	}
	s.startRetentionSweeper()
	return s
}

// region extracts the AWS region from the request context, falling back to the
// configured default.
func (s *logsStore) region(ctx context.Context) string {
	return middleware.RegionFromContext(ctx, s.defaultRegion)
}

// ---- Log group operations --------------------------------------------------

func (s *logsStore) getLogGroup(ctx context.Context, name string) (*LogGroup, *protocol.AWSError) {
	raw, found, err := s.store.Get(ctx, nsLogGroups, serviceutil.RegionKey(s.region(ctx), name))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, errGroupNotFound(name)
	}
	var g LogGroup
	if err := json.Unmarshal([]byte(raw), &g); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &g, nil
}

func (s *logsStore) putLogGroup(ctx context.Context, g *LogGroup) *protocol.AWSError {
	raw, err := json.Marshal(g)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsLogGroups, serviceutil.RegionKey(s.region(ctx), g.Name), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *logsStore) listLogGroups(ctx context.Context, prefix string) ([]*LogGroup, *protocol.AWSError) {
	keys, err := s.store.List(ctx, nsLogGroups, serviceutil.RegionKey(s.region(ctx), prefix))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	groups := make([]*LogGroup, 0, len(keys))
	for _, k := range keys {
		raw, found, err := s.store.Get(ctx, nsLogGroups, k)
		if err != nil {
			return nil, protocol.Wrap(protocol.ErrInternalError, err)
		}
		if !found {
			continue
		}
		var g LogGroup
		if err := json.Unmarshal([]byte(raw), &g); err != nil {
			return nil, protocol.Wrap(protocol.ErrInternalError, err)
		}
		// Apply prefix filter: List returns keys that have the prefix as a
		// storage-level prefix, but CloudWatch uses full path names so we
		// double-check with strings.HasPrefix.
		if prefix == "" || strings.HasPrefix(g.Name, prefix) {
			groups = append(groups, &g)
		}
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
	return groups, nil
}

// ---- Log stream operations -------------------------------------------------

func streamKey(groupName, streamName string) string {
	return groupName + "/" + streamName
}

func (s *logsStore) getLogStream(ctx context.Context, groupName, streamName string) (*LogStream, *protocol.AWSError) {
	raw, found, err := s.store.Get(ctx, nsStreams, serviceutil.RegionKey(s.region(ctx), streamKey(groupName, streamName)))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, errStreamNotFound(streamName)
	}
	var ls LogStream
	if err := json.Unmarshal([]byte(raw), &ls); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &ls, nil
}

func (s *logsStore) putLogStream(ctx context.Context, groupName string, ls *LogStream) *protocol.AWSError {
	raw, err := json.Marshal(ls)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsStreams, serviceutil.RegionKey(s.region(ctx), streamKey(groupName, ls.Name)), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *logsStore) listLogStreams(ctx context.Context, groupName, prefix string) ([]*LogStream, *protocol.AWSError) {
	fullPrefix := groupName + "/"
	if prefix != "" {
		fullPrefix += prefix
	}
	keys, err := s.store.List(ctx, nsStreams, serviceutil.RegionKey(s.region(ctx), fullPrefix))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	streams := make([]*LogStream, 0, len(keys))
	for _, k := range keys {
		// Keys returned by List are already region-scoped; read them directly.
		raw, found, err := s.store.Get(ctx, nsStreams, k)
		if err != nil {
			return nil, protocol.Wrap(protocol.ErrInternalError, err)
		}
		if !found {
			continue
		}
		var ls LogStream
		if err := json.Unmarshal([]byte(raw), &ls); err != nil {
			return nil, protocol.Wrap(protocol.ErrInternalError, err)
		}
		streams = append(streams, &ls)
	}
	sort.Slice(streams, func(i, j int) bool { return streams[i].Name < streams[j].Name })
	return streams, nil
}

func (s *logsStore) deleteLogStream(ctx context.Context, groupName, streamName string) *protocol.AWSError {
	region := s.region(ctx)
	key := serviceutil.RegionKey(region, streamKey(groupName, streamName))
	if err := s.store.Delete(ctx, nsStreams, key); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	// Drop the in-memory write buffer before deleting persisted events, and
	// mark it non-dirty so an in-flight debounced flush becomes a no-op
	// (otherwise it could re-create rows immediately after the delete below).
	s.clearEventCache(region, groupName, streamName)
	if err := s.backend.deleteStream(ctx, region, groupName, streamName); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *logsStore) deleteLogGroup(ctx context.Context, name string) *protocol.AWSError {
	region := s.region(ctx)
	// Delete the group itself.
	if err := s.store.Delete(ctx, nsLogGroups, serviceutil.RegionKey(region, name)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	// Delete all stream records + write buffers belonging to this group. The
	// persisted events themselves are removed in one ranged DELETE below
	// (backend.deleteGroup) rather than per-stream, since the backend has an
	// index that makes a group-wide delete cheap regardless of stream count.
	streams, aerr := s.listLogStreams(ctx, name, "")
	if aerr != nil {
		return aerr
	}
	for _, st := range streams {
		key := serviceutil.RegionKey(region, streamKey(name, st.Name))
		if err := s.store.Delete(ctx, nsStreams, key); err != nil {
			return protocol.Wrap(protocol.ErrInternalError, err)
		}
		s.clearEventCache(region, name, st.Name)
	}
	if err := s.backend.deleteGroup(ctx, region, name); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// ---- Log event operations --------------------------------------------------

func eventsKey(groupName, streamName string) string {
	return groupName + "/" + streamName
}

// streamCacheKey returns the region-scoped cache key for a stream's write
// buffer. Purely an opaque sync.Map key — nothing parses it back apart, so
// it's safe even though group/stream names may themselves contain "/".
func (s *logsStore) streamCacheKey(ctx context.Context, groupName, streamName string) string {
	return serviceutil.RegionKey(s.region(ctx), eventsKey(groupName, streamName))
}

// loadEventCache returns the *eventCache for a stream, creating it (empty)
// on first access. Unlike the previous design's loadStreamCache, this never
// touches the backend — there is nothing to lazily load, since the cache
// only ever holds not-yet-flushed events.
func (s *logsStore) loadEventCache(ctx context.Context, groupName, streamName string) *eventCache {
	key := s.streamCacheKey(ctx, groupName, streamName)
	if v, ok := s.streamCaches.Load(key); ok {
		return v.(*eventCache)
	}
	c := &eventCache{region: s.region(ctx), group: groupName, stream: streamName}
	actual, _ := s.streamCaches.LoadOrStore(key, c)
	return actual.(*eventCache)
}

// clearEventCache drops a stream's write buffer without flushing it (used on
// stream/group deletion, where flushing would just re-create what's about to
// be deleted).
func (s *logsStore) clearEventCache(region, groupName, streamName string) {
	key := serviceutil.RegionKey(region, eventsKey(groupName, streamName))
	if v, ok := s.streamCaches.Load(key); ok {
		c := v.(*eventCache)
		c.mu.Lock()
		c.dirty = false
		c.buffer = nil
		c.lastTS = 0
		c.mu.Unlock()
	}
	s.streamCaches.Delete(key)
}

// getEvents returns every event for a stream, merging the backend's
// persisted (already sorted) events with the cache's small sorted buffer of
// not-yet-flushed events — a linear merge, not a re-sort, so the cost of a
// read stays proportional to persisted+buffered size rather than paying an
// extra O(n log n) on every call.
func (s *logsStore) getEvents(ctx context.Context, groupName, streamName string) ([]LogEvent, *protocol.AWSError) {
	region := s.region(ctx)
	persisted, err := s.backend.getEvents(ctx, region, groupName, streamName)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}

	c := s.loadEventCache(ctx, groupName, streamName)
	c.mu.Lock()
	var buffered []LogEvent
	if len(c.buffer) > 0 {
		buffered = make([]LogEvent, len(c.buffer))
		copy(buffered, c.buffer)
	}
	c.mu.Unlock()

	if len(buffered) == 0 {
		if persisted == nil {
			return []LogEvent{}, nil
		}
		return persisted, nil
	}
	return mergeEventsSorted(persisted, buffered), nil
}

// mergeEventsSorted merges two ascending-by-Timestamp slices in O(len(a)+len(b))
// via a standard two-pointer merge. Ties keep a's element first (persisted
// before buffered), which is an arbitrary but deterministic and stable choice.
func mergeEventsSorted(a, b []LogEvent) []LogEvent {
	if len(b) == 0 {
		return a
	}
	if len(a) == 0 {
		return b
	}
	out := make([]LogEvent, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if a[i].Timestamp <= b[j].Timestamp {
			out = append(out, a[i])
			i++
		} else {
			out = append(out, b[j])
			j++
		}
	}
	out = append(out, a[i:]...)
	out = append(out, b[j:]...)
	return out
}

func (s *logsStore) appendEvents(ctx context.Context, groupName, streamName string, newEvents []LogEvent) *protocol.AWSError {
	if len(newEvents) == 0 {
		return nil
	}
	c := s.loadEventCache(ctx, groupName, streamName)
	c.mu.Lock()

	// Fast path: if every incoming event is at or after the current buffer
	// tail, append without re-sorting (the common case — Lambda's log
	// batcher emits events in monotonically-non-decreasing order).
	monotonic := true
	for _, e := range newEvents {
		if e.Timestamp < c.lastTS {
			monotonic = false
			break
		}
		c.lastTS = e.Timestamp
	}
	c.buffer = append(c.buffer, newEvents...)
	if !monotonic {
		sort.SliceStable(c.buffer, func(i, j int) bool { return c.buffer[i].Timestamp < c.buffer[j].Timestamp })
		if n := len(c.buffer); n > 0 {
			c.lastTS = c.buffer[n-1].Timestamp
		}
	}
	c.dirty = true

	// If a watermark of un-flushed events has accumulated, flush now under
	// the lock instead of waiting for the debounce — bounds the worst-case
	// data loss window on hard crash and bounds peak memory if a stream is
	// extremely chatty. The watermark is intentionally generous so the
	// common case stays on the debounce path. Unlike the previous blob
	// design, hitting this watermark repeatedly is cheap: each flush is a
	// batched INSERT of only the buffered events, not a rewrite of the
	// stream's full history.
	const flushWatermark = 1024
	if len(c.buffer) >= flushWatermark {
		// Use the request ctx for the inline flush so failures propagate.
		aerr := s.flushLocked(ctx, c)
		c.mu.Unlock()
		return aerr
	}

	// Schedule a debounced flush if one isn't already pending.
	if !c.flushScheduled && !s.stopped.Load() {
		c.flushScheduled = true
		s.flushWG.Add(1)
		go s.debouncedFlush(c)
	}
	c.mu.Unlock()
	return nil
}

// flushDebounceInterval is the quiet period after the last append during which
// the cache may accumulate further appends before being persisted to the
// backend. Tuned so a typical Lambda invocation (which produces a burst of
// log lines in <50 ms) results in a single coalesced write.
var flushDebounceInterval = 50 * time.Millisecond

// debouncedFlush runs in its own goroutine; it sleeps for flushDebounceInterval
// and then performs a single flush of the buffer's current contents. Subsequent
// appends during the sleep window do NOT spawn additional goroutines (gated by
// flushScheduled), so flush rate is bounded at 1 per debounce interval per
// stream.
func (s *logsStore) debouncedFlush(c *eventCache) {
	defer s.flushWG.Done()
	select {
	case <-time.After(flushDebounceInterval):
	case <-s.flushBg.Done():
		// Stop was called; do a final flush now using a fresh ctx.
		c.mu.Lock()
		_ = s.flushLocked(context.Background(), c)
		c.flushScheduled = false
		c.mu.Unlock()
		return
	}
	c.mu.Lock()
	_ = s.flushLocked(s.flushBg, c)
	c.flushScheduled = false
	c.mu.Unlock()
}

// flushLocked persists the cache's buffered events to the backend as one
// batched append and clears the buffer. Caller must hold c.mu. No-op when
// the cache is not dirty (e.g. another flush already happened).
func (s *logsStore) flushLocked(ctx context.Context, c *eventCache) *protocol.AWSError {
	if !c.dirty {
		return nil
	}
	// Use a non-cancelled context for backend writes triggered by debounce
	// completion (the request ctx that originally produced the events may
	// already be done — we still want to persist).
	writeCtx := ctx
	if writeCtx == nil || writeCtx.Err() != nil {
		writeCtx = context.Background()
	}
	if err := s.backend.appendEvents(writeCtx, c.region, c.group, c.stream, c.buffer); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	c.buffer = nil
	c.dirty = false
	return nil
}

// Stop synchronously flushes every dirty stream's write buffer. Called by the
// service during graceful shutdown so no in-memory log events are lost. After
// Stop returns, further appends are still served (cache continues to
// function), but they fall back to inline write-through because debounce
// goroutines are no longer scheduled.
func (s *logsStore) Stop(ctx context.Context) {
	if !s.stopped.CompareAndSwap(false, true) {
		return // already stopped
	}
	// Wait for any in-flight debounce goroutines to observe the cancellation
	// and complete their final flush. The cancel signal causes them to flush
	// immediately rather than wait the remaining debounce interval.
	s.flushBgCancel()
	s.flushWG.Wait()

	// Final pass: walk every cache and flush any that are still dirty.
	// (debouncedFlush already flushed each one it owned; this catches any
	// caches that were dirtied between the goroutine's flushLocked and Stop
	// returning, and any caches that never had a debounce scheduled because
	// their only append went via the inline watermark path.)
	s.streamCaches.Range(func(_, v any) bool {
		c := v.(*eventCache)
		c.mu.Lock()
		if c.dirty {
			_ = s.flushLocked(ctx, c)
		}
		c.mu.Unlock()
		return true
	})
}

// ---- RetentionInDays enforcement --------------------------------------------
//
// LogGroup.RetentionInDays is stored but, on its own, does nothing — real
// CloudWatch Logs asynchronously purges events older than the configured
// retention period, and this periodic sweep is Overcast's equivalent
// (storage-plan.md 3.4). It reuses the same background-goroutine lifecycle as
// the debounced flush machinery above: started once from newLogsStore,
// listening on flushBg.Done() for the stop signal, tracked by flushWG so
// Stop() waits for it to exit before returning.

// retentionSweepInterval is how often sweepExpiredEventsOnce runs.
// RetentionInDays is expressed in whole days, so a much finer cadence would
// be wasted work; this balances timeliness (bounding how long expired events
// can linger on disk) against overhead for the common local dev/CI case.
const retentionSweepInterval = 5 * time.Minute

// startRetentionSweeper starts the background retention sweep goroutine.
// Called once from newLogsStore, mirroring how the package already treats
// "start a long-lived background loop tied to this store's lifecycle" as a
// construction-time concern.
//
// The ticker is created here — synchronously, on newLogsStore's calling
// goroutine — rather than inside the spawned goroutine. This matters for
// tests: it guarantees clk.Ticker has already registered with a mock clock
// by the time newLogsStore (and so New) returns, so a test that calls
// mock.Add() immediately after construction can't race against the
// goroutine not having started yet (advancing a mock clock before a ticker
// exists is simply lost — the ticker only anchors its first firing to
// whatever time it observes at creation). The loop itself is kept in its own
// goroutine closure rather than a named method (separate from
// sweepExpiredEventsOnce) so tests can still call sweepExpiredEventsOnce
// directly for a deterministic, single-sweep assertion instead of driving a
// real or mocked ticker.
func (s *logsStore) startRetentionSweeper() {
	ticker := s.clk.Ticker(retentionSweepInterval)
	s.flushWG.Add(1)
	go func() {
		defer s.flushWG.Done()
		defer ticker.Stop()
		for {
			select {
			case <-s.flushBg.Done():
				return
			case <-ticker.C:
				s.sweepExpiredEventsOnce(context.Background())
			}
		}
	}()
}

// sweepExpiredEventsOnce scans every log group, across every region, and
// deletes events older than each group's RetentionInDays via one group-scoped
// ranged delete (eventBackend.deleteEventsOlderThan). Groups with
// RetentionInDays == 0 (the zero value, and what DeleteRetentionPolicy resets
// to — see handler.go/typed_logic.go) are never swept: real CloudWatch Logs
// treats 0/unset as "never expire", and this codebase already follows that
// convention for every other retention-related code path.
func (s *logsStore) sweepExpiredEventsOnce(ctx context.Context) {
	pairs, err := s.store.Scan(ctx, nsLogGroups, "")
	if err != nil {
		return
	}
	now := s.clk.Now().UTC()
	for _, kv := range pairs {
		var g LogGroup
		if err := json.Unmarshal([]byte(kv.Value), &g); err != nil {
			continue
		}
		if g.RetentionInDays <= 0 {
			continue
		}
		region, _ := serviceutil.SplitRegionKey(kv.Key)
		cutoff := now.AddDate(0, 0, -g.RetentionInDays)
		_ = s.backend.deleteEventsOlderThan(ctx, region, g.Name, cutoff)
	}
}

// ---- CloudWatch Logs-specific errors ---------------------------------------

func errGroupNotFound(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ResourceNotFoundException",
		Message:    fmt.Sprintf("The specified log group does not exist: %s", name),
		HTTPStatus: http.StatusBadRequest,
	}
}

func errGroupAlreadyExists(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ResourceAlreadyExistsException",
		Message:    fmt.Sprintf("The specified log group already exists: %s", name),
		HTTPStatus: http.StatusBadRequest,
	}
}

func errStreamNotFound(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ResourceNotFoundException",
		Message:    fmt.Sprintf("The specified log stream does not exist: %s", name),
		HTTPStatus: http.StatusBadRequest,
	}
}

func errStreamAlreadyExists(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ResourceAlreadyExistsException",
		Message:    fmt.Sprintf("The specified log stream already exists: %s", name),
		HTTPStatus: http.StatusBadRequest,
	}
}

func errInvalidParameter(msg string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "InvalidParameterException",
		Message:    msg,
		HTTPStatus: http.StatusBadRequest,
	}
}
