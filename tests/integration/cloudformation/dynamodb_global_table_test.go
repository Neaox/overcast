package cloudformation_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// TestCreateStack_DynamoDBGlobalTableProvisionsLocalReplica covers #523's
// third bullet: AWS::DynamoDB::GlobalTable used to be a no-op stub, so a
// stack that declared one "succeeded" while creating nothing. Overcast
// emulates a single region, so it provisions the Replicas entry matching the
// stack's own deploy region (the test server defaults to us-east-1) rather
// than attempting real cross-region replication.
func TestCreateStack_DynamoDBGlobalTableProvisionsLocalReplica(t *testing.T) {
	// Given: a global table declaring the stack's own region among its replicas
	srv := helpers.NewTestServer(t)
	const stackName = "dynamodb-globaltable-create-stack"
	const tableName = "cfn-globaltable-create-table"
	const template = `{
  "Resources": {
    "Sessions": {
      "Type": "AWS::DynamoDB::GlobalTable",
      "Properties": {
        "TableName": "cfn-globaltable-create-table",
        "AttributeDefinitions": [{"AttributeName": "id", "AttributeType": "S"}],
        "KeySchema": [{"AttributeName": "id", "KeyType": "HASH"}],
        "Tags": [{"Key": "managed-by", "Value": "cloudformation"}],
        "Replicas": [
          {"Region": "eu-west-1"},
          {"Region": "us-east-1"}
        ]
      }
    }
  }
}`

	// When: CloudFormation creates the stack
	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {template},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	// Then: a real table exists for the local (us-east-1) replica, on-demand
	// billed, streaming (global tables always replicate via streams), with its
	// tags applied
	descResp := dynamodbCall(t, srv, "DescribeTable", map[string]any{"TableName": tableName})
	defer descResp.Body.Close()
	helpers.AssertStatus(t, descResp, http.StatusOK)
	var result struct {
		Table struct {
			BillingModeSummary struct {
				BillingMode string `json:"BillingMode"`
			} `json:"BillingModeSummary"`
			StreamSpecification struct {
				StreamEnabled  bool   `json:"StreamEnabled"`
				StreamViewType string `json:"StreamViewType"`
			} `json:"StreamSpecification"`
		} `json:"Table"`
	}
	helpers.DecodeJSON(t, descResp, &result)
	if result.Table.BillingModeSummary.BillingMode != "PAY_PER_REQUEST" {
		t.Errorf("BillingMode = %q, want PAY_PER_REQUEST", result.Table.BillingModeSummary.BillingMode)
	}
	if !result.Table.StreamSpecification.StreamEnabled || result.Table.StreamSpecification.StreamViewType != "NEW_AND_OLD_IMAGES" {
		t.Errorf("StreamSpecification = %+v, want enabled NEW_AND_OLD_IMAGES", result.Table.StreamSpecification)
	}

	putResp := dynamodbCall(t, srv, "PutItem", map[string]any{
		"TableName": tableName,
		"Item":      map[string]any{"id": map[string]string{"S": "row-1"}},
	})
	defer putResp.Body.Close()
	helpers.AssertStatus(t, putResp, http.StatusOK)

	if got := listDynamoDBTags(t, srv, tableName); got["managed-by"] != "cloudformation" {
		t.Errorf("tags = %#v, want managed-by=cloudformation", got)
	}
}

func TestCreateStack_DynamoDBGlobalTableMissingLocalReplicaFailsLoudly(t *testing.T) {
	// Given: a global table whose Replicas never name the stack's own region
	srv := helpers.NewTestServer(t)
	const stackName = "dynamodb-globaltable-missing-replica-stack"
	const template = `{
  "Resources": {
    "Sessions": {
      "Type": "AWS::DynamoDB::GlobalTable",
      "Properties": {
        "TableName": "cfn-globaltable-missing-replica-table",
        "AttributeDefinitions": [{"AttributeName": "id", "AttributeType": "S"}],
        "KeySchema": [{"AttributeName": "id", "KeyType": "HASH"}],
        "Replicas": [{"Region": "eu-west-1"}]
      }
    }
  }
}`

	// When: CloudFormation creates the stack
	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {template},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "ROLLBACK_COMPLETE")

	// Then: the stack reports why, instead of a silent no-op success
	reasons := strings.Join(describeStackEventReasons(t, srv, stackName), "\n")
	const wantReason = "must declare a Replica for the stack's deploy region"
	if !strings.Contains(reasons, wantReason) {
		t.Errorf("stack event reasons = %q, want %q", reasons, wantReason)
	}

	descResp := dynamodbCall(t, srv, "DescribeTable", map[string]any{"TableName": "cfn-globaltable-missing-replica-table"})
	defer descResp.Body.Close()
	helpers.AssertStatus(t, descResp, http.StatusBadRequest)
}

func TestCreateStack_DynamoDBGlobalTableProvisionedBillingRejected(t *testing.T) {
	// Given: a global table asking for provisioned (auto-scaled) capacity,
	// which nothing in Overcast's DynamoDB emulation runs
	srv := helpers.NewTestServer(t)
	const stackName = "dynamodb-globaltable-provisioned-stack"
	const template = `{
  "Resources": {
    "Sessions": {
      "Type": "AWS::DynamoDB::GlobalTable",
      "Properties": {
        "TableName": "cfn-globaltable-provisioned-table",
        "AttributeDefinitions": [{"AttributeName": "id", "AttributeType": "S"}],
        "KeySchema": [{"AttributeName": "id", "KeyType": "HASH"}],
        "BillingMode": "PROVISIONED",
        "Replicas": [{"Region": "us-east-1"}]
      }
    }
  }
}`

	// When: CloudFormation creates the stack
	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {template},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "ROLLBACK_COMPLETE")

	// Then: the stack fails loudly rather than fabricating throughput values
	reasons := strings.Join(describeStackEventReasons(t, srv, stackName), "\n")
	const wantReason = "BillingMode PROVISIONED is not supported"
	if !strings.Contains(reasons, wantReason) {
		t.Errorf("stack event reasons = %q, want %q", reasons, wantReason)
	}
}

func TestUpdateStack_DynamoDBGlobalTableReplicaRemovedFailsUpdate(t *testing.T) {
	// Given: a global table replicated into the stack's own region
	srv := helpers.NewTestServer(t)
	const stackName = "dynamodb-globaltable-replica-removed-stack"
	const tableName = "cfn-globaltable-replica-removed-table"
	const initialTemplate = `{
  "Resources": {
    "Sessions": {
      "Type": "AWS::DynamoDB::GlobalTable",
      "Properties": {
        "TableName": "cfn-globaltable-replica-removed-table",
        "AttributeDefinitions": [{"AttributeName": "id", "AttributeType": "S"}],
        "KeySchema": [{"AttributeName": "id", "KeyType": "HASH"}],
        "Replicas": [{"Region": "us-east-1"}]
      }
    }
  }
}`
	const updatedTemplate = `{
  "Resources": {
    "Sessions": {
      "Type": "AWS::DynamoDB::GlobalTable",
      "Properties": {
        "TableName": "cfn-globaltable-replica-removed-table",
        "AttributeDefinitions": [{"AttributeName": "id", "AttributeType": "S"}],
        "KeySchema": [{"AttributeName": "id", "KeyType": "HASH"}],
        "Replicas": [{"Region": "eu-west-1"}]
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

	putResp := dynamodbCall(t, srv, "PutItem", map[string]any{
		"TableName": tableName,
		"Item":      map[string]any{"id": map[string]string{"S": "row-1"}},
	})
	defer putResp.Body.Close()
	helpers.AssertStatus(t, putResp, http.StatusOK)

	// When: the stack's own region drops out of Replicas
	updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {updatedTemplate},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_ROLLBACK_COMPLETE")

	// Then: the update does not silently keep an unaccounted-for table around;
	// replacement was attempted, could not create a table with no local
	// replica, and rollback preserved the original table and its data
	getResp := dynamodbCall(t, srv, "GetItem", map[string]any{
		"TableName": tableName,
		"Key":       map[string]any{"id": map[string]string{"S": "row-1"}},
	})
	defer getResp.Body.Close()
	helpers.AssertStatus(t, getResp, http.StatusOK)
	var result struct {
		Item map[string]any `json:"Item"`
	}
	helpers.DecodeJSON(t, getResp, &result)
	if len(result.Item) == 0 {
		t.Error("original global table item did not survive the rejected update")
	}
}
