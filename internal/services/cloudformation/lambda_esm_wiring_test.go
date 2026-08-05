package cloudformation

import (
	"context"
	"net/http"
	"reflect"
	"slices"
	"testing"
)

func TestLambdaEventSourceMappingUpdateForwardsInPlaceProperties(t *testing.T) {
	const id = "84d6e94f-372a-4c5e-9d39-6fdf46e15432"
	oldProps := map[string]any{
		"EventSourceArn": "arn:aws:sqs:us-east-1:000000000000:queue",
		"FunctionName":   "old-function",
		"Topics":         []any{"old-topic"},
		"Queues":         []any{"old-queue"},
	}
	newProps := map[string]any{
		"EventSourceArn": "arn:aws:sqs:us-east-1:000000000000:queue",
		"FunctionName":   "new-function",
		"Topics":         []any{"new-topic"},
		"Queues":         []any{"new-queue"},
	}
	router := &captureRouter{}

	// When: properties that AWS marks No interruption change.
	physicalID, _, err := (&lambdaEventSourceMappingHandler{}).Update(
		context.Background(), router, nil, id, newProps, oldProps, &resolveContext{Region: "us-east-1"},
	)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Then: CloudFormation keeps the mapping and delegates validation/support to Lambda.
	if physicalID != id {
		t.Errorf("physical ID = %q, want %q", physicalID, id)
	}
	if len(router.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(router.requests))
	}
	request := router.requests[0]
	if request.method != http.MethodPut || request.path != "/2015-03-31/event-source-mappings/"+id {
		t.Fatalf("request = %s %s", request.method, request.path)
	}
	for key, want := range map[string]any{
		"FunctionName": "new-function",
		"Topics":       []any{"new-topic"},
		"Queues":       []any{"new-queue"},
	} {
		if got := request.body[key]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", key, got, want)
		}
	}
}

func TestLambdaEventSourceMappingCreateForwardsTags(t *testing.T) {
	router := &captureRouter{}
	props := map[string]any{
		"EventSourceArn": "arn:aws:sqs:us-east-1:000000000000:queue",
		"FunctionName":   "function",
		"Tags":           []any{map[string]any{"Key": "stage", "Value": "test"}},
	}

	// When: CloudFormation creates a tagged event source mapping.
	_, _, err := (&lambdaEventSourceMappingHandler{}).Create(
		context.Background(), router, nil, props, &resolveContext{Region: "us-east-1"},
	)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Then: tags reach CreateEventSourceMapping rather than being ignored by CloudFormation.
	if len(router.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(router.requests))
	}
	want := map[string]any{"stage": "test"}
	if got := router.requests[0].body["Tags"]; !reflect.DeepEqual(got, want) {
		t.Errorf("Tags = %#v, want %#v", got, want)
	}
}

func TestLambdaEventSourceMappingUpdateSkipsUnchangedPropertiesAndClearsRemovedOnes(t *testing.T) {
	const id = "84d6e94f-372a-4c5e-9d39-6fdf46e15432"
	base := map[string]any{
		"EventSourceArn": "arn:aws:sqs:us-east-1:000000000000:queue",
		"FunctionName":   "function",
		"BatchSize":      25,
	}

	t.Run("no-op", func(t *testing.T) {
		router := &captureRouter{}
		_, _, err := (&lambdaEventSourceMappingHandler{}).Update(
			context.Background(), router, nil, id, cloneProperties(t, base), cloneProperties(t, base), &resolveContext{Region: "us-east-1"},
		)
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if len(router.requests) != 0 {
			t.Fatalf("requests = %d, want 0", len(router.requests))
		}
	})

	t.Run("removed property", func(t *testing.T) {
		router := &captureRouter{}
		newProps := cloneProperties(t, base)
		delete(newProps, "BatchSize")
		_, _, err := (&lambdaEventSourceMappingHandler{}).Update(
			context.Background(), router, nil, id, newProps, cloneProperties(t, base), &resolveContext{Region: "us-east-1"},
		)
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if len(router.requests) != 1 || router.requests[0].body["BatchSize"] != float64(10) {
			t.Fatalf("requests = %#v, want one BatchSize reset to 10", router.requests)
		}
	})

	t.Run("removed stream batch size", func(t *testing.T) {
		router := &captureRouter{}
		oldProps := cloneProperties(t, base)
		oldProps["EventSourceArn"] = "arn:aws:dynamodb:us-east-1:000000000000:table/table/stream/2026-08-05T00:00:00.000"
		newProps := cloneProperties(t, oldProps)
		delete(newProps, "BatchSize")
		_, _, err := (&lambdaEventSourceMappingHandler{}).Update(
			context.Background(), router, nil, id, newProps, oldProps, &resolveContext{Region: "us-east-1"},
		)
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if len(router.requests) != 1 || router.requests[0].body["BatchSize"] != float64(100) {
			t.Fatalf("requests = %#v, want one BatchSize reset to 100", router.requests)
		}
	})
}

func TestLambdaEventSourceMappingUpdateCompensatesTagsWhenMappingFails(t *testing.T) {
	const (
		id          = "84d6e94f-372a-4c5e-9d39-6fdf46e15432"
		mappingPath = "/2015-03-31/event-source-mappings/84d6e94f-372a-4c5e-9d39-6fdf46e15432"
		tagPath     = "/2017-03-31/tags/arn:aws:lambda:us-east-1:000000000000:event-source-mapping:84d6e94f-372a-4c5e-9d39-6fdf46e15432"
	)
	oldProps := map[string]any{
		"EventSourceArn": "arn:aws:sqs:us-east-1:000000000000:queue",
		"FunctionName":   "function", "BatchSize": 10,
		"Tags": []any{map[string]any{"Key": "stage", "Value": "old"}},
	}
	newProps := cloneProperties(t, oldProps)
	newProps["BatchSize"] = 20
	newProps["Tags"] = []any{map[string]any{"Key": "stage", "Value": "new"}}
	router := &captureRouter{failPath: mappingPath}

	_, _, err := (&lambdaEventSourceMappingHandler{}).Update(
		context.Background(), router, nil, id, newProps, oldProps,
		&resolveContext{Region: "us-east-1", AccountID: "000000000000"},
	)
	if err == nil {
		t.Fatal("Update error = nil, want mapping failure")
	}
	wantPaths := []string{tagPath, mappingPath, tagPath}
	if got := capturedPaths(router.requests); !slices.Equal(got, wantPaths) {
		t.Fatalf("request paths = %v, want %v", got, wantPaths)
	}
	if got := router.requests[2].body["Tags"].(map[string]any)["stage"]; got != "old" {
		t.Errorf("compensated stage tag = %v, want old", got)
	}
}

func TestLambdaEventSourceMappingUpdateCompensatesPartialTagFailure(t *testing.T) {
	const (
		id      = "84d6e94f-372a-4c5e-9d39-6fdf46e15432"
		tagPath = "/2017-03-31/tags/arn:aws:lambda:us-east-1:000000000000:event-source-mapping:84d6e94f-372a-4c5e-9d39-6fdf46e15432"
	)
	oldProps := map[string]any{
		"EventSourceArn": "arn:aws:sqs:us-east-1:000000000000:queue",
		"FunctionName":   "function",
		"Tags": []any{
			map[string]any{"Key": "stage", "Value": "old"},
			map[string]any{"Key": "removed", "Value": "restore"},
		},
	}
	newProps := cloneProperties(t, oldProps)
	newProps["Tags"] = []any{
		map[string]any{"Key": "stage", "Value": "new"},
		map[string]any{"Key": "added", "Value": "temporary"},
	}
	router := &captureRouter{failAt: map[string]map[int]int{tagPath: {2: http.StatusInternalServerError}}}

	_, _, err := (&lambdaEventSourceMappingHandler{}).Update(
		context.Background(), router, nil, id, newProps, oldProps,
		&resolveContext{Region: "us-east-1", AccountID: "000000000000"},
	)
	if err == nil {
		t.Fatal("Update error = nil, want UntagResource failure")
	}
	if len(router.requests) != 4 {
		t.Fatalf("requests = %d, want 4", len(router.requests))
	}
	restored := router.requests[2].body["Tags"].(map[string]any)
	if restored["stage"] != "old" || restored["removed"] != "restore" || router.requests[3].query != "tagKeys=added" {
		t.Fatalf("compensation = tags %#v, query %q", restored, router.requests[3].query)
	}
}
