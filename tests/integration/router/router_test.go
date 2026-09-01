// Package router_test contains integration tests for the router layer:
// health, debug endpoints, and 404 handling.
package router_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
	"github.com/Neaox/overcast/tests/helpers"
)

// ---- Health ----------------------------------------------------------------

func TestHealth_returnsOK(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp, err := http.Get(srv.URL + "/_overcast/health")
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

	resp, err := http.Get(srv.URL + "/_overcast/health")
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

// The runtime MCP endpoint only exists in non-slim builds, so its test lives in
// the //go:build !slim-guarded mcp_test.go rather than here.

// ---- Not-found -------------------------------------------------------------

func TestNotFound_returns404(t *testing.T) {
	// Disable S3 so bucket routes aren't registered.
	// Then GET /some-bucket genuinely matches no route → notFoundHandler.
	srv := helpers.NewTestServer(t)

	resp, err := http.Get(srv.URL + "/some-bucket-that-has-no-handler")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

func TestRouter_s3BucketNamedLikeLambdaAPIVersion_remainsReachable(t *testing.T) {
	// Given: Lambda and S3 are enabled.
	srv := helpers.NewTestServer(t)

	// When: S3 creates a bucket whose legal name matches a Lambda API version.
	req, err := http.NewRequest(http.MethodPut, srv.URL+"/2015-03-31", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: S3 accepts the request instead of Lambda claiming the ambiguous path.
	helpers.AssertStatus(t, resp, http.StatusOK)

	// And: the same bucket remains available for normal object writes.
	objectReq, err := http.NewRequest(http.MethodPut, srv.URL+"/2015-03-31/key.txt", bytes.NewBufferString("contents"))
	if err != nil {
		t.Fatal(err)
	}
	objectResp, err := http.DefaultClient.Do(objectReq)
	if err != nil {
		t.Fatal(err)
	}
	defer objectResp.Body.Close()
	helpers.AssertStatus(t, objectResp, http.StatusOK)
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
	helpers.AssertQueryXMLError(t, resp, "NotImplemented")
}

func TestRouter_unclaimedQueryPOST_returnsNotImplemented(t *testing.T) {
	// Given: an emulator with S3 enabled.
	srv := helpers.NewTestServer(t)

	// When: a caller submits an AWS Query action that no service owns.
	resp, err := http.Post(srv.URL+"/", "application/x-www-form-urlencoded", bytes.NewBufferString("Action=FutureQueryOperation&Version=2099-01-01"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: POST uses the same protocol-correct NotImplemented contract as GET.
	helpers.AssertStatus(t, resp, http.StatusNotImplemented)
	helpers.AssertHeader(t, resp, "x-emulator-unsupported", "true")
	helpers.AssertQueryXMLError(t, resp, "NotImplemented")
}

func TestRouter_unclaimedModeledJSONTarget_returnsNotImplemented(t *testing.T) {
	// Given: an AWS JSON operation modeled for a service Overcast does not yet
	// implement. GameLift has no target dispatcher, so this exercises the
	// registry rather than a service-local unknown-operation response.
	srv := helpers.NewTestServer(t)

	// When: its SDK wire identifier reaches POST /.
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewBufferString("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "GameLift.ListBuilds")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: it receives the common JSON 501 contract, rather than the old
	// hand-written 400 unknown-target response.
	helpers.AssertStatus(t, resp, http.StatusNotImplemented)
	helpers.AssertHeader(t, resp, "x-emulator-unsupported", "true")
	helpers.AssertRequestID(t, resp)
	helpers.AssertJSONError(t, resp, "NotImplemented")
}

func TestRouter_unclaimedModeledRPCv2CBOROperation_returnsNotImplemented(t *testing.T) {
	// Given: a modeled additive RPC v2 CBOR operation for a service without an
	// Overcast dispatcher.
	srv := helpers.NewTestServer(t)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/service/GameLift/operation/ListBuilds", bytes.NewReader([]byte{0xa0}))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Smithy-Protocol", "rpc-v2-cbor")

	// When: the explicit Smithy RPC route reaches the operation registry.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: the modeled operation receives a protocol-shaped 501 rather than
	// an unsupported-protocol error or S3 fallback.
	helpers.AssertStatus(t, resp, http.StatusNotImplemented)
	helpers.AssertHeader(t, resp, "Smithy-Protocol", "rpc-v2-cbor")
	helpers.AssertHeader(t, resp, "x-emulator-unsupported", "true")
	helpers.AssertRequestID(t, resp)
}

func TestRouter_enabledServiceUnimplementedRPCv2CBOROperation_returnsNotImplemented(t *testing.T) {
	// Given: CloudWatch is enabled, but its modeled additive CBOR operations
	// have no RPC dispatcher implementation.
	srv := helpers.NewTestServer(t)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/service/GraniteServiceVersion20100801/operation/AssociateDatasetKmsKey", bytes.NewReader([]byte{0xa0}))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Smithy-Protocol", "rpc-v2-cbor")

	// When: the modeled operation reaches the central RPC dispatcher.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: the service's unknown-operation branch cannot turn it into a 400.
	helpers.AssertStatus(t, resp, http.StatusNotImplemented)
	helpers.AssertHeader(t, resp, "x-emulator-unsupported", "true")
}

func TestRouter_unclaimedModeledRESTPath_returnsNotImplemented(t *testing.T) {
	// Given: a modeled REST JSON operation for a service Overcast does not yet
	// implement, alongside S3's deliberately broad bucket routes.
	srv := helpers.NewTestServer(t)

	// When: the Access Analyzer ListAnalyzers wire path is requested.
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/analyzer", nil)
	if err != nil {
		t.Fatal(err)
	}
	// AWS SDK, CLI, and CDK requests carry their service in the SigV4 scope.
	// This disambiguates the REST operation from S3's valid bucket name space.
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test/20260728/us-east-1/access-analyzer/aws4_request, SignedHeaders=host, Signature=test")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: the modeled AWS operation owns the exact method/path binding and
	// returns a JSON 501 instead of S3 interpreting "analyzer" as a bucket.
	helpers.AssertStatus(t, resp, http.StatusNotImplemented)
	helpers.AssertHeader(t, resp, "x-emulator-unsupported", "true")
	helpers.AssertRequestID(t, resp)
	helpers.AssertJSONError(t, resp, "NotImplemented")
}

func TestRouter_signedRESTRootPath_returnsNotImplemented(t *testing.T) {
	// Given: a modeled REST root operation alongside S3's ListBuckets route.
	srv := helpers.NewTestServer(t)
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test/20260728/us-east-1/mediastore/aws4_request, SignedHeaders=host, Signature=test")

	// When: a MediaStore Data SDK-shaped request reaches the shared root path.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: SigV4 service identity prevents a fallthrough to S3 ListBuckets.
	helpers.AssertStatus(t, resp, http.StatusNotImplemented)
	helpers.AssertHeader(t, resp, "x-emulator-unsupported", "true")
	helpers.AssertJSONError(t, resp, "NotImplemented")
}

func TestRouter_unsignedS3PathMatchingAmbiguousRESTBinding_remainsReachable(t *testing.T) {
	// Given: an unsigned S3 bucket whose name is also an ambiguous REST literal.
	// Only S3 is registered: with API Gateway present its literal /tags/* route
	// legitimately wins, so the precedence this test pins is only observable
	// once the other claimant is out of the way.
	srv := helpers.NewTestServer(t, helpers.WithServiceSubset("s3"))
	create, err := http.NewRequest(http.MethodPut, srv.URL+"/tags", nil)
	if err != nil {
		t.Fatal(err)
	}
	createResp, err := http.DefaultClient.Do(create)
	if err != nil {
		t.Fatal(err)
	}
	createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	put, err := http.NewRequest(http.MethodPut, srv.URL+"/tags/obj.txt", bytes.NewBufferString("contents"))
	if err != nil {
		t.Fatal(err)
	}
	putResp, err := http.DefaultClient.Do(put)
	if err != nil {
		t.Fatal(err)
	}
	putResp.Body.Close()
	helpers.AssertStatus(t, putResp, http.StatusOK)

	// When: an unsigned request reads the object through the ambiguous path.
	resp, err := http.Get(srv.URL + "/tags/obj.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: no empty signing name can claim it; S3 remains the safe fallback.
	helpers.AssertStatus(t, resp, http.StatusOK)
	if got := helpers.ReadBody(t, resp); got != "contents" {
		t.Errorf("object body = %q, want contents", got)
	}
}

func TestRouter_headerlessS3MultipartPathMatchingRPCGrammar_remainsReachable(t *testing.T) {
	// Given: a legitimate S3 bucket and key whose path has Smithy RPC's
	// /service/{service}/operation/{operation} shape.
	srv := helpers.NewTestServer(t)
	create, err := http.NewRequest(http.MethodPut, srv.URL+"/service", nil)
	if err != nil {
		t.Fatal(err)
	}
	createResp, err := http.DefaultClient.Do(create)
	if err != nil {
		t.Fatal(err)
	}
	createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)

	// When: S3 initiates a multipart upload on that exact four-segment path.
	initiate, err := http.NewRequest(http.MethodPost, srv.URL+"/service/example/operation/upload?uploads", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(initiate)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: the absence of Smithy-Protocol leaves the request with S3 instead
	// of the RPC route returning its generic 404.
	helpers.AssertStatus(t, resp, http.StatusOK)
	if got := resp.Header.Get("Content-Type"); got != "application/xml" {
		t.Errorf("Content-Type = %q, want application/xml", got)
	}
}

// ---- Debug endpoints (guarded by cfg.Debug) --------------------------------

func TestDebugHealth_returnsOK(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithDebug(true))

	resp, err := http.Get(srv.URL + "/_overcast/debug/health")
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

	resp, err := http.Get(srv.URL + "/_overcast/debug/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

func TestDebugConfig_returnsConfig(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithDebug(true), helpers.WithRegion("eu-central-1"))

	resp, err := http.Get(srv.URL + "/_overcast/debug/config")
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

	resp, err := http.Get(srv.URL + "/_overcast/debug/state")
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

	resp, err := http.Get(srv.URL + "/_overcast/debug/state/s3:buckets")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
}

// TestReset_wipesState uses a debug-disabled server deliberately: reset moved
// out from under the debug gate (it is neither expensive nor leaky, and adds
// no destructive power the unauthenticated AWS surface doesn't already
// expose), so this proves POST /_overcast/reset works with OVERCAST_DEBUG unset.
func TestReset_wipesState(t *testing.T) {
	srv := helpers.NewTestServer(t)

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
	resetResp, err := http.Post(srv.URL+"/_overcast/reset", "application/json", nil)
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

func TestResetService_knownService(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp, err := http.Post(srv.URL+"/_overcast/reset/s3", "application/json", nil)
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

func TestResetService_unknownService(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp, err := http.Post(srv.URL+"/_overcast/reset/unknown-service", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

// TestDebugReset_gone proves the old debug-gated reset path was moved, not
// aliased: this project is ALPHA and does not keep shims for its own past
// decisions (see /_overcast/reset above). Even with OVERCAST_DEBUG=true (so
// the rest of the /_overcast/debug namespace is mounted — health, config,
// state, metrics, traces), "reset" and "reset/{service}" are no longer
// registered under it, so the request falls through to S3's private
// bucket/object router (bucket="_overcast", key="debug/reset") — the same
// fallback any unclaimed /_overcast/* path hits. That is what "gone" looks
// like at the HTTP layer; it is emphatically not a 200 "reset" response.
func TestDebugReset_gone(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithDebug(true))

	resp, err := http.Post(srv.URL+"/_overcast/debug/reset", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		t.Fatalf("POST /_overcast/debug/reset: status = 200, want anything but — the debug-gated path must be gone, not still answering as reset")
	}
}

func TestDebugMetrics_returnsJSON(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithDebug(true))

	resp, err := http.Get(srv.URL + "/_overcast/debug/metrics")
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
	resp, err := http.Get(srv.URL + "/_overcast/debug/metrics")
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

// Debug reset against a real SQLite-backed store is build-tag-sensitive and
// lives in the //go:build !nosqlite-guarded sqlite_test.go.

// ---- Mixed-backend storage -------------------------------------------------

func TestMixedBackend_isolatesPerServiceData(t *testing.T) {
	// Given: SQS uses a dedicated MemoryStore, everything else uses the default.
	defaultStore := state.NewMemoryStore()
	sqsStore := state.NewMemoryStore()
	ns := state.NewNamespacedStore(defaultStore, map[string]state.Store{
		"sqs": sqsStore,
	})

	srv := helpers.NewTestServer(t,
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
