package bff

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandleLambdaInvoke_survivesLongerThanClientTimeout(t *testing.T) {
	origClient := bffHTTPClient
	bffHTTPClient = &http.Client{Timeout: 200 * time.Millisecond}
	defer func() { bffHTTPClient = origClient }()

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

	prevAPIURL := defaultAPIURL
	defaultAPIURL = upstream.URL
	defer func() { defaultAPIURL = prevAPIURL }()

	req := httptest.NewRequest(http.MethodPost, "/api/lambda/functions/demo/invoke-with-progress",
		strings.NewReader(`{"payload":"{}"}`))
	rec := httptest.NewRecorder()
	handleLambdaInvoke(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "event: result") {
		t.Errorf("invoke stream closed before the result event — the proxy likely "+
			"inherits bffHTTPClient's Timeout; got:\n%s", body)
	}
}
