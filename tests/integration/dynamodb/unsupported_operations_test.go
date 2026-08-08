package dynamodb_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/Neaox/overcast/internal/awsapi"
	"github.com/Neaox/overcast/tests/helpers"
)

// A DynamoDB operation AWS models but Overcast has not implemented must answer
// the repo's standard not-implemented response — HTTP 501 in the service's own
// AWS JSON 1.0 envelope, with the automatic unsupported marker and request ID —
// rather than the 400 UnknownOperationException reserved for targets that name
// no AWS operation at all.
func TestDynamoDB_modeledUnsupportedOperationsReturn501(t *testing.T) {
	operations := []string{
		"CreateGlobalTable",
		"DescribeGlobalTable",
		"DescribeGlobalTableSettings",
		"ListGlobalTables",
		"UpdateGlobalTable",
		"UpdateGlobalTableSettings",
		"ExecuteStatement",
		"ListBackups",
		"RestoreTableFromBackup",
	}

	for _, operation := range operations {
		t.Run(operation, func(t *testing.T) {
			// Given: a running emulator.
			srv := helpers.NewTestServer(t)

			// When: a modeled but unimplemented operation is invoked.
			resp := ddbCall(t, srv, operation, map[string]any{})
			defer resp.Body.Close()

			// Then: it is refused honestly as not implemented.
			helpers.AssertStatus(t, resp, http.StatusNotImplemented)
			helpers.AssertHeader(t, resp, "x-emulator-unsupported", "true")
			helpers.AssertRequestID(t, resp)
			helpers.AssertJSONError(t, resp, "NotImplemented")
		})
	}
}

// TestDynamoDB_noModeledOperationIsUnknown drives every DynamoDB operation in
// the generated AWS corpus through the router and asserts none of them is
// answered as an unknown target. It is deliberately corpus-driven rather than a
// second hand-written list: a list would go stale the next time the pinned
// models add an operation, which is exactly how the GlobalTable operations came
// to answer UnknownOperationException.
//
// Implemented operations answer their own validation errors for an empty body,
// which is fine — the only forbidden answer is "I have never heard of this".
func TestDynamoDB_noModeledOperationIsUnknown(t *testing.T) {
	srv := helpers.NewTestServer(t)

	awsapi.WalkOperations(func(op awsapi.Operation) bool {
		if awsapi.ServiceKey(op.Service) != "dynamodb" {
			return true
		}
		if errType := ddbErrorType(t, srv, op.Name); errType == "UnknownOperationException" {
			t.Errorf("%s answered UnknownOperationException; a modeled operation must be implemented or return 501", op.Name)
		}
		return true
	})
}

// ddbErrorType invokes an operation with an empty body and returns the AWS
// error type it answered with, or "" when it did not answer with an error.
func ddbErrorType(t *testing.T, srv *helpers.TestServer, operation string) string {
	t.Helper()
	resp := ddbCall(t, srv, operation, map[string]any{})
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s: read body: %v", operation, err)
	}
	var errResp struct {
		Type string `json:"__type"`
	}
	_ = json.Unmarshal(body, &errResp)
	return errResp.Type
}

func TestDynamoDB_unknownTargetKeepsUnknownOperationException(t *testing.T) {
	// Given: a running emulator.
	srv := helpers.NewTestServer(t)

	// When: a target names no AWS DynamoDB operation.
	resp := ddbCall(t, srv, "DefinitelyNotAnOperation", map[string]any{})
	defer resp.Body.Close()

	// Then: AWS's unknown-operation rejection is preserved.
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "UnknownOperationException")
	if got := resp.Header.Get("x-emulator-unsupported"); got != "" {
		t.Errorf("x-emulator-unsupported = %q, want it absent for an unknown target", got)
	}
}
