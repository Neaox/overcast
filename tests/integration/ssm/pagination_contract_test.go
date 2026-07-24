package ssm_test

// Pagination contract coverage for SSM's DescribeParameters — part of
// docs/plans/pagination-plan.md's H2 (shared paginator-contract test
// helper) and G3 (invalid-token error mapping for SSM ×3). Seeds more
// parameters than the requested page size, walks DescribeParameters to
// termination via helpers.PaginationContractTest, and asserts a garbled
// NextToken returns SSM's documented InvalidNextToken error instead of
// silently restarting the walk from page 1.

import (
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func TestDescribeParameters_paginationContract(t *testing.T) {
	// Given: 5 parameters under a prefix unique to this test, more than the
	// MaxResults=2 page size below, so the walk must take at least 3 pages.
	srv := helpers.NewTestServer(t)
	names := []string{
		"/pagination-contract/p1",
		"/pagination-contract/p2",
		"/pagination-contract/p3",
		"/pagination-contract/p4",
		"/pagination-contract/p5",
	}
	for _, name := range names {
		putParam(t, srv, name, "v", "String", false)
	}
	// Store.Scan (internal/services/ssm/store.go) returns entries in
	// ascending key order, and DescribeParameters doesn't re-sort after
	// filtering — so the paginated walk's order is ascending parameter
	// name order, which matches `names` as written above.
	wantIDs := names

	fetch := func(t *testing.T, token string) ([]string, string) {
		t.Helper()
		body := map[string]any{
			"MaxResults": 2,
			"ParameterFilters": []map[string]any{
				{"Key": "Name", "Option": "BeginsWith", "Values": []string{"/pagination-contract/"}},
			},
		}
		if token != "" {
			body["NextToken"] = token
		}
		resp := ssmCall(t, srv, "DescribeParameters", body)
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusOK)
		var page struct {
			Parameters []struct {
				Name string `json:"Name"`
			} `json:"Parameters"`
			NextToken string `json:"NextToken"`
		}
		decodeJSON(t, resp, &page)
		got := make([]string, len(page.Parameters))
		for i, p := range page.Parameters {
			got[i] = p.Name
		}
		return got, page.NextToken
	}

	probe := func(t *testing.T) (int, string) {
		t.Helper()
		resp := ssmCall(t, srv, "DescribeParameters", map[string]any{
			"MaxResults": 2,
			"NextToken":  "not-a-real-token",
		})
		defer resp.Body.Close()
		var errResp struct {
			Type string `json:"__type"`
		}
		decodeJSON(t, resp, &errResp)
		return resp.StatusCode, errResp.Type
	}

	// Then: exactly-once + order + termination for the valid walk, and the
	// AWS-documented InvalidNextToken error for the invalid-token probe
	// (see https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_DescribeParameters.html#API_DescribeParameters_Errors).
	helpers.PaginationContractTest(t, wantIDs, func(name string) string { return name }, fetch, probe,
		helpers.PaginationContractOptions{
			WantInvalidTokenStatus:    http.StatusBadRequest,
			WantInvalidTokenErrorCode: "InvalidNextToken",
		})
}
