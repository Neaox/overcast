package cloudformation

import (
	"context"
	"errors"
	"testing"
)

func TestDynamoDBTableUpdate_LocalSecondaryIndexesChangedDoesNotCallDynamoDB(t *testing.T) {
	keySchema := []any{
		map[string]any{"AttributeName": "forumId", "KeyType": "HASH"},
		map[string]any{"AttributeName": "createdAt", "KeyType": "RANGE"},
	}
	oldIndexes := []any{map[string]any{
		"IndexName":  "by-created",
		"KeySchema":  keySchema,
		"Projection": map[string]any{"ProjectionType": "ALL"},
	}}
	tests := []struct {
		name       string
		newIndexes any
	}{
		{name: "removed", newIndexes: nil},
		{name: "changed", newIndexes: []any{map[string]any{
			"IndexName":  "by-created",
			"KeySchema":  keySchema,
			"Projection": map[string]any{"ProjectionType": "KEYS_ONLY"},
		}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Given: an existing table whose create-time-only LSI property changed
			router := &dynamodbTTLRouter{}
			oldProps := map[string]any{"TableName": "test-table", "LocalSecondaryIndexes": oldIndexes}
			newProps := map[string]any{"TableName": "test-table"}
			if tc.newIndexes != nil {
				newProps["LocalSecondaryIndexes"] = tc.newIndexes
			}

			// When: CloudFormation attempts the in-place update
			_, _, err := (&dynamodbTableHandler{}).Update(
				context.Background(), router, nil, "test-table", newProps, oldProps,
				&resolveContext{LogicalID: "Posts", Region: "us-east-1"},
			)

			// Then: the update fails terminally without dispatching UpdateTable
			const want = "AWS::DynamoDB::Table LocalSecondaryIndexes updates are not supported"
			var updateErr updateFailure
			if !errors.As(err, &updateErr) || err.Error() != want {
				t.Fatalf("Update error = %v, want terminal error %q", err, want)
			}
			assertDynamoDBTTLTargets(t, router.requests, nil)
		})
	}
}
