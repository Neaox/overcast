// Package opensearch_test contains integration tests for the OpenSearch emulator.
//
// Run: go test ./tests/integration/opensearch/...
package opensearch_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

const osBasePath = "/_opensearch/domain"

// osDo performs an OpenSearch REST-JSON request.
func osDo(t *testing.T, srv *helpers.TestServer, method, path string, body any) *http.Response {
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

func createDomain(t *testing.T, srv *helpers.TestServer, name string) {
	t.Helper()
	resp := osDo(t, srv, http.MethodPost, osBasePath, map[string]any{
		"DomainName": name,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// ─── CreateDomain ─────────────────────────────────────────────────────────────

func TestCreateDomain_success(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: CreateDomain is called
	resp := osDo(t, srv, http.MethodPost, osBasePath, map[string]any{
		"DomainName": "test-domain",
	})
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// ─── DescribeDomain ───────────────────────────────────────────────────────────

func TestDescribeDomain_success(t *testing.T) {
	// Given: a domain exists
	srv := helpers.NewTestServer(t)
	createDomain(t, srv, "test-domain")

	// When: DescribeDomain is called
	resp := osDo(t, srv, http.MethodGet, osBasePath+"/test-domain", nil)
	defer resp.Body.Close()

	// Then: 200 with domain info
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// ─── ListDomainNames ──────────────────────────────────────────────────────────

func TestListDomainNames_success(t *testing.T) {
	// Given: a domain exists
	srv := helpers.NewTestServer(t)
	createDomain(t, srv, "test-domain")

	// When: ListDomainNames is called
	resp := osDo(t, srv, http.MethodGet, osBasePath, nil)
	defer resp.Body.Close()

	// Then: 200 with the domain in the list
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// ─── DeleteDomain ─────────────────────────────────────────────────────────────

func TestDeleteDomain_success(t *testing.T) {
	// Given: a domain exists
	srv := helpers.NewTestServer(t)
	createDomain(t, srv, "test-domain")

	// When: DeleteDomain is called
	del := osDo(t, srv, http.MethodDelete, osBasePath+"/test-domain", nil)
	defer del.Body.Close()
	helpers.AssertStatus(t, del, http.StatusOK)

	// Then: describe returns 404
	resp := osDo(t, srv, http.MethodGet, osBasePath+"/test-domain", nil)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}
