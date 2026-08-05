package cloudformation_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func TestCreateStack_DynamoDBTableLocalSecondaryIndex(t *testing.T) {
	// Given: a stack template that declares a DynamoDB local secondary index
	srv := helpers.NewTestServer(t)
	const stackName = "dynamodb-lsi-stack"
	const tableName = "cfn-lsi-table"
	const template = `{
  "Resources": {
    "Posts": {
      "Type": "AWS::DynamoDB::Table",
      "Properties": {
        "TableName": "cfn-lsi-table",
        "AttributeDefinitions": [
          {"AttributeName": "forumId", "AttributeType": "S"},
          {"AttributeName": "postId", "AttributeType": "S"},
          {"AttributeName": "createdAt", "AttributeType": "S"}
        ],
        "KeySchema": [
          {"AttributeName": "forumId", "KeyType": "HASH"},
          {"AttributeName": "postId", "KeyType": "RANGE"}
        ],
        "LocalSecondaryIndexes": [{
          "IndexName": "by-created",
          "KeySchema": [
            {"AttributeName": "forumId", "KeyType": "HASH"},
            {"AttributeName": "createdAt", "KeyType": "RANGE"}
          ],
          "Projection": {"ProjectionType": "ALL"}
        }],
        "BillingMode": "PAY_PER_REQUEST"
      }
    }
  }
}`

	// When: CloudFormation creates the table
	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {template},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	putResp := dynamodbCall(t, srv, "PutItem", map[string]any{
		"TableName": tableName,
		"Item": map[string]any{
			"forumId":   map[string]string{"S": "f1"},
			"postId":    map[string]string{"S": "p1"},
			"createdAt": map[string]string{"S": "2026-08-06"},
		},
	})
	defer putResp.Body.Close()
	helpers.AssertStatus(t, putResp, http.StatusOK)

	// Then: the LSI is immediately available to Query without a stack update
	queryResp := dynamodbCall(t, srv, "Query", map[string]any{
		"TableName":              tableName,
		"IndexName":              "by-created",
		"KeyConditionExpression": "forumId = :forum",
		"ExpressionAttributeValues": map[string]any{
			":forum": map[string]string{"S": "f1"},
		},
	})
	defer queryResp.Body.Close()
	helpers.AssertStatus(t, queryResp, http.StatusOK)

	var result struct {
		Count int `json:"Count"`
	}
	helpers.DecodeJSON(t, queryResp, &result)
	if result.Count != 1 {
		t.Errorf("LSI Query count = %d, want 1", result.Count)
	}
}

func TestUpdateStack_DynamoDBTableLocalSecondaryIndexes(t *testing.T) {
	const localIndexes = `"LocalSecondaryIndexes": [{
          "IndexName": "by-created",
          "KeySchema": [
            {"AttributeName": "forumId", "KeyType": "HASH"},
            {"AttributeName": "createdAt", "KeyType": "RANGE"}
          ],
          "Projection": {"ProjectionType": "ALL"}
        }],`
	tests := []struct {
		name       string
		newIndexes string
	}{
		{name: "removed", newIndexes: ""},
		{name: "changed", newIndexes: strings.Replace(localIndexes, `"ALL"`, `"KEYS_ONLY"`, 1)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a stack-created table with a local secondary index
			srv := helpers.NewTestServer(t)
			stackName := "dynamodb-lsi-update-" + tc.name
			tableName := "cfn-lsi-update-" + tc.name
			createResp := cfnQuery(t, srv, "CreateStack", url.Values{
				"StackName":    {stackName},
				"TemplateBody": {dynamodbTableLSITemplate(tableName, localIndexes)},
			})
			defer createResp.Body.Close()
			helpers.AssertStatus(t, createResp, http.StatusOK)
			waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

			// When: the create-time-only LSI property is removed or changed
			updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
				"StackName":    {stackName},
				"TemplateBody": {dynamodbTableLSITemplate(tableName, tc.newIndexes)},
			})
			defer updateResp.Body.Close()
			helpers.AssertStatus(t, updateResp, http.StatusOK)
			waitForStackStatus(t, srv, stackName, "UPDATE_ROLLBACK_COMPLETE")

			// Then: CloudFormation reports unsupported LSI updates and preserves it
			reasons := strings.Join(describeStackEventReasons(t, srv, stackName), "\n")
			const wantReason = "AWS::DynamoDB::Table LocalSecondaryIndexes updates are not supported"
			if !strings.Contains(reasons, wantReason) {
				t.Errorf("stack event reasons = %q, want %q", reasons, wantReason)
			}
			queryResp := dynamodbCall(t, srv, "Query", map[string]any{
				"TableName":              tableName,
				"IndexName":              "by-created",
				"KeyConditionExpression": "forumId = :forum",
				"ExpressionAttributeValues": map[string]any{
					":forum": map[string]string{"S": "f1"},
				},
			})
			defer queryResp.Body.Close()
			helpers.AssertStatus(t, queryResp, http.StatusOK)
		})
	}
}

func dynamodbTableLSITemplate(tableName, localIndexes string) string {
	return `{
  "Resources": {
    "Posts": {
      "Type": "AWS::DynamoDB::Table",
      "Properties": {
        "TableName": "` + tableName + `",
        "AttributeDefinitions": [
          {"AttributeName": "forumId", "AttributeType": "S"},
          {"AttributeName": "postId", "AttributeType": "S"},
          {"AttributeName": "createdAt", "AttributeType": "S"}
        ],
        "KeySchema": [
          {"AttributeName": "forumId", "KeyType": "HASH"},
          {"AttributeName": "postId", "KeyType": "RANGE"}
        ],
        ` + localIndexes + `
        "BillingMode": "PAY_PER_REQUEST"
      }
    }
  }
}`
}

func TestCreateStack_DynamoDBTableTimeToLive(t *testing.T) {
	// Given: a stack template that enables a DynamoDB table TTL attribute
	srv := helpers.NewTestServer(t)
	const stackName = "dynamodb-ttl-stack"
	const tableName = "cfn-ttl-table"
	const template = `{
  "Resources": {
    "Sessions": {
      "Type": "AWS::DynamoDB::Table",
      "Properties": {
        "TableName": "cfn-ttl-table",
        "AttributeDefinitions": [{"AttributeName": "id", "AttributeType": "S"}],
        "KeySchema": [{"AttributeName": "id", "KeyType": "HASH"}],
        "BillingMode": "PAY_PER_REQUEST",
        "TimeToLiveSpecification": {"Enabled": true, "AttributeName": "expiresAt"}
      }
    }
  }
}`

	// When: CloudFormation creates the table
	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {template},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	// Then: the underlying DynamoDB table has the declared TTL configuration
	assertDynamoDBTTL(t, srv, tableName, "ENABLED", "expiresAt")
}

func TestUpdateStack_DynamoDBTableTimeToLive(t *testing.T) {
	// Given: a stack with TTL enabled on its DynamoDB table
	srv := helpers.NewTestServer(t)
	const stackName = "dynamodb-ttl-update-stack"
	const tableName = "cfn-ttl-update-table"
	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName": {stackName},
		"TemplateBody": {dynamodbTableTTLTemplate(
			tableName, `"TimeToLiveSpecification": {"Enabled": true, "AttributeName": "expiresAt"},`,
		)},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	// When: the TTL attribute changes, then the property is removed
	updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName": {stackName},
		"TemplateBody": {dynamodbTableTTLTemplate(
			tableName, `"TimeToLiveSpecification": {"Enabled": true, "AttributeName": "expiresAtV2"},`,
		)},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")
	assertDynamoDBTTL(t, srv, tableName, "ENABLED", "expiresAtV2")

	removeResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {dynamodbTableTTLTemplate(tableName, "")},
	})
	defer removeResp.Body.Close()
	helpers.AssertStatus(t, removeResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	// Then: CloudFormation has reconciled the DynamoDB TTL configuration
	assertDynamoDBTTL(t, srv, tableName, "DISABLED", "")
}

func dynamodbTableTTLTemplate(tableName, ttlProperty string) string {
	return `{
  "Resources": {
    "Sessions": {
      "Type": "AWS::DynamoDB::Table",
      "Properties": {
        "TableName": "` + tableName + `",
        "AttributeDefinitions": [{"AttributeName": "id", "AttributeType": "S"}],
        "KeySchema": [{"AttributeName": "id", "KeyType": "HASH"}],
        "BillingMode": "PAY_PER_REQUEST",
        ` + ttlProperty + `
        "Tags": [{"Key": "managed-by", "Value": "cloudformation"}]
      }
    }
  }
}`
}

func assertDynamoDBTTL(t *testing.T, srv *helpers.TestServer, tableName, wantStatus, wantAttribute string) {
	t.Helper()
	resp := dynamodbCall(t, srv, "DescribeTimeToLive", map[string]any{"TableName": tableName})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		TimeToLiveDescription struct {
			TimeToLiveStatus string `json:"TimeToLiveStatus"`
			AttributeName    string `json:"AttributeName"`
		} `json:"TimeToLiveDescription"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if got := result.TimeToLiveDescription.TimeToLiveStatus; got != wantStatus {
		t.Errorf("TTL status = %q, want %q", got, wantStatus)
	}
	if got := result.TimeToLiveDescription.AttributeName; got != wantAttribute {
		t.Errorf("TTL attribute = %q, want %q", got, wantAttribute)
	}
}

func dynamodbCall(t *testing.T, srv *helpers.TestServer, operation string, body map[string]any) *http.Response {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal DynamoDB %s request: %v", operation, err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("build DynamoDB %s request: %v", operation, err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "DynamoDB_20120810."+operation)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DynamoDB %s request: %v", operation, err)
	}
	return resp
}
