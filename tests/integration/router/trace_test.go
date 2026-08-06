package router_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func TestTrace_requestLifecycle(t *testing.T) {
	// Given: a server with debug tracing enabled
	srv := helpers.NewTestServer(t, helpers.WithDebug(true))

	// When: we send a request
	resp, err := http.Get(srv.URL + "/_health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Then: the response carries a request ID
	reqID := resp.Header.Get("x-amzn-requestid")
	if reqID == "" {
		t.Fatal("expected x-amzn-requestid header")
	}

	// When: we fetch the trace for that request
	traceResp, err := http.Get(srv.URL + "/_debug/trace/" + reqID)
	if err != nil {
		t.Fatal(err)
	}
	defer traceResp.Body.Close()

	helpers.AssertStatus(t, traceResp, http.StatusOK)

	// Then: the trace contains expected request metadata
	var trace struct {
		RequestID  string `json:"requestId"`
		Method     string `json:"method"`
		Path       string `json:"path"`
		StatusCode int    `json:"statusCode"`
		Service    string `json:"service"`
	}
	helpers.DecodeJSON(t, traceResp, &trace)

	if trace.RequestID != reqID {
		t.Errorf("expected requestId %q, got %q", reqID, trace.RequestID)
	}
	if trace.Method != "GET" {
		t.Errorf("expected GET, got %s", trace.Method)
	}
	if trace.Path != "/_health" {
		t.Errorf("expected /_health, got %s", trace.Path)
	}
	if trace.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", trace.StatusCode)
	}
	if trace.Service == "" {
		t.Error("expected non-empty service")
	}
}

func TestTrace_notFound(t *testing.T) {
	// Given: a server with debug tracing enabled
	srv := helpers.NewTestServer(t, helpers.WithDebug(true))

	// When: we fetch a non-existent trace
	resp, err := http.Get(srv.URL + "/_debug/trace/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: we get a 404
	helpers.AssertStatus(t, resp, http.StatusNotFound)

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "trace not found" {
		t.Errorf("expected 'trace not found' error, got %v", body["error"])
	}
}

func TestTrace_listReturnsEntries(t *testing.T) {
	// Given: a server with debug tracing enabled
	srv := helpers.NewTestServer(t, helpers.WithDebug(true))

	// When: we make a request (so there's a trace to list)
	resp, err := http.Get(srv.URL + "/_health")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// When: we list traces
	listResp, err := http.Get(srv.URL + "/_debug/traces")
	if err != nil {
		t.Fatal(err)
	}
	defer listResp.Body.Close()

	helpers.AssertStatus(t, listResp, http.StatusOK)

	var list struct {
		Traces []any  `json:"traces"`
		Next   string `json:"nextCursor"`
	}
	helpers.DecodeJSON(t, listResp, &list)

	if len(list.Traces) == 0 {
		t.Error("expected at least one trace in list")
	}
}

func TestTrace_count(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithDebug(true))

	resp, err := http.Get(srv.URL + "/_health")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	countResp, err := http.Get(srv.URL + "/_debug/traces/count")
	if err != nil {
		t.Fatal(err)
	}
	defer countResp.Body.Close()

	var count struct {
		Count    int `json:"count"`
		Capacity int `json:"capacity"`
	}
	helpers.DecodeJSON(t, countResp, &count)

	if count.Count < 1 {
		t.Errorf("expected count >= 1, got %d", count.Count)
	}
	if count.Capacity != 1000 {
		t.Errorf("expected capacity 1000, got %d", count.Capacity)
	}
}
