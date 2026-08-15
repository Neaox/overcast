package cloudformation

// fail_stack_reason_test.go — what a failed stack says about itself at the
// stack level, as opposed to at the resource level.
//
// The two are different fields with different jobs on real CloudFormation. A
// stack left in CREATE_FAILED by --disable-rollback carries a summary naming
// the logical IDs that failed; the underlying error stays on the resource and
// on its event. A stack that reached a terminal *_ROLLBACK_COMPLETE carries no
// stack-level reason at all, because the explanation is the failure event and
// the failed resource it points at.

import (
	"context"
	"strings"
	"testing"
)

// failReasonUnstableStack builds a one-resource stack whose only resource
// creates and then never settles. disableRollback picks which of the two
// create-failure branches the provisioner takes.
func failReasonUnstableStack(t *testing.T, name string, disableRollback bool) (*provisioner, *Stack, *Template) {
	t.Helper()
	_, resType := registerUnstable(t)
	p := newProvisionerTestFixture(t)
	stack := &Stack{
		StackName:       name,
		StackID:         "arn:aws:cloudformation:us-east-1:000000000000:stack/" + name + "/1111",
		Region:          "us-east-1",
		DisableRollback: disableRollback,
	}
	tmpl := &Template{Resources: map[string]TemplateResource{
		"MyBucket": {Type: resType},
	}}
	return p, stack, tmpl
}

// AWS answers describe-stacks on a --disable-rollback stack with a summary
// naming the logical IDs, not with the underlying service error:
//
//	"StackStatusReason": "The following resource(s) failed to create: [MyBucket]"
//
// https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/stack-failure-options.html
//
// The assertions below match the prefix and the bracketed list rather than the
// whole string. That doc example is rendered with curly quotes, so it is not
// captured output and its punctuation carries no authority — a real-world
// report of the same message ends it with a period and appends a further
// sentence. Pinning either would be false precision.
func TestCreateStack_disableRollbackReasonNamesTheFailedResources(t *testing.T) {
	// Given: a stack created with rollback disabled whose resource fails.
	p, stack, tmpl := failReasonUnstableStack(t, "no-rollback-create", true)

	// When: provisioning fails.
	p.provisionStackResources(stack, tmpl)

	// Then: the stack is CREATE_FAILED and summarises which resources failed.
	if stack.Status != StatusCreateFailed {
		t.Fatalf("stack status = %q, want %q", stack.Status, StatusCreateFailed)
	}
	const wantPrefix = "The following resource(s) failed to create: ["
	if !strings.HasPrefix(stack.StatusReason, wantPrefix) {
		t.Errorf("StackStatusReason = %q, want it to start with %q", stack.StatusReason, wantPrefix)
	}
	if !strings.Contains(stack.StatusReason, "[MyBucket]") {
		t.Errorf("StackStatusReason = %q, want the bracketed logical ID list [MyBucket]", stack.StatusReason)
	}

	// And: the stack-level summary is a summary — the service error belongs to
	// the resource, not to the stack.
	if strings.Contains(stack.StatusReason, "never came up") {
		t.Errorf("StackStatusReason = %q, want the per-resource error left on the resource", stack.StatusReason)
	}

	// And: no detail is lost — the resource record and its event still carry the
	// underlying failure, which is where AWS keeps it.
	failed := stackResourceByLogicalID(t, stack.Resources, "MyBucket")
	if failed.Status != ResourceCreateFailed || !strings.Contains(failed.StatusReason, "never came up") {
		t.Errorf("MyBucket = %+v, want CREATE_FAILED carrying the underlying error", failed)
	}
	assertStackResourceEvent(t, p, stack.StackName, "MyBucket", ResourceCreateFailed, "never came up")

	// And: the summary is what a restart reads back.
	persisted, err := p.store.getStack(context.Background(), stack.StackName)
	if err != nil || persisted == nil {
		t.Fatalf("get persisted stack: %v", err)
	}
	if persisted.StatusReason != stack.StatusReason {
		t.Errorf("persisted StackStatusReason = %q, want %q", persisted.StatusReason, stack.StatusReason)
	}
}

// The retracted half of #823. A stack that reached a terminal rollback status
// has no StackStatusReason on real AWS — describe-stacks omits the key, and the
// CLI renders it as null when it exists — so the surviving explanation is the
// *_IN_PROGRESS event and the failed resource. Clearing it is correct; this
// pins that so nobody "restores" it.
//
// https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/view-stack-events-by-operation.html
func TestProvisioner_terminalRollbackCarriesNoStackReason(t *testing.T) {
	t.Run("create rollback", func(t *testing.T) {
		// Given: a stack whose resource fails, with rollback enabled.
		p, stack, tmpl := failReasonUnstableStack(t, "rolled-back-create", false)

		// When: the failed create is unwound.
		p.provisionStackResources(stack, tmpl)

		// Then: the terminal stack carries no stack-level reason.
		if stack.Status != StatusRollbackComplete {
			t.Fatalf("stack status = %q, want %q", stack.Status, StatusRollbackComplete)
		}
		failReasonAssertNoStackReason(t, p, stack)
	})

	t.Run("update rollback", func(t *testing.T) {
		// Given: an existing stack whose update replaces a resource that then
		// never settles.
		_, resType := registerUnstable(t)
		p := newProvisionerTestFixture(t)
		stack := &Stack{
			StackName: "rolled-back-update",
			StackID:   "arn:aws:cloudformation:us-east-1:000000000000:stack/rolled-back-update/1111",
			Region:    "us-east-1",
			Resources: []StackResource{{
				LogicalID:      "MyBucket",
				PhysicalID:     "original-physical-id",
				Type:           resType,
				Status:         ResourceCreateComplete,
				PropertiesHash: "old-properties",
			}},
		}
		tmpl := &Template{Resources: map[string]TemplateResource{
			"MyBucket": {Type: resType, Properties: map[string]any{"Version": "new"}},
		}}

		// When: the failed update is unwound.
		p.updateStackResources(stack, tmpl, captureStackGeneration(stack))

		// Then: the terminal stack carries no stack-level reason.
		if stack.Status != StatusUpdateRollbackComplete {
			t.Fatalf("stack status = %q, want %q", stack.Status, StatusUpdateRollbackComplete)
		}
		failReasonAssertNoStackReason(t, p, stack)
	})

	t.Run("explicit rollback to stable", func(t *testing.T) {
		// Given: an UPDATE_FAILED stack a client asks to roll back.
		_, resType := registerUnstable(t)
		p := newProvisionerTestFixture(t)
		stack := &Stack{
			StackName:    "rolled-back-to-stable",
			StackID:      "arn:aws:cloudformation:us-east-1:000000000000:stack/rolled-back-to-stable/1111",
			Region:       "us-east-1",
			Status:       StatusUpdateFailed,
			StatusReason: "resource MyBucket failed: the database never came up",
			Resources: []StackResource{{
				LogicalID:    "MyBucket",
				PhysicalID:   "original-physical-id",
				Type:         resType,
				Status:       ResourceUpdateFailed,
				StatusReason: "the database never came up",
			}},
		}
		if err := p.store.putStack(context.Background(), stack); err != nil {
			t.Fatalf("seed stack: %v", err)
		}

		// When: RollbackStack drives it back to a stable state.
		p.rollbackStackResourcesCtx(context.Background(), stack, rollbackStackOptions{})

		// Then: the terminal stack carries no stack-level reason.
		if stack.Status != StatusUpdateRollbackComplete {
			t.Fatalf("stack status = %q, want %q", stack.Status, StatusUpdateRollbackComplete)
		}
		failReasonAssertNoStackReason(t, p, stack)
	})
}

// failReasonAssertNoStackReason checks the terminal stack record — in memory
// and as persisted — carries no stack-level reason.
func failReasonAssertNoStackReason(t *testing.T, p *provisioner, stack *Stack) {
	t.Helper()
	if stack.StatusReason != "" {
		t.Errorf("StackStatusReason = %q, want empty on %s", stack.StatusReason, stack.Status)
	}
	persisted, err := p.store.getStack(context.Background(), stack.StackName)
	if err != nil || persisted == nil {
		t.Fatalf("get persisted stack: %v", err)
	}
	if persisted.StatusReason != "" {
		t.Errorf("persisted StackStatusReason = %q, want empty on %s", persisted.StatusReason, persisted.Status)
	}
}
