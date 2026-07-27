package cloudformation

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// newRollbackTestHandler builds a Handler wired to an in-memory store and a
// real clock. CFNSyncWait is generous so that awaitBriefly blocks until the
// rollback goroutine reaches its terminal state — these stacks have no
// resources to dispatch, so completion is immediate and the assertions below
// observe the end state rather than an IN_PROGRESS snapshot.
func newRollbackTestHandler(t *testing.T) (*Handler, *cfnStore) {
	t.Helper()
	cfg := &config.Config{
		Region:      "us-east-1",
		AccountID:   "000000000000",
		CFNSyncWait: 5 * time.Second,
	}
	clk := clock.New()
	st := newCFNStore(state.NewMemoryStore(), cfg.Region, clk)
	log := serviceutil.NewServiceLogger(zap.NewNop(), "cloudformation")
	prov := newProvisioner(cfg, st, clk, log)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		prov.stop(ctx)
	})
	return newHandler(cfg, st, log, clk, prov), st
}

// seedStack persists a stack in the given status so a rollback can be
// requested against it.
func seedStack(t *testing.T, st *cfnStore, name, status string, resources ...StackResource) *Stack {
	t.Helper()
	stack := &Stack{
		StackName:    name,
		StackID:      "arn:aws:cloudformation:us-east-1:000000000000:stack/" + name + "/11111111-2222-3333-4444-555555555555",
		Region:       "us-east-1",
		TemplateBody: `{"Resources":{}}`,
		Status:       status,
		StatusReason: "seeded",
		Resources:    resources,
		CreatedAt:    time.Unix(0, 0).UTC(),
	}
	if err := st.putStack(context.Background(), stack); err != nil {
		t.Fatalf("putStack: %v", err)
	}
	return stack
}

// cfnPost builds an AWS Query-protocol POST for the given action.
func cfnPost(action string, params map[string]string) *http.Request {
	form := url.Values{}
	form.Set("Action", action)
	form.Set("Version", "2010-05-15")
	for k, v := range params {
		form.Set(k, v)
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

type rollbackStackResponse struct {
	StackID string `xml:"RollbackStackResult>StackId"`
}

type queryErrorResponse struct {
	Code    string `xml:"Error>Code"`
	Message string `xml:"Error>Message"`
}

// ---- valid starting states -------------------------------------------------

func TestRollbackStack_fromUpdateFailed_reachesUpdateRollbackComplete(t *testing.T) {
	// Given: a stack left paused in UPDATE_FAILED by a failed update
	h, st := newRollbackTestHandler(t)
	seeded := seedStack(t, st, "cdk-toolkit", StatusUpdateFailed)

	// When: RollbackStack is called
	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("RollbackStack", map[string]string{"StackName": "cdk-toolkit"}))

	// Then: the call succeeds and echoes the stack ID
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var resp rollbackStackResponse
	if err := xml.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v; body: %s", err, rec.Body.String())
	}
	if resp.StackID != seeded.StackID {
		t.Errorf("StackId = %q, want %q", resp.StackID, seeded.StackID)
	}

	// Then: the stack actually reaches the terminal rollback state
	got, err := st.getStack(context.Background(), "cdk-toolkit")
	if err != nil {
		t.Fatalf("getStack: %v", err)
	}
	if got.Status != StatusUpdateRollbackComplete {
		t.Errorf("stack status = %q, want %q", got.Status, StatusUpdateRollbackComplete)
	}
}

func TestRollbackStack_fromUpdateRollbackFailed_reachesUpdateRollbackComplete(t *testing.T) {
	// Given: a stack whose automatic update rollback itself failed
	h, st := newRollbackTestHandler(t)
	seedStack(t, st, "half-rolled-back", StatusUpdateRollbackFailed)

	// When: RollbackStack is called
	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("RollbackStack", map[string]string{"StackName": "half-rolled-back"}))

	// Then: the retry drives the stack to UPDATE_ROLLBACK_COMPLETE
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	got, err := st.getStack(context.Background(), "half-rolled-back")
	if err != nil {
		t.Fatalf("getStack: %v", err)
	}
	if got.Status != StatusUpdateRollbackComplete {
		t.Errorf("stack status = %q, want %q", got.Status, StatusUpdateRollbackComplete)
	}
}

func TestRollbackStack_fromCreateFailed_reachesRollbackComplete(t *testing.T) {
	// Given: a stack whose initial create failed
	h, st := newRollbackTestHandler(t)
	seedStack(t, st, "never-created", StatusCreateFailed)

	// When: RollbackStack is called
	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("RollbackStack", map[string]string{"StackName": "never-created"}))

	// Then: the stack follows the create-rollback path to ROLLBACK_COMPLETE
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	got, err := st.getStack(context.Background(), "never-created")
	if err != nil {
		t.Fatalf("getStack: %v", err)
	}
	if got.Status != StatusRollbackComplete {
		t.Errorf("stack status = %q, want %q", got.Status, StatusRollbackComplete)
	}
}

func TestRollbackStack_byStackARN_resolvesTheStack(t *testing.T) {
	// Given: a rollbackable stack addressed by ARN rather than name
	h, st := newRollbackTestHandler(t)
	seeded := seedStack(t, st, "arn-addressed", StatusUpdateFailed)

	// When: RollbackStack is called with the stack ARN
	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("RollbackStack", map[string]string{"StackName": seeded.StackID}))

	// Then: the stack is found and rolled back
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	got, err := st.getStack(context.Background(), "arn-addressed")
	if err != nil {
		t.Fatalf("getStack: %v", err)
	}
	if got.Status != StatusUpdateRollbackComplete {
		t.Errorf("stack status = %q, want %q", got.Status, StatusUpdateRollbackComplete)
	}
}

func TestRollbackStack_retiresResourcesLeftInAFailedState(t *testing.T) {
	// Given: an UPDATE_FAILED stack carrying a resource that failed to update
	// and one that was never physically created
	h, st := newRollbackTestHandler(t)
	seedStack(t, st, "messy", StatusUpdateFailed,
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

	// When: RollbackStack is called
	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("RollbackStack", map[string]string{"StackName": "messy"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	// Then: no resource is left in a failed state, and the never-created one
	// is gone from the stack entirely
	got, err := st.getStack(context.Background(), "messy")
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
			t.Errorf("resource %s left in failed status %q after rollback", r.LogicalID, r.Status)
		}
	}
}

// ---- invalid starting states -----------------------------------------------

func TestRollbackStack_fromNonRollbackableState_returnsValidationError(t *testing.T) {
	// Given: stacks in states real CloudFormation refuses to roll back
	for _, status := range []string{
		StatusCreateComplete,
		StatusUpdateComplete,
		StatusUpdateInProgress,
		StatusRollbackComplete,
		StatusDeleteInProgress,
	} {
		t.Run(status, func(t *testing.T) {
			h, st := newRollbackTestHandler(t)
			seedStack(t, st, "stable", status)

			// When: RollbackStack is called
			rec := httptest.NewRecorder()
			h.dispatch(rec, cfnPost("RollbackStack", map[string]string{"StackName": "stable"}))

			// Then: AWS returns 400 ValidationError naming the offending state
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
			want := "Stack [stable] is in " + status + " state and can not be rolled back."
			if errResp.Message != want {
				t.Errorf("error message = %q, want %q", errResp.Message, want)
			}

			// Then: the stack status is untouched
			got, err := st.getStack(context.Background(), "stable")
			if err != nil {
				t.Fatalf("getStack: %v", err)
			}
			if got.Status != status {
				t.Errorf("stack status = %q, want it unchanged at %q", got.Status, status)
			}
		})
	}
}

func TestRollbackStack_unknownStack_returnsDoesNotExistError(t *testing.T) {
	// Given: an empty store
	h, _ := newRollbackTestHandler(t)

	// When: RollbackStack names a stack that was never created
	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("RollbackStack", map[string]string{"StackName": "ghost-stack"}))

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

func TestRollbackStack_missingStackName_returnsValidationError(t *testing.T) {
	// Given: a handler and a request with no StackName
	h, _ := newRollbackTestHandler(t)

	// When: RollbackStack is called
	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("RollbackStack", nil))

	// Then: 400 ValidationError
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
}

// ---- events ----------------------------------------------------------------

type describeEventsResponse struct {
	Events []struct {
		ResourceStatus string `xml:"ResourceStatus"`
		ResourceType   string `xml:"ResourceType"`
	} `xml:"DescribeStackEventsResult>StackEvents>member"`
}

func TestRollbackStack_emitsRollbackEventsVisibleToDescribeStackEvents(t *testing.T) {
	// Given: an UPDATE_FAILED stack
	h, st := newRollbackTestHandler(t)
	seedStack(t, st, "eventful", StatusUpdateFailed)

	// When: the stack is rolled back and its events are described
	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("RollbackStack", map[string]string{"StackName": "eventful"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("rollback status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	evRec := httptest.NewRecorder()
	h.dispatch(evRec, cfnPost("DescribeStackEvents", map[string]string{"StackName": "eventful"}))
	if evRec.Code != http.StatusOK {
		t.Fatalf("DescribeStackEvents status = %d, want 200; body: %s", evRec.Code, evRec.Body.String())
	}

	var resp describeEventsResponse
	if err := xml.Unmarshal(evRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal events: %v; body: %s", err, evRec.Body.String())
	}

	// Then: both rollback transitions are recorded against the stack resource
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
