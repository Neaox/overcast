package router_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func TestTrace(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithDebug(true))

	t.Run("requestLifecycle", func(t *testing.T) {
		body := `Action=CreateQueue&Version=2012-11-05&QueueName=test-trace-queue`
		req, err := http.NewRequest("POST", srv.URL+"/", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		reqID := resp.Header.Get("x-amzn-requestid")
		if reqID == "" {
			reqID = resp.Header.Get("x-amz-request-id")
		}
		if reqID == "" {
			t.Fatal("expected request ID header")
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
		if trace.Path != "/" {
			t.Errorf("expected /, got %s", trace.Path)
		}
		if trace.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d", trace.StatusCode)
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
		resp, err := http.Get(srv.URL + "/2015-03-31/functions")
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
		resp, err := http.Get(srv.URL + "/2015-03-31/functions")
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
