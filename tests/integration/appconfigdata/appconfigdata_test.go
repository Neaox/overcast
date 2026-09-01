// Package appconfigdata_test contains integration tests for the AppConfigData
// runtime data plane.
//
// The AppConfig control-plane calls here are fixtures, not subjects: they seed
// the application, environment, profile and hosted version a session resolves
// against. They go to AppConfig's modeled bindings and carry its SigV4
// credential scope, because /applications is shared with Service Catalog
// AppRegistry and the scope is what tells the two apart (#854). The control
// plane's own coverage is in tests/integration/appconfig.
//
// Run: go test ./tests/integration/appconfigdata/...
package appconfigdata_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// ─── Helpers ─────────────────────────────────────────────────────────────────

// appconfigSigV4 is the credential scope an AWS SDK for AppConfig signs with.
const appconfigSigV4 = "AWS4-HMAC-SHA256 Credential=test/20250101/us-east-1/appconfig/aws4_request, SignedHeaders=host, Signature=fake"

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
	req.Header.Set("Authorization", appconfigSigV4)
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

// startSession calls StartConfigurationSession at its modeled binding and
// returns the InitialConfigurationToken. minimumPollSeconds is sent as
// RequiredMinimumPollIntervalInSeconds when non-nil.
func startSession(t *testing.T, srv *helpers.TestServer, appID, envID, profID string, minimumPollSeconds *int) string {
	t.Helper()
	body := map[string]any{
		"ApplicationIdentifier":          appID,
		"EnvironmentIdentifier":          envID,
		"ConfigurationProfileIdentifier": profID,
	}
	if minimumPollSeconds != nil {
		body["RequiredMinimumPollIntervalInSeconds"] = *minimumPollSeconds
	}
	resp := acdDo(t, srv, http.MethodPost, "/configurationsessions", body)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var result struct {
		InitialConfigurationToken string `json:"InitialConfigurationToken"`
	}
	helpers.DecodeJSON(t, resp, &result)
	return result.InitialConfigurationToken
}

// getConfiguration calls GetLatestConfiguration at its modeled binding, with
// the configuration token in the query member AWS binds it to.
func getConfiguration(t *testing.T, srv *helpers.TestServer, token string) *http.Response {
	t.Helper()
	return acdDo(t, srv, http.MethodGet, "/configuration?configuration_token="+url.QueryEscape(token), nil)
}

// createApp creates an AppConfig application and returns its ID.
func createApp(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	resp := acDo(t, srv, http.MethodPost, "/applications", map[string]any{"Name": name})
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
	resp := acDo(t, srv, http.MethodPost, fmt.Sprintf("/applications/%s/environments", appID),
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
		fmt.Sprintf("/applications/%s/configurationprofiles", appID),
		map[string]any{"Name": name, "LocationUri": "hosted"})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var result struct {
		Id string `json:"Id"`
	}
	helpers.DecodeJSON(t, resp, &result)
	return result.Id
}

// createHostedVersion stores raw configuration content and returns the version
// number, which the operation binds to the Version-Number response header —
// the response body is the configuration content itself.
func createHostedVersion(t *testing.T, srv *helpers.TestServer, appID, profID, content, contentType string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+fmt.Sprintf("/applications/%s/configurationprofiles/%s/hostedconfigurationversions", appID, profID),
		bytes.NewBufferString(content),
	)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", appconfigSigV4)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("createHostedVersion: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusCreated)
	version, convErr := strconv.Atoi(resp.Header.Get("Version-Number"))
	if convErr != nil || version == 0 {
		t.Fatalf("expected a Version-Number header, got %q", resp.Header.Get("Version-Number"))
	}
	return version
}

// ─── StartConfigurationSession ───────────────────────────────────────────────

func TestStartConfigurationSession_success(t *testing.T) {
	// Given: a complete app/env/profile setup
	srv := helpers.NewTestServer(t)
	appID := createApp(t, srv, "myapp")
	envID := createEnv(t, srv, appID, "prod")
	profID := createProfile(t, srv, appID, "myprofile")

	// When: StartConfigurationSession is called
	token := startSession(t, srv, appID, envID, profID, nil)

	// Then: a token comes back
	if token == "" {
		t.Error("expected InitialConfigurationToken to be set")
	}
}

func TestStartConfigurationSession_unknownApp(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: StartConfigurationSession references a non-existent application
	resp := acdDo(t, srv, http.MethodPost, "/configurationsessions", map[string]any{
		"ApplicationIdentifier":          "nonexistent",
		"EnvironmentIdentifier":          "prod",
		"ConfigurationProfileIdentifier": "myprofile",
	})
	defer resp.Body.Close()

	// Then: 404
	helpers.AssertStatus(t, resp, http.StatusNotFound)
	helpers.AssertJSONError(t, resp, "ResourceNotFoundException")
}

func TestStartConfigurationSession_unknownEnvironment(t *testing.T) {
	// Given: an app but no environments
	srv := helpers.NewTestServer(t)
	appID := createApp(t, srv, "myapp")
	profID := createProfile(t, srv, appID, "myprofile")

	// When: StartConfigurationSession references a non-existent environment
	resp := acdDo(t, srv, http.MethodPost, "/configurationsessions", map[string]any{
		"ApplicationIdentifier":          appID,
		"EnvironmentIdentifier":          "nonexistent",
		"ConfigurationProfileIdentifier": profID,
	})
	defer resp.Body.Close()

	// Then: 404
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

func TestStartConfigurationSession_missingApplicationIdentifier(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: a member the model marks required is omitted
	resp := acdDo(t, srv, http.MethodPost, "/configurationsessions", map[string]any{
		"EnvironmentIdentifier":          "prod",
		"ConfigurationProfileIdentifier": "myprofile",
	})
	defer resp.Body.Close()

	// Then: BadRequestException, not a not-found for a resource never named
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "BadRequestException")
}

func TestStartConfigurationSession_pollIntervalOutOfRange(t *testing.T) {
	// Given: a complete app/env/profile setup
	srv := helpers.NewTestServer(t)
	appID := createApp(t, srv, "myapp")
	envID := createEnv(t, srv, appID, "prod")
	profID := createProfile(t, srv, appID, "myprofile")

	// When: RequiredMinimumPollIntervalInSeconds is below the modeled minimum
	resp := acdDo(t, srv, http.MethodPost, "/configurationsessions", map[string]any{
		"ApplicationIdentifier":                appID,
		"EnvironmentIdentifier":                envID,
		"ConfigurationProfileIdentifier":       profID,
		"RequiredMinimumPollIntervalInSeconds": 5,
	})
	defer resp.Body.Close()

	// Then: BadRequestException
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "BadRequestException")
}

// ─── GetLatestConfiguration ───────────────────────────────────────────────────

func TestGetLatestConfiguration_returnsContent(t *testing.T) {
	// Given: a session and a stored configuration version
	srv := helpers.NewTestServer(t)
	appID := createApp(t, srv, "myapp")
	envID := createEnv(t, srv, appID, "prod")
	profID := createProfile(t, srv, appID, "myprofile")
	createHostedVersion(t, srv, appID, profID, `{"feature":"enabled"}`, "application/json")
	token := startSession(t, srv, appID, envID, profID, nil)

	// When: GetLatestConfiguration is called with the session token
	resp := getConfiguration(t, srv, token)
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
	token := startSession(t, srv, appID, envID, profID, nil)

	// When: GetLatestConfiguration is called
	resp := getConfiguration(t, srv, token)
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
	token := startSession(t, srv, appID, envID, profID, nil)

	// First call — gets the content
	resp1 := getConfiguration(t, srv, token)
	defer resp1.Body.Close()
	helpers.AssertStatus(t, resp1, http.StatusOK)
	nextToken := resp1.Header.Get("Next-Poll-Configuration-Token")
	_, _ = io.ReadAll(resp1.Body)

	// When: GetLatestConfiguration is called again (no new version)
	resp2 := getConfiguration(t, srv, nextToken)
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
	token := startSession(t, srv, appID, envID, profID, nil)

	// Consume version 1
	resp1 := getConfiguration(t, srv, token)
	defer resp1.Body.Close()
	nextToken := resp1.Header.Get("Next-Poll-Configuration-Token")
	_, _ = io.ReadAll(resp1.Body)

	// Publish version 2
	createHostedVersion(t, srv, appID, profID, `{"v":2}`, "application/json")

	// When: GetLatestConfiguration is called with the next token
	resp2 := getConfiguration(t, srv, nextToken)
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
	resp := getConfiguration(t, srv, "not-a-real-token")
	defer resp.Body.Close()

	// Then: 400
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "BadRequestException")
}

func TestGetLatestConfiguration_spentTokenIsRejected(t *testing.T) {
	// Given: a session whose token has been used once
	srv := helpers.NewTestServer(t)
	appID := createApp(t, srv, "myapp")
	envID := createEnv(t, srv, appID, "prod")
	profID := createProfile(t, srv, appID, "myprofile")
	token := startSession(t, srv, appID, envID, profID, nil)
	first := getConfiguration(t, srv, token)
	first.Body.Close()
	helpers.AssertStatus(t, first, http.StatusOK)

	// When: the same token is presented again
	resp := getConfiguration(t, srv, token)
	defer resp.Body.Close()

	// Then: BadRequestException — a configuration token is single-use
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "BadRequestException")
}

func TestGetLatestConfiguration_reportsTheRequestedPollInterval(t *testing.T) {
	// Given: a session that asked for a 120-second minimum poll interval
	srv := helpers.NewTestServer(t)
	appID := createApp(t, srv, "myapp")
	envID := createEnv(t, srv, appID, "prod")
	profID := createProfile(t, srv, appID, "myprofile")
	minimum := 120
	token := startSession(t, srv, appID, envID, profID, &minimum)

	// When: the session is polled
	resp := getConfiguration(t, srv, token)
	defer resp.Body.Close()

	// Then: the requested minimum is what the client is told to wait, not the
	// 60-second default
	helpers.AssertStatus(t, resp, http.StatusOK)
	if got := resp.Header.Get("Next-Poll-Interval-In-Seconds"); got != "120" {
		t.Errorf("expected Next-Poll-Interval-In-Seconds=120, got %q", got)
	}
}

// Token lifetime and the minimum-poll-interval refusal need the clock wound
// forward hours at a time, which through the whole router would drive every
// other service's background ticker along with it. They are unit tests on the
// package instead — see internal/services/appconfigdata/service_test.go.
