package appconfigdata_test

import (
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// AppConfigData is a restJson1 service with two operations, and every member of
// both is bound somewhere other than the request body's obvious place:
//
//	POST /configurationsessions   — identifiers in the JSON body
//	GET  /configuration           — ConfigurationToken is an httpQuery member
//	                                named configuration_token, the response is
//	                                the configuration blob itself, and the
//	                                session state comes back in headers.
//
// Both answered 501 to every AWS SDK for the emulator's first 33 releases: the
// service was mounted on an invented /_appconfigdata prefix, with the token as
// a path segment, and the whole suite drove that shape too. See issue #855.

func TestStartConfigurationSession_atTheModeledBinding(t *testing.T) {
	// Given: a complete app/env/profile setup
	srv := helpers.NewTestServer(t)
	appID := createApp(t, srv, "myapp")
	envID := createEnv(t, srv, appID, "prod")
	profID := createProfile(t, srv, appID, "myprofile")

	// When: StartConfigurationSession is sent to POST /configurationsessions
	resp := acdDo(t, srv, http.MethodPost, "/configurationsessions", map[string]any{
		"ApplicationIdentifier":          appID,
		"EnvironmentIdentifier":          envID,
		"ConfigurationProfileIdentifier": profID,
	})
	defer resp.Body.Close()

	// Then: the modeled 201 comes back with a token, not a routing fallback
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var result struct {
		InitialConfigurationToken string `json:"InitialConfigurationToken"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.InitialConfigurationToken == "" {
		t.Error("expected InitialConfigurationToken to be set")
	}
}

func TestGetLatestConfiguration_configurationTokenIsAQueryMember(t *testing.T) {
	// Given: a session over a profile with published content
	srv := helpers.NewTestServer(t)
	appID := createApp(t, srv, "myapp")
	envID := createEnv(t, srv, appID, "prod")
	profID := createProfile(t, srv, appID, "myprofile")
	createHostedVersion(t, srv, appID, profID, `{"feature":"enabled"}`, "application/json")
	token := startSession(t, srv, appID, envID, profID, nil)

	// When: GetLatestConfiguration carries the token in ?configuration_token
	resp := acdDo(t, srv, http.MethodGet,
		"/configuration?configuration_token="+url.QueryEscape(token), nil)
	defer resp.Body.Close()

	// Then: the payload is the configuration itself and the session state is in
	// the modeled response headers
	helpers.AssertStatus(t, resp, http.StatusOK)
	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"feature":"enabled"}` {
		t.Errorf("unexpected payload: %s", body)
	}
	if resp.Header.Get("Next-Poll-Configuration-Token") == "" {
		t.Error("expected a Next-Poll-Configuration-Token header")
	}
	if got := resp.Header.Get("Next-Poll-Interval-In-Seconds"); got != "60" {
		t.Errorf("expected Next-Poll-Interval-In-Seconds=60, got %q", got)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("expected Content-Type=application/json, got %q", got)
	}
}

func TestGetLatestConfiguration_missingConfigurationToken(t *testing.T) {
	// Given: a running emulator
	srv := helpers.NewTestServer(t)

	// When: GetLatestConfiguration omits the required query member
	resp := acdDo(t, srv, http.MethodGet, "/configuration", nil)
	defer resp.Body.Close()

	// Then: the modeled BadRequestException, not a 404 from an unmatched route
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "BadRequestException")
}
