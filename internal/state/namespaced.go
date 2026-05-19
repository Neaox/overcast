package state

import (
	"context"
	"strings"
)

// NamespacedStore routes Store operations to different backends based on the
// service prefix of the namespace argument (the segment before the first ":").
//
//	namespace "sqs:queues"   → service prefix "sqs"
//	namespace "s3:objects"   → service prefix "s3"
//
// Operations whose namespace has no matching override are forwarded to the
// default store. This allows per-service storage modes with a single Store
// interface visible to the rest of the codebase.
type NamespacedStore struct {
	defaultStore Store
	routes       map[string]Store // service prefix → store
}

// NewNamespacedStore creates a NamespacedStore. routes maps service prefixes
// (e.g. "s3", "sqs") to their dedicated Store. Services absent from routes
// use defaultStore.
func NewNamespacedStore(defaultStore Store, routes map[string]Store) *NamespacedStore {
	return &NamespacedStore{
		defaultStore: defaultStore,
		routes:       routes,
	}
}

// storeFor returns the store responsible for a given namespace.
func (s *NamespacedStore) storeFor(namespace string) Store {
	if i := strings.IndexByte(namespace, ':'); i > 0 {
		if st, ok := s.routes[namespace[:i]]; ok {
			return st
		}
	}
	return s.defaultStore
}

func (s *NamespacedStore) Get(ctx context.Context, namespace, key string) (string, bool, error) {
	return s.storeFor(namespace).Get(ctx, namespace, key)
}

func (s *NamespacedStore) Set(ctx context.Context, namespace, key, value string) error {
	return s.storeFor(namespace).Set(ctx, namespace, key, value)
}

func (s *NamespacedStore) Delete(ctx context.Context, namespace, key string) error {
	return s.storeFor(namespace).Delete(ctx, namespace, key)
}

func (s *NamespacedStore) DeletePrefix(ctx context.Context, namespace, prefix string) error {
	store := s.storeFor(namespace)
	if deleter, ok := store.(PrefixDeleter); ok {
		return deleter.DeletePrefix(ctx, namespace, prefix)
	}
	keys, err := store.List(ctx, namespace, prefix)
	if err != nil {
		return err
	}
	for _, key := range keys {
		if err := store.Delete(ctx, namespace, key); err != nil {
			return err
		}
	}
	return nil
}

func (s *NamespacedStore) List(ctx context.Context, namespace, prefix string) ([]string, error) {
	return s.storeFor(namespace).List(ctx, namespace, prefix)
}

func (s *NamespacedStore) Scan(ctx context.Context, namespace, prefix string) ([]KV, error) {
	return s.storeFor(namespace).Scan(ctx, namespace, prefix)
}

// Close closes the default store and all per-service stores. Each underlying
// store is closed exactly once even if it serves multiple namespace prefixes.
func (s *NamespacedStore) Close() error {
	seen := make(map[Store]bool, 1+len(s.routes))
	var first error

	closeOnce := func(st Store) {
		if seen[st] {
			return
		}
		seen[st] = true
		if err := st.Close(); err != nil && first == nil {
			first = err
		}
	}

	closeOnce(s.defaultStore)
	for _, st := range s.routes {
		closeOnce(st)
	}
	return first
}
