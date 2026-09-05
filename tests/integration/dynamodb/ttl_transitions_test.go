package dynamodb_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// TTL status changes are asynchronous on AWS: UpdateTimeToLive returns while
// the table is still ENABLING or DISABLING, "it can take up to one hour for
// the change to fully process", and any further UpdateTimeToLive call for the
// same table inside that window is refused with a ValidationException.
//
//	https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_UpdateTimeToLive.html
//	https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_TimeToLiveDescription.html
//
// Overcast compresses AWS's hour into ttlTransitionDuration so the
// intermediate states are observable without a test waiting an hour; these
// tests drive the mock clock across that window rather than sleeping.
const ttlSettleWindow = 30 * time.Second

func updateTTL(t *testing.T, srv *helpers.TestServer, table string, enabled bool, attribute string) *http.Response {
	t.Helper()
	return ddbCall(t, srv, "UpdateTimeToLive", map[string]any{
		"TableName": table,
		"TimeToLiveSpecification": map[string]any{
			"Enabled":       enabled,
			"AttributeName": attribute,
		},
	})
}

// updateTTLOK issues an UpdateTimeToLive that is expected to succeed.
func updateTTLOK(t *testing.T, srv *helpers.TestServer, table string, enabled bool, attribute string) {
	t.Helper()
	resp := updateTTL(t, srv, table, enabled, attribute)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("UpdateTimeToLive(%q, enabled=%v): status %d: %s",
			table, enabled, resp.StatusCode, helpers.ReadBody(t, resp))
	}
}

// assertTTLDescription reads DescribeTimeToLive and checks both members.
func assertTTLDescription(t *testing.T, srv *helpers.TestServer, table, wantStatus, wantAttribute string) {
	t.Helper()
	resp := ddbCall(t, srv, "DescribeTimeToLive", map[string]any{"TableName": table})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		TimeToLiveDescription struct {
			TimeToLiveStatus string `json:"TimeToLiveStatus"`
			AttributeName    string `json:"AttributeName"`
		} `json:"TimeToLiveDescription"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if got := result.TimeToLiveDescription.TimeToLiveStatus; got != wantStatus {
		t.Errorf("TimeToLiveStatus = %q, want %q", got, wantStatus)
	}
	if got := result.TimeToLiveDescription.AttributeName; got != wantAttribute {
		t.Errorf("AttributeName = %q, want %q", got, wantAttribute)
	}
}

// assertTTLError checks that an UpdateTimeToLive response carries the given
// AWS error code and message.
func assertTTLError(t *testing.T, resp *http.Response, wantCode, wantMessage string) {
	t.Helper()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	var errResp struct {
		Code    string `json:"__type"`
		Message string `json:"Message"`
	}
	helpers.DecodeJSON(t, resp, &errResp)
	if errResp.Code != wantCode {
		t.Errorf("__type = %q, want %q", errResp.Code, wantCode)
	}
	if errResp.Message != wantMessage {
		t.Errorf("Message = %q, want %q", errResp.Message, wantMessage)
	}
}

func TestUpdateTimeToLive_responseEchoesTheSpecification(t *testing.T) {
	// Given: a table with no TTL
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createTable(t, srv, "ttl-response")

	// When: TTL is enabled
	resp := updateTTL(t, srv, "ttl-response", true, "expiresAt")
	defer resp.Body.Close()

	// Then: UpdateTimeToLive answers with TimeToLiveSpecification, not the
	// TimeToLiveDescription DescribeTimeToLive returns — the accepted request
	// is echoed back while the change is still processing
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		TimeToLiveSpecification *struct {
			Enabled       bool   `json:"Enabled"`
			AttributeName string `json:"AttributeName"`
		} `json:"TimeToLiveSpecification"`
		TimeToLiveDescription any `json:"TimeToLiveDescription"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.TimeToLiveDescription != nil {
		t.Errorf("UpdateTimeToLive returned a TimeToLiveDescription member: %v", result.TimeToLiveDescription)
	}
	if result.TimeToLiveSpecification == nil {
		t.Fatal("UpdateTimeToLive returned no TimeToLiveSpecification")
	}
	if !result.TimeToLiveSpecification.Enabled {
		t.Error("Enabled = false, want true")
	}
	if got := result.TimeToLiveSpecification.AttributeName; got != "expiresAt" {
		t.Errorf("AttributeName = %q, want \"expiresAt\"", got)
	}
}

func TestDescribeTimeToLive_freshTableIsDisabledWithoutAttribute(t *testing.T) {
	// Given: a table that has never had TTL configured
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createTable(t, srv, "ttl-fresh")

	// When/Then: DescribeTimeToLive reports DISABLED and names no attribute
	assertTTLDescription(t, srv, "ttl-fresh", "DISABLED", "")
}

func TestUpdateTimeToLive_enableIsEnablingUntilTheWindowElapses(t *testing.T) {
	// Given: a table with no TTL
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createTable(t, srv, "ttl-enabling")

	// When: TTL is enabled
	updateTTLOK(t, srv, "ttl-enabling", true, "expiresAt")

	// Then: the change is still processing
	assertTTLDescription(t, srv, "ttl-enabling", "ENABLING", "expiresAt")

	// And: it settles once the transition window has elapsed
	srv.Clock.Add(ttlSettleWindow)
	assertTTLDescription(t, srv, "ttl-enabling", "ENABLED", "expiresAt")
}

func TestUpdateTimeToLive_disableIsDisablingUntilTheWindowElapses(t *testing.T) {
	// Given: a table whose TTL is already ENABLED
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createTable(t, srv, "ttl-disabling")
	updateTTLOK(t, srv, "ttl-disabling", true, "expiresAt")
	srv.Clock.Add(ttlSettleWindow)

	// When: TTL is disabled
	updateTTLOK(t, srv, "ttl-disabling", false, "expiresAt")

	// Then: the change is still processing, and still names the attribute
	assertTTLDescription(t, srv, "ttl-disabling", "DISABLING", "expiresAt")

	// And: it settles to DISABLED with no attribute
	srv.Clock.Add(ttlSettleWindow)
	assertTTLDescription(t, srv, "ttl-disabling", "DISABLED", "")
}

func TestUpdateTimeToLive_secondRequestDuringTransitionIsRejected(t *testing.T) {
	// Given: a table whose TTL enable is still processing
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createTable(t, srv, "ttl-inflight")
	updateTTLOK(t, srv, "ttl-inflight", true, "expiresAt")

	// When: a second UpdateTimeToLive lands inside the transition window
	resp := updateTTL(t, srv, "ttl-inflight", false, "expiresAt")
	defer resp.Body.Close()

	// Then: AWS's fixed-interval ValidationException
	assertTTLError(t, resp, "ValidationException",
		"Time to live has been modified multiple times within a fixed interval")

	// And: the rejected request did not mutate the pending transition
	assertTTLDescription(t, srv, "ttl-inflight", "ENABLING", "expiresAt")
	srv.Clock.Add(ttlSettleWindow)
	assertTTLDescription(t, srv, "ttl-inflight", "ENABLED", "expiresAt")
}

func TestUpdateTimeToLive_enablingAnAlreadyEnabledTableIsRejected(t *testing.T) {
	// Given: a table whose TTL is settled on ENABLED
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createTable(t, srv, "ttl-reenable")
	updateTTLOK(t, srv, "ttl-reenable", true, "expiresAt")
	srv.Clock.Add(ttlSettleWindow)

	// When: TTL is enabled again with the same attribute
	resp := updateTTL(t, srv, "ttl-reenable", true, "expiresAt")
	defer resp.Body.Close()

	// Then: UpdateTimeToLive is not idempotent — AWS refuses it
	assertTTLError(t, resp, "ValidationException", "TimeToLive is already enabled")

	// And: nothing changed
	assertTTLDescription(t, srv, "ttl-reenable", "ENABLED", "expiresAt")
}

func TestUpdateTimeToLive_changingTheAttributeWhileEnabledIsRejected(t *testing.T) {
	// Given: a table whose TTL is settled on ENABLED
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createTable(t, srv, "ttl-swap")
	updateTTLOK(t, srv, "ttl-swap", true, "expiresAt")
	srv.Clock.Add(ttlSettleWindow)

	// When: the attribute name is changed without disabling first
	resp := updateTTL(t, srv, "ttl-swap", true, "expiresAtV2")
	defer resp.Body.Close()

	// Then: refused — the attribute can only change through a disable
	assertTTLError(t, resp, "ValidationException", "TimeToLive is already enabled")

	// And: the original attribute is untouched
	assertTTLDescription(t, srv, "ttl-swap", "ENABLED", "expiresAt")

	// And: disabling first, then re-enabling, is the supported route
	updateTTLOK(t, srv, "ttl-swap", false, "expiresAt")
	srv.Clock.Add(ttlSettleWindow)
	updateTTLOK(t, srv, "ttl-swap", true, "expiresAtV2")
	srv.Clock.Add(ttlSettleWindow)
	assertTTLDescription(t, srv, "ttl-swap", "ENABLED", "expiresAtV2")
}

func TestUpdateTimeToLive_disablingAnAlreadyDisabledTableIsRejected(t *testing.T) {
	// Given: a table that has never had TTL enabled
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createTable(t, srv, "ttl-redisable")

	// When: TTL is disabled
	resp := updateTTL(t, srv, "ttl-redisable", false, "expiresAt")
	defer resp.Body.Close()

	// Then: AWS refuses the no-op disable
	assertTTLError(t, resp, "ValidationException", "TimeToLive is already disabled")

	// And: nothing was written
	assertTTLDescription(t, srv, "ttl-redisable", "DISABLED", "")
}

func TestUpdateTimeToLive_specificationIsValidatedBeforeTheTableIsLookedUp(t *testing.T) {
	// Given: a server with no tables at all
	srv := helpers.NewTestServer(t, helpers.WithMockClock())

	// When: a malformed specification names a table that does not exist
	resp := ddbCall(t, srv, "UpdateTimeToLive", map[string]any{
		"TableName":               "no-such-table",
		"TimeToLiveSpecification": map[string]any{"Enabled": true},
	})
	defer resp.Body.Close()

	// Then: the request shape is rejected before the lookup, as AWS does
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	var errResp struct {
		Code    string `json:"__type"`
		Message string `json:"Message"`
	}
	helpers.DecodeJSON(t, resp, &errResp)
	if errResp.Code != "ValidationException" {
		t.Errorf("__type = %q, want ValidationException", errResp.Code)
	}
	if !strings.Contains(errResp.Message, "timeToLiveSpecification.attributeName") {
		t.Errorf("Message = %q, want it to name timeToLiveSpecification.attributeName", errResp.Message)
	}
}

func TestUpdateTimeToLive_missingTableIsResourceNotFound(t *testing.T) {
	// Given: a server with no tables at all
	srv := helpers.NewTestServer(t, helpers.WithMockClock())

	// When: a well-formed specification names a table that does not exist
	resp := updateTTL(t, srv, "no-such-table", true, "expiresAt")
	defer resp.Body.Close()

	// Then: ResourceNotFoundException
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	var errResp struct {
		Code string `json:"__type"`
	}
	helpers.DecodeJSON(t, resp, &errResp)
	if errResp.Code != "ResourceNotFoundException" {
		t.Errorf("__type = %q, want ResourceNotFoundException", errResp.Code)
	}
}

func TestUpdateTimeToLive_specificationMembersAreRequired(t *testing.T) {
	// Given: a table
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createTable(t, srv, "ttl-validation")

	tests := []struct {
		name     string
		spec     any
		wantPath string
	}{
		{"missing specification", nil, "timeToLiveSpecification"},
		{"missing Enabled", map[string]any{"AttributeName": "expiresAt"}, "timeToLiveSpecification.enabled"},
		{"missing AttributeName", map[string]any{"Enabled": true}, "timeToLiveSpecification.attributeName"},
		{"empty AttributeName", map[string]any{"Enabled": false, "AttributeName": ""}, "timeToLiveSpecification.attributeName"},
		{"AttributeName too long", map[string]any{"Enabled": true, "AttributeName": strings.Repeat("a", 256)}, "timeToLiveSpecification.attributeName"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// When: the specification is sent
			body := map[string]any{"TableName": "ttl-validation"}
			if tc.spec != nil {
				body["TimeToLiveSpecification"] = tc.spec
			}
			resp := ddbCall(t, srv, "UpdateTimeToLive", body)
			defer resp.Body.Close()

			// Then: a ValidationException naming the offending member
			helpers.AssertStatus(t, resp, http.StatusBadRequest)
			var errResp struct {
				Code    string `json:"__type"`
				Message string `json:"Message"`
			}
			helpers.DecodeJSON(t, resp, &errResp)
			if errResp.Code != "ValidationException" {
				t.Errorf("__type = %q, want ValidationException", errResp.Code)
			}
			if !strings.Contains(errResp.Message, tc.wantPath) {
				t.Errorf("Message = %q, want it to name %q", errResp.Message, tc.wantPath)
			}

			// And: the table's TTL was not touched
			assertTTLDescription(t, srv, "ttl-validation", "DISABLED", "")
		})
	}
}

// TestRPCv2CBOR_UpdateTimeToLive pins the TTL request shape over DynamoDB's
// other wire protocol. TimeToLiveSpecification's members are pointers on the
// request side so an omitted one can be rejected the way AWS rejects it, and
// that is exactly the kind of change a codec can decode differently from JSON.
func TestRPCv2CBOR_UpdateTimeToLive(t *testing.T) {
	// Given: a table with no TTL
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createTable(t, srv, "ttl-cbor")

	// When: TTL is enabled over RPC v2 CBOR
	resp := dynamodbCBORCall(t, srv, "UpdateTimeToLive", map[string]any{
		"TableName": "ttl-cbor",
		"TimeToLiveSpecification": map[string]any{
			"Enabled":       true,
			"AttributeName": "expiresAt",
		},
	})
	defer resp.Body.Close()

	// Then: the specification is echoed back, and the transition has started
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		TimeToLiveSpecification struct {
			Enabled       bool   `cbor:"Enabled"`
			AttributeName string `cbor:"AttributeName"`
		} `cbor:"TimeToLiveSpecification"`
	}
	if err := cbor.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode CBOR response: %v", err)
	}
	if !out.TimeToLiveSpecification.Enabled || out.TimeToLiveSpecification.AttributeName != "expiresAt" {
		t.Errorf("TimeToLiveSpecification = %+v, want enabled expiresAt", out.TimeToLiveSpecification)
	}
	assertTTLDescription(t, srv, "ttl-cbor", "ENABLING", "expiresAt")
}
