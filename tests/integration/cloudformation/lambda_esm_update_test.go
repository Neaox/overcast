package cloudformation_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

const lambdaESMFunctionOneTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Queue": {"Type": "AWS::SQS::Queue", "Properties": {"QueueName": "cfn-esm-update-queue"}},
    "FunctionOne": {"Type": "AWS::Lambda::Function", "Properties": {
      "FunctionName": "cfn-esm-update-one", "Runtime": "python3.11", "Handler": "index.handler",
      "Role": "arn:aws:iam::000000000000:role/lambda-role", "Code": {"ZipFile": "def handler(e, c): return {}"}
    }},
    "FunctionTwo": {"Type": "AWS::Lambda::Function", "Properties": {
      "FunctionName": "cfn-esm-update-two", "Runtime": "python3.11", "Handler": "index.handler",
      "Role": "arn:aws:iam::000000000000:role/lambda-role", "Code": {"ZipFile": "def handler(e, c): return {}"}
    }},
    "Mapping": {"Type": "AWS::Lambda::EventSourceMapping", "Properties": {
      "EventSourceArn": {"Fn::GetAtt": ["Queue", "Arn"]}, "FunctionName": {"Ref": "FunctionOne"}, "BatchSize": 5
    }}
  },
  "Outputs": {
    "MappingId": {"Value": {"Fn::GetAtt": ["Mapping", "Id"]}},
    "MappingArn": {"Value": {"Fn::GetAtt": ["Mapping", "EventSourceMappingArn"]}}
  }
}`

var lambdaESMFunctionTwoTemplate = strings.Replace(lambdaESMFunctionOneTemplate,
	`"FunctionName": {"Ref": "FunctionOne"}, "BatchSize": 5`,
	`"FunctionName": {"Ref": "FunctionTwo"}, "BatchSize": 5`, 1)

func TestUpdateStack_LambdaEventSourceMappingFunctionName(t *testing.T) {
	// Given: a stack whose event source mapping targets the first function.
	srv := helpers.NewTestServer(t)
	createLambdaStack(t, srv, "lambda-esm-function-update", lambdaESMFunctionOneTemplate)
	before := listSingleEventSourceMapping(t, srv)

	// When: the mapping's FunctionName changes to the second function.
	resp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    []string{"lambda-esm-function-update"},
		"TemplateBody": []string{lambdaESMFunctionTwoTemplate},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, "lambda-esm-function-update", "UPDATE_COMPLETE")
	after := listSingleEventSourceMapping(t, srv)

	// Then: Lambda updates the existing mapping in place and CloudFormation exposes only documented attributes.
	if after.UUID != before.UUID {
		t.Errorf("mapping UUID changed from %q to %q", before.UUID, after.UUID)
	}
	if !strings.HasSuffix(after.FunctionArn, ":function:cfn-esm-update-two") {
		t.Errorf("FunctionArn = %q, want second function", after.FunctionArn)
	}
	describe := cfnQuery(t, srv, "DescribeStacks", url.Values{"StackName": []string{"lambda-esm-function-update"}})
	defer describe.Body.Close()
	body := string(readBody(t, describe))
	if !strings.Contains(body, before.UUID) || !strings.Contains(body, before.EventSourceMappingArn) {
		t.Errorf("DescribeStacks outputs do not contain mapping Id and ARN: %s", body)
	}
}

type listedEventSourceMapping struct {
	UUID                  string `json:"UUID"`
	EventSourceMappingArn string `json:"EventSourceMappingArn"`
	FunctionArn           string `json:"FunctionArn"`
}

func listSingleEventSourceMapping(t *testing.T, srv *helpers.TestServer) listedEventSourceMapping {
	t.Helper()
	resp, err := http.Get(srv.URL + "/2015-03-31/event-source-mappings/")
	if err != nil {
		t.Fatalf("ListEventSourceMappings: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Mappings []listedEventSourceMapping `json:"EventSourceMappings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode ListEventSourceMappings: %v", err)
	}
	if len(result.Mappings) != 1 {
		t.Fatalf("event source mappings = %d, want 1", len(result.Mappings))
	}
	return result.Mappings[0]
}
