// Package state defines the Store interface and provides four implementations:
//
//   - MemoryStore  — in-process maps; lost on restart; fastest; best for tests & CI.
//   - SQLiteStore  — synchronous SQLite writes (persistent mode).
//   - WALStore     — memory reads + append-log durability with replay on startup.
//   - HybridStore  — memory reads + async SQLite flush; default for local development.
//   - NamespacedStore — dispatcher that routes operations to per-service stores.
//
// All service handlers receive a Store — they never know or care which
// implementation is backing them. This is Go's equivalent of programming to
// an interface rather than a concrete type.
//
// TypeScript analogy:
//
//	interface Store {
//	  get(namespace: string, key: string): Promise<string | null>
//	  set(namespace: string, key: string, value: string): Promise<void>
//	  delete(namespace: string, key: string): Promise<void>
//	  list(namespace: string, prefix: string): Promise<string[]>
//	}
package state

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"time"
)

// KV is a key-value pair returned by Scan.
type KV struct {
	Key   string
	Value string
}

// SQLiteDBProvider is implemented by stores backed by SQLite (SQLiteStore,
// HybridStore). Services that need dedicated tables (e.g. DynamoDB items)
// can type-assert a Store to this interface to get direct DB access.
type SQLiteDBProvider interface {
	DB() *sql.DB
}

// ReadyAwaiter is implemented by stores that have an asynchronous
// initialisation phase (e.g. HybridStore, which opens SQLite in the
// background). Callers that need to guarantee all persisted data is
// visible before reading — such as startup reload routines — should
// type-assert the Store to ReadyAwaiter and wait before scanning.
//
// Stores that are always immediately ready (MemoryStore, WALStore)
// do not need to implement this interface; callers must treat its absence
// as "already ready".
type ReadyAwaiter interface {
	// WaitReady blocks until the store's background initialisation is
	// complete or ctx is cancelled. Returns nil when the store is ready.
	WaitReady(ctx context.Context) error
}

// NotReadyReporter is implemented by stores with a distinguishable
// "still completing one-time startup work" state — currently, an in-progress
// schema migration (see internal/state/migrate.go). Unlike ReadyAwaiter,
// this is a non-blocking check: middleware.NotReady uses it once per request
// to return a proper "service unavailable, retry" response instead of
// letting the request observe whatever the store would otherwise do during
// this window — HybridStore's TierHot reads silently returning empty because
// the seed hasn't started yet, or SQLiteStore blocking the request
// indefinitely inside ensureReady.
//
// Once a store's one-time startup work finishes (successfully, or by
// degrading — see HybridStore's degradeToMemoryOnly), NotReady must return
// false for the rest of the process's life; it reports a startup phase, not
// an ongoing health condition — PersistentHealthReporter is the interface
// for that.
//
// Stores that never have this kind of startup phase (MemoryStore, WALStore)
// do not need to implement this interface; callers must treat its absence
// as "already ready", the same convention ReadyAwaiter uses.
type NotReadyReporter interface {
	// NotReady reports, without blocking, whether the store is still
	// completing one-time startup work.
	NotReady() bool
}

// Store is the single interface all service state flows through.
// Implementations must be safe for concurrent use from multiple goroutines.
//
// The namespace parameter segments keys by service (e.g. "s3", "sqs") so that
// different services can use the same key names without collision.
type Store interface {
	// Get retrieves a value by namespace+key.
	// Returns ("", false, nil) if the key does not exist.
	Get(ctx context.Context, namespace, key string) (value string, found bool, err error)

	// Set stores a value. Overwrites any existing value for the same key.
	Set(ctx context.Context, namespace, key, value string) error

	// Delete removes a key. Returns nil (not an error) if the key does not exist.
	Delete(ctx context.Context, namespace, key string) error

	// List returns all keys in namespace whose names start with prefix.
	// Returns an empty slice (not nil) when no keys match.
	List(ctx context.Context, namespace, prefix string) (keys []string, err error)

	// ListNamespaces returns all namespaces that currently contain at least one
	// key. Returns an empty slice (not nil) when the store is empty.
	ListNamespaces(ctx context.Context) (namespaces []string, err error)

	// Scan returns all key-value pairs in namespace whose keys start with prefix,
	// in a single atomic read. Prefer Scan over List+Get when you need both keys
	// and values — it avoids N individual Get calls and holds the lock only once.
	// Returns an empty slice (not nil) when no keys match.
	Scan(ctx context.Context, namespace, prefix string) ([]KV, error)

	// ScanPage returns up to limit key-value pairs in namespace whose keys
	// start with prefix, in key order, starting strictly after startAfter —
	// a paginated variant of Scan for namespaces too large to return in one
	// response (e.g. sqs:messages, logs:events). Pass startAfter == "" for
	// the first page.
	//
	// nextKey is the startAfter value to pass for the next page, or "" when
	// this page reached the end of the prefix range (no more results).
	// limit <= 0 means "no limit" — behaves exactly like Scan(ctx, namespace,
	// prefix) with nextKey always "". This keeps ScanPage a strict superset
	// of Scan (a caller can always pass limit 0 and get Scan's behavior)
	// rather than making an unbounded request an error, matching this
	// package's general preference for permissive zero-value defaults (see
	// e.g. HybridOptions' zero-valued fields falling back to documented
	// defaults) over forcing every caller to think about a limit.
	//
	// Returns an empty slice (not nil) when no keys match.
	ScanPage(ctx context.Context, namespace, prefix, startAfter string, limit int) (page []KV, nextKey string, err error)

	// Close releases any resources held by the store (file handles, DB connections).
	// Called once on graceful shutdown.
	Close() error
}

// PrefixDeleter is an optional Store extension for deleting a key range without
// first reading values. Callers should type-assert this for large purges.
type PrefixDeleter interface {
	DeletePrefix(ctx context.Context, namespace, prefix string) error
}

// Flushable is an optional Store extension for backends that buffer writes.
// Flush blocks until all writes accepted before the call are persisted or an
// error proves the persistent backend is currently unavailable.
type Flushable interface {
	Flush(ctx context.Context) error
}

// PersistentHealthReporter is an optional Store extension for exposing live
// persistent-backend health without forcing all Store implementations to carry
// storage-specific fields.
type PersistentHealthReporter interface {
	PersistentHealth() PersistentHealth
}

// PersistentHealth is a small, JSON-friendly snapshot of persistent backend
// status. LastError is intentionally text: callers should not branch on it.
type PersistentHealth struct {
	Mode          string    `json:"mode"`
	Healthy       bool      `json:"healthy"`
	PendingWrites int       `json:"pendingWrites"`
	LastError     string    `json:"lastError,omitempty"`
	LastErrorAt   time.Time `json:"lastErrorAt,omitempty"`
	LastSuccessAt time.Time `json:"lastSuccessAt,omitempty"`
}

// Flush persists buffered writes when the store supports it. Stores without a
// buffered durability layer are already current, so this is a no-op.
func Flush(ctx context.Context, s Store) error {
	flusher, ok := s.(Flushable)
	if !ok {
		return nil
	}
	return flusher.Flush(ctx)
}

// PersistentHealthSnapshot returns persistent backend health when the store has
// one. The boolean is false for memory-only or otherwise non-reporting stores.
//
// A *NamespacedStore does not itself implement PersistentHealthReporter — a
// direct type assertion against it would silently report "no persistent
// health" for every service whenever any unrelated OVERCAST_STATE_<SVC>
// override is configured, even though the underlying per-service stores do
// have real health to report (the same erasure class Unwrap exists to guard
// against). Since this function's callers (the health endpoint, shutdown
// logging) want one aggregate view rather than a specific service's, it
// unwraps a NamespacedStore into its distinct underlying stores and combines
// their reports instead of delegating to Unwrap.
func PersistentHealthSnapshot(s Store) (PersistentHealth, bool) {
	if ns, ok := s.(*NamespacedStore); ok {
		return aggregatePersistentHealth(ns.UnderlyingStores())
	}
	reporter, ok := s.(PersistentHealthReporter)
	if !ok {
		return PersistentHealth{}, false
	}
	return reporter.PersistentHealth(), true
}

// DebugMetricsOptions controls which (potentially expensive) fields
// DebugMetricsReporter.DebugMetrics computes.
type DebugMetricsOptions struct {
	// IncludeNamespaceRowCounts additionally populates
	// DebugMetrics.NamespaceRowCounts. For TierCached namespaces this issues
	// one SQL COUNT(*) per namespace currently known to the store — cheap
	// enough for an on-demand debug call, but not something to compute
	// unconditionally on every /_debug/metrics hit, so callers opt in.
	IncludeNamespaceRowCounts bool
}

// DebugFlushRecord is one entry in DebugMetrics.FlushHistory: a single
// attempt to persist buffered writes, whether or not it succeeded.
type DebugFlushRecord struct {
	Timestamp      time.Time `json:"timestamp"`
	DurationMillis int64     `json:"durationMillis"`
	Entries        int       `json:"entries"`
	Committed      bool      `json:"committed"`
}

// DebugMetrics is a snapshot of storage-layer diagnostics for
// GET /_debug/metrics (storage-plan.md item 3.6): recent flush history, the
// one-time TierHot seed duration, the pending write-ahead log's on-disk
// size, and — only when DebugMetricsOptions.IncludeNamespaceRowCounts is
// set — per-namespace row counts.
type DebugMetrics struct {
	// Mode identifies which backend produced this snapshot (e.g. "hybrid",
	// "persistent"), matching config.Config.State's values.
	Mode string `json:"mode"`

	// FlushHistory holds the most recent flush attempts, oldest first,
	// bounded to a small ring buffer. Empty for backends that write
	// synchronously and never batch/flush (e.g. SQLiteStore).
	FlushHistory []DebugFlushRecord `json:"flushHistory,omitempty"`

	// SeedDurationMillis is how long the background TierHot seed took to
	// complete, in milliseconds, or nil if seeding hasn't finished yet, is
	// still in flight, degraded to memory-only before finishing (see
	// HybridStore.degradeToMemoryOnly — there is no coherent "seed
	// duration" for a seed that didn't complete), or never happens for this
	// backend at all.
	SeedDurationMillis *int64 `json:"seedDurationMillis,omitempty"`

	// PendingLogBytes is the current on-disk size of the not-yet-flushed
	// write-ahead log, in bytes. 0 for backends without one.
	PendingLogBytes int64 `json:"pendingLogBytes,omitempty"`

	// NamespaceRowCounts maps namespace -> row count, populated only when
	// DebugMetricsOptions.IncludeNamespaceRowCounts was set.
	NamespaceRowCounts map[string]int `json:"namespaceRowCounts,omitempty"`
}

// DebugMetricsReporter is an optional Store extension exposing the
// diagnostics in DebugMetrics. Stores without an async write path or a
// one-time startup seed (MemoryStore, WALStore) do not implement it; callers
// must treat its absence as "nothing to report", the same convention
// PersistentHealthReporter and NotReadyReporter use.
type DebugMetricsReporter interface {
	DebugMetrics(ctx context.Context, opts DebugMetricsOptions) DebugMetrics
}

// DebugMetricsSnapshot returns one DebugMetrics entry per distinct
// underlying store that implements DebugMetricsReporter. The returned bool
// is false only when no underlying store reports anything (e.g. store is a
// MemoryStore/WALStore, or a NamespacedStore wrapping only those).
//
// Deliberately does NOT follow PersistentHealthSnapshot's merge-into-one
// approach for a *NamespacedStore: PersistentHealth's fields combine
// sensibly across backends (Healthy is a meaningful AND, PendingWrites is a
// meaningful sum), but DebugMetrics's fields do not — merging two distinct
// stores' flush-history ring buffers into one timeline, or averaging two
// unrelated seed durations, would produce a number that doesn't correspond
// to anything real. So instead of one merged snapshot, this returns one
// snapshot per distinct underlying store and lets the caller (the
// /_debug/metrics handler) render them as a list. It also doesn't follow
// NotReadyReporter's direct-implementation-on-NamespacedStore approach,
// since NotReady's single boolean OR *is* meaningful to compute directly on
// the wrapper, whereas "the" DebugMetrics of a NamespacedStore isn't a
// single value at all once more than one distinct backend is in play.
func DebugMetricsSnapshot(ctx context.Context, store Store, opts DebugMetricsOptions) ([]DebugMetrics, bool) {
	if ns, ok := store.(*NamespacedStore); ok {
		var all []DebugMetrics
		for _, st := range ns.UnderlyingStores() {
			if reporter, ok := st.(DebugMetricsReporter); ok {
				all = append(all, reporter.DebugMetrics(ctx, opts))
			}
		}
		return all, len(all) > 0
	}
	reporter, ok := store.(DebugMetricsReporter)
	if !ok {
		return nil, false
	}
	return []DebugMetrics{reporter.DebugMetrics(ctx, opts)}, true
}

// aggregatePersistentHealth combines the PersistentHealth of every reporting
// store into one snapshot: PendingWrites sums, Healthy is the AND of all
// reporting stores, LastError/LastErrorAt come from whichever unhealthy store
// errored most recently, LastSuccessAt is the most recent across all stores,
// and Mode lists every distinct backend mode present (e.g. "hybrid+memory"
// when a per-service override mixes backends). The boolean is false only when
// none of the stores report health at all (e.g. every backend is memory-only).
func aggregatePersistentHealth(stores []Store) (PersistentHealth, bool) {
	var agg PersistentHealth
	agg.Healthy = true
	found := false
	modes := make(map[string]bool, len(stores))
	var modeList []string
	for _, st := range stores {
		reporter, ok := st.(PersistentHealthReporter)
		if !ok {
			continue
		}
		found = true
		h := reporter.PersistentHealth()
		agg.PendingWrites += h.PendingWrites
		if !h.Healthy {
			agg.Healthy = false
			if h.LastErrorAt.After(agg.LastErrorAt) {
				agg.LastError = h.LastError
				agg.LastErrorAt = h.LastErrorAt
			}
		}
		if h.LastSuccessAt.After(agg.LastSuccessAt) {
			agg.LastSuccessAt = h.LastSuccessAt
		}
		if h.Mode != "" && !modes[h.Mode] {
			modes[h.Mode] = true
			modeList = append(modeList, h.Mode)
		}
	}
	if !found {
		return PersistentHealth{}, false
	}
	sort.Strings(modeList)
	agg.Mode = strings.Join(modeList, "+")
	return agg, true
}
