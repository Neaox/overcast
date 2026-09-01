package cloudformation_test

// event_traceability_test.go — getting from a CloudFormation failure back to
// the request that caused it.
//
// A stack event says what went wrong. It did not say which request produced it,
// so a deploy that failed left no way to reach the trace holding the internal
// calls, bodies and log lines behind the failure — only timestamps to guess
// from, across a CDK deploy that issues thousands of requests.
//
// ClientRequestToken is the field AWS puts on every event of a stack
// operation for exactly this purpose. When the caller sends one it is theirs;
// when they send none — the CDK CLI sends none — Overcast fills it with the
// request ID of the call that started the operation, which is the ID its trace
// is stored under and the one /_overcast/debug/trace/{requestId} is keyed by.

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// stackEventsXML is the slice of DescribeStackEvents this file reads. The
// wider suite compares raw bodies; these assertions are about one field of
// specific events, so they decode.
type stackEventsXML struct {
	XMLName xml.Name `xml:"DescribeStackEventsResponse"`
	Events  []struct {
		LogicalResourceID    string `xml:"LogicalResourceId"`
		ResourceStatus       string `xml:"ResourceStatus"`
		ResourceStatusReason string `xml:"ResourceStatusReason"`
		ClientRequestToken   string `xml:"ClientRequestToken"`
	} `xml:"DescribeStackEventsResult>StackEvents>member"`
}

func describeStackEvents(t *testing.T, srv *helpers.TestServer, stackName string) stackEventsXML {
	t.Helper()
	resp := cfnQuery(t, srv, "DescribeStackEvents", url.Values{"StackName": []string{stackName}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out stackEventsXML
	if err := xml.Unmarshal(readBody(t, resp), &out); err != nil {
		t.Fatalf("decode DescribeStackEvents: %v", err)
	}
	if len(out.Events) == 0 {
		t.Fatalf("DescribeStackEvents(%s) returned no events", stackName)
	}
	return out
}

func TestStackEvents_nameTheRequestThatCausedThem(t *testing.T) {
	srv := helpers.NewTestServer(t)

	// When: a stack is created with no ClientRequestToken of the caller's own,
	// which is what every SDK and the CDK CLI send.
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"traceable-stack"},
		"TemplateBody": []string{minimalTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)

	// The request ID CreateStack answered under. This is the ID its trace is
	// filed under, so it is the one an event has to carry to be worth anything.
	requestID := cr.Header.Get("x-amzn-requestid")
	if requestID == "" {
		t.Fatal("CreateStack sent no x-amzn-requestid, so there is nothing for an event to point at")
	}
	waitForStackStatus(t, srv, "traceable-stack", "CREATE_COMPLETE")

	// Then: every event of the operation names it — the resource-level ones as
	// well as the stack-level ones, because a reader who has the failed
	// resource in front of them should not have to go looking for a different
	// event to find the request.
	for _, e := range describeStackEvents(t, srv, "traceable-stack").Events {
		if e.ClientRequestToken != requestID {
			t.Errorf("event %s/%s ClientRequestToken = %q, want the originating request ID %q",
				e.LogicalResourceID, e.ResourceStatus, e.ClientRequestToken, requestID)
		}
	}
}

func TestStackEvents_carryTheCallersOwnTokenWhenThereIsOne(t *testing.T) {
	srv := helpers.NewTestServer(t)

	// Given: a caller that supplies a token, as AWS's own idempotency guidance
	// tells them to.
	const token = "deploy-2026-08-12-attempt-1"
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":          []string{"caller-token-stack"},
		"TemplateBody":       []string{minimalTemplate},
		"ClientRequestToken": []string{token},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "caller-token-stack", "CREATE_COMPLETE")

	// Then: theirs is what comes back. Overcast's fallback fills a gap; it does
	// not overwrite an answer the caller already gave.
	for _, e := range describeStackEvents(t, srv, "caller-token-stack").Events {
		if e.ClientRequestToken != token {
			t.Errorf("event %s/%s ClientRequestToken = %q, want the caller's %q",
				e.LogicalResourceID, e.ResourceStatus, e.ClientRequestToken, token)
		}
	}
}

// The token belongs to the operation, not to the stack. A stack that is created
// and then updated has two operations behind it and two requests to get back
// to, and an event from the second that still named the first would send a
// reader to a trace with nothing to do with what they are chasing.
func TestStackEvents_eachOperationCarriesItsOwnToken(t *testing.T) {
	srv := helpers.NewTestServer(t)

	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"two-operations-stack"},
		"TemplateBody": []string{minimalTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	createID := cr.Header.Get("x-amzn-requestid")
	waitForStackStatus(t, srv, "two-operations-stack", "CREATE_COMPLETE")

	ur := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    []string{"two-operations-stack"},
		"TemplateBody": []string{minimalTemplate},
	})
	defer ur.Body.Close()
	helpers.AssertStatus(t, ur, http.StatusOK)
	updateID := ur.Header.Get("x-amzn-requestid")
	waitForStackStatus(t, srv, "two-operations-stack", "UPDATE_COMPLETE")

	if createID == updateID {
		t.Fatal("the two operations answered under the same request ID, so this proves nothing")
	}

	// Then: the update's events name the update's request, and the create's
	// still name the create's.
	var sawUpdate, sawCreate bool
	for _, e := range describeStackEvents(t, srv, "two-operations-stack").Events {
		switch e.ClientRequestToken {
		case updateID:
			sawUpdate = true
		case createID:
			sawCreate = true
		default:
			t.Errorf("event %s/%s ClientRequestToken = %q, want one of the two operations' request IDs",
				e.LogicalResourceID, e.ResourceStatus, e.ClientRequestToken)
		}
	}
	if !sawUpdate {
		t.Error("no event names the update's request")
	}
	if !sawCreate {
		t.Error("the create's events lost their token when the update ran")
	}
}
