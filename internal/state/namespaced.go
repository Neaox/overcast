package state

import (
	"context"
	"sort"
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
		return s.StoreFor(namespace[:i])
	}
	return s.defaultStore
}

// StoreFor returns the store responsible for a given service prefix — the
// namespace segment before the colon (e.g. "dynamodb", "s3", "sqs"). Returns
// the default store when no override is registered for servicePrefix.
//
// Callers that need to type-assert a Store to an optional interface
// (state.SQLiteDBProvider, state.ReadyAwaiter, ...) should resolve through
// this method first — see Unwrap — rather than asserting directly against a
// possibly-wrapped Store, which silently erases the capability whenever any
// unrelated service has an OVERCAST_STATE_<SVC> override configured.
func (s *NamespacedStore) StoreFor(servicePrefix string) Store {
	if st, ok := s.routes[servicePrefix]; ok {
		return st
	}
	return s.defaultStore
}

// Unwrap returns the store that actually handles operations for the given
// service prefix. If store is a *NamespacedStore, it resolves to the routed
// store (or the default store when no override exists for servicePrefix).
// For any other Store, it returns store unchanged.
//
// Any consumer that type-asserts a Store to an optional interface
// (SQLiteDBProvider, ReadyAwaiter, PersistentHealthReporter, ...) MUST call
// Unwrap first with its own service prefix. Without it, wrapping the store in
// NamespacedStore — triggered by an unrelated OVERCAST_STATE_<SVC> override —
// silently erases the capability for every other service, because
// *NamespacedStore itself does not implement most optional interfaces.
func Unwrap(store Store, servicePrefix string) Store {
	if ns, ok := store.(*NamespacedStore); ok {
		return ns.StoreFor(servicePrefix)
	}
	return store
}

// UnderlyingStores returns the default store plus every distinct routed
// store, each exactly once even if the same Store instance is shared by
// multiple service prefixes (or is also the default store).
func (s *NamespacedStore) UnderlyingStores() []Store {
	seen := make(map[Store]bool, 1+len(s.routes))
	stores := make([]Store, 0, 1+len(s.routes))
	add := func(st Store) {
		if seen[st] {
			return
		}
		seen[st] = true
		stores = append(stores, st)
	}
	add(s.defaultStore)
	for _, st := range s.routes {
		add(st)
	}
	return stores
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

func (s *NamespacedStore) ListNamespaces(ctx context.Context) ([]string, error) {
	seen := map[string]bool{}
	var namespaces []string
	for _, st := range s.UnderlyingStores() {
		items, err := st.ListNamespaces(ctx)
		if err != nil {
			return nil, err
		}
		for _, namespace := range items {
			if seen[namespace] {
				continue
			}
			seen[namespace] = true
			namespaces = append(namespaces, namespace)
		}
	}
	sort.Strings(namespaces)
	if namespaces == nil {
		return []string{}, nil
	}
	return namespaces, nil
}

func (s *NamespacedStore) Scan(ctx context.Context, namespace, prefix string) ([]KV, error) {
	return s.storeFor(namespace).Scan(ctx, namespace, prefix)
}

func (s *NamespacedStore) ScanPage(ctx context.Context, namespace, prefix, startAfter string, limit int) ([]KV, string, error) {
	return s.storeFor(namespace).ScanPage(ctx, namespace, prefix, startAfter, limit)
}

// Close closes the default store and all per-service stores. Each underlying
// store is closed exactly once even if it serves multiple namespace prefixes.
func (s *NamespacedStore) Close() error {
	var first error
	for _, st := range s.UnderlyingStores() {
		if err := st.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// WaitReady implements ReadyAwaiter by waiting on every distinct underlying
// store that itself implements ReadyAwaiter. Stores that don't implement it
// are treated as already ready, per the ReadyAwaiter contract. Returns the
// first error encountered (including ctx cancellation), or nil once every
// underlying store that needed it is ready.
func (s *NamespacedStore) WaitReady(ctx context.Context) error {
	for _, st := range s.UnderlyingStores() {
		if awaiter, ok := st.(ReadyAwaiter); ok {
			if err := awaiter.WaitReady(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

// NotReady implements state.NotReadyReporter directly on *NamespacedStore
// itself — rather than via a package-level aggregator like
// PersistentHealthSnapshot's — so a caller that type-asserts a possibly-
// wrapped Store to NotReadyReporter (as middleware.NotReady does) sees the
// same interface-erasure protection WaitReady already established in
// Phase 1: any one underlying store still completing startup work makes the
// whole NamespacedStore not ready yet.
func (s *NamespacedStore) NotReady() bool {
	for _, st := range s.UnderlyingStores() {
		if nr, ok := st.(NotReadyReporter); ok && nr.NotReady() {
			return true
		}
	}
	return false
}
