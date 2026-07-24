package cloudformation_test

// Pagination contract coverage for CloudFormation's DescribeStackEvents —
// part of docs/plans/pagination-plan.md's H2 (shared paginator-contract
// test helper) and G3 (invalid-token error mapping). Seeds a stack whose
// event history exceeds the fixed 20-event page size, walks
// DescribeStackEvents to termination via helpers.PaginationContractTest,
// and asserts a garbled NextToken returns CloudFormation's Query-protocol
// ValidationError instead of silently restarting the walk from page 1.

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// stackEventItem is the minimal shape needed to walk DescribeStackEvents'
// pagination and verify its reverse-chronological order — see
// stackEventXML/describeStackEventsResult in
// internal/services/cloudformation/handler.go for the full response shape.
type stackEventItem struct {
	LogicalResourceID string `xml:"LogicalResourceId"`
	ResourceStatus    string `xml:"ResourceStatus"`
	Timestamp         string `xml:"Timestamp"` // RFC3339-ish, lexicographically sortable
}

func TestDescribeStackEvents_paginationContract(t *testing.T) {
	// Given: a stack with 11 S3 buckets. Each resource emits
	// CREATE_IN_PROGRESS + CREATE_COMPLETE (22 resource events) plus the
	// same pair for the stack itself = 24 events total, which exceeds the
	// fixed page size of 20 (eventsPageSize) and forces NextToken.
	const bigTemplate = `{
	  "Resources": {
	    "B01":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"contract-pg-bucket-01"}},
	    "B02":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"contract-pg-bucket-02"}},
	    "B03":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"contract-pg-bucket-03"}},
	    "B04":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"contract-pg-bucket-04"}},
	    "B05":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"contract-pg-bucket-05"}},
	    "B06":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"contract-pg-bucket-06"}},
	    "B07":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"contract-pg-bucket-07"}},
	    "B08":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"contract-pg-bucket-08"}},
	    "B09":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"contract-pg-bucket-09"}},
	    "B10":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"contract-pg-bucket-10"}},
	    "B11":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"contract-pg-bucket-11"}}
	  }
	}`
	srv := helpers.NewTestServer(t)
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"pagination-contract-stack"},
		"TemplateBody": []string{bigTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "pagination-contract-stack", "CREATE_COMPLETE")

	fetch := func(t *testing.T, token string) ([]stackEventItem, string) {
		t.Helper()
		params := url.Values{"StackName": []string{"pagination-contract-stack"}}
		if token != "" {
			params.Set("NextToken", token)
		}
		resp := cfnQuery(t, srv, "DescribeStackEvents", params)
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusOK)
		b := readBody(t, resp)
		var result struct {
			Events    []stackEventItem `xml:"DescribeStackEventsResult>StackEvents>member"`
			NextToken string           `xml:"DescribeStackEventsResult>NextToken"`
		}
		if err := xml.Unmarshal(b, &result); err != nil {
			t.Fatalf("unmarshal DescribeStackEventsResponse: %v\nbody: %s", err, b)
		}
		return result.Events, result.NextToken
	}

	// EventIDs are random UUIDs (see provisioner.go), so their exact walk
	// order can't be predicted without duplicating store internals — id
	// here is LogicalResourceId#ResourceStatus instead, which IS
	// deterministic: each of the 12 logical resources (11 buckets + the
	// stack itself) transitions through CREATE_IN_PROGRESS and
	// CREATE_COMPLETE exactly once, so 24 combinations are each unique.
	idOf := func(e stackEventItem) string { return e.LogicalResourceID + "#" + e.ResourceStatus }

	probe := func(t *testing.T) (int, string) {
		t.Helper()
		resp := cfnQuery(t, srv, "DescribeStackEvents", url.Values{
			"StackName": []string{"pagination-contract-stack"},
			"NextToken": []string{"not-a-real-token"},
		})
		defer resp.Body.Close()
		b := readBody(t, resp)
		var errResp struct {
			Code string `xml:"Error>Code"`
		}
		if err := xml.Unmarshal(b, &errResp); err != nil {
			t.Fatalf("unmarshal error response: %v\nbody: %s", err, b)
		}
		return resp.StatusCode, errResp.Code
	}

	// Then: exactly-once across the full walk (wantIDs is nil — order isn't
	// independently predictable, see idOf's comment above, so only
	// duplicate-detection applies here) and termination within the default
	// page bound, plus the AWS-documented ValidationError for the
	// invalid-token probe (see
	// https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/CommonErrors.html).
	events := helpers.PaginationContractTest(t, nil, idOf, fetch, probe,
		helpers.PaginationContractOptions{
			WantInvalidTokenStatus:    http.StatusBadRequest,
			WantInvalidTokenErrorCode: "ValidationError",
		})

	// And: all 24 events were returned (12 logical resources x 2 statuses).
	if len(events) != 24 {
		t.Fatalf("expected 24 events across the full walk, got %d", len(events))
	}

	// And: the walk is in the operation's documented reverse-chronological
	// order — timestamps must never increase from one event to the next.
	for i := 1; i < len(events); i++ {
		if events[i].Timestamp > events[i-1].Timestamp {
			t.Errorf("events out of reverse-chronological order at index %d: %q came after %q",
				i, events[i].Timestamp, events[i-1].Timestamp)
		}
	}
}
