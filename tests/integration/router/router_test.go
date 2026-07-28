// Package router_test contains integration tests for the router layer:
// health, debug endpoints, and 404 handling.
package router_test

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"testing"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/mcp"
	"github.com/Neaox/overcast/internal/state"
	"github.com/Neaox/overcast/tests/helpers"
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

func TestHealth_includesStorageConfig(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithServiceStates(map[string]config.StateBackend{
		"s3":  config.StateBackendPersistent,
		"sqs": config.StateBackendMemory,
	}))

	resp, err := http.Get(srv.URL + "/_health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		Storage struct {
			Default          string            `json:"default"`
			ServiceOverrides map[string]string `json:"serviceOverrides"`
		} `json:"storage"`
	}
	helpers.DecodeJSON(t, resp, &result)

	if result.Storage.Default != "memory" {
		t.Errorf("expected default storage 'memory', got %q", result.Storage.Default)
	}
	if got := result.Storage.ServiceOverrides["s3"]; got != "persistent" {
		t.Errorf("expected s3 override 'persistent', got %q", got)
	}
	if got := result.Storage.ServiceOverrides["sqs"]; got != "memory" {
		t.Errorf("expected sqs override 'memory', got %q", got)
	}
}

func TestRuntimeMCPInitialize_returnsToolsCapability(t *testing.T) {
	srv := helpers.NewTestServer(t)

	payload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": mcp.ProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "router-test", "version": "1.0"},
		},
	})

	resp, err := http.Post(srv.URL+"/_mcp/", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)

	var body map[string]any
	helpers.DecodeJSON(t, resp, &body)
	result, ok := body["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result object, got %T", body["result"])
	}
	if result["protocolVersion"] != mcp.ProtocolVersion {
		t.Fatalf("protocolVersion = %v, want %q", result["protocolVersion"], mcp.ProtocolVersion)
	}
	caps, ok := result["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities type = %T", result["capabilities"])
	}
	if _, ok := caps["tools"]; !ok {
		t.Fatal("capabilities.tools must be present")
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

func TestRouter_unimplementedLambdaRESTPath_doesNotFallThroughToS3(t *testing.T) {
	// Given: Lambda and S3 are enabled.
	srv := helpers.NewTestServer(t)

	// When: a caller uses a real Lambda API version with an operation that
	// Overcast does not route yet.
	resp, err := http.Get(srv.URL + "/2019-09-30/functions/example/unknown-operation")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: the request receives Lambda's JSON NotImplemented response, rather
	// than S3 interpreting the version segment as a bucket name.
	helpers.AssertStatus(t, resp, http.StatusNotImplemented)
	helpers.AssertHeader(t, resp, "x-emulator-unsupported", "true")
	helpers.AssertJSONError(t, resp, "NotImplemented")
}

func TestRouter_disabledLambdaAlternateAPIVersion_returnsServiceDisabled(t *testing.T) {
	// Given: S3 is enabled but Lambda is disabled.
	srv := helpers.NewTestServer(t, helpers.WithServices("s3"))

	// When: a caller uses Lambda's provisioned-concurrency API version.
	resp, err := http.Get(srv.URL + "/2019-09-30/functions/example/provisioned-concurrency")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: Lambda owns the versioned path and reports its disabled state
	// instead of letting S3 interpret the path as a bucket and key.
	helpers.AssertStatus(t, resp, http.StatusServiceUnavailable)
	helpers.AssertJSONError(t, resp, "ServiceDisabled")
}

func TestRouter_unclaimedQueryGET_doesNotFallThroughToS3(t *testing.T) {
	// Given: an emulator with S3 enabled.
	srv := helpers.NewTestServer(t)

	// When: a caller makes an AWS Query-style GET that no service owns.
	resp, err := http.Get(srv.URL + "/?Action=FutureQueryOperation&Version=2099-01-01")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: it receives an AWS Query NotImplemented error, rather than S3's
	// successful ListBuckets XML response.
	helpers.AssertStatus(t, resp, http.StatusNotImplemented)
	helpers.AssertHeader(t, resp, "x-emulator-unsupported", "true")
	var result struct {
		XMLName xml.Name `xml:"ErrorResponse"`
		Error   struct {
			Code string `xml:"Code"`
		} `xml:"Error"`
	}
	helpers.DecodeXML(t, resp, &result)
	if result.Error.Code != "NotImplemented" {
		t.Errorf("expected Query error code %q, got %q", "NotImplemented", result.Error.Code)
	}
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

// TestDebugMetrics_includesAdvisoriesArray is an end-to-end payload-shape
// test for the Metrics & Health advisories feature: a real HTTP request
// through the real router (not a direct handler call), against the test
// server's default MemoryStore backend, which the memory-mode advisory rule
// exists to flag.
func TestDebugMetrics_includesAdvisoriesArray(t *testing.T) {
	// Given: a debug-enabled server on the default (memory) state backend.
	srv := helpers.NewTestServer(t, helpers.WithDebug(true))

	// When: the metrics endpoint is requested.
	resp, err := http.Get(srv.URL + "/_debug/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: the response carries a non-null advisories array including the
	// memory-mode advisory for the default memory backend.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var body struct {
		Stores     []state.DebugMetrics `json:"stores"`
		Advisories []struct {
			Severity string `json:"severity"`
			Code     string `json:"code"`
			Title    string `json:"title"`
			Detail   string `json:"detail"`
		} `json:"advisories"`
	}
	helpers.DecodeJSON(t, resp, &body)
	if body.Advisories == nil {
		t.Fatal("expected a non-nil (possibly empty) advisories array")
	}
	found := false
	for _, a := range body.Advisories {
		if a.Code == "memory-mode" {
			found = true
			if a.Severity != "info" {
				t.Errorf("expected memory-mode advisory severity %q, got %q", "info", a.Severity)
			}
		}
	}
	if !found {
		t.Fatalf("expected a memory-mode advisory for the default memory backend, got %+v", body.Advisories)
	}
}

// TestDebugReset_withSQLiteStore covers the non-MemoryStore branch of debugReset
// (the resetAllNamespaces code path).
func TestDebugReset_withSQLiteStore(t *testing.T) {
	sqliteStore, err := state.NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { sqliteStore.Close() })

	// Wait for the background schema migration to finish before issuing any
	// requests. Without this, a request that races migration now correctly
	// gets a fast 503 (middleware.NotReady, storage-plan.md) instead of
	// silently blocking-then-succeeding the way it used to — this test cares
	// about debug reset behavior against a SQLiteStore backend, not about
	// exercising that race, so synchronize past it explicitly. SQLiteStore
	// has no ReadyAwaiter of its own; any real operation blocks on the same
	// internal gate migration completion closes.
	if _, _, err := sqliteStore.Get(context.Background(), "warmup", "warmup"); err != nil {
		t.Fatalf("warm-up Get (waiting for migration): %v", err)
	}

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

// ---- Mixed-backend storage -------------------------------------------------

func TestMixedBackend_isolatesPerServiceData(t *testing.T) {
	// Given: SQS uses a dedicated MemoryStore, everything else uses the default.
	defaultStore := state.NewMemoryStore()
	sqsStore := state.NewMemoryStore()
	ns := state.NewNamespacedStore(defaultStore, map[string]state.Store{
		"sqs": sqsStore,
	})

	srv := helpers.NewTestServer(t,
		helpers.WithServices("sqs", "sns"),
		helpers.WithStore(ns),
	)

	// When: Create a queue via SQS (JSON protocol with X-Amz-Target header).
	body, _ := json.Marshal(map[string]any{"QueueName": "test-queue"})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.CreateQueue")
	qResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	qResp.Body.Close()
	helpers.AssertStatus(t, qResp, http.StatusOK)

	// Then: The queue data landed in the SQS-specific store.
	keys, err := sqsStore.List(t.Context(), "sqs:queues", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) == 0 {
		t.Fatal("expected queue to be stored in sqsStore, got 0 keys")
	}

	// And: The default store has no SQS data.
	defaultKeys, err := defaultStore.List(t.Context(), "sqs:queues", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(defaultKeys) != 0 {
		t.Errorf("expected 0 keys in defaultStore for sqs:queues, got %d", len(defaultKeys))
	}
}
