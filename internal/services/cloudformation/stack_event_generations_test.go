package cloudformation

// stack_event_generations_test.go — a stack name's event history belongs to
// the generation that produced it, not to the name.
//
// The AWS CDK found this: `cdk bootstrap` against a CDKToolkit stack left in
// ROLLBACK_COMPLETE deletes the stack and creates it again under the same
// name, then watches DescribeStackEvents for the create. Overcast keyed
// events by stack name, so the new stack's history opened with every event of
// the one just deleted — and the CDK, reading a CREATE_FAILED against a
// resource its template no longer had, reported the bootstrap as failed.
//
// Real CloudFormation scopes DescribeStackEvents to the stack currently
// holding the name; a deleted stack's events are reachable only through its
// stack ID. Every generation of a name has its own uuid in its StackId, so
// that is what the events are keyed by.
// https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_DescribeStackEvents.html

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/internal/config"
)

// refusingCreateHandler refuses to create anything, which is the shortest route
// to a stack that rolls back on create.
type refusingCreateHandler struct{}

func (refusingCreateHandler) Create(context.Context, http.Handler, *config.Config, map[string]any, *resolveContext) (string, map[string]string, error) {
	return "", nil, errors.New("the bucket name is already taken")
}

func (refusingCreateHandler) Delete(context.Context, http.Handler, *config.Config, string, *resolveContext) error {
	return nil
}

// registerFailingCreate installs the handler under a resource type unique to
// the calling test, so the shared resourceHandlers map stays test-local.
func registerFailingCreate(t *testing.T) string {
	t.Helper()
	resType := "Test::EventGeneration::" + t.Name()
	resourceHandlers[resType] = refusingCreateHandler{}
	t.Cleanup(func() { delete(resourceHandlers, resType) })
	return resType
}

// describeStackEventsBody runs DescribeStackEvents over the Query path and
// returns the response body, failing the test on anything but 200.
func describeStackEventsBody(t *testing.T, h *Handler, stackRef string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("DescribeStackEvents", map[string]string{"StackName": stackRef}))
	if rec.Code != http.StatusOK {
		t.Fatalf("DescribeStackEvents(%s): status = %d, want 200; body: %s", stackRef, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// recreateAfterFailedCreate drives one stack name through the CDK's
// bootstrap-retry flow — create, fail, delete, create again — and returns the
// StackIds of the failed generation and of the one now holding the name.
func recreateAfterFailedCreate(t *testing.T, h *Handler, st *cfnStore, name string) (oldARN, newARN string) {
	t.Helper()
	ctx := context.Background()
	resType := registerFailingCreate(t)

	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("CreateStack", map[string]string{
		"StackName":    name,
		"TemplateBody": fmt.Sprintf(`{"Resources":{"BadBucket":{"Type":%q}}}`, resType),
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("first CreateStack: status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	waitForStoredStackStatus(t, st, name, StatusRollbackComplete)
	failed, aerr := st.getStack(ctx, name)
	if aerr != nil || failed == nil {
		t.Fatalf("getStack after the failed create: %v, %v", failed, aerr)
	}

	rec = httptest.NewRecorder()
	h.dispatch(rec, cfnPost("DeleteStack", map[string]string{"StackName": name}))
	if rec.Code != http.StatusOK {
		t.Fatalf("DeleteStack: status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	waitForStoredStackStatus(t, st, name, StatusDeleteComplete)

	rec = httptest.NewRecorder()
	h.dispatch(rec, cfnPost("CreateStack", map[string]string{
		"StackName":    name,
		"TemplateBody": `{"Resources":{}}`,
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("second CreateStack: status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	waitForStoredStackStatus(t, st, name, StatusCreateComplete)
	recreated, aerr := st.getStack(ctx, name)
	if aerr != nil || recreated == nil {
		t.Fatalf("getStack after the recreate: %v, %v", recreated, aerr)
	}
	if recreated.StackID == failed.StackID {
		t.Fatalf("the recreated stack kept StackId %s; each generation must mint its own", failed.StackID)
	}
	return failed.StackID, recreated.StackID
}

func TestDescribeStackEvents_recreatedStackName_startsAFreshEventStream(t *testing.T) {
	// Given: a stack name whose first generation failed and was deleted, and
	// which a second generation now holds
	h, st := newRollbackTestHandler(t)
	h.prov.initRouter(http.NotFoundHandler())
	oldARN, newARN := recreateAfterFailedCreate(t, h, st, "CDKToolkit")

	// When: the new stack's events are read by name
	body := describeStackEventsBody(t, h, "CDKToolkit")

	// Then: the history is the new generation's alone — nothing from the
	// deleted stack, and every event carries the new StackId
	if strings.Contains(body, "BadBucket") {
		t.Errorf("DescribeStackEvents by name carries the deleted generation's BadBucket events:\n%s", body)
	}
	if strings.Contains(body, oldARN) {
		t.Errorf("DescribeStackEvents by name carries events stamped with the deleted StackId %s:\n%s", oldARN, body)
	}
	if !strings.Contains(body, "<StackId>"+newARN+"</StackId>") {
		t.Errorf("DescribeStackEvents by name is missing the new generation's events (StackId %s):\n%s", newARN, body)
	}
	if !strings.Contains(body, "<ResourceStatus>"+StatusCreateComplete+"</ResourceStatus>") {
		t.Errorf("DescribeStackEvents by name is missing the new generation's CREATE_COMPLETE:\n%s", body)
	}
}

func TestDescribeStackEvents_recreatedStackName_typedPathAgrees(t *testing.T) {
	// Given: a stack name whose first generation failed and was deleted, and
	// which a second generation now holds
	h, st := newRollbackTestHandler(t)
	h.prov.initRouter(http.NotFoundHandler())
	oldARN, newARN := recreateAfterFailedCreate(t, h, st, "CDKToolkit")

	// When: the new stack's events are read by name over the typed path
	resp, aerr := h.describeStackEventsTyped(context.Background(), &describeStackEventsReq{StackName: "CDKToolkit"})
	if aerr != nil {
		t.Fatalf("describeStackEventsTyped: %v", aerr)
	}

	// Then: the two dispatch paths agree — only the new generation is listed
	if len(resp.Result.StackEvents) == 0 {
		t.Fatal("typed DescribeStackEvents returned no events for the recreated stack")
	}
	for _, e := range resp.Result.StackEvents {
		if e.LogicalResourceID == "BadBucket" {
			t.Errorf("typed DescribeStackEvents carries the deleted generation's BadBucket event %+v", e)
		}
		if e.StackID != newARN {
			t.Errorf("typed DescribeStackEvents event %s has StackId %s, want %s (old generation was %s)",
				e.EventID, e.StackID, newARN, oldARN)
		}
	}
}

func TestDescribeStackEvents_deletedGeneration_reachableByItsStackID(t *testing.T) {
	// Given: a stack name whose first generation failed and was deleted, and
	// which a second generation now holds
	h, st := newRollbackTestHandler(t)
	h.prov.initRouter(http.NotFoundHandler())
	oldARN, newARN := recreateAfterFailedCreate(t, h, st, "CDKToolkit")

	// When: the deleted generation is read by its StackId
	body := describeStackEventsBody(t, h, oldARN)

	// Then: its own history is still there — the failure it was deleted
	// for included — and none of the new generation's events leak into it
	if !strings.Contains(body, "BadBucket") {
		t.Errorf("DescribeStackEvents by the deleted StackId lost the failed generation's events:\n%s", body)
	}
	if !strings.Contains(body, "<StackId>"+oldARN+"</StackId>") {
		t.Errorf("DescribeStackEvents by the deleted StackId is missing events stamped with it:\n%s", body)
	}
	if strings.Contains(body, newARN) {
		t.Errorf("DescribeStackEvents by the deleted StackId carries the new generation's events:\n%s", body)
	}
}

func TestDescribeStackEvents_unknownStackID_doesNotExist(t *testing.T) {
	// Given: a StackId no generation of any stack was ever minted with
	h, _ := newRollbackTestHandler(t)
	const unknown = "arn:aws:cloudformation:us-east-1:000000000000:stack/never/99999999-8888-7777-6666-555555555555"

	// When: its events are requested
	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("DescribeStackEvents", map[string]string{"StackName": unknown}))

	// Then: the stack does not exist — an ARN reaches a deleted generation's
	// history, not an empty one for a stack that never was
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "<Code>ValidationError</Code>") {
		t.Errorf("body missing ValidationError: %s", rec.Body.String())
	}
}
