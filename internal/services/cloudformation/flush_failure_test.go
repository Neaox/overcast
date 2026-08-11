package cloudformation

// flush_failure_test.go — a store that cannot flush in time must not fail a
// stack that provisioned.
//
// A deploy died on this: every resource created, the stack reached
// CREATE_COMPLETE, and then the end-of-operation flush of the store's pending
// writes ran out of its five seconds and turned the whole thing into
//
//	CREATE_FAILED — persistent state flush failed: context deadline exceeded
//
// against AWS::CloudFormation::Stack, naming no resource, because no resource
// was involved. The flush is store-wide, so it carries every service's queued
// writes — a deploy that has just uploaded its assets can have far more than
// five seconds of them — and an uncommitted batch is neither lost nor
// abandoned: HybridStore puts it back at the head of the pending queue and
// leaves the pending log for the next flush or for replay at startup.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
)

// flushRefusingStore is a store whose flush never completes. Everything else
// about it works: the provisioner reads and writes through it normally, which
// is the situation being modelled — the data is there, it is just not on disk
// yet.
type flushRefusingStore struct {
	state.Store
	err error
}

func (s flushRefusingStore) Flush(context.Context) error { return s.err }

// newFlushRefusingProvisioner is newProvisionerTestFixture with a store that
// refuses to flush.
func newFlushRefusingProvisioner(t *testing.T, err error) *provisioner {
	t.Helper()
	p := newProvisionerTestFixture(t)
	p.store.s = flushRefusingStore{Store: p.store.s, err: err}
	return p
}

// settlingHandler provisions without complaint, so the only thing left that
// could fail the stack is the flush.
type settlingHandler struct{}

func (settlingHandler) Create(context.Context, http.Handler, *config.Config, map[string]any, *resolveContext) (string, map[string]string, error) {
	return "physical-id-of-a-resource-that-provisioned", nil, nil
}

func (settlingHandler) Delete(context.Context, http.Handler, *config.Config, string, *resolveContext) error {
	return nil
}

func registerSettling(t *testing.T) string {
	t.Helper()
	resType := "Test::Flush::" + t.Name()
	resourceHandlers[resType] = settlingHandler{}
	t.Cleanup(func() { delete(resourceHandlers, resType) })
	return resType
}

func TestProvisionStack_flushTimeoutDoesNotFailAStackThatProvisioned(t *testing.T) {
	// Given: a stack of one resource that creates cleanly, and a store whose
	// flush times out.
	p := newFlushRefusingProvisioner(t, context.DeadlineExceeded)
	stack := &Stack{
		StackName: "flush-times-out",
		StackID:   "arn:aws:cloudformation:us-east-1:000000000000:stack/flush-times-out/1111",
		Region:    "us-east-1",
	}
	tmpl := &Template{Resources: map[string]TemplateResource{
		"Widget": {Type: registerSettling(t)},
	}}

	// When: the stack is provisioned.
	p.provisionStackResources(stack, tmpl)

	// Then: it is complete. The resources exist and answer requests; whether
	// the record of them has reached SQLite yet is not CloudFormation's
	// question to answer.
	if stack.Status != StatusCreateComplete {
		t.Fatalf("stack status = %q (%s), want %s", stack.Status, stack.StatusReason, StatusCreateComplete)
	}
	if stack.StatusReason != "" {
		t.Errorf("StackStatusReason = %q, want none", stack.StatusReason)
	}

	// And: no event contradicts that. The failure used to arrive as a second,
	// later event against the stack itself, after CREATE_COMPLETE had already
	// been recorded — which is why a reader could not tell which resource it
	// came from.
	events, err := p.store.getStackEvents(context.Background(), stack.StackName)
	if err != nil {
		t.Fatalf("getStackEvents: %v", err)
	}
	for _, e := range events {
		if strings.Contains(e.ResourceStatusReason, "flush") {
			t.Errorf("event %s/%s reason = %q, want the flush kept out of the stack's events",
				e.LogicalResourceID, e.ResourceStatus, e.ResourceStatusReason)
		}
		if e.ResourceStatus == StatusCreateFailed {
			t.Errorf("event %s = %s, want no failure event", e.LogicalResourceID, e.ResourceStatus)
		}
	}
}

// A flush that fails for a reason other than the clock is the same call with
// the same consequence — the batch goes back on the pending queue either way —
// so it gets the same treatment. Pinned separately because "only tolerate
// timeouts" is the obvious next thing for someone to add.
func TestProvisionStack_flushErrorDoesNotFailAStackThatProvisioned(t *testing.T) {
	p := newFlushRefusingProvisioner(t, errors.New("database is locked"))
	stack := &Stack{
		StackName: "flush-errors",
		StackID:   "arn:aws:cloudformation:us-east-1:000000000000:stack/flush-errors/1111",
		Region:    "us-east-1",
	}
	tmpl := &Template{Resources: map[string]TemplateResource{
		"Widget": {Type: registerSettling(t)},
	}}

	p.provisionStackResources(stack, tmpl)

	if stack.Status != StatusCreateComplete {
		t.Fatalf("stack status = %q (%s), want %s", stack.Status, stack.StatusReason, StatusCreateComplete)
	}
}

// A stack that fails for a real reason still fails, and still says why. The
// change above is about what a flush means, not about softening failures.
func TestProvisionStack_resourceFailureStillFailsTheStackWhenFlushingIsBroken(t *testing.T) {
	_, resType := registerUnstable(t)
	p := newFlushRefusingProvisioner(t, context.DeadlineExceeded)
	stack := &Stack{
		StackName:       "resource-fails",
		StackID:         "arn:aws:cloudformation:us-east-1:000000000000:stack/resource-fails/1111",
		Region:          "us-east-1",
		DisableRollback: true,
	}
	tmpl := &Template{Resources: map[string]TemplateResource{
		"MyBucket": {Type: resType},
	}}

	p.provisionStackResources(stack, tmpl)

	if stack.Status != StatusCreateFailed {
		t.Fatalf("stack status = %q, want %s", stack.Status, StatusCreateFailed)
	}
	if !strings.Contains(stack.StatusReason, "[MyBucket]") {
		t.Errorf("StackStatusReason = %q, want it to name the resource that failed", stack.StatusReason)
	}
}
