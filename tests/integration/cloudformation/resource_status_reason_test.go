package cloudformation_test

// resource_status_reason_test.go — the shape of ResourceStatusReason when a
// resource fails because the service call behind it did.
//
// Real CloudFormation never puts a service's wire body in a stack event. Its
// resource providers report through a ProgressEvent, and CloudFormation renders
// that into one sentence naming the service, the status, the error code, the
// request the call was answered under, the operation's token and the provider's
// classification of the failure. Overcast used to paste the raw XML or JSON
// into the event instead, so a `cdk deploy` printed an error document at the
// operator. See internal/services/cloudformation/status_reason.go.
//
// DynamoDB is the fixture because its rejection is one the template can ask for
// deterministically, it comes back over the JSON protocol (the wire shape that
// used to leak `{"__type":…}` into the event), and the reason it carries is
// DynamoDB's own — this test would still pass if CloudFormation had invented
// the message, so it also asserts the message is the service's, verbatim.

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// forbiddenThroughput is the property block DynamoDB rejects: PAY_PER_REQUEST
// billing forbids provisioned capacity, and CloudFormation forwards both
// verbatim rather than reconciling them — see TestCreateStack_DynamoDBTableBillingMode.
const forbiddenThroughput = `,
        "BillingMode": "PAY_PER_REQUEST",
        "ProvisionedThroughput": {"ReadCapacityUnits": 5, "WriteCapacityUnits": 5}`

// dynamoValidationMessage is what DynamoDB answers for that combination. It is
// spelled out here rather than matched loosely because the point of the test is
// that the service's own sentence reaches the operator unedited.
const dynamoValidationMessage = "One or more parameter values were invalid: " +
	"Neither ReadCapacityUnits nor WriteCapacityUnits can be specified when " +
	"BillingMode is PAY_PER_REQUEST"

func TestCreateStack_failedResourceReasonCarriesTheCloudFormationShape(t *testing.T) {
	// Given: a stack whose only resource asks DynamoDB for a combination it
	// rejects, created under a client request token of the caller's own.
	srv := helpers.NewTestServer(t)
	const stackName = "cfn-reason-shape"
	const token = "caller-token-reason-shape"

	// When: CloudFormation provisions it and rolls the stack back.
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":          {stackName},
		"TemplateBody":       {dynamodbBillingTemplate("cfn-reason-shape-table", forbiddenThroughput)},
		"ClientRequestToken": {token},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "ROLLBACK_COMPLETE")

	// Then: the failed resource's reason is CloudFormation's sentence, not
	// DynamoDB's response body.
	reason := failedResourceReason(t, srv, stackName, "Table")

	wantPrefix := `dynamodb CreateTable: Resource handler returned message: "` +
		dynamoValidationMessage +
		` (Service: DynamoDB, Status Code: 400, Error Code: ValidationException, Request ID: `
	if !strings.HasPrefix(reason, wantPrefix) {
		t.Fatalf("resource status reason = %q,\nwant it to start with %q", reason, wantPrefix)
	}

	// The token half is the operation's, so a reader can get from the reason
	// back to the event stream and the request behind it.
	wantSuffix := `" (RequestToken: ` + token + ", HandlerErrorCode: InvalidRequest)"
	if !strings.HasSuffix(reason, wantSuffix) {
		t.Errorf("resource status reason = %q,\nwant it to end with %q", reason, wantSuffix)
	}

	// The request ID is the one the call was actually answered under, not a
	// placeholder: it is a minted ID, so all this can check is that one is
	// there and that it is not empty.
	if id := requestIDFromReason(reason); id == "" {
		t.Errorf("resource status reason = %q, want a non-empty Request ID", reason)
	}

	// And none of the wire body survives. `__type` is the JSON protocol's error
	// key; its presence is the regression this test exists to catch.
	for _, leaked := range []string{"__type", `{"`, "HTTP 400"} {
		if strings.Contains(reason, leaked) {
			t.Errorf("resource status reason = %q, want no %q from the wire body", reason, leaked)
		}
	}
}

func TestCreateStack_failedResourceReasonTokenMatchesTheStackEvent(t *testing.T) {
	// Given: the same failing stack, created without a token of the caller's
	// own — Overcast fills the operation's token with the request ID of the
	// call that started it (see stackOperationToken), and the reason has to
	// name whatever the events were stamped with.
	srv := helpers.NewTestServer(t)
	const stackName = "cfn-reason-token"

	// When: CloudFormation provisions it and rolls the stack back.
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {dynamodbBillingTemplate("cfn-reason-token-table", forbiddenThroughput)},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "ROLLBACK_COMPLETE")

	// Then: the RequestToken in the reason is the event's ClientRequestToken.
	events := describeStackEventRecords(t, srv, stackName)
	var failed *stackEventRecord
	for i := range events {
		if events[i].LogicalID == "Table" && events[i].Status == "CREATE_FAILED" {
			failed = &events[i]
			break
		}
	}
	if failed == nil {
		t.Fatalf("no CREATE_FAILED event for the Table resource in %+v", events)
	}
	if failed.ClientRequestToken == "" {
		t.Fatal("the CREATE_FAILED event carries no ClientRequestToken to correlate the reason with")
	}
	want := "(RequestToken: " + failed.ClientRequestToken + ", "
	if !strings.Contains(failed.Reason, want) {
		t.Errorf("resource status reason = %q, want it to name the event's own token %q",
			failed.Reason, failed.ClientRequestToken)
	}
}

// ---- Test helpers ----------------------------------------------------------

// stackEventRecord is the part of a stack event this file asserts on.
type stackEventRecord struct {
	LogicalID          string `xml:"LogicalResourceId"`
	Status             string `xml:"ResourceStatus"`
	Reason             string `xml:"ResourceStatusReason"`
	ClientRequestToken string `xml:"ClientRequestToken"`
}

// describeStackEventRecords returns a stack's events, newest first.
// describeStackEventReasons in dynamic_reference_test.go reads only the reason;
// this needs the reason beside the logical ID and the token it was stamped with.
func describeStackEventRecords(t *testing.T, srv *helpers.TestServer, stackName string) []stackEventRecord {
	t.Helper()
	resp := cfnQuery(t, srv, "DescribeStackEvents", url.Values{"StackName": []string{stackName}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	body := readBody(t, resp)
	var result struct {
		Events []stackEventRecord `xml:"DescribeStackEventsResult>StackEvents>member"`
	}
	if err := xml.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal DescribeStackEventsResponse: %v\nbody: %s", err, body)
	}
	return result.Events
}

// failedResourceReason returns the reason on a resource's CREATE_FAILED event.
func failedResourceReason(t *testing.T, srv *helpers.TestServer, stackName, logicalID string) string {
	t.Helper()
	events := describeStackEventRecords(t, srv, stackName)
	for _, event := range events {
		if event.LogicalID == logicalID && event.Status == "CREATE_FAILED" {
			return event.Reason
		}
	}
	t.Fatalf("no CREATE_FAILED event for %s in %+v", logicalID, events)
	return ""
}

// requestIDFromReason reads the Request ID out of a rendered reason.
func requestIDFromReason(reason string) string {
	const marker = "Request ID: "
	start := strings.Index(reason, marker)
	if start < 0 {
		return ""
	}
	rest := reason[start+len(marker):]
	if end := strings.IndexAny(rest, ",)"); end >= 0 {
		return rest[:end]
	}
	return rest
}
