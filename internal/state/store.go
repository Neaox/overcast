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
// Stores that are always immediately ready (MemoryStore, SQLiteStore)
// do not need to implement this interface; callers must treat its absence
// as "already ready".
type ReadyAwaiter interface {
	// WaitReady blocks until the store's background initialisation is
	// complete or ctx is cancelled. Returns nil when the store is ready.
	WaitReady(ctx context.Context) error
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
