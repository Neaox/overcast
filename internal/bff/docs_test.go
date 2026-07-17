package bff

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestDocsPage_validPath(t *testing.T) {
	// Given: a docs filesystem rooted at docs/.
	docsFS := fstest.MapFS{
		"cdk/local-vpc.md": &fstest.MapFile{Data: []byte("# Local VPCs for CDK\n")},
	}
	handler := NewHandler(nil, docsFS, UIConfig{})

	// When: we request a safe docs page path.
	req := httptest.NewRequest(http.MethodGet, "/api/docs/page?path=cdk%2Flocal-vpc.md", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Then: the markdown content is returned.
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/markdown; charset=utf-8" {
		t.Fatalf("expected markdown content-type, got %q", got)
	}
	if rec.Body.String() != "# Local VPCs for CDK\n" {
		t.Fatalf("unexpected body: %q", rec.Body.String())
	}
}

func TestDocsPage_pathTraversal(t *testing.T) {
	// Given: a docs filesystem.
	handler := NewHandler(nil, fstest.MapFS{}, UIConfig{})

	// When: a path traversal request is made.
	req := httptest.NewRequest(http.MethodGet, "/api/docs/page?path=..%2FREADME.md", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Then: it is rejected.
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDocsPage_rejectsDeveloperPlans(t *testing.T) {
	// Given: a docs filesystem that contains developer-only planning notes.
	docsFS := fstest.MapFS{
		"plans/mcp.md": &fstest.MapFile{Data: []byte("# MCP plan\n")},
	}
	handler := NewHandler(nil, docsFS, UIConfig{})

	// When: a plan document is requested directly.
	req := httptest.NewRequest(http.MethodGet, "/api/docs/page?path=plans%2Fmcp.md", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Then: developer-only plans are not served through the docs browser API.
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDocsService_usesServicesSubdirectory(t *testing.T) {
	// Given: the BFF docs filesystem is rooted at docs/.
	docsFS := fstest.MapFS{
		"services/s3.md": &fstest.MapFile{Data: []byte("# S3\n")},
	}
	handler := NewHandler(nil, docsFS, UIConfig{})

	// When: the legacy service docs endpoint is requested.
	req := httptest.NewRequest(http.MethodGet, "/api/docs/s3", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Then: it still returns docs/services/{service}.md.
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "# S3\n" {
		t.Fatalf("unexpected body: %q", rec.Body.String())
	}
}

var _ fs.FS = fstest.MapFS{}
