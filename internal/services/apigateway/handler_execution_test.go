package apigateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/events"
)

type capturingLambdaInvoker struct {
	functionName string
	payload      []byte
}

func (i *capturingLambdaInvoker) Invoke(_ context.Context, functionName string, payload []byte) (*events.InvokeOutcome, error) {
	i.functionName = functionName
	i.payload = append([]byte(nil), payload...)
	return &events.InvokeOutcome{Payload: []byte(`{"statusCode":204}`)}, nil
}

func TestWriteLambdaProxyResponse_multiValueHeadersOverrideSingleHeaders(t *testing.T) {
	// Given: a Lambda proxy response with the same header in both maps.
	rec := httptest.NewRecorder()
	resp := &lambdaProxyResponse{
		StatusCode: http.StatusAccepted,
		Headers: map[string]string{
			"x-mode":  "single",
			"X-Trace": "kept",
		},
		MultiValueHeaders: map[string][]string{
			"X-Mode": {"multi-a", "multi-b"},
		},
		Body: "ok",
	}

	// When: API Gateway writes the Lambda proxy response.
	writeLambdaProxyResponse(rec, resp)

	// Then: multiValueHeaders win for duplicate names, matching AWS Lambda proxy docs.
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d", http.StatusAccepted, rec.Code)
	}
	if got := rec.Header().Values("X-Mode"); len(got) != 2 || got[0] != "multi-a" || got[1] != "multi-b" {
		t.Fatalf("expected X-Mode from multiValueHeaders only, got %#v", got)
	}
	if got := rec.Header().Get("X-Trace"); got != "kept" {
		t.Fatalf("expected X-Trace to be kept from single headers, got %q", got)
	}
	if got := rec.Body.String(); got != "ok" {
		t.Fatalf("expected body %q, got %q", "ok", got)
	}
}

func TestExecuteRestLambdaProxy_absentRequestMapsAreNull(t *testing.T) {
	// Given: a REST Lambda proxy integration request with no query or path params.
	invoker := &capturingLambdaInvoker{}
	h := &Handler{invoker: invoker, clk: clock.NewMock()}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://example.test/hello", nil)
	api := &RestAPI{ID: "api123"}
	resource := &Resource{ID: "res123", Path: "/hello"}
	integration := &Integration{URI: "arn:aws:lambda:us-east-1:000000000000:function:handler"}

	// When: API Gateway invokes the Lambda proxy integration.
	h.executeRestLambdaProxy(rec, req, api, resource, nil, integration, nil, "/hello")

	// Then: absent maps are encoded as JSON null, matching AWS proxy event shape.
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, rec.Code)
	}
	if invoker.functionName != "handler" {
		t.Fatalf("expected function name %q, got %q", "handler", invoker.functionName)
	}
	var event map[string]any
	if err := json.Unmarshal(invoker.payload, &event); err != nil {
		t.Fatalf("unmarshal captured payload: %v", err)
	}
	for _, field := range []string{"queryStringParameters", "multiValueQueryStringParameters", "pathParameters", "stageVariables"} {
		if event[field] != nil {
			t.Fatalf("expected %s to be JSON null, got %#v", field, event[field])
		}
	}
}
