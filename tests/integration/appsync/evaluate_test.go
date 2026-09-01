// Package appsync_test — integration tests for AppSync's two API-independent
// evaluation endpoints.
//
// These live in their own file rather than in appsync_test.go because they are
// the only AppSync operations AWS binds outside the /v1/apis subtree. Overcast
// served them at POST /v1/apis/{apiId}/evaluateCode and
// .../evaluateMappingTemplate for 34 releases, which no SDK, CDK construct or
// `aws appsync evaluate-code` call has ever sent (#860). The pinned model binds
// them to POST /v1/dataplane-evaluatecode and POST /v1/dataplane-evaluatetemplate
// with no apiId anywhere in the request, so every test here drives the modeled
// binding and none of them creates an API first.
package appsync_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

const (
	evaluateCodePath     = "/v1/dataplane-evaluatecode"
	evaluateTemplatePath = "/v1/dataplane-evaluatetemplate"
)

// appsyncRuntime is the only runtime the model's RuntimeName enum admits.
func appsyncRuntime() map[string]any {
	return map[string]any{"name": "APPSYNC_JS", "runtimeVersion": "1.0.0"}
}

// evaluateResponse is the modeled response envelope shared by both operations.
// EvaluateCodeResponse and EvaluateMappingTemplateResponse differ only in the
// shape of `error`, and codeErrors is the member that differs.
type evaluateResponse struct {
	EvaluationResult string   `json:"evaluationResult"`
	Logs             []string `json:"logs"`
	Stash            string   `json:"stash"`
	OutErrors        string   `json:"outErrors"`
	Error            *struct {
		Message    string `json:"message"`
		CodeErrors []struct {
			ErrorType string `json:"errorType"`
			Value     string `json:"value"`
		} `json:"codeErrors"`
	} `json:"error"`
}

// awsErrorBody is the AWS JSON error envelope.
type awsErrorBody struct {
	Type    string `json:"__type"`
	Message string `json:"message"`
}

func postJSON(t *testing.T, srv *helpers.TestServer, path string, body map[string]any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, bytes.NewReader(b))
	if err != nil {
		t.Fatalf("new request %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

func decodeAWSError(t *testing.T, resp *http.Response) awsErrorBody {
	t.Helper()
	var body awsErrorBody
	helpers.DecodeJSON(t, resp, &body)
	return body
}

// TestEvaluateCode_atTheModeledBinding is the route half of #860: the operation
// answers at POST /v1/dataplane-evaluatecode, and no API exists in this server.
func TestEvaluateCode_atTheModeledBinding(t *testing.T) {
	// Given: a server with no AppSync API at all — the operation is
	// API-independent and must not require one.
	srv := helpers.NewTestServer(t)

	// When: EvaluateCode is called at the binding AWS models.
	resp := postJSON(t, srv, evaluateCodePath, map[string]any{
		"runtime":  appsyncRuntime(),
		"code":     "export function request(ctx) { return { value: ctx.arguments.x * 2 }; }",
		"context":  `{"arguments":{"x":21}}`,
		"function": "request",
	})
	defer resp.Body.Close()

	// Then: it evaluates the code and returns the modeled envelope.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var got evaluateResponse
	helpers.DecodeJSON(t, resp, &got)
	if got.Error != nil {
		t.Fatalf("unexpected evaluation error: %+v", got.Error)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(got.EvaluationResult), &result); err != nil {
		t.Fatalf("evaluationResult %q is not JSON: %v", got.EvaluationResult, err)
	}
	if result["value"] != float64(42) {
		t.Errorf("expected value=42, got %v", result["value"])
	}
}

// TestEvaluateCode_contextIsAModeledString is the shape half of #860, and the
// half a route move alone would leave broken.
//
// AWS models `context` as a String carrying JSON (Context is a string shape,
// length 2..28000). The REST handler typed it as map[string]any, so a
// spec-shaped request — the one every SDK sends, because the SDK serialises a
// Go/JS/Java string member — came back
//
//	SerializationException: cannot unmarshal string into Go struct field
//	.context of type map[string]interface {}
//
// This test asserts both directions: no serialisation failure, and the context
// actually reached the evaluated code rather than being silently dropped.
func TestEvaluateCode_contextIsAModeledString(t *testing.T) {
	// Given: a server, and a context in the modeled String form.
	srv := helpers.NewTestServer(t)

	// When: EvaluateCode is called with it.
	resp := postJSON(t, srv, evaluateCodePath, map[string]any{
		"runtime": appsyncRuntime(),
		"code":    "export function request(ctx) { return { seen: ctx.arguments.name }; }",
		"context": `{"arguments":{"name":"Alice"}}`,
	})
	defer resp.Body.Close()

	// Then: the request is not rejected as malformed...
	body := helpers.ReadBody(t, resp)
	if strings.Contains(body, "SerializationException") {
		t.Fatalf("a spec-shaped context was rejected as malformed: %s", body)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	// ...and the context reached the code.
	var got evaluateResponse
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode response: %v (raw %s)", err, body)
	}
	if !strings.Contains(got.EvaluationResult, `"seen":"Alice"`) {
		t.Errorf("context did not reach the evaluated code; evaluationResult=%q", got.EvaluationResult)
	}
}

// TestEvaluateCode_defaultsFunctionToRequest pins the modeled optionality of
// `function`: it is the only non-required member of EvaluateCodeRequest.
func TestEvaluateCode_defaultsFunctionToRequest(t *testing.T) {
	// Given: a server.
	srv := helpers.NewTestServer(t)

	// When: EvaluateCode omits `function`.
	resp := postJSON(t, srv, evaluateCodePath, map[string]any{
		"runtime": appsyncRuntime(),
		"code":    "export function request(ctx) { return 'from-request'; }",
		"context": `{}`,
	})
	defer resp.Body.Close()

	// Then: request() is evaluated.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var got evaluateResponse
	helpers.DecodeJSON(t, resp, &got)
	if got.EvaluationResult != `"from-request"` {
		t.Errorf("expected request() to be the default entry point, got %q (error %+v)", got.EvaluationResult, got.Error)
	}
}

// TestEvaluateCode_capturesLogsAndStash covers the response members the model
// declares beyond evaluationResult, and that Overcast can honestly produce.
func TestEvaluateCode_capturesLogsAndStash(t *testing.T) {
	// Given: a server.
	srv := helpers.NewTestServer(t)

	// When: the code logs and writes to the stash.
	resp := postJSON(t, srv, evaluateCodePath, map[string]any{
		"runtime": appsyncRuntime(),
		"code": `export function request(ctx) {
			console.log("hello from the resolver");
			ctx.stash.marker = "set-by-request";
			return { ok: true };
		}`,
		"context": `{"arguments":{},"stash":{}}`,
	})
	defer resp.Body.Close()

	// Then: logs and stash come back in the modeled envelope.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var got evaluateResponse
	helpers.DecodeJSON(t, resp, &got)
	if got.Error != nil {
		t.Fatalf("unexpected evaluation error: %+v", got.Error)
	}
	if len(got.Logs) == 0 || !strings.Contains(strings.Join(got.Logs, "\n"), "hello from the resolver") {
		t.Errorf("expected console.log output in logs, got %v", got.Logs)
	}
	if !strings.Contains(got.Stash, "set-by-request") {
		t.Errorf("expected the mutated stash to be returned as a JSON string, got %q", got.Stash)
	}
}

// TestEvaluateCode_codeErrorIsAnEvaluationResult pins that a fault in the
// *evaluated* code is a 200 carrying `error`, not an HTTP error. The modeled
// operation code is 200 and EvaluateCodeResponse carries an error member for
// exactly this.
func TestEvaluateCode_codeErrorIsAnEvaluationResult(t *testing.T) {
	// Given: a server.
	srv := helpers.NewTestServer(t)

	// When: the code does not parse.
	resp := postJSON(t, srv, evaluateCodePath, map[string]any{
		"runtime": appsyncRuntime(),
		"code":    "export function request(ctx) { return {",
		"context": `{}`,
	})
	defer resp.Body.Close()

	// Then: 200 with an error payload rather than a 4xx.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var got evaluateResponse
	helpers.DecodeJSON(t, resp, &got)
	if got.Error == nil || got.Error.Message == "" {
		t.Fatalf("expected an error payload for uncompilable code, got %+v", got)
	}
}

func TestEvaluateCode_rejectsRequestsTheModelForbids(t *testing.T) {
	// Given: a server.
	srv := helpers.NewTestServer(t)

	// When/Then: each member the model requires or constrains is enforced.
	cases := []struct {
		name string
		body map[string]any
	}{
		{"missing runtime", map[string]any{
			"code": "export function request(ctx) { return 1; }", "context": `{}`,
		}},
		{"runtime name outside the enum", map[string]any{
			"runtime": map[string]any{"name": "VTL", "runtimeVersion": "1.0.0"},
			"code":    "export function request(ctx) { return 1; }", "context": `{}`,
		}},
		{"missing runtimeVersion", map[string]any{
			"runtime": map[string]any{"name": "APPSYNC_JS"},
			"code":    "export function request(ctx) { return 1; }", "context": `{}`,
		}},
		{"missing code", map[string]any{
			"runtime": appsyncRuntime(), "context": `{}`,
		}},
		{"missing context", map[string]any{
			"runtime": appsyncRuntime(), "code": "export function request(ctx) { return 1; }",
		}},
		{"context that is not JSON", map[string]any{
			"runtime": appsyncRuntime(),
			"code":    "export function request(ctx) { return 1; }", "context": `not json`,
		}},
		{"function outside the documented values", map[string]any{
			"runtime": appsyncRuntime(),
			"code":    "export function request(ctx) { return 1; }", "context": `{}`,
			"function": "somethingElse",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := postJSON(t, srv, evaluateCodePath, tc.body)
			defer resp.Body.Close()

			helpers.AssertStatus(t, resp, http.StatusBadRequest)
			if got := decodeAWSError(t, resp).Type; !strings.Contains(got, "BadRequestException") {
				t.Errorf("expected BadRequestException, got %q", got)
			}
		})
	}
}

// TestEvaluateMappingTemplate_atTheModeledBinding is the route half for the VTL
// operation. It is a distinct shape from EvaluateCode — template plus context,
// no runtime and no function — so it gets its own handler and its own test.
func TestEvaluateMappingTemplate_atTheModeledBinding(t *testing.T) {
	// Given: a server with no AppSync API.
	srv := helpers.NewTestServer(t)

	// When: EvaluateMappingTemplate is called at the binding AWS models.
	resp := postJSON(t, srv, evaluateTemplatePath, map[string]any{
		"template": `{ "result": "$context.arguments.name is $context.arguments.age years old" }`,
		"context":  `{"arguments":{"name":"Alice","age":30}}`,
	})
	defer resp.Body.Close()

	// Then: the template is rendered.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var got evaluateResponse
	helpers.DecodeJSON(t, resp, &got)
	if got.Error != nil {
		t.Fatalf("unexpected evaluation error: %+v", got.Error)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(got.EvaluationResult), &result); err != nil {
		t.Fatalf("evaluationResult %q is not JSON: %v", got.EvaluationResult, err)
	}
	if result["result"] != "Alice is 30 years old" {
		t.Errorf("expected the interpolated template, got %v", result["result"])
	}
}

// TestEvaluateMappingTemplate_rejectsARuntimeMember guards against the two
// operations being collapsed behind one handler because the invented routes
// happened to sit next to each other. EvaluateMappingTemplateRequest has two
// members; `runtime` is not one of them, and `template` is required.
func TestEvaluateMappingTemplate_rejectsRequestsTheModelForbids(t *testing.T) {
	// Given: a server.
	srv := helpers.NewTestServer(t)

	cases := []struct {
		name string
		body map[string]any
	}{
		{"missing template", map[string]any{"context": `{}`}},
		{"missing context", map[string]any{"template": `$util.autoId()`}},
		{"context that is not JSON", map[string]any{"template": `$util.autoId()`, "context": `not json`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := postJSON(t, srv, evaluateTemplatePath, tc.body)
			defer resp.Body.Close()

			helpers.AssertStatus(t, resp, http.StatusBadRequest)
			if got := decodeAWSError(t, resp).Type; !strings.Contains(got, "BadRequestException") {
				t.Errorf("expected BadRequestException, got %q", got)
			}
		})
	}
}

// TestEvaluate_inventedRoutesAreGone asserts the old bindings stop answering.
// Leaving them registered would keep two live spellings of one operation, which
// is how the REST and typed implementations drifted apart in the first place.
func TestEvaluate_inventedRoutesAreGone(t *testing.T) {
	// Given: an API, so the only reason these paths could 501 is that they are
	// no longer registered.
	srv := helpers.NewTestServer(t)
	apiID, _ := createTestAPI(t, srv)

	for _, path := range []string{
		"/v1/apis/" + apiID + "/evaluateCode",
		"/v1/apis/" + apiID + "/evaluateMappingTemplate",
	} {
		t.Run(path, func(t *testing.T) {
			// When: the invented binding is called.
			resp := postJSON(t, srv, path, map[string]any{"context": `{}`})
			defer resp.Body.Close()

			// Then: the /v1/apis catch-all answers NotImplemented.
			helpers.AssertStatus(t, resp, http.StatusNotImplemented)
		})
	}
}
