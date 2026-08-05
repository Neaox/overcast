package cloudformation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"testing"
)

const (
	dynamodbCreateTableTarget      = "DynamoDB_20120810.CreateTable"
	dynamodbDeleteTableTarget      = "DynamoDB_20120810.DeleteTable"
	dynamodbUpdateTableTarget      = "DynamoDB_20120810.UpdateTable"
	dynamodbUpdateTimeToLiveTarget = "DynamoDB_20120810.UpdateTimeToLive"
)

type dynamodbTTLRequest struct {
	target string
	body   map[string]any
}

type dynamodbTTLRouter struct {
	requests     []dynamodbTTLRequest
	statusByCall map[string]map[int]int
	calls        map[string]int
}

func (r *dynamodbTTLRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	target := req.Header.Get("X-Amz-Target")
	body := map[string]any{}
	raw, _ := io.ReadAll(req.Body)
	_ = json.Unmarshal(raw, &body)
	r.requests = append(r.requests, dynamodbTTLRequest{target: target, body: body})
	if r.calls == nil {
		r.calls = make(map[string]int)
	}
	r.calls[target]++
	if status := r.statusByCall[target][r.calls[target]]; status != 0 {
		http.Error(w, "injected failure", status)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"TableDescription": map[string]any{"TableArn": "arn:aws:dynamodb:us-east-1:000000000000:table/test-table"},
	})
}

func TestDynamoDBTableCreate_TimeToLiveFailureDeletesCreatedTable(t *testing.T) {
	// Given: DynamoDB rejects the TTL configuration after creating the table
	router := &dynamodbTTLRouter{statusByCall: map[string]map[int]int{
		dynamodbUpdateTimeToLiveTarget: {1: http.StatusBadRequest},
	}}
	props := map[string]any{
		"TableName": "test-table",
		"TimeToLiveSpecification": map[string]any{
			"Enabled": true, "AttributeName": "expiresAt",
		},
	}

	// When: CloudFormation creates the resource
	physicalID, _, err := (&dynamodbTableHandler{}).Create(
		context.Background(), router, nil, props, &resolveContext{Region: "us-east-1"},
	)

	// Then: creation fails and the table is removed rather than leaked
	if err == nil {
		t.Fatal("Create error = nil, want TTL failure")
	}
	if physicalID != "" {
		t.Errorf("physical ID = %q, want empty after successful compensation", physicalID)
	}
	assertDynamoDBTTLTargets(t, router.requests, []string{
		dynamodbCreateTableTarget,
		dynamodbUpdateTimeToLiveTarget,
		dynamodbDeleteTableTarget,
	})
}

func TestDynamoDBTableCreate_TimeToLiveDeleteFailureRetainsPhysicalID(t *testing.T) {
	// Given: both applying TTL and compensating its created table fail
	router := &dynamodbTTLRouter{statusByCall: map[string]map[int]int{
		dynamodbUpdateTimeToLiveTarget: {1: http.StatusBadRequest},
		dynamodbDeleteTableTarget:      {1: http.StatusInternalServerError},
	}}
	props := map[string]any{
		"TableName": "test-table",
		"TimeToLiveSpecification": map[string]any{
			"Enabled": true, "AttributeName": "expiresAt",
		},
	}

	// When: CloudFormation creates the resource
	physicalID, _, err := (&dynamodbTableHandler{}).Create(
		context.Background(), router, nil, props, &resolveContext{Region: "us-east-1"},
	)

	// Then: stack rollback receives the physical ID and can retry cleanup
	if err == nil {
		t.Fatal("Create error = nil, want TTL and delete failures")
	}
	if physicalID != "test-table" {
		t.Errorf("physical ID = %q, want created table name", physicalID)
	}
	assertDynamoDBTTLTargets(t, router.requests, []string{
		dynamodbCreateTableTarget,
		dynamodbUpdateTimeToLiveTarget,
		dynamodbDeleteTableTarget,
	})
}

func TestDynamoDBTableUpdate_TimeToLiveAttributeFailureRestoresPreviousConfiguration(t *testing.T) {
	// Given: changing the TTL attribute fails after the old attribute is disabled
	router := &dynamodbTTLRouter{statusByCall: map[string]map[int]int{
		dynamodbUpdateTimeToLiveTarget: {2: http.StatusBadRequest},
	}}
	oldProps := dynamodbTTLProperties("expiresAt")
	newProps := dynamodbTTLProperties("expiresAtV2")

	// When: CloudFormation applies the TTL update
	_, _, err := (&dynamodbTableHandler{}).Update(
		context.Background(), router, nil, "test-table", newProps, oldProps, &resolveContext{Region: "us-east-1"},
	)

	// Then: the update is terminal and the old setting has been restored
	var updateErr updateFailure
	if !errors.As(err, &updateErr) {
		t.Fatalf("Update error = %v, want terminal update failure", err)
	}
	assertDynamoDBTTLTargets(t, router.requests, []string{
		dynamodbUpdateTimeToLiveTarget,
		dynamodbUpdateTimeToLiveTarget,
		dynamodbUpdateTimeToLiveTarget,
	})
	assertDynamoDBTTLRequest(t, router.requests[0], false, "expiresAt")
	assertDynamoDBTTLRequest(t, router.requests[1], true, "expiresAtV2")
	assertDynamoDBTTLRequest(t, router.requests[2], true, "expiresAt")
}

func TestDynamoDBTableUpdate_UpdateTableFailureRestoresTimeToLive(t *testing.T) {
	// Given: DynamoDB rejects a later mutable table update after TTL changed
	router := &dynamodbTTLRouter{statusByCall: map[string]map[int]int{
		dynamodbUpdateTableTarget: {1: http.StatusBadRequest},
	}}
	oldProps := dynamodbTTLProperties("expiresAt")
	newProps := dynamodbTTLProperties("expiresAtV2")
	newProps["BillingMode"] = "PAY_PER_REQUEST"

	// When: CloudFormation updates TTL and the table settings
	_, _, err := (&dynamodbTableHandler{}).Update(
		context.Background(), router, nil, "test-table", newProps, oldProps, &resolveContext{Region: "us-east-1"},
	)

	// Then: the original TTL configuration is restored before the terminal failure
	var updateErr updateFailure
	if !errors.As(err, &updateErr) {
		t.Fatalf("Update error = %v, want terminal update failure", err)
	}
	assertDynamoDBTTLTargets(t, router.requests, []string{
		dynamodbUpdateTimeToLiveTarget,
		dynamodbUpdateTimeToLiveTarget,
		dynamodbUpdateTableTarget,
		dynamodbUpdateTimeToLiveTarget,
		dynamodbUpdateTimeToLiveTarget,
	})
	assertDynamoDBTTLRequest(t, router.requests[3], false, "expiresAtV2")
	assertDynamoDBTTLRequest(t, router.requests[4], true, "expiresAt")
}

func dynamodbTTLProperties(attributeName string) map[string]any {
	return map[string]any{
		"TableName": "test-table",
		"TimeToLiveSpecification": map[string]any{
			"Enabled": true, "AttributeName": attributeName,
		},
	}
}

func assertDynamoDBTTLTargets(t *testing.T, requests []dynamodbTTLRequest, want []string) {
	t.Helper()
	got := make([]string, 0, len(requests))
	for _, request := range requests {
		got = append(got, request.target)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("DynamoDB targets = %v, want %v", got, want)
	}
}

func assertDynamoDBTTLRequest(t *testing.T, request dynamodbTTLRequest, wantEnabled bool, wantAttribute string) {
	t.Helper()
	spec, ok := request.body["TimeToLiveSpecification"].(map[string]any)
	if !ok {
		t.Fatalf("TTL request body = %#v, want TimeToLiveSpecification", request.body)
	}
	if got := spec["Enabled"]; got != wantEnabled {
		t.Errorf("TTL Enabled = %v, want %v", got, wantEnabled)
	}
	if got := spec["AttributeName"]; got != wantAttribute {
		t.Errorf("TTL AttributeName = %v, want %q", got, wantAttribute)
	}
}
