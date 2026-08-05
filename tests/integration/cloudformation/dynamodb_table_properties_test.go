package cloudformation_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
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
