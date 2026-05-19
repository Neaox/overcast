// Package athena_test contains integration tests for the Amazon Athena emulator.
//
// Run: go test ./tests/integration/athena/...
package athena_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// athenaCall performs an Athena JSON 1.1 dispatch request.
func athenaCall(t *testing.T, srv *helpers.TestServer, operation string, body map[string]any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s body: %v", operation, err)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "AmazonAthena."+operation)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("athenaCall %s: %v", operation, err)
	}
	return resp
}

func startQuery(t *testing.T, srv *helpers.TestServer, query string) string {
	t.Helper()
	resp := athenaCall(t, srv, "StartQueryExecution", map[string]any{
		"QueryString": query,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		QueryExecutionId string `json:"QueryExecutionId"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.QueryExecutionId == "" {
		t.Fatal("expected QueryExecutionId to be set")
	}
	return result.QueryExecutionId
}

// ─── StartQueryExecution ──────────────────────────────────────────────────────

func TestStartQueryExecution_success(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: StartQueryExecution is called
	qid := startQuery(t, srv, "SELECT 1")

	// Then: a QueryExecutionId is returned
	if qid == "" {
		t.Error("expected QueryExecutionId to be non-empty")
	}
}

// ─── GetQueryExecution ────────────────────────────────────────────────────────

func TestGetQueryExecution_success(t *testing.T) {
	// Given: a query has been started
	srv := helpers.NewTestServer(t)
	qid := startQuery(t, srv, "SELECT 1")

	// When: GetQueryExecution is called
	resp := athenaCall(t, srv, "GetQueryExecution", map[string]any{
		"QueryExecutionId": qid,
	})
	defer resp.Body.Close()

	// Then: 200 with State "SUCCEEDED"
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		QueryExecution struct {
			Status struct {
				State string `json:"State"`
			} `json:"Status"`
		} `json:"QueryExecution"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.QueryExecution.Status.State != "SUCCEEDED" {
		t.Errorf("expected State=SUCCEEDED, got %q", result.QueryExecution.Status.State)
	}
}

// ─── GetQueryResults ──────────────────────────────────────────────────────────

func TestGetQueryResults_success(t *testing.T) {
	// Given: a query has been started
	srv := helpers.NewTestServer(t)
	qid := startQuery(t, srv, "SELECT 1")

	// When: GetQueryResults is called
	resp := athenaCall(t, srv, "GetQueryResults", map[string]any{
		"QueryExecutionId": qid,
	})
	defer resp.Body.Close()

	// Then: 200 with ResultSet
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		ResultSet struct {
			Rows []any `json:"Rows"`
		} `json:"ResultSet"`
	}
	helpers.DecodeJSON(t, resp, &result)
}

// ─── CreateWorkGroup ──────────────────────────────────────────────────────────

func TestCreateWorkGroup_success(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: CreateWorkGroup is called
	resp := athenaCall(t, srv, "CreateWorkGroup", map[string]any{
		"Name": "test-wg",
	})
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// ─── ListWorkGroups ───────────────────────────────────────────────────────────

func TestListWorkGroups_success(t *testing.T) {
	// Given: a work group exists
	srv := helpers.NewTestServer(t)
	cr := athenaCall(t, srv, "CreateWorkGroup", map[string]any{
		"Name": "test-wg",
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)

	// When: ListWorkGroups is called
	resp := athenaCall(t, srv, "ListWorkGroups", map[string]any{})
	defer resp.Body.Close()

	// Then: 200 with at least 1 work group
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		WorkGroups []struct {
			Name string `json:"Name"`
		} `json:"WorkGroups"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.WorkGroups) < 1 {
		t.Error("expected at least 1 work group")
	}
}
