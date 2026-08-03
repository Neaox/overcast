package apigateway_test

// throttle_test.go — usage-plan throttle/quota evaluation and opt-in
// enforcement on the REST v1 execute path, plus the GetUsage reporting
// surface those limits are observed through.
//
// Two behaviours are deliberately separated here, and both are asserted:
//
//   - Evaluation is always on. Every request that passes an API key check
//     against a usage plan is counted, and the counts are readable through
//     GetUsage. Nothing about the response changes.
//   - Rejection is opt-in. Only a server started with
//     WithEnforceAPIGatewayThrottle(true) (OVERCAST_ENFORCE_APIGATEWAY_THROTTLE
//     in production) answers an over-limit request with AWS's 429.

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Neaox/overcast/tests/helpers"
)

// createLimitedUsagePlan creates a usage plan covering {apiID, stageName} with
// the given throttle/quota settings and attaches keyID to it. Passing nil for
// either settings block leaves that limit unconfigured.
func createLimitedUsagePlan(
	t *testing.T, srv *helpers.TestServer,
	name, apiID, stageName, keyID string,
	throttle, quota map[string]any,
) string {
	t.Helper()
	body := map[string]any{
		"name": name,
		"apiStages": []map[string]any{
			{"apiId": apiID, "stage": stageName},
		},
	}
	if throttle != nil {
		body["throttle"] = throttle
	}
	if quota != nil {
		body["quota"] = quota
	}
	resp := apiCall(t, srv, http.MethodPost, "/usageplans", body)
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var out map[string]any
	helpers.DecodeJSON(t, resp, &out)
	planID, _ := out["id"].(string)
	if planID == "" {
		t.Fatalf("usage plan create returned no id: %v", out)
	}

	r := apiCall(t, srv, http.MethodPost, "/usageplans/"+planID+"/keys", map[string]any{
		"keyId":   keyID,
		"keyType": "API_KEY",
	})
	helpers.AssertStatus(t, r, http.StatusCreated)
	r.Body.Close()
	return planID
}

// invokeWithKey executes the deployed /hello method with the given API key
// value and returns the response.
func invokeWithKey(t *testing.T, srv *helpers.TestServer, apiID, stage, keyValue string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet,
		srv.URL+"/restapis/"+apiID+"/"+stage+"/_user_request_/hello", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("x-api-key", keyValue)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp
}

// ─── Default behaviour: measured, never rejected ──────────────────────────

// TestExecuteRestAPI_quotaExceeded_notEnforcedByDefault is the §2.1 fidelity
// guard: a plan whose quota is already blown still serves every request when
// the operator has not opted in.
func TestExecuteRestAPI_quotaExceeded_notEnforcedByDefault(t *testing.T) {
	// Given a stage behind an API key whose usage plan allows one request a day
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	apiID, stage := setupAPIKeyMethod(t, srv, "quota-default")
	const value = "1111def0123456789abcdef0123456789abcdef01"
	keyID := createAPIKey(t, srv, "quota-default-key", value)
	createLimitedUsagePlan(t, srv, "quota-default-plan", apiID, stage, keyID,
		nil, map[string]any{"limit": 1, "period": "DAY"})

	// When we call it three times over that limit
	for i := range 3 {
		resp := invokeWithKey(t, srv, apiID, stage, value)
		// Then every request is served — enforcement is off by default
		if resp.StatusCode != http.StatusOK {
			body := helpers.ReadBody(t, resp)
			t.Fatalf("request %d: expected 200 with enforcement off, got %d (%s)",
				i+1, resp.StatusCode, body)
		}
		resp.Body.Close()
	}
}

// TestExecuteRestAPI_throttleExceeded_notEnforcedByDefault covers the rate
// limit half of the same guard.
func TestExecuteRestAPI_throttleExceeded_notEnforcedByDefault(t *testing.T) {
	// Given a plan with a burst of one and no refill within the test
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	apiID, stage := setupAPIKeyMethod(t, srv, "throttle-default")
	const value = "2222def0123456789abcdef0123456789abcdef01"
	keyID := createAPIKey(t, srv, "throttle-default-key", value)
	createLimitedUsagePlan(t, srv, "throttle-default-plan", apiID, stage, keyID,
		map[string]any{"burstLimit": 1, "rateLimit": 1}, nil)

	// When we exceed the burst
	for i := range 5 {
		resp := invokeWithKey(t, srv, apiID, stage, value)
		// Then nothing is rejected
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: expected 200 with enforcement off, got %d", i+1, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

// ─── Opt-in enforcement ────────────────────────────────────────────────────

func TestExecuteRestAPI_quotaExceeded_enforced(t *testing.T) {
	// Given enforcement is enabled and the plan allows one request a day
	srv := helpers.NewTestServer(t,
		helpers.WithMockClock(), helpers.WithEnforceAPIGatewayThrottle(true))
	apiID, stage := setupAPIKeyMethod(t, srv, "quota-enforced")
	const value = "3333def0123456789abcdef0123456789abcdef01"
	keyID := createAPIKey(t, srv, "quota-enforced-key", value)
	createLimitedUsagePlan(t, srv, "quota-enforced-plan", apiID, stage, keyID,
		nil, map[string]any{"limit": 1, "period": "DAY"})

	// When the first request is made
	first := invokeWithKey(t, srv, apiID, stage, value)
	helpers.AssertStatus(t, first, http.StatusOK)
	first.Body.Close()

	// Then the second exceeds the quota and gets AWS's QUOTA_EXCEEDED shape
	second := invokeWithKey(t, srv, apiID, stage, value)
	helpers.AssertStatus(t, second, http.StatusTooManyRequests)
	if got := second.Header.Get("x-amzn-ErrorType"); got != "LimitExceededException" {
		t.Errorf("expected x-amzn-ErrorType LimitExceededException, got %q", got)
	}
	body := helpers.ReadBody(t, second)
	if body != `{"message":"Limit Exceeded"}` {
		t.Errorf("expected AWS quota body, got %q", body)
	}
	// Quota-period rollover is asserted against the clock directly in
	// internal/services/apigateway/usage_test.go: advancing this server's
	// mock clock by a whole day would drive every clock-driven sweeper in the
	// emulator through 86,400 ticks for one assertion.
}

func TestExecuteRestAPI_throttleExceeded_enforced(t *testing.T) {
	// Given enforcement is enabled and the bucket holds a single token
	srv := helpers.NewTestServer(t,
		helpers.WithMockClock(), helpers.WithEnforceAPIGatewayThrottle(true))
	apiID, stage := setupAPIKeyMethod(t, srv, "throttle-enforced")
	const value = "4444def0123456789abcdef0123456789abcdef01"
	keyID := createAPIKey(t, srv, "throttle-enforced-key", value)
	createLimitedUsagePlan(t, srv, "throttle-enforced-plan", apiID, stage, keyID,
		map[string]any{"burstLimit": 1, "rateLimit": 1}, nil)

	// When the burst is spent
	first := invokeWithKey(t, srv, apiID, stage, value)
	helpers.AssertStatus(t, first, http.StatusOK)
	first.Body.Close()

	// Then the next request is throttled with AWS's THROTTLED shape
	second := invokeWithKey(t, srv, apiID, stage, value)
	helpers.AssertStatus(t, second, http.StatusTooManyRequests)
	if got := second.Header.Get("x-amzn-ErrorType"); got != "TooManyRequestsException" {
		t.Errorf("expected x-amzn-ErrorType TooManyRequestsException, got %q", got)
	}
	body := helpers.ReadBody(t, second)
	if body != `{"message":"Too Many Requests"}` {
		t.Errorf("expected AWS throttle body, got %q", body)
	}

	// And a second of refill puts one token back
	srv.Clock.Add(time.Second)
	third := invokeWithKey(t, srv, apiID, stage, value)
	helpers.AssertStatus(t, third, http.StatusOK)
	third.Body.Close()
}

// TestExecuteRestAPI_planWithoutLimits_neverThrottles pins the "opt-in by
// construction" half: even with enforcement on, a plan that configures no
// throttle and no quota never rejects anything.
func TestExecuteRestAPI_planWithoutLimits_neverThrottles(t *testing.T) {
	// Given enforcement is on but the plan carries no limits
	srv := helpers.NewTestServer(t,
		helpers.WithMockClock(), helpers.WithEnforceAPIGatewayThrottle(true))
	apiID, stage := setupAPIKeyMethod(t, srv, "nolimits")
	const value = "5555def0123456789abcdef0123456789abcdef01"
	keyID := createAPIKey(t, srv, "nolimits-key", value)
	createLimitedUsagePlan(t, srv, "nolimits-plan", apiID, stage, keyID, nil, nil)

	// When many requests are made in the same instant
	for i := range 25 {
		resp := invokeWithKey(t, srv, apiID, stage, value)
		// Then none is rejected
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: expected 200 for an unlimited plan, got %d", i+1, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

// TestExecuteRestAPI_throttleIsPerKey verifies the token bucket is scoped to
// the (plan, key) pair rather than shared across every caller of the plan.
func TestExecuteRestAPI_throttleIsPerKey(t *testing.T) {
	// Given two keys on plans with a single-token burst each
	srv := helpers.NewTestServer(t,
		helpers.WithMockClock(), helpers.WithEnforceAPIGatewayThrottle(true))
	apiID, stage := setupAPIKeyMethod(t, srv, "perkey")
	const valueA = "6666def0123456789abcdef0123456789abcdef01"
	const valueB = "7777def0123456789abcdef0123456789abcdef01"
	keyA := createAPIKey(t, srv, "perkey-a", valueA)
	keyB := createAPIKey(t, srv, "perkey-b", valueB)
	planID := createLimitedUsagePlan(t, srv, "perkey-plan", apiID, stage, keyA,
		map[string]any{"burstLimit": 1, "rateLimit": 1}, nil)
	r := apiCall(t, srv, http.MethodPost, "/usageplans/"+planID+"/keys", map[string]any{
		"keyId": keyB, "keyType": "API_KEY",
	})
	helpers.AssertStatus(t, r, http.StatusCreated)
	r.Body.Close()

	// When key A spends its burst
	a1 := invokeWithKey(t, srv, apiID, stage, valueA)
	helpers.AssertStatus(t, a1, http.StatusOK)
	a1.Body.Close()
	a2 := invokeWithKey(t, srv, apiID, stage, valueA)
	helpers.AssertStatus(t, a2, http.StatusTooManyRequests)
	a2.Body.Close()

	// Then key B still has its own
	b1 := invokeWithKey(t, srv, apiID, stage, valueB)
	helpers.AssertStatus(t, b1, http.StatusOK)
	b1.Body.Close()
}

// ─── GetUsage — the reporting surface ─────────────────────────────────────

func TestGetUsage_reportsUsedAndRemaining(t *testing.T) {
	// Given two served requests against a plan with a daily quota of 10
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	apiID, stage := setupAPIKeyMethod(t, srv, "usage-report")
	const value = "8888def0123456789abcdef0123456789abcdef01"
	keyID := createAPIKey(t, srv, "usage-report-key", value)
	planID := createLimitedUsagePlan(t, srv, "usage-report-plan", apiID, stage, keyID,
		nil, map[string]any{"limit": 10, "period": "DAY"})
	for range 2 {
		resp := invokeWithKey(t, srv, apiID, stage, value)
		helpers.AssertStatus(t, resp, http.StatusOK)
		resp.Body.Close()
	}

	// When usage is read back for today (the mock clock starts at the epoch)
	resp := apiCall(t, srv, http.MethodGet,
		"/usageplans/"+planID+"/usage?startDate=1970-01-01&endDate=1970-01-01", nil)
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out map[string]any
	helpers.DecodeJSON(t, resp, &out)

	// Then AWS's Usage shape reports [used, remaining] for the key
	if out["usagePlanId"] != planID {
		t.Errorf("expected usagePlanId %q, got %v", planID, out["usagePlanId"])
	}
	if out["startDate"] != "1970-01-01" || out["endDate"] != "1970-01-01" {
		t.Errorf("unexpected date echo: %v / %v", out["startDate"], out["endDate"])
	}
	values, ok := out["values"].(map[string]any)
	if !ok {
		t.Fatalf("expected a `values` map, got %#v", out["values"])
	}
	days, ok := values[keyID].([]any)
	if !ok || len(days) != 1 {
		t.Fatalf("expected one daily entry for key %s, got %#v", keyID, values[keyID])
	}
	entry, ok := days[0].([]any)
	if !ok || len(entry) != 2 {
		t.Fatalf("expected a [used, remaining] pair, got %#v", days[0])
	}
	if fmt.Sprint(entry[0]) != "2" {
		t.Errorf("expected used=2, got %v", entry[0])
	}
	if fmt.Sprint(entry[1]) != "8" {
		t.Errorf("expected remaining=8, got %v", entry[1])
	}
}

func TestGetUsage_filtersByKeyId(t *testing.T) {
	// Given a plan with two keys, only one of which was used
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	apiID, stage := setupAPIKeyMethod(t, srv, "usage-filter")
	const valueA = "9999def0123456789abcdef0123456789abcdef01"
	const valueB = "aaaadef0123456789abcdef0123456789abcdef01"
	keyA := createAPIKey(t, srv, "usage-filter-a", valueA)
	keyB := createAPIKey(t, srv, "usage-filter-b", valueB)
	planID := createLimitedUsagePlan(t, srv, "usage-filter-plan", apiID, stage, keyA, nil, nil)
	r := apiCall(t, srv, http.MethodPost, "/usageplans/"+planID+"/keys", map[string]any{
		"keyId": keyB, "keyType": "API_KEY",
	})
	helpers.AssertStatus(t, r, http.StatusCreated)
	r.Body.Close()
	resp := invokeWithKey(t, srv, apiID, stage, valueA)
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// When usage is requested for key B only
	got := apiCall(t, srv, http.MethodGet,
		"/usageplans/"+planID+"/usage?startDate=1970-01-01&endDate=1970-01-01&keyId="+keyB, nil)
	helpers.AssertStatus(t, got, http.StatusOK)
	var out map[string]any
	helpers.DecodeJSON(t, got, &out)

	// Then only that key is present, with a zero count
	values, _ := out["values"].(map[string]any)
	if _, present := values[keyA]; present {
		t.Errorf("keyId filter leaked key %s: %#v", keyA, values)
	}
	days, ok := values[keyB].([]any)
	if !ok || len(days) != 1 {
		t.Fatalf("expected one daily entry for key %s, got %#v", keyB, values[keyB])
	}
	entry := days[0].([]any)
	if fmt.Sprint(entry[0]) != "0" {
		t.Errorf("expected used=0 for the unused key, got %v", entry[0])
	}
}

func TestGetUsage_missingDates(t *testing.T) {
	// Given any usage plan
	srv := helpers.NewTestServer(t)
	resp := apiCall(t, srv, http.MethodPost, "/usageplans", map[string]any{"name": "usage-dates"})
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var out map[string]any
	helpers.DecodeJSON(t, resp, &out)
	planID := out["id"].(string)

	// When startDate/endDate are omitted
	got := apiCall(t, srv, http.MethodGet, "/usageplans/"+planID+"/usage", nil)

	// Then AWS's BadRequestException is returned
	helpers.AssertStatus(t, got, http.StatusBadRequest)
	got.Body.Close()
}

func TestGetUsage_unknownPlan(t *testing.T) {
	// Given a plan ID that does not exist
	srv := helpers.NewTestServer(t)

	// When usage is requested for it
	got := apiCall(t, srv, http.MethodGet,
		"/usageplans/nosuch/usage?startDate=1970-01-01&endDate=1970-01-01", nil)

	// Then AWS's NotFoundException is returned
	helpers.AssertStatus(t, got, http.StatusNotFound)
	got.Body.Close()
}
