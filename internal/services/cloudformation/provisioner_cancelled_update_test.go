package cloudformation

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/Neaox/overcast/internal/config"
)

// cancellingUpdateHandler updates like rollbackUpdateHandler and then cancels
// the walk's context after the named resource, so the next loop iteration
// takes the ctx.Err() exit — the shutdown shape, where the update stops
// between resources rather than failing on one.
type cancellingUpdateHandler struct {
	rollbackUpdateHandler
	cancel        context.CancelFunc
	cancelAfterID string
}

func (h *cancellingUpdateHandler) Update(ctx context.Context, next http.Handler, cfg *config.Config, physicalID string, props map[string]any, previous map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	id, attrs, err := h.rollbackUpdateHandler.Update(ctx, next, cfg, physicalID, props, previous, rCtx)
	if h.cancel != nil && physicalID == h.cancelAfterID {
		h.cancel()
	}
	return id, attrs, err
}

// cancelledUpdateFixture is disableRollbackUpdateFixture with rollback left
// enabled: a cancelled walk never runs the reverse pass regardless of
// DisableRollback (the context that would drive it is the one that died), so
// the retention must not depend on the flag.
func cancelledUpdateFixture(resourceType string) (*Stack, *Template) {
	stack, tmpl := disableRollbackUpdateFixture(resourceType)
	stack.StackName = "cancelled-update"
	stack.StackID = "arn:aws:cloudformation:us-east-1:000000000000:stack/cancelled-update/1111"
	stack.DisableRollback = false
	return stack, tmpl
}

// A cancelled update stops where it stands, and the stack record is the only
// account of where that was. Discarding the accumulated records would persist
// a pre-update state that no longer matches the service-side resources.
func TestUpdateStack_cancelledUpdateRetainsAppliedResources(t *testing.T) {
	// Given: a three-resource update whose context is cancelled after the
	// first resource has applied.
	handler := &cancellingUpdateHandler{cancelAfterID: "first-id"}
	resourceType := registerRollbackUpdateHandler(t, handler)
	p := newProvisionerTestFixture(t)
	stack, tmpl := cancelledUpdateFixture(resourceType)
	ctx, cancel := context.WithCancel(p.regionCtx(stack.Region))
	defer cancel()
	handler.cancel = cancel

	// When: the walk reaches the cancellation.
	p.updateStackResourcesCtx(ctx, stack, tmpl, captureStackGeneration(stack))

	// Then: only the first resource was touched — no reverse pass ran even
	// though rollback is enabled — and the stack is UPDATE_FAILED.
	wantCalls := []rollbackUpdateCall{{physicalID: "first-id", version: "new"}}
	if !slices.Equal(handler.calls, wantCalls) {
		t.Fatalf("update calls = %+v, want %+v (a cancelled walk neither continues nor reverses)", handler.calls, wantCalls)
	}
	if stack.Status != StatusUpdateFailed || stack.StatusReason != "cancelled" {
		t.Fatalf("stack = status %q reason %q, want UPDATE_FAILED / cancelled", stack.Status, stack.StatusReason)
	}

	// And: the resource the walk already updated keeps the state it reached.
	first := stackResourceByLogicalID(t, stack.Resources, "First")
	if first.Status != ResourceUpdateComplete || first.Properties["Version"] != "new" || first.Attributes["Version"] != "new" {
		t.Errorf("First = %+v, want UPDATE_COMPLETE holding the applied state", first)
	}

	// And: the resources the walk never reached keep their prior records —
	// nothing touched them, so nothing about them may change.
	second := stackResourceByLogicalID(t, stack.Resources, "Second")
	if second.Status != ResourceCreateComplete || second.Properties["Version"] != "old" || second.PropertiesHash != "old-second" {
		t.Errorf("Second = %+v, want the untouched prior record", second)
	}
	if second.DeletionPolicy != "Retain" || second.UpdateReplacePolicy != "Snapshot" {
		t.Errorf("Second lost its policies: %+v", second)
	}
	third := stackResourceByLogicalID(t, stack.Resources, "Third")
	if third.Status != ResourceCreateComplete || third.Properties["Version"] != "old" || third.PropertiesHash != "old-third" {
		t.Errorf("Third = %+v, want the untouched prior record", third)
	}

	// And: the retained list is what was persisted, not just what the local
	// pointer holds.
	persisted, err := p.store.getStack(context.Background(), stack.StackName)
	if err != nil || persisted == nil {
		t.Fatalf("get persisted stack: %v", err)
	}
	if persisted.Status != StatusUpdateFailed {
		t.Fatalf("persisted status = %q, want %q", persisted.Status, StatusUpdateFailed)
	}
	if len(persisted.Resources) != 3 {
		t.Fatalf("persisted resources = %+v, want all three retained", persisted.Resources)
	}
	persistedFirst := stackResourceByLogicalID(t, persisted.Resources, "First")
	if persistedFirst.Status != ResourceUpdateComplete || persistedFirst.Properties["Version"] != "new" {
		t.Errorf("persisted First = %+v, want the applied update to survive persistence", persistedFirst)
	}
}

// The point of retaining the records is the next attempt: it must pick up
// where the cancelled one stopped rather than replay a resource that already
// updated.
func TestUpdateStack_cancelledUpdateRecoveryUsesRetainedRecords(t *testing.T) {
	// Given: an update cancelled after its first resource applied.
	handler := &cancellingUpdateHandler{cancelAfterID: "first-id"}
	resourceType := registerRollbackUpdateHandler(t, handler)
	p := newProvisionerTestFixture(t)
	stack, tmpl := cancelledUpdateFixture(resourceType)
	ctx, cancel := context.WithCancel(p.regionCtx(stack.Region))
	defer cancel()
	handler.cancel = cancel
	p.updateStackResourcesCtx(ctx, stack, tmpl, captureStackGeneration(stack))
	if stack.Status != StatusUpdateFailed {
		t.Fatalf("stack status = %q, want %q before recovery", stack.Status, StatusUpdateFailed)
	}

	// When: the same update is retried on a live context.
	handler.cancel = nil
	handler.calls = nil
	p.updateStackResources(stack, tmpl, captureStackGeneration(stack))

	// Then: the resource that already updated is left alone, and only the two
	// the cancellation stopped short of are touched.
	wantCalls := []rollbackUpdateCall{
		{physicalID: "second-id", version: "new"},
		{physicalID: "third-id", version: "new"},
	}
	if !slices.Equal(handler.calls, wantCalls) {
		t.Fatalf("recovery update calls = %+v, want %+v", handler.calls, wantCalls)
	}
	if stack.Status != StatusUpdateComplete {
		t.Fatalf("stack status = %q, want %q", stack.Status, StatusUpdateComplete)
	}
	for _, logicalID := range []string{"First", "Second", "Third"} {
		resource := stackResourceByLogicalID(t, stack.Resources, logicalID)
		if resource.Properties["Version"] != "new" {
			t.Errorf("%s = %+v, want the recovered update applied", logicalID, resource)
		}
	}
}
