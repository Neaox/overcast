package bff

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandleEvents_survivesLongerThanClientTimeout(t *testing.T) {
	origClient := bffHTTPClient
	bffHTTPClient = &http.Client{Timeout: 200 * time.Millisecond}
	defer func() { bffHTTPClient = origClient }()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flusher", 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(200)

		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				fmt.Fprintf(w, "data: {\"type\":\"heartbeat\"}\n\n")
				flusher.Flush()
			}
		}
	}))
	defer upstream.Close()

	prevAPIURL := defaultAPIURL
	defaultAPIURL = upstream.URL
	defer func() { defaultAPIURL = prevAPIURL }()

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	ctx, cancel := context.WithTimeout(req.Context(), 1*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	handleEvents(rec, req)

	body := rec.Body.String()
	count := strings.Count(body, `data: {"type":"heartbeat"}`)
	if count < 10 {
		t.Errorf("expected at least 10 heartbeat frames over 1 s, got %d — "+
			"SSE proxy likely inherits bffHTTPClient Timeout", count)
	}
}

func TestHandleEvents_ForwardsResumePoint(t *testing.T) {
	type received struct {
		header string
		query  string
	}
	got := make(chan received, 1)

	_, restore := stubEmulatorForProxyTests(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- received{
			header: r.Header.Get("Last-Event-ID"),
			query:  r.URL.Query().Get("last_event_id"),
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
	}))
	defer restore()

	t.Run("header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
		req.Header.Set("Last-Event-ID", "run7-42")
		handleEvents(httptest.NewRecorder(), req)

		if r := <-got; r.header != "run7-42" {
			t.Errorf("upstream Last-Event-ID = %q, want run7-42", r.header)
		}
	})

	t.Run("query param", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/events?last_event_id=run7-99", nil)
		handleEvents(httptest.NewRecorder(), req)

		if r := <-got; r.query != "run7-99" {
			t.Errorf("upstream last_event_id = %q, want run7-99", r.query)
		}
	})

	t.Run("query param survives alongside source filters", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/events?source=s3&last_event_id=run7-1", nil)
		handleEvents(httptest.NewRecorder(), req)

		if r := <-got; r.query != "run7-1" {
			t.Errorf("upstream last_event_id = %q, want run7-1", r.query)
		}
	})
}
