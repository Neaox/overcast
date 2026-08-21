package cloudformation

// Typed-path (CBOR wire, exercised here as direct Go calls per the pattern in
// provisioner_trace_test.go) coverage for #829: getStackByNameOrARN's
// DELETE_COMPLETE-by-name exclusion is a store-level change, and both the
// legacy Query/XML dispatch (handler.go, covered in stack_ref_test.go) and
// this typed dispatch (typed_logic.go) call into it independently, so both
// must be pinned.

import (
	"context"
	"testing"
)

func TestTypedReadOps_byName_deletedStack_doNotExist(t *testing.T) {
	// Given: a stack that has finished deleting
	h, st := newRollbackTestHandler(t)
	seeded := seedStack(t, st, "typed-name-deleted", StatusDeleteComplete, StackResource{
		LogicalID:  "Queue",
		PhysicalID: "http://localhost:4566/000000000000/q",
		Type:       "AWS::SQS::Queue",
		Status:     ResourceCreateComplete,
	})
	ctx := context.Background()

	// When/Then: every typed read operation refuses the name
	if _, aerr := h.describeStacksTyped(ctx, &describeStacksReq{StackName: "typed-name-deleted"}); aerr == nil {
		t.Error("describeStacksTyped by name after delete: want an error, got none")
	} else if aerr.Code != "ValidationError" {
		t.Errorf("describeStacksTyped by name after delete: code = %q, want ValidationError", aerr.Code)
	}
	if _, aerr := h.getTemplateTyped(ctx, &getTemplateReq{StackName: "typed-name-deleted"}); aerr == nil {
		t.Error("getTemplateTyped by name after delete: want an error, got none")
	}
	if _, aerr := h.describeStackResourcesTyped(ctx, &describeStackResourcesReq{StackName: "typed-name-deleted"}); aerr == nil {
		t.Error("describeStackResourcesTyped by name after delete: want an error, got none")
	}
	if _, aerr := h.listStackResourcesTyped(ctx, &listStackResourcesReq{StackName: "typed-name-deleted"}); aerr == nil {
		t.Error("listStackResourcesTyped by name after delete: want an error, got none")
	}
	if _, aerr := h.describeStackEventsTyped(ctx, &describeStackEventsReq{StackName: "typed-name-deleted"}); aerr == nil {
		t.Error("describeStackEventsTyped by name after delete: want an error, got none")
	}
	if _, aerr := h.getTemplateSummaryTyped(ctx, &getTemplateSummaryReq{StackName: "typed-name-deleted"}); aerr == nil {
		t.Error("getTemplateSummaryTyped by name after delete: want an error, got none")
	}

	// And: the same stack is still fully readable by ARN through the typed path
	resp, aerr := h.describeStacksTyped(ctx, &describeStacksReq{StackName: seeded.StackID})
	if aerr != nil {
		t.Fatalf("describeStacksTyped by ARN after delete: %v", aerr)
	}
	if len(resp.Result.Stacks) != 1 || resp.Result.Stacks[0].StackStatus != StatusDeleteComplete {
		t.Errorf("describeStacksTyped by ARN after delete: %#v", resp.Result.Stacks)
	}
}

func TestTypedDescribeStacks_deleteInProgress_stillResolvesByName(t *testing.T) {
	h, st := newRollbackTestHandler(t)
	seedStack(t, st, "typed-still-deleting", StatusDeleteInProgress)

	resp, aerr := h.describeStacksTyped(context.Background(), &describeStacksReq{StackName: "typed-still-deleting"})
	if aerr != nil {
		t.Fatalf("describeStacksTyped: %v", aerr)
	}
	if len(resp.Result.Stacks) != 1 || resp.Result.Stacks[0].StackStatus != StatusDeleteInProgress {
		t.Errorf("describeStacksTyped result = %#v, want one DELETE_IN_PROGRESS stack", resp.Result.Stacks)
	}
}
