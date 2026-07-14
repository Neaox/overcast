package apigateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
	if invoker.functionName != "handler" {
		t.Fatalf("expected function name %q, got %q", "handler", invoker.functionName)
	}
	event := capturedProxyEvent(t, rec, invoker)
	for _, field := range []string{"queryStringParameters", "multiValueQueryStringParameters", "pathParameters", "stageVariables"} {
		if event[field] != nil {
			t.Fatalf("expected %s to be JSON null, got %#v", field, event[field])
		}
	}
	if event["body"] != nil {
		t.Fatalf("expected body to be JSON null for GET request with no body, got %#v", event["body"])
	}
}

func TestExecuteRestLambdaProxy_requestBodyPresent(t *testing.T) {
	// Given: a REST Lambda proxy integration request with a body.
	invoker := &capturingLambdaInvoker{}
	h := &Handler{invoker: invoker, clk: clock.NewMock()}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://example.test/hello", strings.NewReader("payload"))
	api := &RestAPI{ID: "api123"}
	resource := &Resource{ID: "res123", Path: "/hello"}
	integration := &Integration{URI: "arn:aws:lambda:us-east-1:000000000000:function:handler"}

	// When: API Gateway invokes the Lambda proxy integration.
	h.executeRestLambdaProxy(rec, req, api, resource, nil, integration, nil, "/hello")

	// Then: body is encoded as the request payload string.
	event := capturedProxyEvent(t, rec, invoker)
	if event["body"] != "payload" {
		t.Fatalf("expected body %q, got %#v", "payload", event["body"])
	}
}

func TestExecuteV2LambdaProxy_payloadFormatOneEmptyRequestBodyIsNull(t *testing.T) {
	// Given: an HTTP API Lambda proxy integration using payload format 1.0.
	invoker := &capturingLambdaInvoker{}
	h := &Handler{invoker: invoker, clk: clock.NewMock()}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://example.test/hello", nil)
	api := &APIV2{ApiID: "api123"}
	route := &RouteV2{RouteKey: "GET /hello"}
	integration := &IntegrationV2{IntegrationURI: "arn:aws:lambda:us-east-1:000000000000:function:handler", PayloadFormatVersion: "1.0"}

	// When: API Gateway invokes the Lambda proxy integration.
	h.executeV2LambdaProxy(rec, req, api, route, integration, nil, "/hello")

	// Then: body is JSON null in the v1-shaped event.
	event := capturedProxyEvent(t, rec, invoker)
	if event["body"] != nil {
		t.Fatalf("expected body to be JSON null for GET request with no body, got %#v", event["body"])
	}
}

func capturedProxyEvent(t *testing.T, rec *httptest.ResponseRecorder, invoker *capturingLambdaInvoker) map[string]any {
	t.Helper()
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, rec.Code)
	}
	var event map[string]any
	if err := json.Unmarshal(invoker.payload, &event); err != nil {
		t.Fatalf("unmarshal captured payload: %v", err)
	}
	return event
}
