package cloudformation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
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

func TestDynamoDBTableCreate_TimeToLiveMissingEnabledDoesNotCreateTable(t *testing.T) {
	// Given: a TTL property without CloudFormation's required Enabled field
	router := &dynamodbTTLRouter{}
	props := map[string]any{
		"TableName":               "test-table",
		"TimeToLiveSpecification": map[string]any{"AttributeName": "expiresAt"},
	}

	// When: CloudFormation validates the resource before creation
	physicalID, _, err := (&dynamodbTableHandler{}).Create(
		context.Background(), router, nil, props, &resolveContext{LogicalID: "Sessions", Region: "us-east-1"},
	)

	// Then: the modeled validation error is returned without creating a table
	const want = "Properties validation failed for resource Sessions with message: #/TimeToLiveSpecification: required key [Enabled] not found"
	if err == nil || err.Error() != want {
		t.Fatalf("Create error = %v, want %q", err, want)
	}
	if physicalID != "" {
		t.Errorf("physical ID = %q, want empty", physicalID)
	}
	assertDynamoDBTTLTargets(t, router.requests, nil)
}

func TestDynamoDBTableCreate_DisabledTimeToLiveWithoutAttributeIsNoOp(t *testing.T) {
	// Given: valid CloudFormation TTL configuration for an already-disabled new table
	router := &dynamodbTTLRouter{}
	props := map[string]any{
		"TableName":               "test-table",
		"TimeToLiveSpecification": map[string]any{"Enabled": false},
	}

	// When: CloudFormation creates the table
	_, _, err := (&dynamodbTableHandler{}).Create(
		context.Background(), router, nil, props, &resolveContext{LogicalID: "Sessions", Region: "us-east-1"},
	)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Then: it creates only the table and does not send an invalid TTL API shape
	assertDynamoDBTTLTargets(t, router.requests, []string{dynamodbCreateTableTarget})
}

func TestDynamoDBTableCreate_TimeToLiveAttributeLengthValidation(t *testing.T) {
	tests := []struct {
		name          string
		attributeName string
		wantDetail    string
	}{
		{name: "empty", attributeName: "", wantDetail: "expected minLength: 1, actual: 0"},
		{name: "minimum", attributeName: "a"},
		{name: "maximum", attributeName: strings.Repeat("a", 255)},
		{name: "too long", attributeName: strings.Repeat("a", 256), wantDetail: "expected maxLength: 255, actual: 256"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Given: an enabled TTL property with a boundary attribute name
			router := &dynamodbTTLRouter{}
			props := map[string]any{
				"TableName": "test-table",
				"TimeToLiveSpecification": map[string]any{
					"Enabled": true, "AttributeName": tc.attributeName,
				},
			}

			// When: CloudFormation validates the resource before creation
			_, _, err := (&dynamodbTableHandler{}).Create(
				context.Background(), router, nil, props, &resolveContext{LogicalID: "Sessions", Region: "us-east-1"},
			)

			// Then: names inside 1..255 are accepted and names outside are rejected
			if tc.wantDetail == "" {
				if err != nil {
					t.Fatalf("Create: %v", err)
				}
				assertDynamoDBTTLTargets(t, router.requests, []string{dynamodbCreateTableTarget, dynamodbUpdateTimeToLiveTarget})
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantDetail) {
				t.Fatalf("Create error = %v, want detail %q", err, tc.wantDetail)
			}
			assertDynamoDBTTLTargets(t, router.requests, nil)
		})
	}
}

func TestDynamoDBTableUpdate_TimeToLiveAttributeChangeIsRefused(t *testing.T) {
	// Given: a template that renames the TTL attribute while TTL is enabled
	router := &dynamodbTTLRouter{}
	oldProps := dynamodbTTLProperties("expiresAt")
	newProps := dynamodbTTLProperties("expiresAtV2")

	// When: CloudFormation applies the TTL update
	_, _, err := (&dynamodbTableHandler{}).Update(
		context.Background(), router, nil, "test-table", newProps, oldProps, &resolveContext{Region: "us-east-1"},
	)

	// Then: the update fails terminally with AWS's own refusal — a rename has
	// to go through a disable first, and DynamoDB refuses a second
	// UpdateTimeToLive inside the transition window either way
	var updateErr updateFailure
	if !errors.As(err, &updateErr) {
		t.Fatalf("Update error = %v, want terminal update failure", err)
	}
	if !strings.Contains(err.Error(), "Cannot change time-to-live attribute name") {
		t.Errorf("Update error = %v, want AWS's attribute-name refusal", err)
	}

	// And: nothing was sent to DynamoDB, so the table is untouched
	assertDynamoDBTTLTargets(t, router.requests, nil)
}

func TestDynamoDBTableUpdate_UpdateTableFailureRestoresTimeToLive(t *testing.T) {
	// Given: DynamoDB rejects a later mutable table update after TTL changed
	router := &dynamodbTTLRouter{statusByCall: map[string]map[int]int{
		dynamodbUpdateTableTarget: {1: http.StatusBadRequest},
	}}
	oldProps := map[string]any{"TableName": "test-table"}
	newProps := dynamodbTTLProperties("expiresAt")
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
		dynamodbUpdateTableTarget,
		dynamodbUpdateTimeToLiveTarget,
	})
	assertDynamoDBTTLRequest(t, router.requests[0], true, "expiresAt")
	assertDynamoDBTTLRequest(t, router.requests[2], false, "expiresAt")
}

func TestDynamoDBTableUpdate_DisableTimeToLiveWithoutAttributeUsesPreviousAttribute(t *testing.T) {
	// Given: an enabled TTL property being disabled without repeating AttributeName
	router := &dynamodbTTLRouter{}
	oldProps := dynamodbTTLProperties("expiresAt")
	newProps := map[string]any{
		"TableName":               "test-table",
		"TimeToLiveSpecification": map[string]any{"Enabled": false},
	}

	// When: CloudFormation applies the update
	_, _, err := (&dynamodbTableHandler{}).Update(
		context.Background(), router, nil, "test-table", newProps, oldProps,
		&resolveContext{LogicalID: "Sessions", Region: "us-east-1"},
	)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Then: DynamoDB receives the old attribute required by its API shape
	assertDynamoDBTTLTargets(t, router.requests, []string{dynamodbUpdateTimeToLiveTarget})
	assertDynamoDBTTLRequest(t, router.requests[0], false, "expiresAt")
}

func TestDynamoDBTableUpdate_DisabledTimeToLiveWithoutAttributeIsNoOp(t *testing.T) {
	// Given: an already-disabled table receiving explicit disabled TTL configuration
	router := &dynamodbTTLRouter{}
	oldProps := map[string]any{"TableName": "test-table"}
	newProps := map[string]any{
		"TableName":               "test-table",
		"TimeToLiveSpecification": map[string]any{"Enabled": false},
	}

	// When: CloudFormation applies the update
	_, _, err := (&dynamodbTableHandler{}).Update(
		context.Background(), router, nil, "test-table", newProps, oldProps,
		&resolveContext{LogicalID: "Sessions", Region: "us-east-1"},
	)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Then: no invalid UpdateTimeToLive request is sent
	assertDynamoDBTTLTargets(t, router.requests, nil)
}

func TestDynamoDBTableUpdate_TimeToLiveMissingEnabledDoesNotMutateTable(t *testing.T) {
	// Given: invalid TTL configuration alongside an otherwise mutable table property
	router := &dynamodbTTLRouter{}
	oldProps := map[string]any{"TableName": "test-table", "BillingMode": "PROVISIONED"}
	newProps := map[string]any{
		"TableName":   "test-table",
		"BillingMode": "PAY_PER_REQUEST",
		"TimeToLiveSpecification": map[string]any{
			"AttributeName": "expiresAt",
		},
	}

	// When: CloudFormation validates the update
	_, _, err := (&dynamodbTableHandler{}).Update(
		context.Background(), router, nil, "test-table", newProps, oldProps,
		&resolveContext{LogicalID: "Sessions", Region: "us-east-1"},
	)

	// Then: it rejects the property before either DynamoDB operation can mutate
	const want = "Properties validation failed for resource Sessions with message: #/TimeToLiveSpecification: required key [Enabled] not found"
	var updateErr updateFailure
	if !errors.As(err, &updateErr) || err.Error() != want {
		t.Fatalf("Update error = %v, want terminal error %q", err, want)
	}
	assertDynamoDBTTLTargets(t, router.requests, nil)
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
