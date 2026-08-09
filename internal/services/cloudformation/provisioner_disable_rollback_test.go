package cloudformation

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// disableRollbackUpdateFixture builds a three-resource stack whose middle
// resource rejects the update. Third depends on Second, so orchestration never
// reaches it — it is the "untouched" case the retained list must not lose.
func disableRollbackUpdateFixture(resourceType string) (*Stack, *Template) {
	stack := &Stack{
		StackName:       "disabled-rollback-update",
		StackID:         "arn:aws:cloudformation:us-east-1:000000000000:stack/disabled-rollback-update/1111",
		Region:          "us-east-1",
		DisableRollback: true,
		Resources: []StackResource{
			{
				LogicalID:      "First",
				PhysicalID:     "first-id",
				Type:           resourceType,
				Status:         ResourceCreateComplete,
				Attributes:     map[string]string{"Version": "old"},
				PropertiesHash: "old-first",
				Properties:     map[string]any{"Version": "old"},
			},
			{
				LogicalID:           "Second",
				PhysicalID:          "second-id",
				Type:                resourceType,
				Status:              ResourceCreateComplete,
				Attributes:          map[string]string{"Version": "old"},
				PropertiesHash:      "old-second",
				Properties:          map[string]any{"Version": "old"},
				DeletionPolicy:      "Retain",
				UpdateReplacePolicy: "Snapshot",
			},
			{
				LogicalID:      "Third",
				PhysicalID:     "third-id",
				Type:           resourceType,
				Status:         ResourceCreateComplete,
				PropertiesHash: "old-third",
				Properties:     map[string]any{"Version": "old"},
			},
		},
	}
	tmpl := &Template{Resources: map[string]TemplateResource{
		"First":  {Type: resourceType, Properties: map[string]any{"Version": "new"}},
		"Second": {Type: resourceType, Properties: map[string]any{"Version": "new"}, DependsOn: "First"},
		"Third":  {Type: resourceType, Properties: map[string]any{"Version": "new"}, DependsOn: "Second"},
	}}
	return stack, tmpl
}

// With rollback disabled the stack is the only record of what the attempt
// reached. Discarding it would leave DescribeStackResources reporting the
// pre-update properties of a resource the update already changed.
func TestUpdateStack_disableRollbackRetainsAttemptedResources(t *testing.T) {
	// Given: a stack with rollback disabled whose second resource rejects the
	// update without applying it.
	handler := &rollbackUpdateHandler{forwardFailureID: "second-id"}
	resourceType := registerRollbackUpdateHandler(t, handler)
	p := newProvisionerTestFixture(t)
	stack, tmpl := disableRollbackUpdateFixture(resourceType)

	// When: the update fails.
	p.updateStackResources(stack, tmpl, captureStackGeneration(stack))

	// Then: nothing is reversed, and the stack is UPDATE_FAILED.
	wantCalls := []rollbackUpdateCall{
		{physicalID: "first-id", version: "new"},
		{physicalID: "second-id", version: "new"},
	}
	if !slices.Equal(handler.calls, wantCalls) {
		t.Fatalf("update calls = %+v, want %+v (no reverse update with rollback disabled)", handler.calls, wantCalls)
	}
	if stack.Status != StatusUpdateFailed || !strings.Contains(stack.StatusReason, "later resource update failed") {
		t.Fatalf("stack = status %q reason %q, want UPDATE_FAILED naming the resource failure", stack.Status, stack.StatusReason)
	}

	// And: the resource that did update keeps the state it reached.
	first := stackResourceByLogicalID(t, stack.Resources, "First")
	if first.Status != ResourceUpdateComplete || first.Properties["Version"] != "new" || first.Attributes["Version"] != "new" {
		t.Errorf("First = %+v, want UPDATE_COMPLETE holding the attempted state", first)
	}

	// And: the resource that failed stays visibly failed, still describing the
	// state it actually holds — the update was rejected, not applied.
	second := stackResourceByLogicalID(t, stack.Resources, "Second")
	if second.Status != ResourceUpdateFailed || !strings.Contains(second.StatusReason, "later resource update failed") {
		t.Errorf("Second = %+v, want UPDATE_FAILED with the handler's reason", second)
	}
	if second.PhysicalID != "second-id" || second.Properties["Version"] != "old" || second.PropertiesHash != "old-second" {
		t.Errorf("Second = %+v, want the unapplied update to leave the previous state recorded", second)
	}
	if second.DeletionPolicy != "Retain" || second.UpdateReplacePolicy != "Snapshot" {
		t.Errorf("Second lost its policies: %+v", second)
	}

	// And: the resource the update never reached keeps its prior record.
	third := stackResourceByLogicalID(t, stack.Resources, "Third")
	if third.Status != ResourceCreateComplete || third.Properties["Version"] != "old" || third.PropertiesHash != "old-third" {
		t.Errorf("Third = %+v, want the untouched prior record", third)
	}

	// And: the retained list is what a restart reads back.
	persisted, err := p.store.getStack(context.Background(), stack.StackName)
	if err != nil || persisted == nil {
		t.Fatalf("get persisted stack: %v", err)
	}
	if persisted.Status != StatusUpdateFailed {
		t.Fatalf("persisted status = %q, want %q", persisted.Status, StatusUpdateFailed)
	}
	persistedSecond := stackResourceByLogicalID(t, persisted.Resources, "Second")
	if persistedSecond.Status != ResourceUpdateFailed || persistedSecond.StatusReason == "" {
		t.Errorf("persisted Second = %+v, want the failure to survive persistence", persistedSecond)
	}
	if len(persisted.Resources) != 3 {
		t.Errorf("persisted resources = %+v, want all three retained", persisted.Resources)
	}
	assertStackResourceEvent(t, p, stack.StackName, "Second", ResourceUpdateFailed, "later resource update failed")
}

// The point of retaining the records is the next attempt: it has to pick up
// where the failed one stopped rather than replay it.
func TestUpdateStack_disableRollbackRecoveryUsesRetainedRecords(t *testing.T) {
	// Given: an update that failed on Second with rollback disabled.
	handler := &rollbackUpdateHandler{forwardFailureID: "second-id"}
	resourceType := registerRollbackUpdateHandler(t, handler)
	p := newProvisionerTestFixture(t)
	stack, tmpl := disableRollbackUpdateFixture(resourceType)
	p.updateStackResources(stack, tmpl, captureStackGeneration(stack))
	if stack.Status != StatusUpdateFailed {
		t.Fatalf("stack status = %q, want %q before recovery", stack.Status, StatusUpdateFailed)
	}

	// When: the blocker clears and the same update is retried.
	handler.forwardFailureID = ""
	handler.calls = nil
	p.updateStackResources(stack, tmpl, captureStackGeneration(stack))

	// Then: the resource that already updated is left alone, and only the two
	// the failure stopped short of are touched.
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

// A handler that mutated service state and then failed its own compensation
// leaves the resource dirty. With rollback disabled there is no reverse pass to
// report that, so the resource record itself has to carry it.
func TestUpdateStack_disableRollbackKeepsDirtyResourceFailed(t *testing.T) {
	// Given: an existing Lambda mapping whose tag write lands and whose service
	// update then fails along with the tag compensation.
	const (
		id           = "84d6e94f-372a-4c5e-9d39-6fdf46e15432"
		mappingPath  = "/2015-03-31/event-source-mappings/" + id
		tagPath      = "/2017-03-31/tags/arn:aws:lambda:us-east-1:000000000000:event-source-mapping:" + id
		resourceType = "AWS::Lambda::EventSourceMapping"
	)
	oldProps := map[string]any{
		"EventSourceArn": "arn:aws:sqs:us-east-1:000000000000:queue",
		"FunctionName":   "function",
		"BatchSize":      10,
		"Tags":           []any{map[string]any{"Key": "stage", "Value": "old"}},
	}
	newProps := cloneProperties(t, oldProps)
	newProps["BatchSize"] = 20
	newProps["Tags"] = []any{map[string]any{"Key": "stage", "Value": "new"}}
	router := &captureRouter{failAt: map[string]map[int]int{
		mappingPath: {1: http.StatusBadRequest},
		tagPath:     {2: http.StatusInternalServerError},
	}}
	p := newProvisionerTestFixture(t)
	p.initRouter(router)
	stack := &Stack{
		StackName:       "dirty-lambda-update-no-rollback",
		StackID:         "arn:aws:cloudformation:us-east-1:000000000000:stack/dirty-lambda-update-no-rollback/1111",
		Region:          "us-east-1",
		DisableRollback: true,
		Resources: []StackResource{{
			LogicalID:      "Mapping",
			PhysicalID:     id,
			Type:           resourceType,
			Status:         ResourceCreateComplete,
			PropertiesHash: hashResourceProperties(resourceType, oldProps, nil),
			Properties:     oldProps,
		}},
	}
	tmpl := &Template{Resources: map[string]TemplateResource{
		"Mapping": {Type: resourceType, Properties: newProps},
	}}

	// When: the uncompensated failure reaches orchestration.
	p.updateStackResources(stack, tmpl, captureStackGeneration(stack))

	// Then: the stack fails without attempting a reverse pass, and the mapping
	// is recorded with the mutated state it was left in.
	if stack.Status != StatusUpdateFailed {
		t.Fatalf("stack status = %q, want %q", stack.Status, StatusUpdateFailed)
	}
	mapping := stackResourceByLogicalID(t, stack.Resources, "Mapping")
	if mapping.Status != ResourceUpdateFailed || !strings.Contains(mapping.StatusReason, "injected compensation failure") {
		t.Fatalf("Mapping = %+v, want UPDATE_FAILED naming the failed compensation", mapping)
	}
	if mapping.Properties["BatchSize"] != float64(20) && mapping.Properties["BatchSize"] != 20 {
		t.Errorf("Mapping properties = %+v, want the attempted properties recorded", mapping.Properties)
	}
	persisted, err := p.store.getStack(context.Background(), stack.StackName)
	if err != nil || persisted == nil {
		t.Fatalf("get persisted stack: %v", err)
	}
	persistedMapping := stackResourceByLogicalID(t, persisted.Resources, "Mapping")
	if persistedMapping.Status != ResourceUpdateFailed || !strings.Contains(persistedMapping.StatusReason, "injected compensation failure") {
		t.Fatalf("persisted Mapping = %+v, want the dirty failure to survive a restart", persistedMapping)
	}
}

// A resource the update creates rather than changes takes the other
// no-rollback branch. It must be retained for the same reason: it exists
// service-side, and a later attempt that cannot see it creates a second one.
func TestUpdateStack_disableRollbackRetainsFailedNewResource(t *testing.T) {
	// Given: a stack whose update adds a resource that fails to provision.
	handler := &rollbackUpdateHandler{}
	resourceType := registerRollbackUpdateHandler(t, handler)
	_, failingType := registerUnstable(t)
	p := newProvisionerTestFixture(t)
	stack := &Stack{
		StackName:       "disabled-rollback-new-resource",
		StackID:         "arn:aws:cloudformation:us-east-1:000000000000:stack/disabled-rollback-new-resource/1111",
		Region:          "us-east-1",
		DisableRollback: true,
		Resources: []StackResource{{
			LogicalID:      "Existing",
			PhysicalID:     "existing-id",
			Type:           resourceType,
			Status:         ResourceCreateComplete,
			PropertiesHash: "old-existing",
			Properties:     map[string]any{"Version": "old"},
		}},
	}
	tmpl := &Template{Resources: map[string]TemplateResource{
		"Existing": {Type: resourceType, Properties: map[string]any{"Version": "new"}},
		"Added":    {Type: failingType, DependsOn: "Existing"},
	}}

	// When: the new resource fails.
	p.updateStackResources(stack, tmpl, captureStackGeneration(stack))

	// Then: both the updated resource and the failed new one are retained.
	if stack.Status != StatusUpdateFailed {
		t.Fatalf("stack status = %q, want %q", stack.Status, StatusUpdateFailed)
	}
	existing := stackResourceByLogicalID(t, stack.Resources, "Existing")
	if existing.Status != ResourceUpdateComplete || existing.Properties["Version"] != "new" {
		t.Errorf("Existing = %+v, want the applied update retained", existing)
	}
	added := stackResourceByLogicalID(t, stack.Resources, "Added")
	if added.Status != ResourceCreateFailed || added.PhysicalID != unstablePhysicalID {
		t.Errorf("Added = %+v, want CREATE_FAILED naming the resource that exists", added)
	}
}
