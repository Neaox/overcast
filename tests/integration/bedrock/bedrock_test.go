// Package bedrock_test contains integration tests for the Bedrock Runtime emulator.
//
// Run: go test ./tests/integration/bedrock/...
package bedrock_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// brDo performs a Bedrock REST-JSON request.
func brDo(t *testing.T, srv *helpers.TestServer, method, path string, body any) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, srv.URL+path, rdr)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// ─── InvokeModel ──────────────────────────────────────────────────────────────

func TestInvokeModel_success(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: InvokeModel is called
	resp := brDo(t, srv, http.MethodPost, "/_bedrock/model/anthropic.claude-v2/invoke", map[string]any{
		"prompt": "hello",
	})
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// ─── Converse ─────────────────────────────────────────────────────────────────

func TestConverse_success(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: Converse is called
	resp := brDo(t, srv, http.MethodPost, "/_bedrock/model/anthropic.claude-v2/converse", map[string]any{
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"text": "hello"},
				},
			},
		},
	})
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)
}
