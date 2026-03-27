// Package router_test contains integration tests for the router layer:
// health, debug endpoints, and 404 handling.
package router_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/your-org/overcast/internal/state"
	"github.com/your-org/overcast/tests/helpers"
)

// ---- Health ----------------------------------------------------------------

func TestHealth_returnsOK(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp, err := http.Get(srv.URL + "/_health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		Status string `json:"status"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Status != "ok" {
		t.Errorf("expected status 'ok', got %q", result.Status)
	}
}

// ---- Not-found -------------------------------------------------------------

func TestNotFound_returns404(t *testing.T) {
	// Disable S3 so bucket routes aren't registered.
	// Then GET /some-bucket genuinely matches no route → notFoundHandler.
	srv := helpers.NewTestServer(t, helpers.WithServices("sqs"))

	resp, err := http.Get(srv.URL + "/some-bucket-that-has-no-handler")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

// ---- Debug endpoints (guarded by cfg.Debug) --------------------------------

func TestDebugHealth_returnsOK(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithDebug(true))

	resp, err := http.Get(srv.URL + "/_debug/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Status string `json:"status"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Status != "ok" {
		t.Errorf("expected status 'ok', got %q", result.Status)
	}
}

func TestDebugHealth_notMountedWhenDisabled(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithDebug(false))

	resp, err := http.Get(srv.URL + "/_debug/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

func TestDebugConfig_returnsConfig(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithDebug(true), helpers.WithRegion("eu-central-1"))

	resp, err := http.Get(srv.URL + "/_debug/config")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Region string `json:"region"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Region != "eu-central-1" {
		t.Errorf("expected region 'eu-central-1', got %q", result.Region)
	}
}

func TestDebugState_returnsJSON(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithDebug(true))

	resp, err := http.Get(srv.URL + "/_debug/state")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("expected valid JSON, got error: %v", err)
	}
}

func TestDebugStateNamespace_returnsJSON(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithDebug(true))

	resp, err := http.Get(srv.URL + "/_debug/state/s3:buckets")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestDebugReset_wipesState(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithDebug(true))

	// Pre-populate state via SQS CreateQueue.
	body, _ := json.Marshal(map[string]any{"QueueName": "reset-test-queue"})
	createReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(body))
	createReq.Header.Set("Content-Type", "application/x-amz-json-1.0")
	createReq.Header.Set("X-Amz-Target", "AmazonSQS.CreateQueue")

	createResp, err := http.DefaultClient.Do(createReq)
	if err != nil {
		t.Fatal(err)
	}
	createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)

	// Reset all state.
	resetResp, err := http.Post(srv.URL+"/_debug/reset", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resetResp.Body.Close()

	helpers.AssertStatus(t, resetResp, http.StatusOK)
	var result map[string]string
	helpers.DecodeJSON(t, resetResp, &result)
	if result["status"] != "reset" {
		t.Errorf("expected status 'reset', got %q", result["status"])
	}
}

func TestDebugResetService_knownService(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithDebug(true))

	resp, err := http.Post(srv.URL+"/_debug/reset/s3", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result map[string]string
	helpers.DecodeJSON(t, resp, &result)
	if result["service"] != "s3" {
		t.Errorf("expected service 's3', got %q", result["service"])
	}
}

func TestDebugResetService_unknownService(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithDebug(true))

	resp, err := http.Post(srv.URL+"/_debug/reset/unknown-service", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

func TestDebugMetrics_returnsJSON(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithDebug(true))

	resp, err := http.Get(srv.URL + "/_debug/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
}

// TestDebugReset_withSQLiteStore covers the non-MemoryStore branch of debugReset
// (the resetAllNamespaces code path).
func TestDebugReset_withSQLiteStore(t *testing.T) {
	sqliteStore, err := state.NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { sqliteStore.Close() })

	srv := helpers.NewTestServer(t, helpers.WithDebug(true), helpers.WithStore(sqliteStore))

	// Create a queue to populate state.
	body, _ := json.Marshal(map[string]any{"QueueName": "sqlite-reset-queue"})
	createReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(body))
	createReq.Header.Set("Content-Type", "application/x-amz-json-1.0")
	createReq.Header.Set("X-Amz-Target", "AmazonSQS.CreateQueue")

	createResp, err := http.DefaultClient.Do(createReq)
	if err != nil {
		t.Fatal(err)
	}
	createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)

	// Reset via debug endpoint — exercises resetAllNamespaces.
	resetResp, err := http.Post(srv.URL+"/_debug/reset", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resetResp.Body.Close()

	helpers.AssertStatus(t, resetResp, http.StatusOK)
	var result map[string]string
	helpers.DecodeJSON(t, resetResp, &result)
	if result["status"] != "reset" {
		t.Errorf("expected status 'reset', got %q", result["status"])
	}
}
