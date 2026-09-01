package cloudformation_test

// stack_tag_reconciliation_gap_1310_test.go — #1310's audit found three
// resource types (AWS::DynamoDB::Table, AWS::SSM::Parameter,
// AWS::StepFunctions::StateMachine) whose Create/Update handlers already
// merged the stack's own tags and reconciled them correctly — but the
// resource type was missing from hashResourceProperties/
// resourcePropertiesMatch's effective-stack-tag switch (provisioner.go). That
// switch is what makes a stack-tag-only change (no change to the resource's
// own template properties) reach Update at all; without it,
// resourcePropertiesMatch treats the resource as unchanged and skips it
// outright, so a stack-tag-only UpdateStack silently did nothing to these
// three types' tags. Each test below reproduces exactly that shape: an
// UpdateStack whose only difference from the create template is the stack's
// own Tags.
//
// (Contrast with cloudtrail_stack_tags_test.go, transfer_stack_tags_test.go
// and iam_managedpolicy_instanceprofile_stack_tags_test.go, which cover the
// five resource types that had no stack-tag merging code at all before
// #1310 — a different gap, closed by adding the merge, not by fixing a
// switch omission.)

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// ssmJSONCall/listSSMParameterTags are defined in ssm_properties_test.go;
// listSFNTags (via sfnCFNJSONCall) is defined in stepfunctions_properties_test.go.

const dynamodbStackTagReconcileTemplate = `{
  "Resources": {
    "Table": {
      "Type": "AWS::DynamoDB::Table",
      "Properties": {
        "TableName": "cfn-stack-tag-gap-table",
        "AttributeDefinitions": [{"AttributeName": "id", "AttributeType": "S"}],
        "KeySchema": [{"AttributeName": "id", "KeyType": "HASH"}],
        "BillingMode": "PAY_PER_REQUEST",
        "Tags": [{"Key": "owner", "Value": "resource"}]
      }
    }
  }
}`

func TestUpdateStack_DynamoDBTable_stackTagOnlyChangeReconciles(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "dynamodb-stack-tag-gap-update"

	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":           {stackName},
		"TemplateBody":        {dynamodbStackTagReconcileTemplate},
		"Tags.member.1.Key":   {"env"},
		"Tags.member.1.Value": {"dev"},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	if got := listDynamoDBTags(t, srv, "cfn-stack-tag-gap-table"); got["env"] != "dev" {
		t.Fatalf("initial table tags = %#v, want env=dev", got)
	}

	// When: the template is byte-for-byte identical; only the stack's own
	// Tags change.
	updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":           {stackName},
		"TemplateBody":        {dynamodbStackTagReconcileTemplate},
		"Tags.member.1.Key":   {"env"},
		"Tags.member.1.Value": {"prod"},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	if got := listDynamoDBTags(t, srv, "cfn-stack-tag-gap-table"); got["env"] != "prod" || got["owner"] != "resource" {
		t.Fatalf("reconciled table tags = %#v, want env=prod (reconciled) and owner=resource (unchanged)", got)
	}
}

const ssmStackTagReconcileTemplate = `{
  "Resources": {
    "Param": {
      "Type": "AWS::SSM::Parameter",
      "Properties": {
        "Name": "/cfn-stack-tag-gap/param",
        "Type": "String",
        "Value": "v1",
        "Tags": [{"Key": "owner", "Value": "resource"}]
      }
    }
  }
}`

func TestUpdateStack_SSMParameter_stackTagOnlyChangeReconciles(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "ssm-stack-tag-gap-update"

	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":           {stackName},
		"TemplateBody":        {ssmStackTagReconcileTemplate},
		"Tags.member.1.Key":   {"env"},
		"Tags.member.1.Value": {"dev"},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	if got := listSSMParameterTags(t, srv, "/cfn-stack-tag-gap/param"); got["env"] != "dev" {
		t.Fatalf("initial parameter tags = %#v, want env=dev", got)
	}

	updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":           {stackName},
		"TemplateBody":        {ssmStackTagReconcileTemplate},
		"Tags.member.1.Key":   {"env"},
		"Tags.member.1.Value": {"prod"},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	if got := listSSMParameterTags(t, srv, "/cfn-stack-tag-gap/param"); got["env"] != "prod" || got["owner"] != "resource" {
		t.Fatalf("reconciled parameter tags = %#v, want env=prod (reconciled) and owner=resource (unchanged)", got)
	}
}

const sfnStackTagReconcileTemplate = `{
  "Resources": {
    "StateMachine": {
      "Type": "AWS::StepFunctions::StateMachine",
      "Properties": {
        "StateMachineName": "cfn-stack-tag-gap-sm",
        "DefinitionString": "{\"StartAt\":\"Pass\",\"States\":{\"Pass\":{\"Type\":\"Pass\",\"End\":true}}}",
        "RoleArn": "arn:aws:iam::000000000000:role/sfn-role",
        "Tags": [{"Key": "owner", "Value": "resource"}]
      }
    }
  }
}`

func TestUpdateStack_StepFunctionsStateMachine_stackTagOnlyChangeReconciles(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "sfn-stack-tag-gap-update"

	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":           {stackName},
		"TemplateBody":        {sfnStackTagReconcileTemplate},
		"Tags.member.1.Key":   {"env"},
		"Tags.member.1.Value": {"dev"},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	arn := describeStackResourceIDs(t, srv, stackName)["StateMachine"]
	if got := listSFNTags(t, srv, arn); got["env"] != "dev" {
		t.Fatalf("initial state machine tags = %#v, want env=dev", got)
	}

	updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":           {stackName},
		"TemplateBody":        {sfnStackTagReconcileTemplate},
		"Tags.member.1.Key":   {"env"},
		"Tags.member.1.Value": {"prod"},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	if got := listSFNTags(t, srv, arn); got["env"] != "prod" || got["owner"] != "resource" {
		t.Fatalf("reconciled state machine tags = %#v, want env=prod (reconciled) and owner=resource (unchanged)", got)
	}
}
