package dynamodb_test

import (
	"net/http"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// CreateTable billing-mode defaulting and throughput validation.
//
// AWS evidence (see .changelog fragment and docs/dev/compatibility/services/dynamodb.yaml):
//   - CreateTableInput.ProvisionedThroughput, pinned Smithy model
//     (models/aws revision 66e973ca): "If you set BillingMode as PROVISIONED,
//     you must specify this property. If you set BillingMode as
//     PAY_PER_REQUEST, you cannot specify this property."
//   - BillingMode defaults to PROVISIONED when omitted (AWS CreateTable API
//     reference; the requirement above is what makes ProvisionedThroughput
//     mandatory for a request that names no billing mode).
//   - ProvisionedThroughput.ReadCapacityUnits / .WriteCapacityUnits are both
//     `smithy.api#required` and target PositiveLongObject (range min 1), so an
//     absent or zero unit count is not "specified".

const (
	wantThroughputRequired  = "One or more parameter values were invalid: ReadCapacityUnits and WriteCapacityUnits must both be specified when BillingMode is PROVISIONED"
	wantThroughputForbidden = "One or more parameter values were invalid: Neither ReadCapacityUnits nor WriteCapacityUnits can be specified when BillingMode is PAY_PER_REQUEST"
)

// billingRequest builds a minimal CreateTable body, letting each case decide
// what to say about billing mode and throughput.
func billingRequest(name string, extra map[string]any) map[string]any {
	body := map[string]any{
		"TableName":            name,
		"AttributeDefinitions": []map[string]any{{"AttributeName": "id", "AttributeType": "S"}},
		"KeySchema":            []map[string]any{{"AttributeName": "id", "KeyType": "HASH"}},
	}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

func TestCreateTable_billingModeThroughputCombinations(t *testing.T) {
	throughput := map[string]any{"ReadCapacityUnits": 5, "WriteCapacityUnits": 5}

	cases := []struct {
		name        string
		extra       map[string]any
		wantStatus  int
		wantMessage string
	}{
		{
			// AWS default: no BillingMode means PROVISIONED, which requires throughput.
			name:       "omitted billing mode with throughput is provisioned",
			extra:      map[string]any{"ProvisionedThroughput": throughput},
			wantStatus: http.StatusOK,
		},
		{
			name:        "omitted billing mode without throughput is rejected",
			extra:       map[string]any{},
			wantStatus:  http.StatusBadRequest,
			wantMessage: wantThroughputRequired,
		},
		{
			name:       "explicit provisioned with throughput",
			extra:      map[string]any{"BillingMode": "PROVISIONED", "ProvisionedThroughput": throughput},
			wantStatus: http.StatusOK,
		},
		{
			name:        "explicit provisioned without throughput is rejected",
			extra:       map[string]any{"BillingMode": "PROVISIONED"},
			wantStatus:  http.StatusBadRequest,
			wantMessage: wantThroughputRequired,
		},
		{
			// Both units are required members: one alone is not "specified".
			name:        "explicit provisioned with only read units is rejected",
			extra:       map[string]any{"BillingMode": "PROVISIONED", "ProvisionedThroughput": map[string]any{"ReadCapacityUnits": 5}},
			wantStatus:  http.StatusBadRequest,
			wantMessage: wantThroughputRequired,
		},
		{
			name:       "pay per request without throughput",
			extra:      map[string]any{"BillingMode": "PAY_PER_REQUEST"},
			wantStatus: http.StatusOK,
		},
		{
			name:        "pay per request with throughput is rejected",
			extra:       map[string]any{"BillingMode": "PAY_PER_REQUEST", "ProvisionedThroughput": throughput},
			wantStatus:  http.StatusBadRequest,
			wantMessage: wantThroughputForbidden,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a running emulator.
			srv := helpers.NewTestServer(t)

			// When: CreateTable is called with this billing-mode/throughput combination.
			resp := ddbCall(t, srv, "CreateTable", billingRequest("billing-table", tc.extra))
			defer resp.Body.Close()

			// Then: DynamoDB answers as AWS does for that combination.
			helpers.AssertStatus(t, resp, tc.wantStatus)
			if tc.wantStatus == http.StatusOK {
				return
			}
			assertValidationMessage(t, resp, tc.wantMessage)
		})
	}
}

func TestCreateTable_globalSecondaryIndexThroughputCombinations(t *testing.T) {
	throughput := map[string]any{"ReadCapacityUnits": 5, "WriteCapacityUnits": 5}
	gsi := func(withThroughput bool) []map[string]any {
		index := map[string]any{
			"IndexName":  "by-email",
			"KeySchema":  []map[string]any{{"AttributeName": "email", "KeyType": "HASH"}},
			"Projection": map[string]any{"ProjectionType": "ALL"},
		}
		if withThroughput {
			index["ProvisionedThroughput"] = throughput
		}
		return []map[string]any{index}
	}
	attrs := []map[string]any{
		{"AttributeName": "id", "AttributeType": "S"},
		{"AttributeName": "email", "AttributeType": "S"},
	}

	cases := []struct {
		name        string
		extra       map[string]any
		wantStatus  int
		wantMessage string
	}{
		{
			name: "provisioned table with index throughput",
			extra: map[string]any{
				"BillingMode":            "PROVISIONED",
				"ProvisionedThroughput":  throughput,
				"GlobalSecondaryIndexes": gsi(true),
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "provisioned table without index throughput is rejected",
			extra: map[string]any{
				"BillingMode":            "PROVISIONED",
				"ProvisionedThroughput":  throughput,
				"GlobalSecondaryIndexes": gsi(false),
			},
			wantStatus:  http.StatusBadRequest,
			wantMessage: "One or more parameter values were invalid: Both ReadCapacityUnits and WriteCapacityUnits must be specified for index: by-email",
		},
		{
			name: "pay per request table without index throughput",
			extra: map[string]any{
				"BillingMode":            "PAY_PER_REQUEST",
				"GlobalSecondaryIndexes": gsi(false),
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "pay per request table with index throughput is rejected",
			extra: map[string]any{
				"BillingMode":            "PAY_PER_REQUEST",
				"GlobalSecondaryIndexes": gsi(true),
			},
			wantStatus:  http.StatusBadRequest,
			wantMessage: "One or more parameter values were invalid: Neither ReadCapacityUnits nor WriteCapacityUnits can be specified when BillingMode is PAY_PER_REQUEST for index: by-email",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a running emulator.
			srv := helpers.NewTestServer(t)

			// When: CreateTable declares a GSI under this billing mode.
			body := billingRequest("gsi-billing-table", tc.extra)
			body["AttributeDefinitions"] = attrs
			resp := ddbCall(t, srv, "CreateTable", body)
			defer resp.Body.Close()

			// Then: the per-index throughput rules match AWS.
			helpers.AssertStatus(t, resp, tc.wantStatus)
			if tc.wantStatus == http.StatusOK {
				return
			}
			assertValidationMessage(t, resp, tc.wantMessage)
		})
	}
}

func TestCreateTable_invalidBillingModeRejected(t *testing.T) {
	// Given: a running emulator.
	srv := helpers.NewTestServer(t)

	// When: CreateTable names a billing mode outside the modeled enum.
	resp := ddbCall(t, srv, "CreateTable", billingRequest("bad-billing-table", map[string]any{
		"BillingMode": "ON_DEMAND",
	}))
	defer resp.Body.Close()

	// Then: the request is rejected with AWS's constraint-violation wording.
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	assertValidationMessage(t, resp,
		"1 validation error detected: Value 'ON_DEMAND' at 'billingMode' failed to satisfy constraint: Member must satisfy enum value set: [PROVISIONED, PAY_PER_REQUEST]")
}

func TestCreateTable_rejectedRequestCreatesNoTable(t *testing.T) {
	// Given: a running emulator.
	srv := helpers.NewTestServer(t)

	// When: CreateTable is rejected for an invalid throughput combination.
	resp := ddbCall(t, srv, "CreateTable", billingRequest("unborn-table", map[string]any{
		"BillingMode":           "PAY_PER_REQUEST",
		"ProvisionedThroughput": map[string]any{"ReadCapacityUnits": 5, "WriteCapacityUnits": 5},
	}))
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)

	// Then: nothing was persisted — validation runs before any mutation.
	desc := ddbCall(t, srv, "DescribeTable", map[string]any{"TableName": "unborn-table"})
	defer desc.Body.Close()
	helpers.AssertStatus(t, desc, http.StatusBadRequest)
	helpers.AssertJSONError(t, desc, "ResourceNotFoundException")
}

func TestCreateTable_defaultedProvisionedTableReportsNoBillingModeSummary(t *testing.T) {
	// Given: a table created without an explicit BillingMode.
	srv := helpers.NewTestServer(t)
	resp := ddbCall(t, srv, "CreateTable", billingRequest("defaulted-table", map[string]any{
		"ProvisionedThroughput": map[string]any{"ReadCapacityUnits": 3, "WriteCapacityUnits": 4},
	}))
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// When: the table is described.
	desc := ddbCall(t, srv, "DescribeTable", map[string]any{"TableName": "defaulted-table"})
	var result struct {
		Table struct {
			BillingModeSummary    *struct{ BillingMode string } `json:"BillingModeSummary"`
			ProvisionedThroughput *struct {
				ReadCapacityUnits  int64
				WriteCapacityUnits int64
			} `json:"ProvisionedThroughput"`
		} `json:"Table"`
	}
	helpers.DecodeJSON(t, desc, &result)

	// Then: AWS reports no BillingModeSummary for a table left on the default
	// PROVISIONED mode, and echoes the throughput that was supplied.
	if result.Table.BillingModeSummary != nil {
		t.Errorf("expected no BillingModeSummary for a defaulted table, got %+v", result.Table.BillingModeSummary)
	}
	if result.Table.ProvisionedThroughput == nil {
		t.Fatal("expected ProvisionedThroughput to be echoed")
	}
	if result.Table.ProvisionedThroughput.ReadCapacityUnits != 3 ||
		result.Table.ProvisionedThroughput.WriteCapacityUnits != 4 {
		t.Errorf("unexpected throughput echo: %+v", result.Table.ProvisionedThroughput)
	}
}

// assertValidationMessage checks both the modeled error type and its message,
// because the message is the part callers read and AWS's wording is specific.
func assertValidationMessage(t *testing.T, resp *http.Response, want string) {
	t.Helper()
	var errResp struct {
		Type    string `json:"__type"`
		Message string `json:"message"`
	}
	helpers.DecodeJSON(t, resp, &errResp)
	if errResp.Type != "ValidationException" {
		t.Errorf("expected ValidationException, got %q", errResp.Type)
	}
	if errResp.Message != want {
		t.Errorf("message mismatch\n got: %s\nwant: %s", errResp.Message, want)
	}
}
