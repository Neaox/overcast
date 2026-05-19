// Package waf_test contains integration tests for the WAFv2 emulator.
//
// Run: go test ./tests/integration/waf/...
package waf_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// wafCall performs a WAFv2 X-Amz-Target dispatch request.
func wafCall(t *testing.T, srv *helpers.TestServer, operation string, body map[string]any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s body: %v", operation, err)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "AWSWAF_20190729."+operation)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("wafCall %s: %v", operation, err)
	}
	return resp
}

var defaultWebACLReq = map[string]any{
	"Name":          "test-acl",
	"Scope":         "REGIONAL",
	"DefaultAction": map[string]any{"Allow": map[string]any{}},
	"VisibilityConfig": map[string]any{
		"SampledRequestsEnabled":   false,
		"CloudWatchMetricsEnabled": false,
		"MetricName":               "test-acl",
	},
	"Rules": []any{},
}

// ─── CreateWebACL ─────────────────────────────────────────────────────────────

func TestCreateWebACL_success(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: CreateWebACL is called
	resp := wafCall(t, srv, "CreateWebACL", defaultWebACLReq)
	defer resp.Body.Close()

	// Then: 200 with Summary.Id and Summary.LockToken
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Summary struct {
			Id        string `json:"Id"`
			Name      string `json:"Name"`
			LockToken string `json:"LockToken"`
			ARN       string `json:"ARN"`
		} `json:"Summary"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Summary.Id == "" {
		t.Error("expected Summary.Id to be set")
	}
	if result.Summary.LockToken == "" {
		t.Error("expected Summary.LockToken to be set")
	}
	if result.Summary.Name != "test-acl" {
		t.Errorf("expected Summary.Name=test-acl, got %q", result.Summary.Name)
	}
}

// ─── GetWebACL ────────────────────────────────────────────────────────────────

func TestGetWebACL_success(t *testing.T) {
	// Given: an existing web ACL
	srv := helpers.NewTestServer(t)
	cr := wafCall(t, srv, "CreateWebACL", defaultWebACLReq)
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	var createResult struct {
		Summary struct {
			Id   string `json:"Id"`
			Name string `json:"Name"`
		} `json:"Summary"`
	}
	helpers.DecodeJSON(t, cr, &createResult)

	// When: GetWebACL is called
	resp := wafCall(t, srv, "GetWebACL", map[string]any{
		"Id":    createResult.Summary.Id,
		"Name":  createResult.Summary.Name,
		"Scope": "REGIONAL",
	})
	defer resp.Body.Close()

	// Then: 200 with WebACL.Id matching
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		WebACL struct {
			Id   string `json:"Id"`
			Name string `json:"Name"`
		} `json:"WebACL"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.WebACL.Id != createResult.Summary.Id {
		t.Errorf("expected WebACL.Id=%q, got %q", createResult.Summary.Id, result.WebACL.Id)
	}
}

func TestGetWebACL_notFound(t *testing.T) {
	// Given: no web ACLs
	srv := helpers.NewTestServer(t)

	// When: GetWebACL is called with a non-existent ID
	resp := wafCall(t, srv, "GetWebACL", map[string]any{
		"Id":    "nonexistent",
		"Name":  "nothing",
		"Scope": "REGIONAL",
	})
	defer resp.Body.Close()

	// Then: 400 with WAFNonexistentItemException
	helpers.AssertJSONError(t, resp, "WAFNonexistentItemException")
}

// ─── ListWebACLs ──────────────────────────────────────────────────────────────

func TestListWebACLs_success(t *testing.T) {
	// Given: two web ACLs
	srv := helpers.NewTestServer(t)
	for _, name := range []string{"acl-1", "acl-2"} {
		r := wafCall(t, srv, "CreateWebACL", map[string]any{
			"Name":          name,
			"Scope":         "REGIONAL",
			"DefaultAction": map[string]any{"Allow": map[string]any{}},
			"VisibilityConfig": map[string]any{
				"SampledRequestsEnabled":   false,
				"CloudWatchMetricsEnabled": false,
				"MetricName":               name,
			},
			"Rules": []any{},
		})
		r.Body.Close()
	}

	// When: ListWebACLs is called
	resp := wafCall(t, srv, "ListWebACLs", map[string]any{"Scope": "REGIONAL"})
	defer resp.Body.Close()

	// Then: 200 with 2 ACLs
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		WebACLs []struct {
			Id string `json:"Id"`
		} `json:"WebACLs"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.WebACLs) != 2 {
		t.Errorf("expected 2 ACLs, got %d", len(result.WebACLs))
	}
}

// ─── DeleteWebACL ─────────────────────────────────────────────────────────────

func TestDeleteWebACL_success(t *testing.T) {
	// Given: an existing web ACL
	srv := helpers.NewTestServer(t)
	cr := wafCall(t, srv, "CreateWebACL", defaultWebACLReq)
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	var createResult struct {
		Summary struct {
			Id        string `json:"Id"`
			Name      string `json:"Name"`
			LockToken string `json:"LockToken"`
		} `json:"Summary"`
	}
	helpers.DecodeJSON(t, cr, &createResult)

	// When: DeleteWebACL is called
	resp := wafCall(t, srv, "DeleteWebACL", map[string]any{
		"Id":        createResult.Summary.Id,
		"Name":      createResult.Summary.Name,
		"Scope":     "REGIONAL",
		"LockToken": createResult.Summary.LockToken,
	})
	defer resp.Body.Close()

	// Then: 200
	helpers.AssertStatus(t, resp, http.StatusOK)
}
