// golden_test.go contains wire-byte golden tests for SSM — see
// docs/plans/wire-byte-goldens.md for the harness design.
//
// SSM (Parameter Store) is one of the pilots for the P2 delegation sweep
// (docs/plans/level2-codegen.md §3 Track 2, §4 phase P2): its paginated
// operations (DescribeParameters, GetParameterHistory, GetParametersByPath)
// are exactly the divergence class PR #271 fixed once, with nothing
// structurally preventing a future regression. These goldens pin the exact
// legacy-path response bytes so a later change that routes JSON traffic
// through the typed dispatcher (or deletes the legacy handler) can be
// verified byte-identical with zero test-code changes — see GoldenTest's
// doc comment in tests/helpers/golden.go for the record → assert lifecycle.
//
// Coverage — every SSM operation is deterministic under a mock clock stopped
// at the Unix epoch (no UUIDs, no random IDs; NextToken is a base64-encoded
// array index, not random — see internal/serviceutil/pagination.go), so all
// ten registered operations are covered:
//
//	PutParameter, GetParameter, GetParameters, GetParametersByPath,
//	DescribeParameters, GetParameterHistory, AddTagsToResource,
//	ListTagsForResource, DeleteParameter, DeleteParameters
//
// No SSM operations are excluded.
//
// Record: go test ./tests/integration/ssm/ -run TestGolden -record
// Assert: go test ./tests/integration/ssm/ -run TestGolden
package ssm_test

import (
	"net/http"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

const ssmGoldenDir = "goldens"

func TestGolden_PutParameter(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())

	resp := ssmCall(t, srv, "PutParameter", map[string]any{
		"Name": "/golden/param", "Value": "hello", "Type": "String",
	})
	helpers.GoldenTest(t, ssmGoldenDir, "PutParameter", resp, nil)
}

func TestGolden_GetParameter(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	putParam(t, srv, "/golden/param", "hello", "String", false)

	resp := ssmCall(t, srv, "GetParameter", map[string]any{"Name": "/golden/param"})
	helpers.GoldenTest(t, ssmGoldenDir, "GetParameter", resp, nil)
}

func TestGolden_GetParameters(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	putParam(t, srv, "/golden/a", "va", "String", false)
	putParam(t, srv, "/golden/b", "vb", "String", false)

	resp := ssmCall(t, srv, "GetParameters", map[string]any{
		"Names": []string{"/golden/a", "/golden/b", "/golden/missing"},
	})
	helpers.GoldenTest(t, ssmGoldenDir, "GetParameters", resp, nil)
}

func TestGolden_GetParametersByPath(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	putParam(t, srv, "/golden/path/a", "va", "String", false)
	putParam(t, srv, "/golden/path/b", "vb", "String", false)

	resp := ssmCall(t, srv, "GetParametersByPath", map[string]any{
		"Path": "/golden/path", "Recursive": true,
	})
	helpers.GoldenTest(t, ssmGoldenDir, "GetParametersByPath", resp, nil)
}

func TestGolden_DescribeParameters(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	putParam(t, srv, "/golden/describe/a", "va", "String", false)
	putParam(t, srv, "/golden/describe/b", "vb", "StringList", false)

	resp := ssmCall(t, srv, "DescribeParameters", map[string]any{})
	helpers.GoldenTest(t, ssmGoldenDir, "DescribeParameters", resp, nil)
}

func TestGolden_GetParameterHistory(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	putParam(t, srv, "/golden/history", "v1", "String", false)
	putParam(t, srv, "/golden/history", "v2", "String", true)

	resp := ssmCall(t, srv, "GetParameterHistory", map[string]any{"Name": "/golden/history"})
	helpers.GoldenTest(t, ssmGoldenDir, "GetParameterHistory", resp, nil)
}

func TestGolden_AddTagsToResource(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	putParam(t, srv, "/golden/tags", "v1", "String", false)

	resp := ssmCall(t, srv, "AddTagsToResource", map[string]any{
		"ResourceType": "Parameter",
		"ResourceId":   "/golden/tags",
		"Tags":         []map[string]string{{"Key": "env", "Value": "test"}},
	})
	helpers.GoldenTest(t, ssmGoldenDir, "AddTagsToResource", resp, nil)
}

func TestGolden_ListTagsForResource(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	putParam(t, srv, "/golden/tags", "v1", "String", false)
	resp := ssmCall(t, srv, "AddTagsToResource", map[string]any{
		"ResourceType": "Parameter",
		"ResourceId":   "/golden/tags",
		"Tags":         []map[string]string{{"Key": "env", "Value": "test"}},
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	resp = ssmCall(t, srv, "ListTagsForResource", map[string]any{
		"ResourceType": "Parameter", "ResourceId": "/golden/tags",
	})
	helpers.GoldenTest(t, ssmGoldenDir, "ListTagsForResource", resp, nil)
}

func TestGolden_DeleteParameter(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	putParam(t, srv, "/golden/delete", "v1", "String", false)

	resp := ssmCall(t, srv, "DeleteParameter", map[string]any{"Name": "/golden/delete"})
	helpers.GoldenTest(t, ssmGoldenDir, "DeleteParameter", resp, nil)
}

func TestGolden_DeleteParameters(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	putParam(t, srv, "/golden/delete/a", "va", "String", false)
	putParam(t, srv, "/golden/delete/b", "vb", "String", false)

	resp := ssmCall(t, srv, "DeleteParameters", map[string]any{
		"Names": []string{"/golden/delete/a", "/golden/delete/b", "/golden/missing"},
	})
	helpers.GoldenTest(t, ssmGoldenDir, "DeleteParameters", resp, nil)
}
