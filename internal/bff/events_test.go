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

// TestHandleEvents_survivesLongerThanClientTimeout verifies the SSE proxy does
// not inherit the short Timeout from bffHTTPClient. We shrink bffHTTPClient's
// timeout to 200 ms and assert the stream stays open for a full second—proving
// the events handler uses a separate streaming client.
func TestHandleEvents_survivesLongerThanClientTimeout(t *testing.T) {
	// Given: bffHTTPClient has a very short timeout.
	origClient := bffHTTPClient
	bffHTTPClient = &http.Client{Timeout: 200 * time.Millisecond}
	defer func() { bffHTTPClient = origClient }()

	// And: an upstream SSE endpoint that sends heartbeats every 50 ms.
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

	// When: we open the SSE proxy for 1 s (well beyond the 200 ms client timeout).
	req := httptest.NewRequest(http.MethodGet, "/api/events?ep="+upstream.URL, nil)
	ctx, cancel := context.WithTimeout(req.Context(), 1*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	handleEvents(rec, req)

	// Then: heartbeat frames should keep flowing for the full second.
	body := rec.Body.String()
	count := strings.Count(body, `data: {"type":"heartbeat"}`)
	if count < 10 {
		t.Errorf("expected at least 10 heartbeat frames over 1 s, got %d — "+
			"SSE proxy likely inherits bffHTTPClient Timeout", count)
	}
}

// TestHandleEvents_ForwardsResumePoint pins that a reconnecting browser's
// resume point survives the proxy hop. The BFF builds a fresh upstream
// request rather than forwarding the client's, so neither the Last-Event-ID
// header nor the ?last_event_id fallback reaches /_events unless it is
// copied across deliberately — and without it the emulator replays its whole
// history buffer on every reconnect.
func TestHandleEvents_ForwardsResumePoint(t *testing.T) {
	type received struct {
		header string
		query  string
	}
	got := make(chan received, 1)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- received{
			header: r.Header.Get("Last-Event-ID"),
			query:  r.URL.Query().Get("last_event_id"),
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	t.Run("header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/events?ep="+upstream.URL, nil)
		req.Header.Set("Last-Event-ID", "run7-42")
		handleEvents(httptest.NewRecorder(), req)

		if r := <-got; r.header != "run7-42" {
			t.Errorf("upstream Last-Event-ID = %q, want run7-42", r.header)
		}
	})

	t.Run("query param", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/events?ep="+upstream.URL+"&last_event_id=run7-99", nil)
		handleEvents(httptest.NewRecorder(), req)

		if r := <-got; r.query != "run7-99" {
			t.Errorf("upstream last_event_id = %q, want run7-99", r.query)
		}
	})

	t.Run("query param survives alongside source filters", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/events?ep="+upstream.URL+"&source=s3&last_event_id=run7-1", nil)
		handleEvents(httptest.NewRecorder(), req)

		if r := <-got; r.query != "run7-1" {
			t.Errorf("upstream last_event_id = %q, want run7-1", r.query)
		}
	})
}
