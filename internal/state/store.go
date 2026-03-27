// Package state defines the Store interface and provides two implementations:
// MemoryStore (default, in-process maps) and SQLiteStore (persistent).
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

import "context"

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

	// Close releases any resources held by the store (file handles, DB connections).
	// Called once on graceful shutdown.
	Close() error
}
