package cloudformation

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
)

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
	p.updateStackResources(stack, tmpl, nil)

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
