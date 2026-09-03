//go:build !slim

package bff

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// publishedDocs is the docs tree the console binary embeds. The handler derives
// both the docs navigation and the search corpus from it — there is no
// generated index to load, so a search test has to supply the same input the
// binary gets.
func publishedDocs() fs.FS {
	return os.DirFS(filepath.Join("..", "..", "docs"))
}

func TestDocsSearch_cdkQuery(t *testing.T) {
	// Given: the BFF is serving with docs routes enabled.
	handler := NewHandler(nil, publishedDocs(), UIConfig{})

	// The CDK VPC material is two pages — the stack that creates the VPC, and
	// the lookups for one it did not create — so each query has to reach its
	// own half rather than whichever page mentions "vpc" most.
	for _, tc := range []struct{ query, want string }{
		{"cdk+local+vpc+stack", "cdk/local-vpc.md"},
		{"cdk+vpc+lookup+import", "cdk/vpc-lookups.md"},
	} {
		// When: we search docs for that half.
		req := httptest.NewRequest(http.MethodGet, "/api/docs/search?q="+tc.query+"&limit=3", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		// Then: the search index ranks the page that answers it first.
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d: %s", tc.query, rec.Code, rec.Body.String())
		}
		var body struct {
			Query   string `json:"query"`
			Results []struct {
				Href  string `json:"Href"`
				Title string `json:"Title"`
			} `json:"results"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: decode response: %v", tc.query, err)
		}
		if len(body.Results) == 0 {
			t.Fatalf("%s: expected docs search results", tc.query)
		}
		if body.Results[0].Href != tc.want {
			t.Fatalf("%s: expected %s first, got %#v", tc.query, tc.want, body.Results[0])
		}
	}
}

func TestDocsSearch_emptyQuery(t *testing.T) {
	// Given: the BFF is serving with docs routes enabled.
	handler := NewHandler(nil, publishedDocs(), UIConfig{})

	// When: we search docs with an empty query.
	req := httptest.NewRequest(http.MethodGet, "/api/docs/search?q=&limit=3", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Then: the response preserves array semantics for results.
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Query   string            `json:"query"`
		Results []json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Results == nil {
		t.Fatalf("expected empty results array, got nil: %s", rec.Body.String())
	}
	if len(body.Results) != 0 {
		t.Fatalf("expected no results, got %d", len(body.Results))
	}
}
