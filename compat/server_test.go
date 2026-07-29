package compat

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// newTestUIFS writes a minimal dashboard build to a temp dir and returns it as
// a filesystem, standing in for compat/ui/dist.
func newTestUIFS(t *testing.T) *os.Root {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>dashboard"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("open root: %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root
}

func TestHandlerServesUIAlongsideMCP(t *testing.T) {
	// Given: a server with both a dashboard UI and an orchestrator, which is
	// what --serve --interactive produces once the UI is not suppressed
	srv := NewServer(newTestUIFS(t).FS())
	orch := NewOrchestrator(context.Background(), nil, func([]byte) {}, slog.New(slog.DiscardHandler))
	srv.SetOrchestrator(orch)

	// When: the handler is built
	// Then: registration does not panic. "GET /" and "/mcp/" are a ServeMux
	// registration conflict — more specific in method, less specific in path —
	// so the UI route must be registered as plain "/".
	var handler http.Handler
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Handler panicked with a UI and an orchestrator: %v", r)
			}
		}()
		handler = srv.Handler()
	}()

	// And: the UI is actually served at /
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("GET / = %d, want 200", rec.Code)
	}

	// And: /mcp/ still reaches the MCP server rather than the file server,
	// which would 404 on it.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/mcp/", nil))
	if rec.Code == http.StatusNotFound {
		t.Errorf("GET /mcp/ = 404 — the file server claimed the MCP route")
	}
}

func TestHandlerWithoutUIStillRoutes(t *testing.T) {
	// Given: a server started with --no-ui (an external Vite serves the UI)
	srv := NewServer(nil)
	orch := NewOrchestrator(context.Background(), nil, func([]byte) {}, slog.New(slog.DiscardHandler))
	srv.SetOrchestrator(orch)

	// When: a request arrives for an API route
	// Then: it is served, and no UI route was registered
	handler := srv.Handler()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/suites", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("GET /suites = %d, want 200", rec.Code)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET / = %d, want 404 when no UI is configured", rec.Code)
	}
}
