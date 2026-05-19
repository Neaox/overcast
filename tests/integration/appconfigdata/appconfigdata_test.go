// Package appconfigdata_test contains integration tests for the AppConfig
// hosted configuration versions API and the AppConfigData runtime data plane.
//
// Run: go test ./tests/integration/appconfigdata/...
package appconfigdata_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// ─── Helpers ─────────────────────────────────────────────────────────────────

// acDo performs an AppConfig control-plane REST-JSON request.
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

// acdDo performs an AppConfigData data-plane request.
func acdDo(t *testing.T, srv *helpers.TestServer, method, path string, body any) *http.Response {
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
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// createApp creates an AppConfig application and returns its ID.
func createApp(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	resp := acDo(t, srv, http.MethodPost, "/_appconfig/applications", map[string]any{"Name": name})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var result struct {
		Id string `json:"Id"`
	}
	helpers.DecodeJSON(t, resp, &result)
	return result.Id
}

// createEnv creates an AppConfig environment and returns its ID.
func createEnv(t *testing.T, srv *helpers.TestServer, appID, name string) string {
	t.Helper()
	resp := acDo(t, srv, http.MethodPost, fmt.Sprintf("/_appconfig/applications/%s/environments", appID),
		map[string]any{"Name": name})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var result struct {
		Id string `json:"Id"`
	}
	helpers.DecodeJSON(t, resp, &result)
	return result.Id
}

// createProfile creates a configuration profile and returns its ID.
func createProfile(t *testing.T, srv *helpers.TestServer, appID, name string) string {
	t.Helper()
	resp := acDo(t, srv, http.MethodPost,
		fmt.Sprintf("/_appconfig/applications/%s/configurationprofiles", appID),
		map[string]any{"Name": name, "LocationUri": "hosted"})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var result struct {
		Id string `json:"Id"`
	}
	helpers.DecodeJSON(t, resp, &result)
	return result.Id
}

// createHostedVersion stores raw configuration content and returns the version number.
func createHostedVersion(t *testing.T, srv *helpers.TestServer, appID, profID, content, contentType string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+fmt.Sprintf("/_appconfig/applications/%s/configurationprofiles/%s/hostedconfigurationversions", appID, profID),
		bytes.NewBufferString(content),
	)
	req.Header.Set("Content-Type", contentType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("createHostedVersion: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var result struct {
		VersionNumber int `json:"VersionNumber"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.VersionNumber == 0 {
		t.Fatal("expected VersionNumber > 0")
	}
	return result.VersionNumber
}

// ─── CreateHostedConfigurationVersion ────────────────────────────────────────

func TestCreateHostedConfigurationVersion_success(t *testing.T) {
	// Given: an app and profile
	srv := helpers.NewTestServer(t)
	appID := createApp(t, srv, "myapp")
	profID := createProfile(t, srv, appID, "myprofile")

	// When: a hosted configuration version is created
	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+fmt.Sprintf("/_appconfig/applications/%s/configurationprofiles/%s/hostedconfigurationversions", appID, profID),
		bytes.NewBufferString(`{"feature":"enabled"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	// Then: 201 with VersionNumber=1
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var result struct {
		VersionNumber          int    `json:"VersionNumber"`
		ApplicationId          string `json:"ApplicationId"`
		ConfigurationProfileId string `json:"ConfigurationProfileId"`
		ContentType            string `json:"ContentType"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.VersionNumber != 1 {
		t.Errorf("expected VersionNumber=1, got %d", result.VersionNumber)
	}
	if result.ApplicationId != appID {
		t.Errorf("expected ApplicationId=%s, got %s", appID, result.ApplicationId)
	}
	if result.ConfigurationProfileId != profID {
		t.Errorf("expected ConfigurationProfileId=%s, got %s", profID, result.ConfigurationProfileId)
	}
	if result.ContentType != "application/json" {
		t.Errorf("expected ContentType=application/json, got %s", result.ContentType)
	}
}

func TestCreateHostedConfigurationVersion_incrementsVersion(t *testing.T) {
	// Given: an app and profile with one existing version
	srv := helpers.NewTestServer(t)
	appID := createApp(t, srv, "myapp")
	profID := createProfile(t, srv, appID, "myprofile")
	v1 := createHostedVersion(t, srv, appID, profID, `{"v":1}`, "application/json")

	// When: a second version is created
	v2 := createHostedVersion(t, srv, appID, profID, `{"v":2}`, "application/json")

	// Then: version numbers are sequential
	if v1 != 1 {
		t.Errorf("expected v1=1, got %d", v1)
	}
	if v2 != 2 {
		t.Errorf("expected v2=2, got %d", v2)
	}
}

func TestCreateHostedConfigurationVersion_unknownProfile(t *testing.T) {
	// Given: an existing app but nonexistent profile
	srv := helpers.NewTestServer(t)
	appID := createApp(t, srv, "myapp")

	// When: CreateHostedConfigurationVersion references a missing profile
	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+fmt.Sprintf("/_appconfig/applications/%s/configurationprofiles/nonexistent/hostedconfigurationversions", appID),
		bytes.NewBufferString(`{}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	// Then: 404
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

// ─── GetHostedConfigurationVersion ───────────────────────────────────────────

func TestGetHostedConfigurationVersion_success(t *testing.T) {
	// Given: a version exists
	srv := helpers.NewTestServer(t)
	appID := createApp(t, srv, "myapp")
	profID := createProfile(t, srv, appID, "myprofile")
	createHostedVersion(t, srv, appID, profID, `{"feature":"on"}`, "application/json")

	// When: GetHostedConfigurationVersion is called
	resp := acDo(t, srv, http.MethodGet,
		fmt.Sprintf("/_appconfig/applications/%s/configurationprofiles/%s/hostedconfigurationversions/1", appID, profID),
		nil)
	defer resp.Body.Close()

	// Then: 200 with the raw configuration in the body
	helpers.AssertStatus(t, resp, http.StatusOK)
	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"feature":"on"}` {
		t.Errorf("unexpected body: %s", body)
	}
	ct := resp.Header.Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type=application/json, got %s", ct)
	}
}

func TestGetHostedConfigurationVersion_notFound(t *testing.T) {
	// Given: an app and profile with no versions
	srv := helpers.NewTestServer(t)
	appID := createApp(t, srv, "myapp")
	profID := createProfile(t, srv, appID, "myprofile")

	// When: version 99 is requested
	resp := acDo(t, srv, http.MethodGet,
		fmt.Sprintf("/_appconfig/applications/%s/configurationprofiles/%s/hostedconfigurationversions/99", appID, profID),
		nil)
	defer resp.Body.Close()

	// Then: 404
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

// ─── ListHostedConfigurationVersions ─────────────────────────────────────────

func TestListHostedConfigurationVersions_success(t *testing.T) {
	// Given: two versions exist
	srv := helpers.NewTestServer(t)
	appID := createApp(t, srv, "myapp")
	profID := createProfile(t, srv, appID, "myprofile")
	createHostedVersion(t, srv, appID, profID, `{"v":1}`, "application/json")
	createHostedVersion(t, srv, appID, profID, `{"v":2}`, "application/json")

	// When: ListHostedConfigurationVersions is called
	resp := acDo(t, srv, http.MethodGet,
		fmt.Sprintf("/_appconfig/applications/%s/configurationprofiles/%s/hostedconfigurationversions", appID, profID),
		nil)
	defer resp.Body.Close()

	// Then: both versions are returned
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Items []struct {
			VersionNumber int `json:"VersionNumber"`
		} `json:"Items"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(result.Items))
	}
}

// ─── DeleteHostedConfigurationVersion ────────────────────────────────────────

func TestDeleteHostedConfigurationVersion_success(t *testing.T) {
	// Given: a version exists
	srv := helpers.NewTestServer(t)
	appID := createApp(t, srv, "myapp")
	profID := createProfile(t, srv, appID, "myprofile")
	createHostedVersion(t, srv, appID, profID, `{"feature":"on"}`, "application/json")

	// When: the version is deleted
	resp := acDo(t, srv, http.MethodDelete,
		fmt.Sprintf("/_appconfig/applications/%s/configurationprofiles/%s/hostedconfigurationversions/1", appID, profID),
		nil)
	defer resp.Body.Close()

	// Then: 204
	helpers.AssertStatus(t, resp, http.StatusNoContent)

	// And: subsequent GET returns 404
	resp2 := acDo(t, srv, http.MethodGet,
		fmt.Sprintf("/_appconfig/applications/%s/configurationprofiles/%s/hostedconfigurationversions/1", appID, profID),
		nil)
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusNotFound)
}

// ─── StartConfigurationSession ───────────────────────────────────────────────

func TestStartConfigurationSession_success(t *testing.T) {
	// Given: a complete app/env/profile setup
	srv := helpers.NewTestServer(t)
	appID := createApp(t, srv, "myapp")
	envID := createEnv(t, srv, appID, "prod")
	profID := createProfile(t, srv, appID, "myprofile")

	// When: StartConfigurationSession is called
	resp := acdDo(t, srv, http.MethodPost, "/_appconfigdata/configurationsessions", map[string]any{
		"ApplicationIdentifier":          appID,
		"EnvironmentIdentifier":          envID,
		"ConfigurationProfileIdentifier": profID,
	})
	defer resp.Body.Close()

	// Then: 201 with an InitialConfigurationToken
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var result struct {
		InitialConfigurationToken string `json:"InitialConfigurationToken"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.InitialConfigurationToken == "" {
		t.Error("expected InitialConfigurationToken to be set")
	}
}

func TestStartConfigurationSession_unknownApp(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: StartConfigurationSession references a non-existent application
	resp := acdDo(t, srv, http.MethodPost, "/_appconfigdata/configurationsessions", map[string]any{
		"ApplicationIdentifier":          "nonexistent",
		"EnvironmentIdentifier":          "prod",
		"ConfigurationProfileIdentifier": "myprofile",
	})
	defer resp.Body.Close()

	// Then: 404
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

func TestStartConfigurationSession_unknownEnvironment(t *testing.T) {
	// Given: an app but no environments
	srv := helpers.NewTestServer(t)
	appID := createApp(t, srv, "myapp")
	profID := createProfile(t, srv, appID, "myprofile")

	// When: StartConfigurationSession references a non-existent environment
	resp := acdDo(t, srv, http.MethodPost, "/_appconfigdata/configurationsessions", map[string]any{
		"ApplicationIdentifier":          appID,
		"EnvironmentIdentifier":          "nonexistent",
		"ConfigurationProfileIdentifier": profID,
	})
	defer resp.Body.Close()

	// Then: 404
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

// ─── GetLatestConfiguration ───────────────────────────────────────────────────

func TestGetLatestConfiguration_returnsContent(t *testing.T) {
	// Given: a session and a stored configuration version
	srv := helpers.NewTestServer(t)
	appID := createApp(t, srv, "myapp")
	envID := createEnv(t, srv, appID, "prod")
	profID := createProfile(t, srv, appID, "myprofile")
	createHostedVersion(t, srv, appID, profID, `{"feature":"enabled"}`, "application/json")

	sessionResp := acdDo(t, srv, http.MethodPost, "/_appconfigdata/configurationsessions", map[string]any{
		"ApplicationIdentifier":          appID,
		"EnvironmentIdentifier":          envID,
		"ConfigurationProfileIdentifier": profID,
	})
	defer sessionResp.Body.Close()
	helpers.AssertStatus(t, sessionResp, http.StatusCreated)
	var session struct {
		InitialConfigurationToken string `json:"InitialConfigurationToken"`
	}
	helpers.DecodeJSON(t, sessionResp, &session)

	// When: GetLatestConfiguration is called with the session token
	resp := acdDo(t, srv, http.MethodGet,
		fmt.Sprintf("/_appconfigdata/configuration/%s", session.InitialConfigurationToken),
		nil)
	defer resp.Body.Close()

	// Then: 200 with configuration content
	helpers.AssertStatus(t, resp, http.StatusOK)
	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"feature":"enabled"}` {
		t.Errorf("unexpected body: %s", body)
	}
	if resp.Header.Get("Next-Poll-Configuration-Token") == "" {
		t.Error("expected Next-Poll-Configuration-Token header")
	}
	if resp.Header.Get("Next-Poll-Interval-In-Seconds") != "60" {
		t.Errorf("expected Next-Poll-Interval-In-Seconds=60, got %s", resp.Header.Get("Next-Poll-Interval-In-Seconds"))
	}
	if resp.Header.Get("Content-Type") != "application/json" {
		t.Errorf("expected Content-Type=application/json, got %s", resp.Header.Get("Content-Type"))
	}
}

func TestGetLatestConfiguration_emptyWhenNoVersions(t *testing.T) {
	// Given: a session with no hosted configuration versions
	srv := helpers.NewTestServer(t)
	appID := createApp(t, srv, "myapp")
	envID := createEnv(t, srv, appID, "prod")
	profID := createProfile(t, srv, appID, "myprofile")

	sessionResp := acdDo(t, srv, http.MethodPost, "/_appconfigdata/configurationsessions", map[string]any{
		"ApplicationIdentifier":          appID,
		"EnvironmentIdentifier":          envID,
		"ConfigurationProfileIdentifier": profID,
	})
	defer sessionResp.Body.Close()
	helpers.AssertStatus(t, sessionResp, http.StatusCreated)
	var session struct {
		InitialConfigurationToken string `json:"InitialConfigurationToken"`
	}
	helpers.DecodeJSON(t, sessionResp, &session)

	// When: GetLatestConfiguration is called
	resp := acdDo(t, srv, http.MethodGet,
		fmt.Sprintf("/_appconfigdata/configuration/%s", session.InitialConfigurationToken),
		nil)
	defer resp.Body.Close()

	// Then: 200 with empty body (no content yet)
	helpers.AssertStatus(t, resp, http.StatusOK)
	body, _ := io.ReadAll(resp.Body)
	if len(body) != 0 {
		t.Errorf("expected empty body, got %q", body)
	}
	if resp.Header.Get("Next-Poll-Configuration-Token") == "" {
		t.Error("expected Next-Poll-Configuration-Token header")
	}
}

func TestGetLatestConfiguration_unchangedReturnsEmpty(t *testing.T) {
	// Given: a session that already retrieved the current version
	srv := helpers.NewTestServer(t)
	appID := createApp(t, srv, "myapp")
	envID := createEnv(t, srv, appID, "prod")
	profID := createProfile(t, srv, appID, "myprofile")
	createHostedVersion(t, srv, appID, profID, `{"key":"val"}`, "application/json")

	sessionResp := acdDo(t, srv, http.MethodPost, "/_appconfigdata/configurationsessions", map[string]any{
		"ApplicationIdentifier":          appID,
		"EnvironmentIdentifier":          envID,
		"ConfigurationProfileIdentifier": profID,
	})
	defer sessionResp.Body.Close()
	var session struct {
		InitialConfigurationToken string `json:"InitialConfigurationToken"`
	}
	helpers.DecodeJSON(t, sessionResp, &session)

	// First call — gets the content
	resp1 := acdDo(t, srv, http.MethodGet,
		fmt.Sprintf("/_appconfigdata/configuration/%s", session.InitialConfigurationToken),
		nil)
	defer resp1.Body.Close()
	helpers.AssertStatus(t, resp1, http.StatusOK)
	nextToken := resp1.Header.Get("Next-Poll-Configuration-Token")
	_, _ = io.ReadAll(resp1.Body)

	// When: GetLatestConfiguration is called again (no new version)
	resp2 := acdDo(t, srv, http.MethodGet,
		fmt.Sprintf("/_appconfigdata/configuration/%s", nextToken),
		nil)
	defer resp2.Body.Close()

	// Then: 200 with empty body (unchanged)
	helpers.AssertStatus(t, resp2, http.StatusOK)
	body, _ := io.ReadAll(resp2.Body)
	if len(body) != 0 {
		t.Errorf("expected empty body on second call (unchanged), got %q", body)
	}
}

func TestGetLatestConfiguration_newVersionDelivered(t *testing.T) {
	// Given: a session that retrieved version 1, then a new version 2 is published
	srv := helpers.NewTestServer(t)
	appID := createApp(t, srv, "myapp")
	envID := createEnv(t, srv, appID, "prod")
	profID := createProfile(t, srv, appID, "myprofile")
	createHostedVersion(t, srv, appID, profID, `{"v":1}`, "application/json")

	sessionResp := acdDo(t, srv, http.MethodPost, "/_appconfigdata/configurationsessions", map[string]any{
		"ApplicationIdentifier":          appID,
		"EnvironmentIdentifier":          envID,
		"ConfigurationProfileIdentifier": profID,
	})
	defer sessionResp.Body.Close()
	var session struct {
		InitialConfigurationToken string `json:"InitialConfigurationToken"`
	}
	helpers.DecodeJSON(t, sessionResp, &session)

	// Consume version 1
	resp1 := acdDo(t, srv, http.MethodGet,
		fmt.Sprintf("/_appconfigdata/configuration/%s", session.InitialConfigurationToken),
		nil)
	defer resp1.Body.Close()
	nextToken := resp1.Header.Get("Next-Poll-Configuration-Token")
	_, _ = io.ReadAll(resp1.Body)

	// Publish version 2
	createHostedVersion(t, srv, appID, profID, `{"v":2}`, "application/json")

	// When: GetLatestConfiguration is called with the next token
	resp2 := acdDo(t, srv, http.MethodGet,
		fmt.Sprintf("/_appconfigdata/configuration/%s", nextToken),
		nil)
	defer resp2.Body.Close()

	// Then: the new content is returned
	helpers.AssertStatus(t, resp2, http.StatusOK)
	body, _ := io.ReadAll(resp2.Body)
	if string(body) != `{"v":2}` {
		t.Errorf("expected v2 content, got %q", body)
	}
}

func TestGetLatestConfiguration_invalidToken(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: GetLatestConfiguration is called with a bogus token
	resp := acdDo(t, srv, http.MethodGet, "/_appconfigdata/configuration/not-a-real-token", nil)
	defer resp.Body.Close()

	// Then: 400
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}
