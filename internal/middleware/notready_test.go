package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Neaox/overcast/internal/state"
)

// fakeNotReadyStore is a minimal state.Store + state.NotReadyReporter double
// with a directly settable NotReady() return value, so NotReady middleware
// tests don't depend on real SQLite migration timing. Every Store method
// besides NotReady is unused by this middleware and left as a zero-value
// stub.
type fakeNotReadyStore struct {
	notReady bool
}

func (f *fakeNotReadyStore) NotReady() bool { return f.notReady }

func (f *fakeNotReadyStore) Get(context.Context, string, string) (string, bool, error) {
	return "", false, nil
}
func (f *fakeNotReadyStore) Set(context.Context, string, string, string) error { return nil }
func (f *fakeNotReadyStore) Delete(context.Context, string, string) error      { return nil }
func (f *fakeNotReadyStore) List(context.Context, string, string) ([]string, error) {
	return nil, nil
}
func (f *fakeNotReadyStore) ListNamespaces(context.Context) ([]string, error) { return nil, nil }
func (f *fakeNotReadyStore) Scan(context.Context, string, string) ([]state.KV, error) {
	return nil, nil
}
func (f *fakeNotReadyStore) ScanPage(context.Context, string, string, string, int) ([]state.KV, string, error) {
	return nil, "", nil
}
func (f *fakeNotReadyStore) Close() error { return nil }

var _ state.Store = (*fakeNotReadyStore)(nil)
var _ state.NotReadyReporter = (*fakeNotReadyStore)(nil)

func passThroughHandler() (http.Handler, *bool) {
	called := false
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}), &called
}

func TestNotReady_MigratingRequest_S3ReturnsXML503(t *testing.T) {
	store := &fakeNotReadyStore{notReady: true}
	next, called := passThroughHandler()
	handler := NotReady(store)(next)

	req := httptest.NewRequest(http.MethodPut, "/my-bucket/my-key", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if *called {
		t.Fatal("expected the wrapped handler not to run while the store is not ready")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/xml" {
		t.Fatalf("Content-Type = %q, want application/xml (S3 XML error format)", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "ServiceUnavailable") {
		t.Fatalf("body missing ServiceUnavailable code: %s", body)
	}
	if got := rec.Header().Get("Retry-After"); got == "" {
		t.Fatal("expected a Retry-After header")
	}
}

func TestNotReady_MigratingRequest_JSONServiceReturnsJSON503(t *testing.T) {
	store := &fakeNotReadyStore{notReady: true}
	next, called := passThroughHandler()
	handler := NotReady(store)(next)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Amz-Target", "AmazonSQS.SendMessage")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if *called {
		t.Fatal("expected the wrapped handler not to run while the store is not ready")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/x-amz-json-1.0" {
		t.Fatalf("Content-Type = %q, want application/x-amz-json-1.0 (JSON error format)", ct)
	}
	if !strings.Contains(rec.Body.String(), "ServiceUnavailable") {
		t.Fatalf("body missing ServiceUnavailable code: %s", rec.Body.String())
	}
}

func TestNotReady_InternalPathsAreExempt(t *testing.T) {
	store := &fakeNotReadyStore{notReady: true}
	for _, path := range []string{"/_debug/state", "/_health", "/_/info", "/_overcast/init"} {
		t.Run(path, func(t *testing.T) {
			next, called := passThroughHandler()
			handler := NotReady(store)(next)

			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if !*called {
				t.Fatalf("expected %s to bypass the not-ready check even while the store is not ready", path)
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (handler ran and wrote it)", rec.Code)
			}
		})
	}
}

func TestNotReady_ReadyStore_PassesThrough(t *testing.T) {
	store := &fakeNotReadyStore{notReady: false}
	next, called := passThroughHandler()
	handler := NotReady(store)(next)

	req := httptest.NewRequest(http.MethodGet, "/my-bucket", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !*called {
		t.Fatal("expected the wrapped handler to run once the store reports ready")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestNotReady_StoreWithoutNotReadyReporter_AlwaysPassesThrough(t *testing.T) {
	store := state.NewMemoryStore() // does not implement state.NotReadyReporter
	next, called := passThroughHandler()
	handler := NotReady(store)(next)

	req := httptest.NewRequest(http.MethodGet, "/my-bucket", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !*called {
		t.Fatal("expected the wrapped handler to run for a store that never has a not-ready window")
	}
}
