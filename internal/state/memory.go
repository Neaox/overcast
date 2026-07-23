package state

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/tidwall/btree"
)

// MemoryStore is the default Store implementation.
// All data lives in-process and is lost when the process exits.
// This is the right default for local development: zero configuration,
// instant startup, deterministic test state.
//
// Performance characteristics:
//   - Get/Set/Delete: O(log n) per namespace, protected by RWMutex
//   - List/Scan: O(log n + m) prefix scan via btree (m = matching keys)
//   - RWMutex allows many concurrent readers OR one exclusive writer
//
// Memory note: MemoryStore does not enforce size limits. For local dev and CI
// the footprint is naturally bounded by test duration. Overcast is not designed
// for production use where unbounded growth would be a concern.
type MemoryStore struct {
	mu   sync.RWMutex
	data map[string]*btree.Map[string, string] // namespace → sorted key-value map
}

// NewMemoryStore returns an initialised MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		data: make(map[string]*btree.Map[string, string], 16),
	}
}

func (s *MemoryStore) Get(_ context.Context, namespace, key string) (string, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tree, ok := s.data[namespace]
	if !ok {
		return "", false, nil
	}
	v, ok := tree.Get(key)
	return v, ok, nil
}

func (s *MemoryStore) Set(_ context.Context, namespace, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tree := s.data[namespace]
	if tree == nil {
		tree = &btree.Map[string, string]{}
		s.data[namespace] = tree
	}
	tree.Set(key, value)
	return nil
}

func (s *MemoryStore) Delete(_ context.Context, namespace, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if tree, ok := s.data[namespace]; ok {
		tree.Delete(key)
	}
	return nil
}

// DeletePrefix removes all keys with prefix under a single write lock.
func (s *MemoryStore) DeletePrefix(_ context.Context, namespace, prefix string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tree, ok := s.data[namespace]
	if !ok {
		return nil
	}
	var keys []string
	tree.Ascend(prefix, func(key string, _ string) bool {
		if !strings.HasPrefix(key, prefix) {
			return false
		}
		keys = append(keys, key)
		return true
	})
	for _, key := range keys {
		tree.Delete(key)
	}
	return nil
}

func (s *MemoryStore) List(_ context.Context, namespace, prefix string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tree, ok := s.data[namespace]
	if !ok {
		return []string{}, nil
	}

	var keys []string
	tree.Ascend(prefix, func(key string, _ string) bool {
		if !strings.HasPrefix(key, prefix) {
			return false
		}
		keys = append(keys, key)
		return true
	})
	if keys == nil {
		return []string{}, nil // always a slice, never nil
	}
	return keys, nil
}

func (s *MemoryStore) ListNamespaces(_ context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	namespaces := make([]string, 0, len(s.data))
	for namespace, tree := range s.data {
		if tree != nil && tree.Len() > 0 {
			namespaces = append(namespaces, namespace)
		}
	}
	sort.Strings(namespaces)
	if namespaces == nil {
		return []string{}, nil
	}
	return namespaces, nil
}

// Scan returns all key-value pairs whose keys start with prefix, under a single
// RLock. This is the preferred way to read a batch of items — it avoids N
// individual Get calls each acquiring their own lock.
func (s *MemoryStore) Scan(_ context.Context, namespace, prefix string) ([]KV, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tree, ok := s.data[namespace]
	if !ok {
		return []KV{}, nil
	}

	var pairs []KV
	tree.Ascend(prefix, func(key string, value string) bool {
		if !strings.HasPrefix(key, prefix) {
			return false
		}
		pairs = append(pairs, KV{Key: key, Value: value})
		return true
	})
	if pairs == nil {
		return []KV{}, nil
	}
	return pairs, nil
}

func (s *MemoryStore) Close() error {
	return nil // nothing to release
}

// Reset wipes all stored data atomically.
func (s *MemoryStore) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = make(map[string]*btree.Map[string, string], 16)
}

// Len returns the number of entries. Used by /_debug/state and tests.
func (s *MemoryStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := 0
	for _, tree := range s.data {
		total += tree.Len()
	}
	return total
}

// storeKey builds the composite map key. Uses a null byte separator because
// AWS resource names are always printable ASCII/UTF-8.
// Still used by HybridStore for its dirty map.
func storeKey(namespace, key string) string {
	return namespace + "\x00" + key
}

// splitStoreKey is the inverse of storeKey: given "namespace\x00key" it
// returns ("namespace", "key"). Callers must only pass values produced by
// storeKey — the separator is always present.
func splitStoreKey(composite string) (namespace, key string) {
	i := strings.IndexByte(composite, '\x00')
	if i < 0 {
		// Should never happen — splitStoreKey is only called with keys we created.
		return composite, ""
	}
	return composite[:i], composite[i+1:]
}
