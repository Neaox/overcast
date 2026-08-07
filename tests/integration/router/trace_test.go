package router_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func TestTrace(t *testing.T) {
	if os.Getenv("CI") == "" {
		t.Skip("skipping trace integration tests outside CI (port exhaustion on Windows)")
	}
	srv := helpers.NewTestServer(t, helpers.WithDebug(true))

	t.Run("requestLifecycle", func(t *testing.T) {
		body := bytes.NewBufferString(`{"FunctionName":"test-trace-func","Role":"arn:aws:iam::000000000000:role/test","Handler":"index.handler","Runtime":"nodejs22.x"}`)
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/2015-03-31/functions", body)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}

		reqID := resp.Header.Get("x-amzn-requestid")
		if reqID == "" {
			reqID = resp.Header.Get("x-amz-request-id")
		}
		if reqID == "" {
			t.Fatal("expected x-amzn-requestid header")
		}

		traceResp, err := http.Get(srv.URL + "/_debug/trace/" + reqID)
		if err != nil {
			t.Fatal(err)
		}
		defer traceResp.Body.Close()

		helpers.AssertStatus(t, traceResp, http.StatusOK)

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
		if trace.Method != "POST" {
			t.Errorf("expected POST, got %s", trace.Method)
		}
		if trace.Path != "/2015-03-31/functions" {
			t.Errorf("expected /2015-03-31/functions, got %s", trace.Path)
		}
		if trace.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d", trace.StatusCode)
		}
		if trace.Service != "lambda" {
			t.Errorf("expected lambda, got %s", trace.Service)
		}
	})

	t.Run("notFound", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/_debug/trace/nonexistent")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		helpers.AssertStatus(t, resp, http.StatusNotFound)

		var body map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["error"] != "trace not found" {
			t.Errorf("expected 'trace not found' error, got %v", body["error"])
		}
	})

	t.Run("listReturnsEntries", func(t *testing.T) {
		body := bytes.NewBufferString(`{"FunctionName":"test-trace-list","Role":"arn:aws:iam::000000000000:role/test","Handler":"index.handler","Runtime":"nodejs22.x"}`)
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/2015-03-31/functions", body)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()

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
	})

	t.Run("count", func(t *testing.T) {
		body := bytes.NewBufferString(`{"FunctionName":"test-trace-count","Role":"arn:aws:iam::000000000000:role/test","Handler":"index.handler","Runtime":"nodejs22.x"}`)
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/2015-03-31/functions", body)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
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
	})
}
