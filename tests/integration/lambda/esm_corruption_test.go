package lambda_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func TestGetEventSourceMapping_storeFailure(t *testing.T) {
	// Given: the state store fails while reading a named event source mapping.
	store := helpers.NewMockStore()
	srv := helpers.NewTestServer(t, helpers.WithStore(store))
	store.GetError = errors.New("injected get failure")

	// When: the mapping is requested by UUID.
	resp := doJSON(t, http.MethodGet, esmURL(srv)+"/b4eeec4d-62be-4a60-b005-c8ffbe1c1214", nil)

	// Then: an infrastructure failure remains an AWS-shaped internal error.
	assertLambdaInternalError(t, resp)
}

func TestListEventSourceMappings_storeFailure(t *testing.T) {
	// Given: the state store fails while scanning event source mappings.
	store := helpers.NewMockStore()
	srv := helpers.NewTestServer(t, helpers.WithStore(store))
	store.ScanError = errors.New("injected scan failure")

	// When: event source mappings are listed.
	resp := doJSON(t, http.MethodGet, esmURL(srv), nil)

	// Then: an infrastructure failure remains an AWS-shaped internal error.
	assertLambdaInternalError(t, resp)
}

func TestGetEventSourceMapping_corruptNamedRecord(t *testing.T) {
	// Given: a named event source mapping record contains malformed JSON.
	srv := helpers.NewTestServer(t)
	const id = "b4eeec4d-62be-4a60-b005-c8ffbe1c1214"
	if err := srv.Store.Set(context.Background(), "lambda:esm", "us-east-1/"+id, `{malformed`); err != nil {
		t.Fatalf("seed corrupt event source mapping: %v", err)
	}

	// When: the corrupt mapping is requested by UUID.
	resp := doJSON(t, http.MethodGet, esmURL(srv)+"/"+id, nil)

	// Then: the isolated record behaves as absent rather than exposing a 500.
	helpers.AssertStatus(t, resp, http.StatusNotFound)
	helpers.AssertRequestID(t, resp)
	var body struct {
		Type string `json:"__type"`
	}
	decodeJSON(t, resp, &body)
	if body.Type != "ResourceNotFoundException" {
		t.Fatalf("error type = %q, want ResourceNotFoundException", body.Type)
	}
}

func TestListEventSourceMappings_corruptRecordAmongValidRecords(t *testing.T) {
	// Given: one valid mapping and one malformed persisted record coexist.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "esm-list-corrupt")
	valid := createESM(t, srv, map[string]any{
		"FunctionName": "esm-list-corrupt", "EventSourceArn": sqsARN("list-corrupt"),
	})
	const corruptID = "e2b96138-d6fd-483d-8922-866d2b55f160"
	if err := srv.Store.Set(context.Background(), "lambda:esm", "us-east-1/"+corruptID, `{malformed`); err != nil {
		t.Fatalf("seed corrupt event source mapping: %v", err)
	}

	// When: all event source mappings are listed.
	resp := doJSON(t, http.MethodGet, esmURL(srv), nil)

	// Then: only the valid mapping is returned.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Mappings []map[string]any `json:"EventSourceMappings"`
	}
	decodeJSON(t, resp, &result)
	if len(result.Mappings) != 1 || result.Mappings[0]["UUID"] != valid["UUID"] {
		t.Fatalf("mappings = %#v, want only UUID %v", result.Mappings, valid["UUID"])
	}
}

func assertLambdaInternalError(t *testing.T, resp *http.Response) {
	t.Helper()
	helpers.AssertStatus(t, resp, http.StatusInternalServerError)
	helpers.AssertRequestID(t, resp)
	var body struct {
		Type string `json:"__type"`
	}
	decodeJSON(t, resp, &body)
	if body.Type != "InternalError" {
		t.Fatalf("error type = %q, want InternalError", body.Type)
	}
}
