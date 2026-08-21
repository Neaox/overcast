package cloudformation

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

const (
	dynamodbTagResourceTarget   = "DynamoDB_20120810.TagResource"
	dynamodbUntagResourceTarget = "DynamoDB_20120810.UntagResource"
)

// ── dynamodbGSIUpdates (pure diff) ──────────────────────────────────────────

func TestDynamoDBGSIUpdates(t *testing.T) {
	statusHash := map[string]any{"AttributeName": "status", "KeyType": "HASH"}
	gsi := func(name, projection string, throughput map[string]any) map[string]any {
		out := map[string]any{
			"IndexName":  name,
			"KeySchema":  []any{statusHash},
			"Projection": map[string]any{"ProjectionType": projection},
		}
		if throughput != nil {
			out["ProvisionedThroughput"] = throughput
		}
		return out
	}
	throughput := func(read, write int) map[string]any {
		return map[string]any{"ReadCapacityUnits": read, "WriteCapacityUnits": write}
	}

	t.Run("create only", func(t *testing.T) {
		props := map[string]any{"GlobalSecondaryIndexes": []any{gsi("by-status", "ALL", nil)}}
		updates, err := dynamodbGSIUpdates(props, nil)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(updates) != 1 || updates[0]["Create"] == nil {
			t.Fatalf("updates = %#v, want one Create", updates)
		}
	})

	t.Run("delete only", func(t *testing.T) {
		oldProps := map[string]any{"GlobalSecondaryIndexes": []any{gsi("by-status", "ALL", nil)}}
		updates, err := dynamodbGSIUpdates(nil, oldProps)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(updates) != 1 {
			t.Fatalf("updates = %#v, want one Delete", updates)
		}
		del, ok := updates[0]["Delete"].(map[string]any)
		if !ok || del["IndexName"] != "by-status" {
			t.Fatalf("updates[0] = %#v, want Delete by-status", updates[0])
		}
	})

	t.Run("throughput-only change updates", func(t *testing.T) {
		oldProps := map[string]any{"GlobalSecondaryIndexes": []any{gsi("by-status", "ALL", throughput(5, 5))}}
		newProps := map[string]any{"GlobalSecondaryIndexes": []any{gsi("by-status", "ALL", throughput(20, 15))}}
		updates, err := dynamodbGSIUpdates(newProps, oldProps)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(updates) != 1 {
			t.Fatalf("updates = %#v, want one Update", updates)
		}
		upd, ok := updates[0]["Update"].(map[string]any)
		if !ok || upd["IndexName"] != "by-status" {
			t.Fatalf("updates[0] = %#v, want Update by-status", updates[0])
		}
	})

	t.Run("unchanged index produces no update", func(t *testing.T) {
		props := map[string]any{"GlobalSecondaryIndexes": []any{gsi("by-status", "ALL", nil)}}
		updates, err := dynamodbGSIUpdates(props, props)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(updates) != 0 {
			t.Fatalf("updates = %#v, want none", updates)
		}
	})

	t.Run("projection change is rejected", func(t *testing.T) {
		oldProps := map[string]any{"GlobalSecondaryIndexes": []any{gsi("by-status", "ALL", nil)}}
		newProps := map[string]any{"GlobalSecondaryIndexes": []any{gsi("by-status", "KEYS_ONLY", nil)}}
		if _, err := dynamodbGSIUpdates(newProps, oldProps); err == nil {
			t.Fatal("err = nil, want KeySchema/Projection rejection")
		}
	})

	t.Run("delete and create dispatch in delete-then-create order", func(t *testing.T) {
		oldProps := map[string]any{"GlobalSecondaryIndexes": []any{gsi("by-old", "ALL", nil)}}
		newProps := map[string]any{"GlobalSecondaryIndexes": []any{gsi("by-new", "ALL", nil)}}
		updates, err := dynamodbGSIUpdates(newProps, oldProps)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(updates) != 2 || updates[0]["Delete"] == nil || updates[1]["Create"] == nil {
			t.Fatalf("updates = %#v, want [Delete, Create]", updates)
		}
	})

	t.Run("malformed GlobalSecondaryIndexes is rejected", func(t *testing.T) {
		props := map[string]any{"GlobalSecondaryIndexes": "not-a-list"}
		if _, err := dynamodbGSIUpdates(props, nil); err == nil {
			t.Fatal("err = nil, want rejection of non-list GlobalSecondaryIndexes")
		}
	})
}

// ── dynamodbGlobalTableReplicaProps (pure translation) ──────────────────────

func TestDynamoDBGlobalTableReplicaProps(t *testing.T) {
	baseProps := func(extra map[string]any) map[string]any {
		props := map[string]any{
			"TableName":            "sessions",
			"AttributeDefinitions": []any{map[string]any{"AttributeName": "id", "AttributeType": "S"}},
			"KeySchema":            []any{map[string]any{"AttributeName": "id", "KeyType": "HASH"}},
			"Replicas": []any{
				map[string]any{"Region": "eu-west-1"},
				map[string]any{"Region": "us-east-1"},
			},
		}
		for k, v := range extra {
			props[k] = v
		}
		return props
	}

	t.Run("matching replica translates and forces streaming", func(t *testing.T) {
		tableProps, found, err := dynamodbGlobalTableReplicaProps(baseProps(nil), "us-east-1")
		if err != nil || !found {
			t.Fatalf("found = %v, err = %v, want found with no error", found, err)
		}
		if tableProps["TableName"] != "sessions" {
			t.Errorf("TableName = %v, want sessions", tableProps["TableName"])
		}
		if tableProps["BillingMode"] != "PAY_PER_REQUEST" {
			t.Errorf("BillingMode = %v, want PAY_PER_REQUEST (default)", tableProps["BillingMode"])
		}
		stream, ok := tableProps["StreamSpecification"].(map[string]any)
		if !ok || stream["StreamViewType"] != "NEW_AND_OLD_IMAGES" {
			t.Errorf("StreamSpecification = %#v, want forced NEW_AND_OLD_IMAGES", tableProps["StreamSpecification"])
		}
	})

	t.Run("no replica for region is not found, not an error", func(t *testing.T) {
		_, found, err := dynamodbGlobalTableReplicaProps(baseProps(nil), "ap-southeast-2")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if found {
			t.Fatal("found = true, want false when no Replica names the region")
		}
	})

	t.Run("no Replicas property at all is not found", func(t *testing.T) {
		_, found, err := dynamodbGlobalTableReplicaProps(map[string]any{"TableName": "sessions"}, "us-east-1")
		if err != nil || found {
			t.Fatalf("found = %v, err = %v, want (false, nil)", found, err)
		}
	})

	t.Run("BillingMode PAY_PER_REQUEST is forwarded explicitly", func(t *testing.T) {
		tableProps, found, err := dynamodbGlobalTableReplicaProps(baseProps(map[string]any{"BillingMode": "PAY_PER_REQUEST"}), "us-east-1")
		if err != nil || !found {
			t.Fatalf("found = %v, err = %v", found, err)
		}
		if tableProps["BillingMode"] != "PAY_PER_REQUEST" {
			t.Errorf("BillingMode = %v, want PAY_PER_REQUEST", tableProps["BillingMode"])
		}
	})

	t.Run("BillingMode PROVISIONED is rejected", func(t *testing.T) {
		if _, _, err := dynamodbGlobalTableReplicaProps(baseProps(map[string]any{"BillingMode": "PROVISIONED"}), "us-east-1"); err == nil {
			t.Fatal("err = nil, want PROVISIONED rejection")
		}
	})

	t.Run("top-level WriteProvisionedThroughputSettings is rejected", func(t *testing.T) {
		extra := map[string]any{"WriteProvisionedThroughputSettings": map[string]any{}}
		if _, _, err := dynamodbGlobalTableReplicaProps(baseProps(extra), "us-east-1"); err == nil {
			t.Fatal("err = nil, want WriteProvisionedThroughputSettings rejection")
		}
	})

	t.Run("per-replica ReadProvisionedThroughputSettings is rejected", func(t *testing.T) {
		extra := map[string]any{"Replicas": []any{
			map[string]any{"Region": "us-east-1", "ReadProvisionedThroughputSettings": map[string]any{}},
		}}
		if _, _, err := dynamodbGlobalTableReplicaProps(baseProps(extra), "us-east-1"); err == nil {
			t.Fatal("err = nil, want ReadProvisionedThroughputSettings rejection")
		}
	})

	t.Run("Tags and TimeToLiveSpecification pass through unchanged", func(t *testing.T) {
		extra := map[string]any{
			"Tags":                    []any{map[string]any{"Key": "team", "Value": "platform"}},
			"TimeToLiveSpecification": map[string]any{"Enabled": true, "AttributeName": "expiresAt"},
		}
		tableProps, found, err := dynamodbGlobalTableReplicaProps(baseProps(extra), "us-east-1")
		if err != nil || !found {
			t.Fatalf("found = %v, err = %v", found, err)
		}
		if _, ok := tableProps["Tags"]; !ok {
			t.Error("Tags not forwarded")
		}
		if _, ok := tableProps["TimeToLiveSpecification"]; !ok {
			t.Error("TimeToLiveSpecification not forwarded")
		}
	})
}

// ── Create/Update dispatch: tags and dirty-update marking ──────────────────

func TestDynamoDBTableCreate_TagResourceFailureDeletesCreatedTable(t *testing.T) {
	// Given: DynamoDB rejects the tags after creating the table
	router := &dynamodbTTLRouter{statusByCall: map[string]map[int]int{
		dynamodbTagResourceTarget: {1: http.StatusBadRequest},
	}}
	props := map[string]any{
		"TableName": "test-table",
		"Tags":      []any{map[string]any{"Key": "team", "Value": "platform"}},
	}

	// When: Create runs
	physicalID, attrs, err := (&dynamodbTableHandler{}).Create(
		context.Background(), router, nil, props, &resolveContext{Region: "us-east-1", AccountID: "000000000000"},
	)

	// Then: it fails and the newly created table is torn down
	if err == nil {
		t.Fatal("err = nil, want TagResource failure")
	}
	if physicalID != "" || attrs != nil {
		t.Errorf("physicalID = %q, attrs = %#v, want empty on failure", physicalID, attrs)
	}
	assertDynamoDBTTLTargets(t, router.requests, []string{dynamodbCreateTableTarget, dynamodbTagResourceTarget, dynamodbDeleteTableTarget})
}

func TestDynamoDBTableUpdate_GlobalSecondaryIndexFailureIsDirty(t *testing.T) {
	// Given: an update that adds two GSIs, and DynamoDB rejects the second one
	router := &dynamodbTTLRouter{statusByCall: map[string]map[int]int{
		dynamodbUpdateTableTarget: {2: http.StatusBadRequest},
	}}
	statusHash := []any{map[string]any{"AttributeName": "status", "KeyType": "HASH"}}
	ownerHash := []any{map[string]any{"AttributeName": "owner", "KeyType": "HASH"}}
	oldProps := map[string]any{"TableName": "test-table"}
	newProps := map[string]any{
		"TableName": "test-table",
		"GlobalSecondaryIndexes": []any{
			map[string]any{"IndexName": "by-owner", "KeySchema": ownerHash, "Projection": map[string]any{"ProjectionType": "ALL"}},
			map[string]any{"IndexName": "by-status", "KeySchema": statusHash, "Projection": map[string]any{"ProjectionType": "ALL"}},
		},
	}

	// When: Update runs
	_, _, err := (&dynamodbTableHandler{}).Update(
		context.Background(), router, nil, "test-table", newProps, oldProps,
		&resolveContext{Region: "us-east-1", AccountID: "000000000000"},
	)

	// Then: the update fails and is marked dirty — the first GSI create already
	// applied and cannot be compensated
	var updateErr updateFailure
	if !errors.As(err, &updateErr) {
		t.Fatalf("err = %v, want updateFailure", err)
	}
	if !updateErr.dirty {
		t.Error("dirty = false, want true: one GlobalSecondaryIndexUpdates call already succeeded")
	}
}
