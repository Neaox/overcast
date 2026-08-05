package cloudformation

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestLogsLogGroupHandlerCreate_retentionFailureDeletesCreatedGroup(t *testing.T) {
	// Given: a Logs router that fails to set retention after creating the group
	var targets []string
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targets = append(targets, r.Header.Get("X-Amz-Target"))
		if r.Header.Get("X-Amz-Target") == "Logs_20140328.PutRetentionPolicy" {
			http.Error(w, "retention failed", http.StatusInternalServerError)
		}
	})
	h := &logsLogGroupHandler{}

	// When: CloudFormation creates a log group with retention
	_, _, err := h.Create(context.Background(), router, nil, map[string]any{
		"LogGroupName":    "/cloudformation/cleanup-on-retention-failure",
		"RetentionInDays": 7,
	}, &resolveContext{Region: "us-east-1", AccountID: "000000000000"})

	// Then: creation fails and the partially created group is deleted
	if err == nil {
		t.Fatal("Create returned nil error after PutRetentionPolicy failed")
	}
	wantTargets := []string{
		"Logs_20140328.CreateLogGroup",
		"Logs_20140328.PutRetentionPolicy",
		"Logs_20140328.DeleteLogGroup",
	}
	if !reflect.DeepEqual(targets, wantTargets) {
		t.Errorf("dispatched targets = %v, want %v", targets, wantTargets)
	}
}

func TestLogsLogGroupHandlerUpdate_retentionRemovalError(t *testing.T) {
	// Given: a Logs router that fails to remove a retention policy
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Amz-Target") != "Logs_20140328.DeleteRetentionPolicy" {
			t.Errorf("target = %q, want DeleteRetentionPolicy", r.Header.Get("X-Amz-Target"))
		}
		http.Error(w, "retention removal failed", http.StatusInternalServerError)
	})
	h := &logsLogGroupHandler{}

	// When: an update removes RetentionInDays
	_, _, err := h.Update(context.Background(), router, nil, "/cloudformation/remove-retention", map[string]any{}, map[string]any{
		"RetentionInDays": 7,
	}, &resolveContext{
		Region:    "us-east-1",
		AccountID: "000000000000",
	})

	// Then: CloudFormation receives the service error instead of completing the update
	if err == nil {
		t.Fatal("Update returned nil error after DeleteRetentionPolicy failed")
	}
}

func TestLogsLogGroupHandlerUpdate_retentionOmittedBeforeAndAfter(t *testing.T) {
	// Given: a Logs router that records unexpected service calls
	dispatched := false
	router := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		dispatched = true
	})
	h := &logsLogGroupHandler{}

	// When: an update leaves RetentionInDays omitted
	_, _, err := h.Update(context.Background(), router, nil, "/cloudformation/no-retention", map[string]any{}, map[string]any{}, &resolveContext{
		Region:    "us-east-1",
		AccountID: "000000000000",
	})

	// Then: CloudFormation does not dispatch an unnecessary retention API call
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if dispatched {
		t.Fatal("Update dispatched a Logs API call when retention stayed omitted")
	}
}

func TestLogsLogGroupHandlerCreate_tagFailureDeletesCreatedGroup(t *testing.T) {
	// Given: a Logs router that fails to tag a newly created log group
	var targets []string
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := r.Header.Get("X-Amz-Target")
		targets = append(targets, target)
		if target == "Logs_20140328.TagLogGroup" {
			http.Error(w, "tagging failed", http.StatusInternalServerError)
		}
	})
	h := &logsLogGroupHandler{}

	// When: CloudFormation creates a log group with tags
	_, _, err := h.Create(context.Background(), router, nil, map[string]any{
		"LogGroupName": "/cloudformation/cleanup-on-tag-failure",
		"Tags":         []any{map[string]any{"Key": "environment", "Value": "test"}},
	}, &resolveContext{Region: "us-east-1", AccountID: "000000000000"})

	// Then: creation fails and the partially created group is deleted
	if err == nil {
		t.Fatal("Create returned nil error after TagLogGroup failed")
	}
	wantTargets := []string{
		"Logs_20140328.CreateLogGroup",
		"Logs_20140328.TagLogGroup",
		"Logs_20140328.DeleteLogGroup",
	}
	if !reflect.DeepEqual(targets, wantTargets) {
		t.Errorf("dispatched targets = %v, want %v", targets, wantTargets)
	}
}

func TestLogsLogGroupHandlerCreate_cleanupFailureReturnsPhysicalIDForRollback(t *testing.T) {
	// Given: tagging and the immediate cleanup delete both fail once
	const name = "/cloudformation/cleanup-retry"
	failures := map[string]int{
		"Logs_20140328.TagLogGroup":    1,
		"Logs_20140328.DeleteLogGroup": 1,
	}
	var targets []string
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := r.Header.Get("X-Amz-Target")
		targets = append(targets, target)
		if failures[target] > 0 {
			failures[target]--
			http.Error(w, "injected failure", http.StatusInternalServerError)
		}
	})
	h := &logsLogGroupHandler{}

	// When: CloudFormation creates the log group and cleanup cannot remove it
	physicalID, _, err := h.Create(context.Background(), router, nil, map[string]any{
		"LogGroupName": name,
		"Tags":         []any{map[string]any{"Key": "environment", "Value": "test"}},
	}, &resolveContext{Region: "us-east-1", AccountID: "000000000000"})

	// Then: the surviving resource identity is returned so rollback can retry deletion
	if err == nil || physicalID != name || !strings.Contains(err.Error(), "TagLogGroup") || !strings.Contains(err.Error(), "cleanup DeleteLogGroup") {
		t.Fatalf("Create = physicalID %q, err %v", physicalID, err)
	}
	if err := h.Delete(context.Background(), router, nil, physicalID, &resolveContext{Region: "us-east-1"}); err != nil {
		t.Fatalf("retry Delete: %v", err)
	}
	wantTargets := []string{
		"Logs_20140328.CreateLogGroup",
		"Logs_20140328.TagLogGroup",
		"Logs_20140328.DeleteLogGroup",
		"Logs_20140328.DeleteLogGroup",
	}
	if !reflect.DeepEqual(targets, wantTargets) {
		t.Fatalf("dispatched targets = %v, want %v", targets, wantTargets)
	}
}

func TestLogsLogGroupHandlerCreate_mergesStackTagsWithResourcePrecedence(t *testing.T) {
	// Given: stack and resource tags overlap on environment
	var tagBody map[string]any
	router := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Amz-Target") != "Logs_20140328.TagLogGroup" {
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&tagBody); err != nil {
			t.Errorf("decode TagLogGroup request: %v", err)
		}
	})
	h := &logsLogGroupHandler{}

	// When: CloudFormation creates the log group
	_, _, err := h.Create(context.Background(), router, nil, map[string]any{
		"LogGroupName": "/cloudformation/stack-tags",
		"Tags":         []any{map[string]any{"Key": "environment", "Value": "resource"}},
	}, &resolveContext{
		Region: "us-east-1", AccountID: "000000000000",
		StackTags: []Tag{{Key: "environment", Value: "stack"}, {Key: "team", Value: "platform"}},
	})

	// Then: stack tags propagate and the resource tag wins the collision
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if got := tagBody["tags"]; !reflect.DeepEqual(got, map[string]any{"environment": "resource", "team": "platform"}) {
		t.Fatalf("TagLogGroup tags = %#v", got)
	}
}

func TestLogsLogGroupHandlerUpdate_reconcilesTags(t *testing.T) {
	// Given: a log group whose new template changes, adds, and removes tags
	type request struct {
		target string
		body   map[string]any
	}
	var requests []request
	router := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		requests = append(requests, request{target: r.Header.Get("X-Amz-Target"), body: body})
	})
	h := &logsLogGroupHandler{}

	// When: CloudFormation updates the resource tags
	_, _, err := h.Update(context.Background(), router, nil, "/cloudformation/tag-reconcile", map[string]any{
		"Tags": []any{
			map[string]any{"Key": "environment", "Value": "production"},
			map[string]any{"Key": "project", "Value": "overcast"},
		},
	}, map[string]any{
		"Tags": []any{
			map[string]any{"Key": "environment", "Value": "development"},
			map[string]any{"Key": "owner", "Value": "platform"},
		},
	}, &resolveContext{Region: "us-east-1", AccountID: "000000000000"})

	// Then: changed and added tags are upserted, and removed keys are untagged
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %#v, want TagLogGroup then UntagLogGroup", requests)
	}
	if requests[0].target != "Logs_20140328.TagLogGroup" {
		t.Errorf("first target = %q, want TagLogGroup", requests[0].target)
	}
	if got := requests[0].body["tags"]; !reflect.DeepEqual(got, map[string]any{"environment": "production", "project": "overcast"}) {
		t.Errorf("TagLogGroup tags = %#v", got)
	}
	if requests[1].target != "Logs_20140328.UntagLogGroup" {
		t.Errorf("second target = %q, want UntagLogGroup", requests[1].target)
	}
	if got := requests[1].body["tags"]; !reflect.DeepEqual(got, []any{"owner"}) {
		t.Errorf("UntagLogGroup tags = %#v, want [owner]", got)
	}
}

func TestLogsLogGroupHandlerUpdate_unchangedTagsDoNotDispatch(t *testing.T) {
	// Given: a log group whose desired tags have not changed
	dispatched := false
	router := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		dispatched = true
	})
	h := &logsLogGroupHandler{}
	props := map[string]any{"Tags": []any{map[string]any{"Key": "environment", "Value": "test"}}}

	// When: CloudFormation updates the unchanged tag set
	_, _, err := h.Update(context.Background(), router, nil, "/cloudformation/tag-noop", props, props, &resolveContext{
		Region:    "us-east-1",
		AccountID: "000000000000",
	})

	// Then: no tag API calls are issued
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if dispatched {
		t.Fatal("Update dispatched a Logs API call when tags were unchanged")
	}
}

func TestLogsLogGroupHandlerUpdate_reconcilesStackTagChanges(t *testing.T) {
	// Given: the template tags stay fixed while stack tags change
	type request struct {
		target string
		body   map[string]any
	}
	var requests []request
	router := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		requests = append(requests, request{target: r.Header.Get("X-Amz-Target"), body: body})
	})
	props := map[string]any{"Tags": []any{map[string]any{"Key": "environment", "Value": "resource"}}}

	// When: one stack tag is added and another is removed
	_, _, err := (&logsLogGroupHandler{}).Update(context.Background(), router, nil, "/cloudformation/stack-tag-update", props, props, &resolveContext{
		Region: "us-east-1", AccountID: "000000000000",
		StackTags:         []Tag{{Key: "owner", Value: "operations"}},
		PreviousStackTags: []Tag{{Key: "team", Value: "platform"}},
	})

	// Then: the effective tag set is reconciled through Logs
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %#v, want TagLogGroup then UntagLogGroup", requests)
	}
	if requests[0].target != "Logs_20140328.TagLogGroup" || !reflect.DeepEqual(requests[0].body["tags"], map[string]any{"owner": "operations"}) {
		t.Errorf("TagLogGroup request = %#v", requests[0])
	}
	if requests[1].target != "Logs_20140328.UntagLogGroup" || !reflect.DeepEqual(requests[1].body["tags"], []any{"team"}) {
		t.Errorf("UntagLogGroup request = %#v", requests[1])
	}
}

func TestLogsLogGroupHandlerUpdate_resourceTagOverrideMakesStackCollisionNoOp(t *testing.T) {
	// Given: a resource tag overrides the same stack tag before and after the update
	dispatched := false
	router := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { dispatched = true })
	props := map[string]any{"Tags": []any{map[string]any{"Key": "environment", "Value": "resource"}}}

	// When: only the overridden stack tag value changes
	_, _, err := (&logsLogGroupHandler{}).Update(context.Background(), router, nil, "/cloudformation/stack-tag-collision", props, props, &resolveContext{
		Region: "us-east-1", AccountID: "000000000000",
		StackTags:         []Tag{{Key: "environment", Value: "new-stack"}},
		PreviousStackTags: []Tag{{Key: "environment", Value: "old-stack"}},
	})

	// Then: the unchanged effective tag set dispatches no Logs operation
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if dispatched {
		t.Fatal("Update dispatched a Logs API call for an overridden stack-tag change")
	}
}

func TestLogsLogGroupResourcePropertiesHashIncludesEffectiveStackTags(t *testing.T) {
	// Given: unchanged log group properties
	props := map[string]any{"LogGroupName": "/cloudformation/hash-stack-tags"}

	// When: an effective stack tag changes
	oldHash := hashResourceProperties("AWS::Logs::LogGroup", props, []Tag{{Key: "environment", Value: "old"}})
	newHash := hashResourceProperties("AWS::Logs::LogGroup", props, []Tag{{Key: "environment", Value: "new"}})

	// Then: the resource hash changes so UpdateStack dispatches the handler
	if oldHash == newHash {
		t.Fatal("log group resource hash did not change with propagated stack tags")
	}

	// And: a resource-level override suppresses irrelevant stack-tag churn
	props["Tags"] = []any{map[string]any{"Key": "environment", "Value": "resource"}}
	oldHash = hashResourceProperties("AWS::Logs::LogGroup", props, []Tag{{Key: "environment", Value: "old"}})
	newHash = hashResourceProperties("AWS::Logs::LogGroup", props, []Tag{{Key: "environment", Value: "new"}})
	if oldHash != newHash {
		t.Fatal("log group resource hash changed for a stack tag overridden by the resource")
	}
}

func TestLogsLogGroupHandlerUpdate_omittedOrEmptyTagsRemovePreviousTags(t *testing.T) {
	for _, tc := range []struct {
		name  string
		props map[string]any
	}{
		{name: "omitted", props: map[string]any{}},
		{name: "empty", props: map[string]any{"Tags": []any{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a tag set from the previous template
			var target string
			var body map[string]any
			router := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				target = r.Header.Get("X-Amz-Target")
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode request: %v", err)
				}
			})
			h := &logsLogGroupHandler{}

			// When: the Tags property is omitted or explicitly empty
			_, _, err := h.Update(context.Background(), router, nil, "/cloudformation/tag-removal", tc.props, map[string]any{
				"Tags": []any{map[string]any{"Key": "owner", "Value": "platform"}},
			}, &resolveContext{Region: "us-east-1", AccountID: "000000000000"})

			// Then: the previous CloudFormation-owned tag is removed
			if err != nil {
				t.Fatalf("Update returned error: %v", err)
			}
			if target != "Logs_20140328.UntagLogGroup" {
				t.Fatalf("target = %q, want UntagLogGroup", target)
			}
			if got := body["tags"]; !reflect.DeepEqual(got, []any{"owner"}) {
				t.Errorf("UntagLogGroup tags = %#v, want [owner]", got)
			}
		})
	}
}

func TestLogsLogGroupHandlerUpdate_untagFailurePropagates(t *testing.T) {
	// Given: a Logs router that fails after TagLogGroup succeeds
	var targets []string
	untagCalls := 0
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := r.Header.Get("X-Amz-Target")
		targets = append(targets, target)
		if target == "Logs_20140328.UntagLogGroup" && untagCalls == 0 {
			untagCalls++
			http.Error(w, "untag failed", http.StatusInternalServerError)
		}
	})
	h := &logsLogGroupHandler{}

	// When: an update both adds and removes tags
	_, _, err := h.Update(context.Background(), router, nil, "/cloudformation/tag-failure", map[string]any{
		"Tags": []any{map[string]any{"Key": "new", "Value": "value"}},
	}, map[string]any{
		"Tags": []any{map[string]any{"Key": "old", "Value": "value"}},
	}, &resolveContext{Region: "us-east-1", AccountID: "000000000000"})

	// Then: the service error aborts the CloudFormation update
	if err == nil {
		t.Fatal("Update returned nil error after UntagLogGroup failed")
	}
	wantTargets := []string{
		"Logs_20140328.TagLogGroup",
		"Logs_20140328.UntagLogGroup",
		"Logs_20140328.TagLogGroup",
		"Logs_20140328.UntagLogGroup",
	}
	if !reflect.DeepEqual(targets, wantTargets) {
		t.Errorf("dispatched targets = %v, want %v", targets, wantTargets)
	}
}
