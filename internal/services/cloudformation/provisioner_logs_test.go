package cloudformation

import (
	"context"
	"net/http"
	"reflect"
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
