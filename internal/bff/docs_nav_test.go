//go:build !slim

package bff

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

type navResponse struct {
	Entries []struct {
		Path        string `json:"path"`
		Href        string `json:"href"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Section     string `json:"section"`
		Headings    []struct {
			Depth int    `json:"depth"`
			Text  string `json:"text"`
			ID    string `json:"id"`
		} `json:"headings"`
	} `json:"entries"`
}

func getNav(t *testing.T, handler http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/docs/nav", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestDocsNav_servesEveryPublishedDoc(t *testing.T) {
	// Given: the BFF serving the docs tree the binary embeds.
	handler := NewHandler(nil, publishedDocs(), UIConfig{})

	// When: the console asks for the docs navigation.
	rec := getNav(t, handler)

	// Then: it gets one entry per page, each usable as a sidebar link.
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body navResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Entries) == 0 {
		t.Fatal("expected navigation entries")
	}
	seen := map[string]bool{}
	for _, e := range body.Entries {
		switch {
		case e.Title == "":
			t.Errorf("%s: no title", e.Path)
		case e.Section == "":
			t.Errorf("%s: no section", e.Path)
		case e.Path != "docs/"+e.Href:
			t.Errorf("%s: href %q does not match path", e.Path, e.Href)
		case seen[e.Href]:
			t.Errorf("%s: listed twice", e.Href)
		}
		seen[e.Href] = true
	}
	// The README is the console's default page, so it has to be in the nav.
	if !seen["README.md"] {
		t.Error("README.md is missing from the navigation")
	}
}

// TestDocsNav_carriesHeadingsForTheTableOfContents pins the half of the nav the
// sidebar does not use: the "On this page" list links to heading ids, which the
// docs viewer recomputes client-side from the heading text (web/src/lib/slug.ts).
// If the two ever disagree the anchors silently go nowhere.
func TestDocsNav_carriesHeadingsForTheTableOfContents(t *testing.T) {
	// Given: the docs navigation.
	handler := NewHandler(nil, publishedDocs(), UIConfig{})
	rec := getNav(t, handler)
	var body navResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// When/Then: every heading carries the text and the id the viewer needs.
	withHeadings := 0
	for _, e := range body.Entries {
		if len(e.Headings) > 0 {
			withHeadings++
		}
		for _, h := range e.Headings {
			if h.Text == "" || h.ID == "" || h.Depth < 1 || h.Depth > 6 {
				t.Errorf("%s: unusable heading %+v", e.Path, h)
			}
		}
	}
	if withHeadings == 0 {
		t.Fatal("no page reported any headings")
	}
}

// TestDocs_reportUnavailableRatherThanEmpty covers the slim build and any other
// binary compiled without the docs: an empty corpus must say so. Answering 200
// with no entries would render an empty sidebar and a search box over nothing,
// both of which look like working features.
func TestDocs_reportUnavailableRatherThanEmpty(t *testing.T) {
	// Given: a BFF with no docs embedded.
	handler := NewHandler(nil, fstest.MapFS{}, UIConfig{})

	for _, path := range []string{"/api/docs/nav", "/api/docs/search?q=lambda"} {
		// When: the console asks for navigation or search.
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		// Then: it is told the docs are unavailable.
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: expected 503, got %d: %s", path, rec.Code, rec.Body.String())
		}
	}
}

// TestDocsNav_doesNotShadowTheServiceDocRoute guards the route ordering:
// /api/docs/{service} is a wildcard that would happily read "nav" as a service
// name if chi preferred it, which would 404 the sidebar.
func TestDocsNav_doesNotShadowTheServiceDocRoute(t *testing.T) {
	// Given: a BFF serving both the nav and per-service docs.
	handler := NewHandler(nil, fstest.MapFS{
		"services/s3.md": &fstest.MapFile{Data: []byte("# S3\n")},
	}, UIConfig{})

	// When: a real service doc is requested.
	req := httptest.NewRequest(http.MethodGet, "/api/docs/s3", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Then: it is served, so the static /api/docs/nav route has not swallowed
	// the wildcard.
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for /api/docs/s3, got %d: %s", rec.Code, rec.Body.String())
	}
}
