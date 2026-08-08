package cloudformation

import (
	"context"
	"encoding/xml"
	"net/http/httptest"
	"strings"
	"testing"
)

type listStacksResponse struct {
	Summaries []struct {
		StackName    string `xml:"StackName"`
		StackStatus  string `xml:"StackStatus"`
		StatusReason string `xml:"StackStatusReason"`
	} `xml:"ListStacksResult>StackSummaries>member"`
}

// A stack that failed is the case a console user most needs the reason for,
// and ListStacks is where they see the stack first. AWS carries
// StackStatusReason on StackSummary for exactly that; Overcast dropped it, so
// the list could show that a deploy had failed but never why.
//
// https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_StackSummary.html
func TestListStacks_carriesStackStatusReason(t *testing.T) {
	// Given: one failed stack with a reason and one healthy stack without
	h, st := newRollbackTestHandler(t)
	failed := seedStack(t, st, "broken-stack", StatusUpdateRollbackFailed)
	failed.StatusReason = "resource Queue failed: queue name already in use"
	if err := st.putStack(context.Background(), failed); err != nil {
		t.Fatalf("putStack: %v", err)
	}
	healthy := seedStack(t, st, "good-stack", StatusCreateComplete)
	healthy.StatusReason = ""
	if err := st.putStack(context.Background(), healthy); err != nil {
		t.Fatalf("putStack: %v", err)
	}

	// When: the stacks are listed
	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("ListStacks", nil))

	// Then: the failed stack's summary carries the reason
	var resp listStacksResponse
	if err := xml.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v; body: %s", err, rec.Body.String())
	}
	byName := map[string]string{}
	for _, s := range resp.Summaries {
		byName[s.StackName] = s.StatusReason
	}
	const want = "resource Queue failed: queue name already in use"
	if got := byName["broken-stack"]; got != want {
		t.Errorf("broken-stack StackStatusReason = %q, want %q", got, want)
	}

	// Then: a stack with no reason omits the element entirely, as AWS does
	if got := byName["good-stack"]; got != "" {
		t.Errorf("good-stack StackStatusReason = %q, want empty", got)
	}
	if strings.Count(rec.Body.String(), "<StackStatusReason>") != 1 {
		t.Errorf("expected exactly one StackStatusReason element; body: %s", rec.Body.String())
	}
}

// The Query dispatch path and the typed path build the summary separately, so
// a field added to one can silently miss the other.
func TestListStacksTyped_carriesStackStatusReason(t *testing.T) {
	// Given: a stack that rolled back after a failed update
	h, st := newRollbackTestHandler(t)
	stack := seedStack(t, st, "rolled-back", StatusUpdateRollbackFailed)
	stack.StatusReason = "rollback failed: bucket not empty"
	if err := st.putStack(context.Background(), stack); err != nil {
		t.Fatalf("putStack: %v", err)
	}

	// When: the typed ListStacks runs
	resp, aerr := h.listStacksTyped(context.Background(), &listStacksReq{})
	if aerr != nil {
		t.Fatalf("listStacksTyped: %v", aerr)
	}

	// Then: the reason survives into the summary
	if len(resp.Result.StackSummaries) != 1 {
		t.Fatalf("summaries = %d, want 1", len(resp.Result.StackSummaries))
	}
	const want = "rollback failed: bucket not empty"
	if got := resp.Result.StackSummaries[0].StatusReason; got != want {
		t.Errorf("StackStatusReason = %q, want %q", got, want)
	}
}
