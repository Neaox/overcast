package cloudformation

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/overcast-sh/overcast/internal/config"
)

// blockedDeleteHandler refuses to delete while blocked is set. It is the shape
// ContinueUpdateRollback exists for: a resource whose teardown failed for a
// reason outside the stack — a port still held, a dependency still standing —
// which an operator clears before asking CloudFormation to try again.
type blockedDeleteHandler struct {
	blocked  atomic.Bool
	attempts atomic.Int32
}

func (h *blockedDeleteHandler) Create(_ context.Context, _ http.Handler, _ *config.Config, _ map[string]any, _ *resolveContext) (string, map[string]string, error) {
	return "physical-id", nil, nil
}

func (h *blockedDeleteHandler) Delete(_ context.Context, _ http.Handler, _ *config.Config, _ string, _ *resolveContext) error {
	h.attempts.Add(1)
	if h.blocked.Load() {
		return errors.New("host port 80 is already in use")
	}
	return nil
}

// continueUpdateRollbackResponse is the (empty) result AWS returns. The struct
// exists to assert the envelope is named as the SDK expects, since an
// unmarshal against the wrong element name silently yields a zero value.
type continueUpdateRollbackResponse struct {
	XMLName   xml.Name
	RequestID string `xml:"ResponseMetadata>RequestId"`
}

// ---- the recovery path -----------------------------------------------------

func TestContinueUpdateRollback_retriesTheRollbackOnceTheBlockerIsCleared(t *testing.T) {
	// Given: an automatic update rollback that could not delete a resource,
	// leaving the stack UPDATE_ROLLBACK_FAILED
	h, st := newRollbackTestHandler(t)
	handler := &blockedDeleteHandler{}
	handler.blocked.Store(true)
	resType := registerResourceHandler(t, "Blocked", handler)
	seedStack(t, st, "wedged", StatusUpdateRollbackFailed, StackResource{
		LogicalID:    "Service",
		PhysicalID:   "physical-id",
		Type:         resType,
		Status:       ResourceDeleteFailed,
		StatusReason: "host port 80 is already in use",
	})

	// When: the operator clears the blocker and continues the rollback
	handler.blocked.Store(false)
	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("ContinueUpdateRollback", map[string]string{"StackName": "wedged"}))

	// Then: the call succeeds with AWS's empty result envelope
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var resp continueUpdateRollbackResponse
	if err := xml.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v; body: %s", err, rec.Body.String())
	}
	if resp.XMLName.Local != "ContinueUpdateRollbackResponse" {
		t.Errorf("response element = %q, want ContinueUpdateRollbackResponse", resp.XMLName.Local)
	}
	if resp.RequestID == "" {
		t.Error("response carries no RequestId")
	}

	// Then: the delete is retried and the stack reaches a terminal state the
	// next update can start from
	if got := handler.attempts.Load(); got != 1 {
		t.Errorf("delete attempts = %d, want 1", got)
	}
	got, err := st.getStack(context.Background(), "wedged")
	if err != nil {
		t.Fatalf("getStack: %v", err)
	}
	if got.Status != StatusUpdateRollbackComplete {
		t.Fatalf("stack status = %q, want %q", got.Status, StatusUpdateRollbackComplete)
	}
	if len(got.Resources) != 0 {
		t.Errorf("resources = %+v, want the retired resource gone", got.Resources)
	}
}

func TestContinueUpdateRollback_whenTheBlockerRemains_staysUpdateRollbackFailed(t *testing.T) {
	// Given: the same wedged stack, with the blocker still in place
	h, st := newRollbackTestHandler(t)
	handler := &blockedDeleteHandler{}
	handler.blocked.Store(true)
	resType := registerResourceHandler(t, "StillBlocked", handler)
	seedStack(t, st, "still-wedged", StatusUpdateRollbackFailed, StackResource{
		LogicalID:  "Service",
		PhysicalID: "physical-id",
		Type:       resType,
		Status:     ResourceDeleteFailed,
	})

	// When: the rollback is continued anyway
	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("ContinueUpdateRollback", map[string]string{"StackName": "still-wedged"}))

	// Then: AWS accepts the request — the failure is reported on the stack, not
	// to the caller — and the stack lands back where it started, retryable
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	got, err := st.getStack(context.Background(), "still-wedged")
	if err != nil {
		t.Fatalf("getStack: %v", err)
	}
	if got.Status != StatusUpdateRollbackFailed {
		t.Errorf("stack status = %q, want %q", got.Status, StatusUpdateRollbackFailed)
	}
	if len(got.Resources) != 1 || got.Resources[0].Status != ResourceDeleteFailed {
		t.Errorf("resources = %+v, want the blocked resource still listed as DELETE_FAILED", got.Resources)
	}
}

func TestContinueUpdateRollback_clearsResourcesTheFailedUpdateLeftBehind(t *testing.T) {
	// Given: a stack carrying each failed-resource shape a rollback has to deal
	// with — one that failed to update, one that was never physically created
	h, st := newRollbackTestHandler(t)
	seedStack(t, st, "messy-continue", StatusUpdateRollbackFailed,
		StackResource{
			LogicalID:    "Queue",
			PhysicalID:   "http://localhost:4566/000000000000/q",
			Type:         "AWS::SQS::Queue",
			Status:       ResourceUpdateFailed,
			StatusReason: "boom",
		},
		StackResource{
			LogicalID: "Ghost",
			Type:      "AWS::SQS::Queue",
			Status:    ResourceCreateFailed,
		},
	)

	// When: the rollback is continued
	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("ContinueUpdateRollback", map[string]string{"StackName": "messy-continue"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	// Then: nothing is left in a failed state, and the never-created resource
	// has left the stack entirely
	got, err := st.getStack(context.Background(), "messy-continue")
	if err != nil {
		t.Fatalf("getStack: %v", err)
	}
	if got.Status != StatusUpdateRollbackComplete {
		t.Fatalf("stack status = %q, want %q", got.Status, StatusUpdateRollbackComplete)
	}
	for _, r := range got.Resources {
		if r.LogicalID == "Ghost" {
			t.Errorf("resource Ghost should have been dropped, still present with status %q", r.Status)
		}
		if strings.HasSuffix(r.Status, "_FAILED") {
			t.Errorf("resource %s left in failed status %q", r.LogicalID, r.Status)
		}
	}
}

func TestContinueUpdateRollback_byStackARN_resolvesTheStack(t *testing.T) {
	// Given: a wedged stack addressed by ARN rather than name
	h, st := newRollbackTestHandler(t)
	seeded := seedStack(t, st, "arn-continue", StatusUpdateRollbackFailed)

	// When: ContinueUpdateRollback is called with the stack ARN
	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("ContinueUpdateRollback", map[string]string{"StackName": seeded.StackID}))

	// Then: the stack is found and continued
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	got, err := st.getStack(context.Background(), "arn-continue")
	if err != nil {
		t.Fatalf("getStack: %v", err)
	}
	if got.Status != StatusUpdateRollbackComplete {
		t.Errorf("stack status = %q, want %q", got.Status, StatusUpdateRollbackComplete)
	}
}

func TestContinueUpdateRollback_emitsRollbackEventsVisibleToDescribeStackEvents(t *testing.T) {
	// Given: a wedged stack
	h, st := newRollbackTestHandler(t)
	seedStack(t, st, "eventful-continue", StatusUpdateRollbackFailed)

	// When: the rollback is continued and the events are described
	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("ContinueUpdateRollback", map[string]string{"StackName": "eventful-continue"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("continue status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	evRec := httptest.NewRecorder()
	h.dispatch(evRec, cfnPost("DescribeStackEvents", map[string]string{"StackName": "eventful-continue"}))
	if evRec.Code != http.StatusOK {
		t.Fatalf("DescribeStackEvents status = %d, want 200; body: %s", evRec.Code, evRec.Body.String())
	}

	var resp describeEventsResponse
	if err := xml.Unmarshal(evRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal events: %v; body: %s", err, evRec.Body.String())
	}

	seen := map[string]bool{}
	for _, e := range resp.Events {
		if e.ResourceType == "AWS::CloudFormation::Stack" {
			seen[e.ResourceStatus] = true
		}
	}
	for _, want := range []string{StatusUpdateRollbackInProgress, StatusUpdateRollbackComplete} {
		if !seen[want] {
			t.Errorf("expected a %s stack event; got %+v", want, resp.Events)
		}
	}
}

// ---- ResourcesToSkip -------------------------------------------------------

func TestContinueUpdateRollback_resourcesToSkip_completesOverAResourceThatStillFails(t *testing.T) {
	// Given: a stack whose rollback is blocked by a resource that cannot be
	// cleaned up at all
	h, st := newRollbackTestHandler(t)
	handler := &blockedDeleteHandler{}
	handler.blocked.Store(true)
	resType := registerResourceHandler(t, "Unfixable", handler)
	seedStack(t, st, "skip-me", StatusUpdateRollbackFailed, StackResource{
		LogicalID:  "Service",
		PhysicalID: "physical-id",
		Type:       resType,
		Status:     ResourceDeleteFailed,
	})

	// When: the operator skips it
	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("ContinueUpdateRollback", map[string]string{
		"StackName":                "skip-me",
		"ResourcesToSkip.member.1": "Service",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	// Then: no delete is attempted, the resource is reported UPDATE_COMPLETE,
	// and the stack completes
	if got := handler.attempts.Load(); got != 0 {
		t.Errorf("delete attempts = %d, want 0 — a skipped resource must not be touched", got)
	}
	got, err := st.getStack(context.Background(), "skip-me")
	if err != nil {
		t.Fatalf("getStack: %v", err)
	}
	if got.Status != StatusUpdateRollbackComplete {
		t.Fatalf("stack status = %q, want %q", got.Status, StatusUpdateRollbackComplete)
	}
	if len(got.Resources) != 1 || got.Resources[0].Status != ResourceUpdateComplete {
		t.Errorf("resources = %+v, want the skipped resource at %s", got.Resources, ResourceUpdateComplete)
	}
}

func TestContinueUpdateRollback_resourcesToSkip_rejectsAResourceThatDidNotFail(t *testing.T) {
	// Given: a wedged stack whose other resource is perfectly healthy
	h, st := newRollbackTestHandler(t)
	seedStack(t, st, "healthy-skip", StatusUpdateRollbackFailed,
		StackResource{LogicalID: "Healthy", PhysicalID: "id", Type: "AWS::SQS::Queue", Status: ResourceCreateComplete},
		StackResource{LogicalID: "Broken", PhysicalID: "id2", Type: "AWS::SQS::Queue", Status: ResourceDeleteFailed},
	)

	// When: the healthy resource is named in ResourcesToSkip
	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("ContinueUpdateRollback", map[string]string{
		"StackName":                "healthy-skip",
		"ResourcesToSkip.member.1": "Healthy",
	}))

	// Then: the request is rejected before the stack is touched
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	var errResp queryErrorResponse
	if err := xml.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("unmarshal error response: %v; body: %s", err, rec.Body.String())
	}
	if errResp.Code != "ValidationError" {
		t.Errorf("error code = %q, want ValidationError", errResp.Code)
	}
	if !strings.Contains(errResp.Message, "Healthy") {
		t.Errorf("error message = %q, want it to name the resource", errResp.Message)
	}
	got, err := st.getStack(context.Background(), "healthy-skip")
	if err != nil {
		t.Fatalf("getStack: %v", err)
	}
	if got.Status != StatusUpdateRollbackFailed {
		t.Errorf("stack status = %q, want it unchanged at %q", got.Status, StatusUpdateRollbackFailed)
	}
}

func TestContinueUpdateRollback_resourcesToSkip_rejectsAnUnknownResource(t *testing.T) {
	// Given: a wedged stack
	h, st := newRollbackTestHandler(t)
	seedStack(t, st, "unknown-skip", StatusUpdateRollbackFailed,
		StackResource{LogicalID: "Broken", PhysicalID: "id", Type: "AWS::SQS::Queue", Status: ResourceDeleteFailed})

	// When: ResourcesToSkip names a resource the stack has never held
	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("ContinueUpdateRollback", map[string]string{
		"StackName":                "unknown-skip",
		"ResourcesToSkip.member.1": "Nope",
	}))

	// Then: 400 ValidationError naming it
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	var errResp queryErrorResponse
	if err := xml.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("unmarshal error response: %v; body: %s", err, rec.Body.String())
	}
	if errResp.Code != "ValidationError" || !strings.Contains(errResp.Message, "Nope") {
		t.Errorf("error = %#v, want a ValidationError naming Nope", errResp)
	}
}

func TestContinueUpdateRollback_resourcesToSkip_resolvesANestedStackPath(t *testing.T) {
	// Given: a parent whose rollback is blocked by a nested stack, which is
	// itself blocked by one of its own resources
	h, st := newRollbackTestHandler(t)
	childName := "wedged-parent-NestedStack-abcd1234"
	childARN := "arn:aws:cloudformation:us-east-1:000000000000:stack/" + childName + "/66666666-7777-8888-9999-000000000000"

	child := seedStack(t, st, childName, StatusUpdateRollbackFailed, StackResource{
		LogicalID:    "Inner",
		PhysicalID:   "inner-id",
		Type:         "AWS::SQS::Queue",
		Status:       ResourceDeleteFailed,
		StatusReason: "still in use",
	})
	child.StackID = childARN
	if err := st.putStack(context.Background(), child); err != nil {
		t.Fatalf("putStack child: %v", err)
	}

	seedStack(t, st, "wedged-parent", StatusUpdateRollbackFailed, StackResource{
		LogicalID:  "Child",
		PhysicalID: childARN,
		Type:       "AWS::CloudFormation::Stack",
		Status:     ResourceUpdateFailed,
	})

	// When: the inner resource is skipped through the nested-stack path form
	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("ContinueUpdateRollback", map[string]string{
		"StackName":                "wedged-parent",
		"ResourcesToSkip.member.1": "Child.Inner",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	// Then: the child's resource is skipped, the child reaches a terminal
	// rollback state, and the parent completes with it
	gotChild, err := st.getStack(context.Background(), childName)
	if err != nil {
		t.Fatalf("getStack child: %v", err)
	}
	if len(gotChild.Resources) != 1 || gotChild.Resources[0].Status != ResourceUpdateComplete {
		t.Errorf("child resources = %+v, want the skipped resource at %s", gotChild.Resources, ResourceUpdateComplete)
	}
	if gotChild.Status != StatusUpdateRollbackComplete {
		t.Errorf("child stack status = %q, want %q", gotChild.Status, StatusUpdateRollbackComplete)
	}
	gotParent, err := st.getStack(context.Background(), "wedged-parent")
	if err != nil {
		t.Fatalf("getStack parent: %v", err)
	}
	if gotParent.Status != StatusUpdateRollbackComplete {
		t.Errorf("parent stack status = %q, want %q", gotParent.Status, StatusUpdateRollbackComplete)
	}
}

func TestContinueUpdateRollback_resourcesToSkip_rejectsAnUnknownNestedStack(t *testing.T) {
	// Given: a wedged stack with no nested stack named Child
	h, st := newRollbackTestHandler(t)
	seedStack(t, st, "no-child", StatusUpdateRollbackFailed,
		StackResource{LogicalID: "Broken", PhysicalID: "id", Type: "AWS::SQS::Queue", Status: ResourceDeleteFailed})

	// When: a nested path is given anyway
	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("ContinueUpdateRollback", map[string]string{
		"StackName":                "no-child",
		"ResourcesToSkip.member.1": "Child.Inner",
	}))

	// Then: 400 ValidationError naming the member
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	var errResp queryErrorResponse
	if err := xml.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("unmarshal error response: %v; body: %s", err, rec.Body.String())
	}
	if errResp.Code != "ValidationError" || !strings.Contains(errResp.Message, "Child.Inner") {
		t.Errorf("error = %#v, want a ValidationError naming Child.Inner", errResp)
	}
}

// ---- invalid starting states -----------------------------------------------

func TestContinueUpdateRollback_fromAnyOtherState_returnsValidationError(t *testing.T) {
	// Given: stacks in every state AWS refuses to continue a rollback from.
	// UPDATE_FAILED is in the list on purpose: there is no rollback under way
	// to continue, and RollbackStack — not this operation — is what starts one.
	for _, status := range []string{
		StatusCreateComplete,
		StatusUpdateComplete,
		StatusUpdateFailed,
		StatusUpdateRollbackComplete,
		StatusUpdateRollbackInProgress,
		StatusCreateFailed,
		StatusRollbackComplete,
	} {
		t.Run(status, func(t *testing.T) {
			h, st := newRollbackTestHandler(t)
			seeded := seedStack(t, st, "settled", status)

			// When: ContinueUpdateRollback is called
			rec := httptest.NewRecorder()
			h.dispatch(rec, cfnPost("ContinueUpdateRollback", map[string]string{"StackName": "settled"}))

			// Then: AWS's update guard rejects it, naming the offending state
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
			}
			var errResp queryErrorResponse
			if err := xml.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
				t.Fatalf("unmarshal error response: %v; body: %s", err, rec.Body.String())
			}
			if errResp.Code != "ValidationError" {
				t.Errorf("error code = %q, want ValidationError", errResp.Code)
			}
			want := fmt.Sprintf("Stack:%s is in %s state and can not be updated.", seeded.StackID, status)
			if errResp.Message != want {
				t.Errorf("error message = %q, want %q", errResp.Message, want)
			}

			// Then: the stack status is untouched
			got, err := st.getStack(context.Background(), "settled")
			if err != nil {
				t.Fatalf("getStack: %v", err)
			}
			if got.Status != status {
				t.Errorf("stack status = %q, want it unchanged at %q", got.Status, status)
			}
		})
	}
}

func TestContinueUpdateRollback_unknownStack_returnsDoesNotExistError(t *testing.T) {
	// Given: an empty store
	h, _ := newRollbackTestHandler(t)

	// When: ContinueUpdateRollback names a stack that was never created
	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("ContinueUpdateRollback", map[string]string{"StackName": "ghost-stack"}))

	// Then: the same not-found shape the other CFN actions use
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	var errResp queryErrorResponse
	if err := xml.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("unmarshal error response: %v; body: %s", err, rec.Body.String())
	}
	if errResp.Code != "ValidationError" {
		t.Errorf("error code = %q, want ValidationError", errResp.Code)
	}
	if want := "Stack [ghost-stack] does not exist"; errResp.Message != want {
		t.Errorf("error message = %q, want %q", errResp.Message, want)
	}
}

func TestContinueUpdateRollback_missingStackName_returnsValidationError(t *testing.T) {
	// Given: a handler and a request with no StackName
	h, _ := newRollbackTestHandler(t)

	// When: ContinueUpdateRollback is called
	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("ContinueUpdateRollback", nil))

	// Then: 400 ValidationError
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
}
