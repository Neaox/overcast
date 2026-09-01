package appconfigdata

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/services/appconfig"
	"github.com/overcast-sh/overcast/internal/state"
)

// A configuration token has a lifetime, and — when the session asked for one —
// a minimum gap between polls. Both need the clock wound forward by hours,
// which through the whole router would drive every other service's background
// ticker along with it, so they are exercised here against the service alone.
// Everything else about these two operations is covered end to end in
// tests/integration/appconfigdata.

// stubReader resolves every identifier to a fixed application, environment and
// profile, with no configuration content — all these tests need.
type stubReader struct{}

func (stubReader) ResolveApplication(context.Context, string) (*appconfig.Application, bool) {
	return &appconfig.Application{ID: "app-1"}, true
}

func (stubReader) ResolveEnvironment(context.Context, string, string) (*appconfig.Environment, bool) {
	return &appconfig.Environment{ID: "env-1"}, true
}

func (stubReader) ResolveProfile(context.Context, string, string) (*appconfig.ConfigurationProfile, bool) {
	return &appconfig.ConfigurationProfile{ID: "prof-1"}, true
}

func (stubReader) LatestVersionNumber(context.Context, string, string) (int, error) { return 0, nil }

func (stubReader) GetHostedConfigVersionByNum(context.Context, string, string, int) (*appconfig.HostedConfigurationVersion, bool) {
	return nil, false
}

// labelledReader is a stubReader whose profile has one hosted configuration
// version, carrying a user-defined label.
type labelledReader struct {
	stubReader
	hcv appconfig.HostedConfigurationVersion
}

func (labelledReader) LatestVersionNumber(context.Context, string, string) (int, error) {
	return 1, nil
}

func (r labelledReader) GetHostedConfigVersionByNum(_ context.Context, _, _ string, version int) (*appconfig.HostedConfigurationVersion, bool) {
	if version != 1 {
		return nil, false
	}
	hcv := r.hcv
	return &hcv, true
}

// newTestService returns a service on a mock clock, plus the chi router its
// routes are registered on, so requests take the real bindings.
func newTestService(t *testing.T) (*clock.Mock, chi.Router) {
	t.Helper()
	return newTestServiceWith(t, stubReader{})
}

func newTestServiceWith(t *testing.T, reader appConfigReader) (*clock.Mock, chi.Router) {
	t.Helper()
	mock := clock.NewMock()
	svc := New(nil, state.NewMemoryStore(), zap.NewNop(), mock, reader)
	router := chi.NewRouter()
	svc.RegisterRoutes(router)
	return mock, router
}

func do(router chi.Router, method, target, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

// startSession runs StartConfigurationSession and returns its token.
func startSession(t *testing.T, router chi.Router, body string) string {
	t.Helper()
	response := do(router, http.MethodPost, "/configurationsessions", body)
	if response.Code != http.StatusCreated {
		t.Fatalf("StartConfigurationSession: got %d, body %s", response.Code, response.Body)
	}
	var result struct{ InitialConfigurationToken string }
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decoding StartConfigurationSession response: %v", err)
	}
	return result.InitialConfigurationToken
}

func assertErrorCode(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Errorf("expected status %d, got %d (body %s)", status, response.Code, response.Body)
	}
	var result struct {
		Type string `json:"__type"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decoding error response: %v (body %s)", err, response.Body)
	}
	if result.Type != code {
		t.Errorf("expected error code %q, got %q", code, result.Type)
	}
}

func TestGetLatestConfiguration_expiredToken(t *testing.T) {
	// Given: a session token, and a clock past the 24 hours AWS keeps one valid
	mock, router := newTestService(t)
	token := startSession(t, router,
		`{"ApplicationIdentifier":"a","EnvironmentIdentifier":"e","ConfigurationProfileIdentifier":"p"}`)

	// When: the token is presented after that window
	mock.Add(tokenLifetime + time.Minute)
	response := do(router, http.MethodGet, "/configuration?configuration_token="+token, "")

	// Then: BadRequestException, which is what AWS documents for an expired token
	assertErrorCode(t, response, http.StatusBadRequest, "BadRequestException")
}

func TestGetLatestConfiguration_requiredMinimumPollInterval(t *testing.T) {
	// Given: a session with a 120-second minimum, one poll already spent
	mock, router := newTestService(t)
	token := startSession(t, router,
		`{"ApplicationIdentifier":"a","EnvironmentIdentifier":"e","ConfigurationProfileIdentifier":"p","RequiredMinimumPollIntervalInSeconds":120}`)
	first := do(router, http.MethodGet, "/configuration?configuration_token="+token, "")
	if first.Code != http.StatusOK {
		t.Fatalf("first poll: got %d, body %s", first.Code, first.Body)
	}
	if got := first.Header().Get("Next-Poll-Interval-In-Seconds"); got != "120" {
		t.Errorf("expected the requested minimum to be reported, got %q", got)
	}
	next := first.Header().Get("Next-Poll-Configuration-Token")

	// When: a second poll comes in before the interval has passed
	tooSoon := do(router, http.MethodGet, "/configuration?configuration_token="+next, "")

	// Then: it is refused — AWS models this as
	// InvalidParameterProblem.PollIntervalNotSatisfied
	assertErrorCode(t, tooSoon, http.StatusBadRequest, "BadRequestException")

	// And: the same token still works once the interval has elapsed, because
	// the refusal does not spend it
	mock.Add(121 * time.Second)
	after := do(router, http.MethodGet, "/configuration?configuration_token="+next, "")
	if after.Code != http.StatusOK {
		t.Errorf("poll after the interval elapsed: got %d, body %s", after.Code, after.Body)
	}
}

func TestGetLatestConfiguration_versionLabelHeader(t *testing.T) {
	// Given: a profile whose latest hosted configuration version carries a
	// VersionLabel, as CreateHostedConfigurationVersion stores it
	_, router := newTestServiceWith(t, labelledReader{hcv: appconfig.HostedConfigurationVersion{
		VersionNumber: 1,
		ContentType:   "application/json",
		VersionLabel:  "release-1",
		Content:       `{"feature":true}`,
	}})
	token := startSession(t, router,
		`{"ApplicationIdentifier":"a","EnvironmentIdentifier":"e","ConfigurationProfileIdentifier":"p"}`)

	// When: the first poll returns that version's content
	response := do(router, http.MethodGet, "/configuration?configuration_token="+token, "")

	// Then: the label rides along as the Version-Label header, the name the
	// model binds GetLatestConfiguration's VersionLabel output member to
	if response.Code != http.StatusOK {
		t.Fatalf("GetLatestConfiguration: got %d, body %s", response.Code, response.Body)
	}
	if got := response.Header().Get("Version-Label"); got != "release-1" {
		t.Errorf("expected Version-Label %q, got %q", "release-1", got)
	}
	if got := response.Body.String(); got != `{"feature":true}` {
		t.Errorf("expected the version's content as the payload, got %q", got)
	}

	// And: a version without a label sends no header at all, as AWS does
	_, unlabelled := newTestServiceWith(t, labelledReader{hcv: appconfig.HostedConfigurationVersion{
		VersionNumber: 1,
		ContentType:   "text/plain",
		Content:       "plain",
	}})
	token = startSession(t, unlabelled,
		`{"ApplicationIdentifier":"a","EnvironmentIdentifier":"e","ConfigurationProfileIdentifier":"p"}`)
	response = do(unlabelled, http.MethodGet, "/configuration?configuration_token="+token, "")
	if response.Code != http.StatusOK {
		t.Fatalf("GetLatestConfiguration without a label: got %d, body %s", response.Code, response.Body)
	}
	if _, present := response.Header()["Version-Label"]; present {
		t.Errorf("expected no Version-Label header for an unlabelled version, got %q", response.Header().Get("Version-Label"))
	}
}
