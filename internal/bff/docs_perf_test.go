//go:build !slim

package bff

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// BenchmarkDocsNavRequest is what every /api/docs/nav request after the first
// one costs. It exists because that used to be ~0.4ms and ~176 KB of garbage
// per request: the handler re-projected and re-encoded the same navigation
// every time, for a body that cannot change until the binary does. The
// navigation is now encoded once, so a request is a buffer write, and this
// benchmark is what would notice if that regressed.
func BenchmarkDocsNavRequest(b *testing.B) {
	handler := NewHandler(nil, publishedDocs(), UIConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/docs/nav", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req) // build the index first
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("got %d", rec.Code)
		}
	}
}

// BenchmarkDocsSearchRequest is one query against a warm index: postings
// lookups and a sort, with no parsing or scoring left to do.
func BenchmarkDocsSearchRequest(b *testing.B) {
	handler := NewHandler(nil, publishedDocs(), UIConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/docs/search?q=log+group+retention&limit=10", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("got %d", rec.Code)
		}
	}
}
