package lambda

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// runtime_api_telemetry_delivery_test.go — what happens to a Telemetry API
// record when its delivery hiccups.
//
// The delivery worker POSTs each record batch to the subscriber's destination.
// One transient transport failure — a connection reset while the extension's
// server is briefly saturated, exactly what a loaded CI host produces — used
// to lose the record outright: one attempt, then a Debug line nobody reads.
// For the three init-phase platform records that is observable as an extension
// that was told initStart and initReport but never initRuntimeDone, which is
// the very failure issue #1437 records. Delivery is at-least-once now: a
// transport error is retried on the worker with a short backoff, in order,
// and only an endpoint that stays dead loses records — at Warn, not Debug.

// registerAndSubscribe registers an extension over the real HTTP surface and
// subscribes it to platform records at destinationURI, returning the extension
// identifier.
func registerAndSubscribe(t *testing.T, addr, destinationURI string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/2020-01-01/extension/register",
		bytes.NewBufferString(`{"events":["INVOKE","SHUTDOWN"]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Lambda-Extension-Name", "collector")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	extID := resp.Header.Get("Lambda-Extension-Identifier")
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if extID == "" {
		t.Fatal("missing extension identifier")
	}

	sub, err := json.Marshal(map[string]any{
		"types":       []string{"platform"},
		"destination": map[string]string{"protocol": "HTTP", "URI": destinationURI},
	})
	if err != nil {
		t.Fatal(err)
	}
	put, err := http.NewRequest(http.MethodPut, "http://"+addr+"/2020-08-15/logs", bytes.NewReader(sub))
	if err != nil {
		t.Fatal(err)
	}
	put.Header.Set("Lambda-Extension-Identifier", extID)
	put.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(put)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("logs subscribe status = %d, want 200", resp.StatusCode)
	}
	return extID
}

// TestTelemetryDelivery_transientPostFailureIsRetried pins at-least-once
// delivery: the first POST for a record dies at the transport level, and the
// record still arrives on a later attempt rather than being dropped.
func TestTelemetryDelivery_transientPostFailureIsRetried(t *testing.T) {
	srv, addr := newRuntimeAPITestServer(t)
	srv.RegisterContainerConfig("127.0.0.1", runtimeContainerConfig{
		FunctionARN:  "arn:aws:lambda:us-east-1:000000000000:function:demo",
		FunctionName: "demo",
		Handler:      "index.handler",
	})

	var mu sync.Mutex
	var received []string
	killed := false
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		if !killed {
			// First delivery attempt: die at the transport level, the way a
			// briefly saturated extension server does under CI load.
			killed = true
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Error("test server does not support hijacking")
				return
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			conn.Close()
			return
		}
		received = append(received, string(body))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(dest.Close)

	registerAndSubscribe(t, addr, dest.URL)

	event := `{"time":"2026-08-24T00:00:00.000Z","type":"platform.initRuntimeDone","record":{"initializationType":"on-demand","phase":"init","status":"success"}}`
	srv.PublishInitPhaseRecord("127.0.0.1", event)

	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		got := strings.Join(received, "\n")
		mu.Unlock()
		if strings.Contains(got, "platform.initRuntimeDone") {
			return // delivered on a retry — the record survived the hiccup
		}
		if time.Now().After(deadline) {
			t.Fatalf("the record was never redelivered after a transport failure; destination saw:\n%s", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
