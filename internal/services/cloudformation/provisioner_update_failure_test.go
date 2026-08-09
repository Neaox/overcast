package cloudformation

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
)

type rollbackUpdateCall struct {
	physicalID string
	version    string
}

type rollbackUpdateHandler struct {
	calls             []rollbackUpdateCall
	forwardFailureID  string
	reverseFailureID  string
	reverseFailureMsg string
}

type rollbackUpdateTestHandler interface {
	resourceHandler
	resourceUpdater
}

func (h *rollbackUpdateHandler) Create(context.Context, http.Handler, *config.Config, map[string]any, *resolveContext) (string, map[string]string, error) {
	return "created-id", nil, nil
}

func (h *rollbackUpdateHandler) Delete(context.Context, http.Handler, *config.Config, string, *resolveContext) error {
	return nil
}

func (h *rollbackUpdateHandler) Update(_ context.Context, _ http.Handler, _ *config.Config, physicalID string, props map[string]any, _ map[string]any, _ *resolveContext) (string, map[string]string, error) {
	version, _ := props["Version"].(string)
	h.calls = append(h.calls, rollbackUpdateCall{physicalID: physicalID, version: version})
	if physicalID == h.forwardFailureID && version == "new" {
		return "", nil, failUpdate(errors.New("later resource update failed"))
	}
	if physicalID == h.reverseFailureID && version == "old" {
		return "", nil, failUpdate(errors.New(h.reverseFailureMsg))
	}
	return physicalID, map[string]string{"Version": version}, nil
}

type stabilizingRollbackUpdateHandler struct {
	*rollbackUpdateHandler
	stabilizeCalls int
}

func (h *stabilizingRollbackUpdateHandler) Stabilize(context.Context, http.Handler, *config.Config, clock.Clock, string, *resolveContext) error {
	h.stabilizeCalls++
	if h.stabilizeCalls == 1 {
		return errors.New("updated resource did not stabilize")
	}
	return nil
}

func registerRollbackUpdateHandler(t *testing.T, handler rollbackUpdateTestHandler) string {
	t.Helper()
	resourceType := "Test::Rollback::" + t.Name()
	resourceHandlers[resourceType] = handler
	t.Cleanup(func() { delete(resourceHandlers, resourceType) })
	return resourceType
}

func TestUpdateStack_lambdaCompensationFailureLeavesRollbackFailed(t *testing.T) {
	// Given: an existing Lambda mapping whose tags mutate before its service
	// update and tag compensation both fail.
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
		StackName: "dirty-lambda-update",
		StackID:   "arn:aws:cloudformation:us-east-1:000000000000:stack/dirty-lambda-update/1111",
		Region:    "us-east-1",
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

	// When: stack orchestration receives the failed, uncompensated update.
	p.updateStackResources(stack, tmpl, captureStackGeneration(stack))

	// Then: it does not claim rollback completed or hide the dirty resource.
	if got, want := capturedPaths(router.requests), []string{tagPath, mappingPath, tagPath}; !slices.Equal(got, want) {
		t.Fatalf("request paths = %v, want %v", got, want)
	}
	if applied := router.requests[0].body["Tags"].(map[string]any)["stage"]; applied != "new" {
		t.Errorf("applied stage tag = %v, want new", applied)
	}
	if restored := router.requests[2].body["Tags"].(map[string]any)["stage"]; restored != "old" {
		t.Errorf("compensation stage tag = %v, want old", restored)
	}
	if stack.Status != StatusUpdateRollbackFailed {
		t.Fatalf("stack status = %q, want %q", stack.Status, StatusUpdateRollbackFailed)
	}
	if !strings.Contains(stack.StatusReason, "rollback failed") || !strings.Contains(stack.StatusReason, "injected compensation failure") {
		t.Errorf("stack status reason = %q, want rollback and compensation failures", stack.StatusReason)
	}
	if len(stack.Resources) != 1 || stack.Resources[0].PhysicalID != id || stack.Resources[0].Status != ResourceUpdateFailed {
		t.Fatalf("stack resources = %+v, want retained UPDATE_FAILED mapping", stack.Resources)
	}
	if !strings.Contains(stack.Resources[0].StatusReason, "injected compensation failure") {
		t.Errorf("resource status reason = %q, want compensation failure", stack.Resources[0].StatusReason)
	}
	events, err := p.store.getStackEvents(context.Background(), stack.StackName)
	if err != nil {
		t.Fatalf("get stack events: %v", err)
	}
	assertedResourceFailure := false
	assertedStackFailure := false
	for _, event := range events {
		if event.LogicalResourceID == "Mapping" && event.ResourceStatus == ResourceUpdateFailed && strings.Contains(event.ResourceStatusReason, "injected compensation failure") {
			assertedResourceFailure = true
		}
		if event.LogicalResourceID == stack.StackName && event.ResourceStatus == StatusUpdateRollbackFailed && strings.Contains(event.ResourceStatusReason, "rollback failed") {
			assertedStackFailure = true
		}
	}
	if !assertedResourceFailure || !assertedStackFailure {
		t.Fatalf("events missing terminal evidence: resource=%v stack=%v; events=%+v", assertedResourceFailure, assertedStackFailure, events)
	}
}

func TestUpdateStack_failedReverseUpdateRetainsAttemptedResource(t *testing.T) {
	// Given: the first resource updates successfully, a dependent resource
	// fails, and restoring the first resource also fails.
	handler := &rollbackUpdateHandler{
		forwardFailureID:  "second-id",
		reverseFailureID:  "first-id",
		reverseFailureMsg: "first reverse update failed",
	}
	resourceType := registerRollbackUpdateHandler(t, handler)
	p := newProvisionerTestFixture(t)
	stack := &Stack{
		StackName: "failed-earlier-reverse",
		StackID:   "arn:aws:cloudformation:us-east-1:000000000000:stack/failed-earlier-reverse/1111",
		Region:    "us-east-1",
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
				LogicalID:      "Second",
				PhysicalID:     "second-id",
				Type:           resourceType,
				Status:         ResourceCreateComplete,
				PropertiesHash: "old-second",
				Properties:     map[string]any{"Version": "old"},
			},
		},
	}
	tmpl := &Template{Resources: map[string]TemplateResource{
		"First": {
			Type: resourceType, Properties: map[string]any{"Version": "new"},
			DeletionPolicy: "Retain", UpdateReplacePolicy: "Snapshot",
		},
		"Second": {
			Type: resourceType, Properties: map[string]any{"Version": "new"},
			DependsOn: "First",
		},
	}}

	// When: the later failure starts update rollback.
	p.updateStackResources(stack, tmpl, captureStackGeneration(stack))

	// Then: the failed reverse update remains visible rather than being
	// overwritten by the previous COMPLETE record.
	wantCalls := []rollbackUpdateCall{
		{physicalID: "first-id", version: "new"},
		{physicalID: "second-id", version: "new"},
		{physicalID: "first-id", version: "old"},
	}
	if !slices.Equal(handler.calls, wantCalls) {
		t.Fatalf("update calls = %+v, want %+v", handler.calls, wantCalls)
	}
	if stack.Status != StatusUpdateRollbackFailed || !strings.Contains(stack.StatusReason, "first reverse update failed") {
		t.Fatalf("stack = status %q reason %q, want rollback failure", stack.Status, stack.StatusReason)
	}
	first := stackResourceByLogicalID(t, stack.Resources, "First")
	if first.PhysicalID != "first-id" || first.Status != ResourceUpdateFailed || !strings.Contains(first.StatusReason, "first reverse update failed") {
		t.Fatalf("retained First = %+v, want failed attempted resource", first)
	}
	if first.Attributes["Version"] != "new" || first.Properties["Version"] != "new" || first.DeletionPolicy != "Retain" || first.UpdateReplacePolicy != "Snapshot" || first.Timestamp.IsZero() {
		t.Errorf("retained First lost attempted state: %+v", first)
	}
	assertStackResourceEvent(t, p, stack.StackName, "First", ResourceUpdateFailed, "restore First: first reverse update failed")
}

func TestUpdateStack_stabilizationFailureRollback(t *testing.T) {
	tests := []struct {
		name              string
		reverseFailureMsg string
		wantStackStatus   string
		wantResourceState string
		wantStabilizes    int
	}{
		{name: "reverse succeeds", wantStackStatus: StatusUpdateRollbackComplete, wantResourceState: ResourceCreateComplete, wantStabilizes: 2},
		{name: "reverse fails", reverseFailureMsg: "stabilization reverse update failed", wantStackStatus: StatusUpdateRollbackFailed, wantResourceState: ResourceUpdateFailed, wantStabilizes: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Given: an in-place update applies but its first stabilization fails.
			base := &rollbackUpdateHandler{}
			if tc.reverseFailureMsg != "" {
				base.reverseFailureID = "resource-id"
				base.reverseFailureMsg = tc.reverseFailureMsg
			}
			handler := &stabilizingRollbackUpdateHandler{rollbackUpdateHandler: base}
			resourceType := registerRollbackUpdateHandler(t, handler)
			p := newProvisionerTestFixture(t)
			stack, tmpl := stabilizationRollbackFixture(resourceType)

			// When: CloudFormation rolls the failed update back.
			p.updateStackResources(stack, tmpl, captureStackGeneration(stack))

			// Then: the updater receives the previous properties and the terminal
			// state reflects whether the reverse update succeeded.
			wantCalls := []rollbackUpdateCall{
				{physicalID: "resource-id", version: "new"},
				{physicalID: "resource-id", version: "old"},
			}
			if !slices.Equal(base.calls, wantCalls) {
				t.Fatalf("update calls = %+v, want %+v", base.calls, wantCalls)
			}
			if handler.stabilizeCalls != tc.wantStabilizes {
				t.Fatalf("stabilize calls = %d, want %d", handler.stabilizeCalls, tc.wantStabilizes)
			}
			if stack.Status != tc.wantStackStatus {
				t.Fatalf("stack status = %q, want %q; reason: %s", stack.Status, tc.wantStackStatus, stack.StatusReason)
			}
			resource := stackResourceByLogicalID(t, stack.Resources, "Resource")
			if resource.Status != tc.wantResourceState {
				t.Fatalf("resource status = %q, want %q: %+v", resource.Status, tc.wantResourceState, resource)
			}
			if tc.reverseFailureMsg == "" {
				if resource.Properties["Version"] != "old" {
					t.Fatalf("restored resource = %+v, want previous properties", resource)
				}
				return
			}
			if resource.PhysicalID != "resource-id" || !strings.Contains(resource.StatusReason, tc.reverseFailureMsg) || !strings.Contains(stack.StatusReason, tc.reverseFailureMsg) {
				t.Fatalf("retained resource/stack lost reverse failure: resource=%+v stack=%q", resource, stack.StatusReason)
			}
			if resource.Attributes["Version"] != "new" || resource.Properties["Version"] != "new" || resource.DeletionPolicy != "Retain" || resource.UpdateReplacePolicy != "Snapshot" || resource.Timestamp.IsZero() {
				t.Errorf("retained resource lost attempted state: %+v", resource)
			}
			assertStackResourceEvent(t, p, stack.StackName, "Resource", ResourceUpdateFailed, "restore Resource: "+tc.reverseFailureMsg)
		})
	}
}

func stabilizationRollbackFixture(resourceType string) (*Stack, *Template) {
	stack := &Stack{
		StackName: "failed-stabilization",
		StackID:   "arn:aws:cloudformation:us-east-1:000000000000:stack/failed-stabilization/1111",
		Region:    "us-east-1",
		Resources: []StackResource{{
			LogicalID:      "Resource",
			PhysicalID:     "resource-id",
			Type:           resourceType,
			Status:         ResourceCreateComplete,
			Attributes:     map[string]string{"Version": "old"},
			PropertiesHash: "old",
			Properties:     map[string]any{"Version": "old"},
		}},
	}
	tmpl := &Template{Resources: map[string]TemplateResource{
		"Resource": {
			Type: resourceType, Properties: map[string]any{"Version": "new"},
			DeletionPolicy: "Retain", UpdateReplacePolicy: "Snapshot",
		},
	}}
	return stack, tmpl
}

func stackResourceByLogicalID(t *testing.T, resources []StackResource, logicalID string) StackResource {
	t.Helper()
	for _, resource := range resources {
		if resource.LogicalID == logicalID {
			return resource
		}
	}
	t.Fatalf("resource %s not found in %+v", logicalID, resources)
	return StackResource{}
}

func assertStackResourceEvent(t *testing.T, p *provisioner, stackName, logicalID, status, reason string) {
	t.Helper()
	events, err := p.store.getStackEvents(context.Background(), stackName)
	if err != nil {
		t.Fatalf("get stack events: %v", err)
	}
	for _, event := range events {
		if event.LogicalResourceID == logicalID && event.ResourceStatus == status && strings.Contains(event.ResourceStatusReason, reason) {
			return
		}
	}
	t.Fatalf("event %s %s containing %q not found in %+v", logicalID, status, reason, events)
}
