// Package appconfig_test contains integration tests for the AppConfig emulator.
//
// Run: go test ./tests/integration/appconfig/...
package appconfig_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// acDo performs an AppConfig REST-JSON request.
func acDo(t *testing.T, srv *helpers.TestServer, method, path string, body any) *http.Response {
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

func createApplication(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	resp := acDo(t, srv, http.MethodPost, "/_appconfig/applications", map[string]any{
		"Name": name,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var result struct {
		Id   string `json:"Id"`
		Name string `json:"Name"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Id == "" {
		t.Fatal("expected Id to be set")
	}
	return result.Id
}

// ─── CreateApplication ────────────────────────────────────────────────────────

func TestCreateApplication_success(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: CreateApplication is called
	resp := acDo(t, srv, http.MethodPost, "/_appconfig/applications", map[string]any{
		"Name": "my-app",
	})
	defer resp.Body.Close()

	// Then: 201 with Id set
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var result struct {
		Id   string `json:"Id"`
		Name string `json:"Name"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Id == "" {
		t.Error("expected Id to be set")
	}
	if result.Name != "my-app" {
		t.Errorf("expected Name=my-app, got %q", result.Name)
	}
}

// ─── GetApplication ───────────────────────────────────────────────────────────

func TestGetApplication_success(t *testing.T) {
	// Given: an application exists
	srv := helpers.NewTestServer(t)
	id := createApplication(t, srv, "my-app")

	// When: GetApplication is called
	resp := acDo(t, srv, http.MethodGet, fmt.Sprintf("/_appconfig/applications/%s", id), nil)
	defer resp.Body.Close()

	// Then: 200 with matching Name
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Name string `json:"Name"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Name != "my-app" {
		t.Errorf("expected Name=my-app, got %q", result.Name)
	}
}

// ─── ListApplications ─────────────────────────────────────────────────────────

func TestListApplications_success(t *testing.T) {
	// Given: an application exists
	srv := helpers.NewTestServer(t)
	createApplication(t, srv, "my-app")

	// When: ListApplications is called
	resp := acDo(t, srv, http.MethodGet, "/_appconfig/applications", nil)
	defer resp.Body.Close()

	// Then: 200 with at least 1 item
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Items []struct {
			Id   string `json:"Id"`
			Name string `json:"Name"`
		} `json:"Items"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.Items) < 1 {
		t.Error("expected at least 1 application in list")
	}
}

// ─── DeleteApplication ────────────────────────────────────────────────────────

func TestDeleteApplication_success(t *testing.T) {
	// Given: an application exists
	srv := helpers.NewTestServer(t)
	id := createApplication(t, srv, "my-app")

	// When: DeleteApplication is called
	del := acDo(t, srv, http.MethodDelete, fmt.Sprintf("/_appconfig/applications/%s", id), nil)
	defer del.Body.Close()
	helpers.AssertStatus(t, del, http.StatusNoContent)

	// Then: GetApplication returns 404
	resp := acDo(t, srv, http.MethodGet, fmt.Sprintf("/_appconfig/applications/%s", id), nil)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}
