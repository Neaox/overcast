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
	nsEvents    = "logs:events"  // key: <groupName>/<streamName>
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

// storedEvents holds the full list of events for a stream (stored as a single
// JSON blob per stream). This keeps reads simple and avoids per-event keys.
type storedEvents struct {
	Events []LogEvent `json:"events"`
}

// logsStore wraps state.Store with CloudWatch Logs-specific helpers.
//
// Event storage uses a hybrid model: per-stream in-memory caches are the
// authoritative source for reads and writes during the process's lifetime.
// Mutations mark the cache dirty and schedule a debounced async flush to
// state.Store; getEvents always serves from the cache (which is consistent
// with un-flushed writes). On graceful shutdown, Stop synchronously flushes
// every dirty cache so nothing is lost.
//
// This eliminates three cliffs from the previous write-through design:
//   - global mutex contention across streams (now per-stream),
//   - JSON unmarshal on every append (now once on first access),
//   - JSON marshal + state.Store write on every append (now coalesced; one
//     write per debounce window or when a size watermark is hit).
type logsStore struct {
	store         state.Store
	clk           clock.Clock
	defaultRegion string

	// streamCaches is a sync.Map of region-scoped event-key → *streamCache.
	// sync.Map fits the access pattern (write-once, read-many for the cache
	// pointer; no iteration required) and avoids a single coarse lock.
	streamCaches sync.Map

	// flushBg is the context for background flush operations. Cancelled by
	// Stop so any in-flight debounce timers exit promptly.
	flushBg       context.Context
	flushBgCancel context.CancelFunc

	// flushWG tracks scheduled debounce goroutines so Stop can wait for them.
	flushWG sync.WaitGroup

	stopped atomic.Bool // set true once Stop has been called
}

// streamCache is the per-stream authoritative event list. Loaded lazily from
// state.Store on first access; mutations are coalesced via a debounced flush
// goroutine.
type streamCache struct {
	mu     sync.Mutex
	events []LogEvent // sorted ascending by Timestamp
	lastTS int64      // Timestamp of last event; used for monotonic fast-path
	loaded bool       // false until first read from state.Store completes

	// dirty is true when events has been mutated since the last successful
	// state.Store write. Caller of flushLocked clears it after Set returns.
	dirty bool

	// flushScheduled is true when a debounced flush goroutine is pending or
	// running. Prevents unbounded goroutine creation under append bursts.
	flushScheduled bool
}

func newLogsStore(store state.Store, clk clock.Clock, defaultRegion string) *logsStore {
	bg, cancel := context.WithCancel(context.Background())
	return &logsStore{
		store:         store,
		clk:           clk,
		defaultRegion: defaultRegion,
		flushBg:       bg,
		flushBgCancel: cancel,
	}
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
	// Drop the in-memory cache before deleting events, and mark the cache
	// non-dirty so any in-flight debounced flush becomes a no-op (otherwise
	// it would re-create the events blob immediately after Delete).
	eventsCacheKey := serviceutil.RegionKey(region, eventsKey(groupName, streamName))
	if v, ok := s.streamCaches.Load(eventsCacheKey); ok {
		c := v.(*streamCache)
		c.mu.Lock()
		c.dirty = false
		c.events = nil
		c.lastTS = 0
		c.loaded = true // prevent reload from soon-to-be-deleted store entry
		c.mu.Unlock()
	}
	s.invalidateStreamCache(eventsCacheKey)
	if err := s.store.Delete(ctx, nsEvents, eventsCacheKey); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *logsStore) deleteLogGroup(ctx context.Context, name string) *protocol.AWSError {
	// Delete the group itself.
	if err := s.store.Delete(ctx, nsLogGroups, serviceutil.RegionKey(s.region(ctx), name)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	// Delete all streams and events belonging to this group.
	streams, aerr := s.listLogStreams(ctx, name, "")
	if aerr != nil {
		return aerr
	}
	for _, st := range streams {
		if aerr := s.deleteLogStream(ctx, name, st.Name); aerr != nil {
			return aerr
		}
	}
	return nil
}

// ---- Log event operations --------------------------------------------------

func eventsKey(groupName, streamName string) string {
	return groupName + "/" + streamName
}

// streamCacheKey returns the region-scoped cache key for a stream's events.
func (s *logsStore) streamCacheKey(ctx context.Context, groupName, streamName string) string {
	return serviceutil.RegionKey(s.region(ctx), eventsKey(groupName, streamName))
}

// loadStreamCache returns the *streamCache for a stream, creating it on first
// access. The cache is populated from state.Store lazily inside getOrLoad to
// avoid blocking other streams behind a global init.
func (s *logsStore) loadStreamCache(key string) *streamCache {
	if v, ok := s.streamCaches.Load(key); ok {
		return v.(*streamCache)
	}
	c := &streamCache{}
	actual, _ := s.streamCaches.LoadOrStore(key, c)
	return actual.(*streamCache)
}

// ensureLoaded reads the persisted blob from state.Store on first access.
// Caller must hold c.mu.
func (s *logsStore) ensureLoaded(ctx context.Context, c *streamCache, key string) *protocol.AWSError {
	if c.loaded {
		return nil
	}
	raw, found, err := s.store.Get(ctx, nsEvents, key)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if found {
		var se storedEvents
		if err := json.Unmarshal([]byte(raw), &se); err != nil {
			return protocol.Wrap(protocol.ErrInternalError, err)
		}
		c.events = se.Events
		if n := len(c.events); n > 0 {
			// Stored events are sorted; trust the last entry.
			c.lastTS = c.events[n-1].Timestamp
		}
	}
	c.loaded = true
	return nil
}

func (s *logsStore) getEvents(ctx context.Context, groupName, streamName string) ([]LogEvent, *protocol.AWSError) {
	key := s.streamCacheKey(ctx, groupName, streamName)
	c := s.loadStreamCache(key)
	c.mu.Lock()
	defer c.mu.Unlock()
	if aerr := s.ensureLoaded(ctx, c, key); aerr != nil {
		return nil, aerr
	}
	if len(c.events) == 0 {
		return []LogEvent{}, nil
	}
	// Return a copy so callers can sort/slice without mutating the cache.
	out := make([]LogEvent, len(c.events))
	copy(out, c.events)
	return out, nil
}

func (s *logsStore) appendEvents(ctx context.Context, groupName, streamName string, newEvents []LogEvent) *protocol.AWSError {
	if len(newEvents) == 0 {
		return nil
	}
	key := s.streamCacheKey(ctx, groupName, streamName)
	c := s.loadStreamCache(key)
	c.mu.Lock()
	if aerr := s.ensureLoaded(ctx, c, key); aerr != nil {
		c.mu.Unlock()
		return aerr
	}

	// Fast path: if every incoming event is at or after the current tail,
	// append without re-sorting (the common case — Lambda's log batcher
	// emits events in monotonically-non-decreasing order).
	monotonic := true
	for _, e := range newEvents {
		if e.Timestamp < c.lastTS {
			monotonic = false
			break
		}
		c.lastTS = e.Timestamp
	}
	c.events = append(c.events, newEvents...)
	if !monotonic {
		sort.SliceStable(c.events, func(i, j int) bool { return c.events[i].Timestamp < c.events[j].Timestamp })
		if n := len(c.events); n > 0 {
			c.lastTS = c.events[n-1].Timestamp
		}
	}
	c.dirty = true

	// If a watermark of un-flushed events has accumulated, flush now under
	// the lock instead of waiting for the debounce — bounds the worst-case
	// data loss window on hard crash and bounds peak memory if a stream is
	// extremely chatty. The watermark is intentionally generous so the
	// common case stays on the debounce path.
	const flushWatermark = 1024
	if len(c.events) >= flushWatermark && c.dirty {
		// Use the request ctx for the inline flush so failures propagate.
		aerr := s.flushLocked(ctx, c, key)
		c.mu.Unlock()
		return aerr
	}

	// Schedule a debounced flush if one isn't already pending.
	if !c.flushScheduled && !s.stopped.Load() {
		c.flushScheduled = true
		s.flushWG.Add(1)
		go s.debouncedFlush(c, key)
	}
	c.mu.Unlock()
	return nil
}

// flushDebounceInterval is the quiet period after the last append during which
// the cache may accumulate further appends before being persisted to
// state.Store. Tuned so a typical Lambda invocation (which produces a burst
// of log lines in <50 ms) results in a single coalesced write.
var flushDebounceInterval = 50 * time.Millisecond

// debouncedFlush runs in its own goroutine; it sleeps for flushDebounceInterval
// and then performs a single write of the cache's current contents. Subsequent
// appends during the sleep window do NOT spawn additional goroutines (gated by
// flushScheduled), so flush rate is bounded at 1 per debounce interval per
// stream.
func (s *logsStore) debouncedFlush(c *streamCache, key string) {
	defer s.flushWG.Done()
	select {
	case <-time.After(flushDebounceInterval):
	case <-s.flushBg.Done():
		// Stop was called; do a final flush now using a fresh ctx.
		c.mu.Lock()
		_ = s.flushLocked(context.Background(), c, key)
		c.flushScheduled = false
		c.mu.Unlock()
		return
	}
	c.mu.Lock()
	_ = s.flushLocked(s.flushBg, c, key)
	c.flushScheduled = false
	c.mu.Unlock()
}

// flushLocked persists the cache's current events slice to state.Store and
// clears the dirty flag. Caller must hold c.mu. No-op when the cache is not
// dirty (e.g. another flush already happened).
func (s *logsStore) flushLocked(ctx context.Context, c *streamCache, key string) *protocol.AWSError {
	if !c.dirty {
		return nil
	}
	raw, err := json.Marshal(storedEvents{Events: c.events})
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	// Use a non-cancelled context for store writes triggered by debounce
	// completion (the request ctx that originally produced the events may
	// already be done — we still want to persist).
	writeCtx := ctx
	if writeCtx == nil || writeCtx.Err() != nil {
		writeCtx = context.Background()
	}
	if err := s.store.Set(writeCtx, nsEvents, key, string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	c.dirty = false
	return nil
}

// Stop synchronously flushes every dirty stream cache. Called by the service
// during graceful shutdown so no in-memory log events are lost. After Stop
// returns, further appends are still served (cache continues to function),
// but they fall back to inline write-through because debounce goroutines are
// no longer scheduled.
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
	s.streamCaches.Range(func(k, v any) bool {
		c := v.(*streamCache)
		c.mu.Lock()
		if c.dirty {
			_ = s.flushLocked(ctx, c, k.(string))
		}
		c.mu.Unlock()
		return true
	})
}

// invalidateStreamCache drops the in-memory cache for a stream so a subsequent
// access reloads from state.Store. Called on stream/group deletion.
func (s *logsStore) invalidateStreamCache(key string) {
	s.streamCaches.Delete(key)
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
