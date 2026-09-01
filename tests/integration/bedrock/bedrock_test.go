// Package bedrock_test contains integration tests for the Bedrock Runtime emulator.
//
// Every request here goes to the binding the pinned model gives the operation —
// POST /model/{modelId}/invoke and POST /model/{modelId}/converse — because that
// is the only address an AWS SDK will ever use. The suite used to drive an
// emulator-invented /_bedrock prefix, so it exercised the handlers for 33
// releases without once sending a request a client could send, and both
// operations answered 501 to every real caller. See issue #857.
//
// Run: go test ./tests/integration/bedrock/...
package bedrock_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// bedrockSigV4 is the credential scope an AWS SDK signs a bedrock-runtime
// request with. Overcast validates no signature, but the scope is how the
// router decides a path belongs to a service rather than to an S3 bucket of the
// same name, so a suite that omits it is not testing what a client does.
const bedrockSigV4 = "AWS4-HMAC-SHA256 Credential=test/20260811/us-east-1/bedrock/aws4_request, " +
	"SignedHeaders=host;x-amz-date, Signature=fake"

// brDo performs a Bedrock Runtime request. body is sent verbatim: InvokeModel's
// payload is an opaque blob, so the helper must not assume JSON.
func brDo(t *testing.T, srv *helpers.TestServer, method, path, contentType string, body []byte) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, srv.URL+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", bedrockSigV4)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func brJSON(t *testing.T, srv *helpers.TestServer, path string, body any) *http.Response {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return brDo(t, srv, http.MethodPost, path, "application/json", encoded)
}

// ─── InvokeModel ──────────────────────────────────────────────────────────────

func TestInvokeModel_modeledBinding(t *testing.T) {
	// Given: an emulator with no state of any kind — Bedrock stores nothing
	srv := helpers.NewTestServer(t)

	// When: InvokeModel is sent to POST /model/{modelId}/invoke with an opaque
	// payload, the way an SDK sends it
	resp := brJSON(t, srv, "/model/anthropic.claude-v2/invoke", map[string]any{
		"anthropic_version": "bedrock-2023-05-31",
		"messages":          []map[string]any{{"role": "user", "content": "hello"}},
	})
	defer resp.Body.Close()

	// Then: the request is served, and answers with a body and the Content-Type
	// that InvokeModelResponse binds as a required header
	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertHeader(t, resp, "Content-Type", "application/json")
	if body := helpers.ReadBody(t, resp); !json.Valid([]byte(body)) {
		t.Errorf("response body is not valid JSON: %q", body)
	}
}

func TestInvokeModel_inferenceProfileARNModelID(t *testing.T) {
	// Given: an inference-profile ARN in the modelId position. modelId is a
	// non-greedy httpLabel, so an SDK percent-encodes the ARN's slashes and the
	// route still sees one path segment.
	srv := helpers.NewTestServer(t)
	arn := "arn:aws:bedrock:us-east-1:000000000000:inference-profile/us.anthropic.claude-3-5-sonnet-20241022-v2:0"

	// When: InvokeModel is called with that ARN as the model
	resp := brJSON(t, srv, "/model/"+url.PathEscape(arn)+"/invoke", map[string]any{"prompt": "hello"})
	defer resp.Body.Close()

	// Then: the route matches it rather than 404ing on the encoded separator
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestInvokeModel_missingBody(t *testing.T) {
	// Given: an emulator
	srv := helpers.NewTestServer(t)

	// When: InvokeModel is called with no payload at all, which the model marks
	// @required
	resp := brDo(t, srv, http.MethodPost, "/model/anthropic.claude-v2/invoke", "application/json", nil)
	defer resp.Body.Close()

	// Then: AWS's ValidationException, not a canned 200
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ValidationException")
}

// ─── Converse ─────────────────────────────────────────────────────────────────

func TestConverse_modeledBinding(t *testing.T) {
	// Given: an emulator
	srv := helpers.NewTestServer(t)

	// When: Converse is sent to POST /model/{modelId}/converse with the modeled
	// request shape
	resp := brJSON(t, srv, "/model/anthropic.claude-v2/converse", map[string]any{
		"messages": []map[string]any{
			{"role": "user", "content": []map[string]any{{"text": "hello"}}},
		},
		"inferenceConfig": map[string]any{"maxTokens": 100},
	})
	defer resp.Body.Close()

	// Then: a complete ConverseResponse comes back — every member the model
	// marks @required, with wire-legal enum values
	helpers.AssertStatus(t, resp, http.StatusOK)
	var got struct {
		Output struct {
			Message struct {
				Role    string `json:"role"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		} `json:"output"`
		StopReason string `json:"stopReason"`
		Usage      struct {
			InputTokens  *int `json:"inputTokens"`
			OutputTokens *int `json:"outputTokens"`
			TotalTokens  *int `json:"totalTokens"`
		} `json:"usage"`
		Metrics struct {
			LatencyMs *int64 `json:"latencyMs"`
		} `json:"metrics"`
	}
	helpers.DecodeJSON(t, resp, &got)

	if got.Output.Message.Role != "assistant" {
		t.Errorf("output.message.role = %q, want assistant", got.Output.Message.Role)
	}
	if len(got.Output.Message.Content) != 1 || got.Output.Message.Content[0].Text == "" {
		t.Errorf("output.message.content = %+v, want one text block", got.Output.Message.Content)
	}
	if got.StopReason != "end_turn" {
		t.Errorf("stopReason = %q, want end_turn — the model's enum values are lower-case", got.StopReason)
	}
	// TokenUsage marks inputTokens, outputTokens and totalTokens @required, so
	// each has to be present rather than omitted.
	if got.Usage.InputTokens == nil || got.Usage.OutputTokens == nil || got.Usage.TotalTokens == nil {
		t.Errorf("usage = %+v, want inputTokens, outputTokens and totalTokens all present", got.Usage)
	}
	if got.Metrics.LatencyMs == nil {
		t.Error("metrics.latencyMs is absent, but ConverseMetrics marks it @required")
	}
}

// ─── The neighbouring bindings ────────────────────────────────────────────────

func TestInvokeModel_streamingSiblingIsNotSwallowed(t *testing.T) {
	// Given: an emulator. /model/{modelId}/invoke-with-response-stream is a
	// separate modeled operation that answers in application/vnd.amazon.eventstream.
	srv := helpers.NewTestServer(t)

	// When: it is called
	resp := brJSON(t, srv, "/model/anthropic.claude-v2/invoke-with-response-stream", map[string]any{"prompt": "hi"})
	defer resp.Body.Close()

	// Then: an honest 501 — never the non-streaming canned body under a greedy
	// route, which a client would try to parse as an event stream
	helpers.AssertStatus(t, resp, http.StatusNotImplemented)
	if marker := resp.Header.Get("x-emulator-unsupported"); marker != "true" {
		t.Errorf("x-emulator-unsupported = %q, want true", marker)
	}
}

func TestInvokeModel_inventedPrefixIsGone(t *testing.T) {
	// Given: an emulator. /_bedrock was Overcast's own invention and is the
	// reason #857 went unnoticed for 33 releases; keeping it would mean
	// maintaining a wire contract AWS does not have.
	srv := helpers.NewTestServer(t)

	// When: the old prefix is called
	resp := brJSON(t, srv, "/_bedrock/model/anthropic.claude-v2/invoke", map[string]any{"prompt": "hi"})
	defer resp.Body.Close()

	// Then: it is no longer served
	if resp.StatusCode == http.StatusOK {
		t.Errorf("/_bedrock still answers %d; the invented prefix must not survive the move", resp.StatusCode)
	}
	if body := helpers.ReadBody(t, resp); strings.Contains(body, "canned response") {
		t.Errorf("/_bedrock returned a Bedrock response body: %s", body)
	}
}
