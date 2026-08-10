package cloudformation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// These tests cover the legacy Query dispatch path (h.dispatch). The typed
// path is exercised end-to-end by tests/integration/cloudformation's ARN
// tests; both paths must keep resolving stack references the same way.

// waitForStoredStackStatus polls the store until the named stack reaches the
// wanted status, failing the test on timeout. Provisioner flows are
// asynchronous, so handler-level tests observe terminal states this way.
func waitForStoredStackStatus(t *testing.T, st *cfnStore, name, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		stack, err := st.getStack(context.Background(), name)
		if err == nil && stack != nil && stack.Status == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	stack, _ := st.getStack(context.Background(), name)
	got := "<missing>"
	if stack != nil {
		got = stack.Status
	}
	t.Fatalf("timed out waiting for stack %q to reach %s; last status %s", name, want, got)
}

func TestLegacyReadOps_byStackARN_resolveTheStack(t *testing.T) {
	// Given: a healthy stack with a resource and a recorded event
	h, st := newRollbackTestHandler(t)
	seeded := seedStack(t, st, "arn-read", StatusCreateComplete, StackResource{
		LogicalID:  "Queue",
		PhysicalID: "http://localhost:4566/000000000000/q",
		Type:       "AWS::SQS::Queue",
		Status:     ResourceCreateComplete,
	})
	if err := st.appendStackEvent(context.Background(), "arn-read", StackEvent{
		StackID:           seeded.StackID,
		StackName:         "arn-read",
		EventID:           "seeded-event-id",
		LogicalResourceID: "arn-read",
		ResourceType:      "AWS::CloudFormation::Stack",
		ResourceStatus:    StatusCreateComplete,
	}); err != nil {
		t.Fatalf("appendStackEvent: %v", err)
	}

	// When/Then: every read operation accepts the stack ARN as StackName
	for _, tc := range []struct {
		action string
		expect string // substring the 200 body must carry
	}{
		{"DescribeStacks", "<StackName>arn-read</StackName>"},
		{"GetTemplate", "Resources"},
		{"DescribeStackResources", "<LogicalResourceId>Queue</LogicalResourceId>"},
		{"ListStackResources", "<LogicalResourceId>Queue</LogicalResourceId>"},
		{"DescribeStackEvents", "<EventId>seeded-event-id</EventId>"},
		{"GetTemplateSummary", "GetTemplateSummaryResult"},
	} {
		rec := httptest.NewRecorder()
		h.dispatch(rec, cfnPost(tc.action, map[string]string{"StackName": seeded.StackID}))
		if rec.Code != http.StatusOK {
			t.Errorf("%s by ARN: status = %d, want 200; body: %s", tc.action, rec.Code, rec.Body.String())
			continue
		}
		if !strings.Contains(rec.Body.String(), tc.expect) {
			t.Errorf("%s by ARN: body missing %q: %s", tc.action, tc.expect, rec.Body.String())
		}
	}
}

func TestLegacyDescribeStacks_deletedStack_byARN_stillDescribable(t *testing.T) {
	// Given: a stack that has finished deleting (its record is retained)
	h, st := newRollbackTestHandler(t)
	seeded := seedStack(t, st, "arn-deleted", StatusDeleteComplete)

	// When: DescribeStacks is called with the stack ARN
	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("DescribeStacks", map[string]string{"StackName": seeded.StackID}))

	// Then: the deleted stack's final state is returned, as on AWS
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), StatusDeleteComplete) {
		t.Errorf("body missing %s: %s", StatusDeleteComplete, rec.Body.String())
	}
}

func TestLegacyUpdateStack_byStackARN_updatesTheStack(t *testing.T) {
	// Given: a healthy stack addressed by ARN
	h, st := newRollbackTestHandler(t)
	seeded := seedStack(t, st, "arn-update", StatusCreateComplete)

	// When: UpdateStack is called with the stack ARN
	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("UpdateStack", map[string]string{
		"StackName":    seeded.StackID,
		"TemplateBody": `{"Resources":{}}`,
	}))

	// Then: the stack is found and the update runs to completion
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	waitForStoredStackStatus(t, st, "arn-update", StatusUpdateComplete)
}

func TestLegacyUpdateStack_deletedStack_doesNotExist(t *testing.T) {
	// Given: a stack that has finished deleting
	h, st := newRollbackTestHandler(t)
	seedStack(t, st, "gone", StatusDeleteComplete)

	// When: UpdateStack targets it by name
	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("UpdateStack", map[string]string{
		"StackName":    "gone",
		"TemplateBody": `{"Resources":{}}`,
	}))

	// Then: AWS treats a deleted stack as nonexistent for mutations
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "does not exist") {
		t.Errorf("body missing does-not-exist message: %s", rec.Body.String())
	}
}

func TestLegacyDeleteStack_byStackARN_deletesTheStack(t *testing.T) {
	// Given: a healthy stack addressed by ARN
	h, st := newRollbackTestHandler(t)
	seeded := seedStack(t, st, "arn-delete", StatusCreateComplete)

	// When: DeleteStack is called with the stack ARN
	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("DeleteStack", map[string]string{"StackName": seeded.StackID}))

	// Then: the stack is found and deletion runs to completion
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	waitForStoredStackStatus(t, st, "arn-delete", StatusDeleteComplete)
}

func TestLegacyDeleteStack_alreadyDeleted_isANoOp(t *testing.T) {
	// Given: a stack that already finished deleting
	h, st := newRollbackTestHandler(t)
	seedStack(t, st, "twice-deleted", StatusDeleteComplete)

	// When: DeleteStack is called again
	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("DeleteStack", map[string]string{"StackName": "twice-deleted"}))

	// Then: the call succeeds without re-running the delete — the record is
	// untouched and no new events are appended
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	stack, err := st.getStack(context.Background(), "twice-deleted")
	if err != nil || stack == nil {
		t.Fatalf("getStack: %v, %v", stack, err)
	}
	if stack.StatusReason != "seeded" {
		t.Errorf("StatusReason = %q, want the seeded value untouched", stack.StatusReason)
	}
	events, evErr := st.getStackEvents(context.Background(), "twice-deleted")
	if evErr != nil {
		t.Fatalf("getStackEvents: %v", evErr)
	}
	if len(events) != 0 {
		t.Errorf("a second DeleteStack appended %d events, want 0", len(events))
	}
}

func TestGetStackByNameOrARN_staleARNAfterRecreate_doesNotResolve(t *testing.T) {
	// Given: a stack name that was deleted and recreated — the old incarnation's
	// ARN carries a uuid the current record does not have
	_, st := newRollbackTestHandler(t)
	old := seedStack(t, st, "reborn", StatusDeleteComplete)
	staleARN := old.StackID
	recreated := seedStack(t, st, "reborn", StatusCreateComplete)
	recreated.StackID = "arn:aws:cloudformation:us-east-1:000000000000:stack/reborn/99999999-8888-7777-6666-555555555555"
	if err := st.putStack(context.Background(), recreated); err != nil {
		t.Fatalf("putStack: %v", err)
	}

	// When/Then: the stale ARN resolves to nothing; the current one resolves
	stack, aerr := st.getStackByNameOrARN(context.Background(), staleARN)
	if aerr != nil || stack != nil {
		t.Errorf("stale ARN resolved to %#v, %v; want nil, nil", stack, aerr)
	}
	stack, aerr = st.getStackByNameOrARN(context.Background(), recreated.StackID)
	if aerr != nil || stack == nil || stack.StackID != recreated.StackID {
		t.Errorf("current ARN resolved to %#v, %v; want the recreated stack", stack, aerr)
	}
}
