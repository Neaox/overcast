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
	if err := st.appendStackEvent(context.Background(), seeded.StackID, StackEvent{
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

// TestLegacyReadOps_byName_deletedStack_doNotExist is TestLegacyReadOps_byStackARN_resolveTheStack's
// mirror: on real AWS a DELETE_COMPLETE stack is addressable only by its
// unique stack ID, so every one of these operations must answer
// "does not exist" when given the name instead — see #829 and the AWS
// DescribeStacks/DescribeStackEvents/ListStackResources StackName doc:
// "Deleted stacks: You must specify the unique stack ID."
func TestLegacyReadOps_byName_deletedStack_doNotExist(t *testing.T) {
	// Given: a stack that has finished deleting (its record is retained, ARN-only)
	h, st := newRollbackTestHandler(t)
	seedStack(t, st, "name-deleted", StatusDeleteComplete, StackResource{
		LogicalID:  "Queue",
		PhysicalID: "http://localhost:4566/000000000000/q",
		Type:       "AWS::SQS::Queue",
		Status:     ResourceCreateComplete,
	})

	// When/Then: every read operation refuses the stack name with the same
	// ValidationError AWS gives a name that was never created
	for _, action := range []string{
		"DescribeStacks",
		"GetTemplate",
		"DescribeStackResources",
		"ListStackResources",
		"DescribeStackEvents",
		"GetTemplateSummary",
	} {
		rec := httptest.NewRecorder()
		h.dispatch(rec, cfnPost(action, map[string]string{"StackName": "name-deleted"}))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s by name after delete: status = %d, want 400; body: %s", action, rec.Code, rec.Body.String())
			continue
		}
		if !strings.Contains(rec.Body.String(), "does not exist") {
			t.Errorf("%s by name after delete: body missing does-not-exist message: %s", action, rec.Body.String())
		}
	}

	// And: the same stack is still fully readable by ARN
	seeded, err := st.getStack(context.Background(), "name-deleted")
	if err != nil || seeded == nil {
		t.Fatalf("getStack: %v, %v", seeded, err)
	}
	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("DescribeStacks", map[string]string{"StackName": seeded.StackID}))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), StatusDeleteComplete) {
		t.Errorf("DescribeStacks by ARN after delete: status %d, body: %s", rec.Code, rec.Body.String())
	}
}

// TestLegacyDescribeStacks_deleteInProgress_stillResolvesByName pins the
// waiter-safety requirement: a stack mid-delete is not yet DELETE_COMPLETE,
// so name-based reads must keep resolving it. `cdk destroy` and the AWS SDK's
// stack-delete-complete waiter poll DescribeStacks by name (or ARN — either
// works while the delete is running) throughout the delete; only the
// terminal DELETE_COMPLETE record excludes the name path.
func TestLegacyDescribeStacks_deleteInProgress_stillResolvesByName(t *testing.T) {
	h, st := newRollbackTestHandler(t)
	seedStack(t, st, "still-deleting", StatusDeleteInProgress)

	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("DescribeStacks", map[string]string{"StackName": "still-deleting"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), StatusDeleteInProgress) {
		t.Errorf("body missing %s: %s", StatusDeleteInProgress, rec.Body.String())
	}
}

// TestGetStackByNameOrARN_nameReuse_newGenerationResolvesByName is the
// load-bearing name-reuse case: stacks are keyed by name (one row per name;
// putStack overwrites it), so a DELETE_COMPLETE occupant is only ever
// excluded from the *name* path — it never blocks the name being reused, and
// the moment a new CreateStack lands under it, the name resolves that new
// generation exactly as if the old one had never existed. The old
// generation's own ARN embeds its own uuid and, while it was still the sole
// occupant, was the only handle that resolved it — but once overwritten, the
// row is gone, so the old ARN resolves to nothing afterward too (that half is
// TestGetStackByNameOrARN_staleARNAfterRecreate_doesNotResolve, from #827).
func TestGetStackByNameOrARN_nameReuse_newGenerationResolvesByName(t *testing.T) {
	_, st := newRollbackTestHandler(t)
	ctx := context.Background()
	old := seedStack(t, st, "reused-name", StatusDeleteComplete)

	// While only the old, deleted generation occupies the name, the name
	// resolves to nothing (matching TestLegacyReadOps_byName_deletedStack_doNotExist)
	// but the old generation's own ARN still finds it.
	stack, aerr := st.getStackByNameOrARN(ctx, "reused-name")
	if aerr != nil || stack != nil {
		t.Fatalf("name resolved a DELETE_COMPLETE occupant: %#v, %v", stack, aerr)
	}
	stack, aerr = st.getStackByNameOrARN(ctx, old.StackID)
	if aerr != nil || stack == nil || stack.Status != StatusDeleteComplete {
		t.Fatalf("old ARN resolved to %#v, %v; want the DELETE_COMPLETE record", stack, aerr)
	}

	recreated := seedStack(t, st, "reused-name", StatusCreateComplete)
	recreated.StackID = "arn:aws:cloudformation:us-east-1:000000000000:stack/reused-name/99999999-8888-7777-6666-555555555555"
	if err := st.putStack(ctx, recreated); err != nil {
		t.Fatalf("putStack: %v", err)
	}

	// The name now resolves the new generation, live and unremarkable.
	stack, aerr = st.getStackByNameOrARN(ctx, "reused-name")
	if aerr != nil || stack == nil || stack.StackID != recreated.StackID {
		t.Errorf("name resolved to %#v, %v; want the recreated stack", stack, aerr)
	}

	// The new generation's own ARN finds only the new record.
	stack, aerr = st.getStackByNameOrARN(ctx, recreated.StackID)
	if aerr != nil || stack == nil || stack.Status != StatusCreateComplete {
		t.Errorf("new ARN resolved to %#v, %v; want the new CREATE_COMPLETE record", stack, aerr)
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
	seeded := seedStack(t, st, "twice-deleted", StatusDeleteComplete)

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
	events, evErr := st.getStackEvents(context.Background(), seeded.StackID)
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
