package cloudformation_test

import (
	"net/http"
	"net/url"
	"reflect"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// TestCreateStack_DynamoDBTableTags covers #523: AWS::DynamoDB::Table's Tags
// property, and propagated stack-level tags, used to be parsed into nothing —
// DynamoDB grew TagResource/ListTagsOfResource/UntagResource (#685) after the
// CloudFormation adapter's original allow-list was written, and nothing ever
// went back to wire Tags through. Resource tags take precedence over
// stack tags on a key collision, matching every other tag-forwarding handler
// (Lambda, Logs, Secrets Manager) in this adapter.
func TestCreateStack_DynamoDBTableTags(t *testing.T) {
	// Given: a stack template with a resource tag and a colliding stack tag
	srv := helpers.NewTestServer(t)
	const stackName = "dynamodb-tags-create-stack"
	const tableName = "cfn-tags-create-table"
	const template = `{
  "Resources": {
    "Sessions": {
      "Type": "AWS::DynamoDB::Table",
      "Properties": {
        "TableName": "cfn-tags-create-table",
        "AttributeDefinitions": [{"AttributeName": "id", "AttributeType": "S"}],
        "KeySchema": [{"AttributeName": "id", "KeyType": "HASH"}],
        "BillingMode": "PAY_PER_REQUEST",
        "Tags": [
          {"Key": "environment", "Value": "resource"},
          {"Key": "project", "Value": "overcast"}
        ]
      }
    }
  }
}`

	// When: CloudFormation creates the table with both resource and stack tags
	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":           {stackName},
		"TemplateBody":        {template},
		"Tags.member.1.Key":   {"environment"},
		"Tags.member.1.Value": {"stack"},
		"Tags.member.2.Key":   {"team"},
		"Tags.member.2.Value": {"platform"},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	// Then: the resource tag wins the collision and both sets are present
	if got := listDynamoDBTags(t, srv, tableName); !reflect.DeepEqual(got, map[string]string{
		"environment": "resource",
		"project":     "overcast",
		"team":        "platform",
	}) {
		t.Fatalf("tags = %#v", got)
	}
}

func TestUpdateStack_DynamoDBTableTags(t *testing.T) {
	// Given: a stack-created table with CloudFormation-owned tags
	srv := helpers.NewTestServer(t)
	const stackName = "dynamodb-tags-update-stack"
	const tableName = "cfn-tags-update-table"
	const initialTemplate = `{
  "Resources": {
    "Sessions": {
      "Type": "AWS::DynamoDB::Table",
      "Properties": {
        "TableName": "cfn-tags-update-table",
        "AttributeDefinitions": [{"AttributeName": "id", "AttributeType": "S"}],
        "KeySchema": [{"AttributeName": "id", "KeyType": "HASH"}],
        "BillingMode": "PAY_PER_REQUEST",
        "Tags": [
          {"Key": "environment", "Value": "development"},
          {"Key": "owner", "Value": "platform"}
        ]
      }
    }
  }
}`
	const updatedTemplate = `{
  "Resources": {
    "Sessions": {
      "Type": "AWS::DynamoDB::Table",
      "Properties": {
        "TableName": "cfn-tags-update-table",
        "AttributeDefinitions": [{"AttributeName": "id", "AttributeType": "S"}],
        "KeySchema": [{"AttributeName": "id", "KeyType": "HASH"}],
        "BillingMode": "PAY_PER_REQUEST",
        "Tags": [
          {"Key": "environment", "Value": "production"},
          {"Key": "project", "Value": "overcast"}
        ]
      }
    }
  }
}`
	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {initialTemplate},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	if got := listDynamoDBTags(t, srv, tableName); !reflect.DeepEqual(got, map[string]string{
		"environment": "development",
		"owner":       "platform",
	}) {
		t.Fatalf("initial tags = %#v", got)
	}

	// When: the resource tags are changed, added, and removed
	updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {updatedTemplate},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	// Then: DynamoDB's tag set matches the updated CloudFormation tags exactly
	if got := listDynamoDBTags(t, srv, tableName); !reflect.DeepEqual(got, map[string]string{
		"environment": "production",
		"project":     "overcast",
	}) {
		t.Fatalf("updated tags = %#v", got)
	}
}

func listDynamoDBTags(t *testing.T, srv *helpers.TestServer, tableName string) map[string]string {
	t.Helper()
	resp := dynamodbCall(t, srv, "ListTagsOfResource", map[string]any{
		"ResourceArn": dynamodbTestTableARN(tableName),
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		Tags []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"Tags"`
	}
	helpers.DecodeJSON(t, resp, &result)
	out := make(map[string]string, len(result.Tags))
	for _, tag := range result.Tags {
		out[tag.Key] = tag.Value
	}
	return out
}

// dynamodbTestTableARN builds the ARN for a table created by the default test
// server, whose account ID and region are fixed across this test suite (see
// tests/integration/dynamodb for the same constants used directly).
func dynamodbTestTableARN(tableName string) string {
	return "arn:aws:dynamodb:us-east-1:000000000000:table/" + tableName
}
