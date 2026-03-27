package helpers

import (
	"context"
	"strings"
	"sync"
)

// MockStore is a hand-written test double for state.Store.
// Use it in unit tests that need to isolate a component from real storage.
//
// It records all calls so tests can assert on what was called, in what order,
// with what arguments.
//
// Example:
//
//	mock := helpers.NewMockStore()
//	mock.SetData("s3:buckets", "my-bucket", `{"name":"my-bucket"}`)
//	// ... inject mock into component under test ...
//	if len(mock.SetCalls) != 1 { t.Error("expected one Set call") }
type MockStore struct {
	mu   sync.RWMutex
	data map[string]string // "namespace\x00key" → value

	// Recorded calls — inspect these in tests to assert behaviour.
	GetCalls    []StoreCall
	SetCalls    []StoreCall
	DeleteCalls []StoreCall
	ListCalls   []StoreCall

	// Errors to inject — set these to simulate failures.
	GetError    error
	SetError    error
	DeleteError error
	ListError   error
}

// StoreCall records a single call to a store method.
type StoreCall struct {
	Namespace string
	Key       string
	Value     string // only set for Set calls
	Prefix    string // only set for List calls
}

// NewMockStore returns an initialised MockStore.
func NewMockStore() *MockStore {
	return &MockStore{data: make(map[string]string)}
}

// SetData pre-populates the mock with a value, without recording a SetCall.
// Use this in the Given section of tests to set up initial state.
func (m *MockStore) SetData(namespace, key, value string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[namespace+"\x00"+key] = value
}

func (m *MockStore) Get(_ context.Context, namespace, key string) (string, bool, error) {
	m.mu.Lock()
	m.GetCalls = append(m.GetCalls, StoreCall{Namespace: namespace, Key: key})
	m.mu.Unlock()

	if m.GetError != nil {
		return "", false, m.GetError
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.data[namespace+"\x00"+key]
	return v, ok, nil
}

func (m *MockStore) Set(_ context.Context, namespace, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SetCalls = append(m.SetCalls, StoreCall{Namespace: namespace, Key: key, Value: value})
	if m.SetError != nil {
		return m.SetError
	}
	m.data[namespace+"\x00"+key] = value
	return nil
}

func (m *MockStore) Delete(_ context.Context, namespace, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.DeleteCalls = append(m.DeleteCalls, StoreCall{Namespace: namespace, Key: key})
	if m.DeleteError != nil {
		return m.DeleteError
	}
	delete(m.data, namespace+"\x00"+key)
	return nil
}

func (m *MockStore) List(_ context.Context, namespace, prefix string) ([]string, error) {
	m.mu.Lock()
	m.ListCalls = append(m.ListCalls, StoreCall{Namespace: namespace, Prefix: prefix})
	m.mu.Unlock()

	if m.ListError != nil {
		return nil, m.ListError
	}

	nsPrefix := namespace + "\x00"
	fullPrefix := nsPrefix + prefix

	m.mu.RLock()
	defer m.mu.RUnlock()

	var keys []string
	for k := range m.data {
		if strings.HasPrefix(k, fullPrefix) {
			keys = append(keys, strings.TrimPrefix(k, nsPrefix))
		}
	}
	if keys == nil {
		keys = []string{}
	}
	return keys, nil
}

func (m *MockStore) Close() error { return nil }

// Reset clears all stored data and recorded calls.
func (m *MockStore) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data = make(map[string]string)
	m.GetCalls = nil
	m.SetCalls = nil
	m.DeleteCalls = nil
	m.ListCalls = nil
}
