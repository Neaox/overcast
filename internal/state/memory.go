package state

import (
	"context"
	"strings"
	"sync"
)

// MemoryStore is the default Store implementation.
// All data lives in-process and is lost when the process exits.
// This is the right default for local development: zero configuration,
// instant startup, deterministic test state.
//
// Performance characteristics:
//   - Get/Set/Delete: O(1) average, protected by RWMutex
//   - List: O(n) scan over all keys in the store
//   - RWMutex allows many concurrent readers OR one exclusive writer
//
// Memory note: MemoryStore does not enforce size limits. For local dev and CI
// the footprint is naturally bounded by test duration. Overcast is not designed
// for production use where unbounded growth would be a concern.
type MemoryStore struct {
	mu   sync.RWMutex
	data map[string]string // composite key: "namespace\x00key" → value
}

// NewMemoryStore returns an initialised MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		// Pre-allocate with modest capacity to avoid early rehashes.
		data: make(map[string]string, 64),
	}
}

func (s *MemoryStore) Get(_ context.Context, namespace, key string) (string, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	v, ok := s.data[storeKey(namespace, key)]
	return v, ok, nil
}

func (s *MemoryStore) Set(_ context.Context, namespace, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data[storeKey(namespace, key)] = value
	return nil
}

func (s *MemoryStore) Delete(_ context.Context, namespace, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.data, storeKey(namespace, key))
	return nil
}

func (s *MemoryStore) List(_ context.Context, namespace, prefix string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	fullPrefix := storeKey(namespace, prefix)
	nsPrefix := namespace + "\x00"

	var keys []string
	for k := range s.data {
		if strings.HasPrefix(k, fullPrefix) {
			keys = append(keys, strings.TrimPrefix(k, nsPrefix))
		}
	}
	if keys == nil {
		return []string{}, nil // always a slice, never nil
	}
	return keys, nil
}

func (s *MemoryStore) Close() error {
	return nil // nothing to release
}

// Reset wipes all stored data atomically.
// Allocating a new map lets the GC reclaim the old map in full —
// faster than ranging and deleting individual entries.
func (s *MemoryStore) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = make(map[string]string, 64)
}

// Len returns the number of entries. Used by /_debug/state and tests.
func (s *MemoryStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.data)
}

// storeKey builds the composite map key. Uses a null byte separator because
// AWS resource names are always printable ASCII/UTF-8.
func storeKey(namespace, key string) string {
	return namespace + "\x00" + key
}
