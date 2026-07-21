//go:build !slim

package bff

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestDocsSearch_cdkQuery(t *testing.T) {
	// Given: the BFF is serving with docs routes enabled.
	handler := NewHandler(nil, fstest.MapFS{}, UIConfig{})

	// When: we search docs for the local CDK VPC pattern.
	req := httptest.NewRequest(http.MethodGet, "/api/docs/search?q=cdk+local+vpc&limit=3", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Then: the generated search index returns the local VPC guide.
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Query   string `json:"query"`
		Results []struct {
			Href  string `json:"Href"`
			Title string `json:"Title"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Results) == 0 {
		t.Fatal("expected docs search results")
	}
	if body.Results[0].Href != "cdk/local-vpc.md" {
		t.Fatalf("expected local VPC guide first, got %#v", body.Results[0])
	}
}

func TestDocsSearch_emptyQuery(t *testing.T) {
	// Given: the BFF is serving with docs routes enabled.
	handler := NewHandler(nil, fstest.MapFS{}, UIConfig{})

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
