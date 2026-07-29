package bff

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestHandleLambdaInvoke_survivesLongerThanClientTimeout pins that the invoke
// progress stream is proxied with the streaming client rather than
// bffHTTPClient.
//
// A Lambda invocation may run for the function's full configured timeout — up
// to AWS's 15-minute maximum. bffHTTPClient's Timeout covers reading the
// response body, so proxying through it cancelled the upstream request part
// way through a long invocation: the emulator logged the invoke as a timeout
// (its request context was cancelled) and the console reported "Stream ended
// without result" because the SSE stream closed before the result event.
func TestHandleLambdaInvoke_survivesLongerThanClientTimeout(t *testing.T) {
	// Given: bffHTTPClient has a very short timeout.
	origClient := bffHTTPClient
	bffHTTPClient = &http.Client{Timeout: 200 * time.Millisecond}
	defer func() { bffHTTPClient = origClient }()

	// And: an upstream invoke endpoint that streams progress for 1 s — well
	// past that timeout — before delivering the result.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flusher", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		deadline := time.After(1 * time.Second)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				fmt.Fprint(w, "event: progress\ndata: Invoking function handler\n\n")
				flusher.Flush()
			case <-deadline:
				fmt.Fprint(w, "event: result\ndata: {\"statusCode\":200}\n\n")
				flusher.Flush()
				return
			}
		}
	}))
	defer upstream.Close()

	// When: the console invokes through the BFF.
	req := httptest.NewRequest(http.MethodPost, "/api/lambda/functions/demo/invoke-with-progress",
		strings.NewReader(`{"payload":"{}"}`))
	req.Header.Set(endpointHeader, upstream.URL)
	rec := httptest.NewRecorder()
	handleLambdaInvoke(rec, req)

	// Then: the stream survives long enough to carry the result event.
	body := rec.Body.String()
	if !strings.Contains(body, "event: result") {
		t.Errorf("invoke stream closed before the result event — the proxy likely "+
			"inherits bffHTTPClient's Timeout; got:\n%s", body)
	}
}
