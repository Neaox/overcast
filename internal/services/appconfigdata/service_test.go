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

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/services/appconfig"
	"github.com/Neaox/overcast/internal/state"
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

// newTestService returns a service on a mock clock, plus the chi router its
// routes are registered on, so requests take the real bindings.
func newTestService(t *testing.T) (*clock.Mock, chi.Router) {
	t.Helper()
	mock := clock.NewMock()
	svc := New(nil, state.NewMemoryStore(), zap.NewNop(), mock, stubReader{})
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
