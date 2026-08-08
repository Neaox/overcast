package cloudformation_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

const lambdaESMTagsTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Queue": {"Type": "AWS::SQS::Queue", "Properties": {"QueueName": "cfn-esm-tags-queue"}},
    "Function": {"Type": "AWS::Lambda::Function", "Properties": {
      "FunctionName": "cfn-esm-tags-fn", "Runtime": "python3.11", "Handler": "index.handler",
      "Role": "arn:aws:iam::000000000000:role/lambda-role", "Code": {"ZipFile": "def handler(e, c): return {}"}
    }},
    "Mapping": {"Type": "AWS::Lambda::EventSourceMapping", "Properties": {
      "EventSourceArn": {"Fn::GetAtt": ["Queue", "Arn"]}, "FunctionName": {"Ref": "Function"}, "BatchSize": 5
    }}
  },
  "Outputs": {
    "MappingArn": {"Value": {"Fn::GetAtt": ["Mapping", "EventSourceMappingArn"]}}
  }
}`

// An ordinary SQS→Lambda stack deployed with stack-level tags — `cdk deploy
// --tags team=platform`, or any `Tags.of(app).add(...)` — puts the merged tag
// set on every taggable resource, event source mappings included. The template
// needs no unusual property for this to happen, so a mapping that refuses Tags
// makes the whole class of tagged deploys fail at CREATE.
func TestCreateStack_LambdaEventSourceMappingCarriesStackTags(t *testing.T) {
	// Given: a stack whose only unusual feature is a stack-level tag.
	srv := helpers.NewTestServer(t)

	// When: the stack is created with `--tags team=platform`.
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":           {"lambda-esm-stack-tags"},
		"TemplateBody":        {lambdaESMTagsTemplate},
		"Tags.member.1.Key":   {"team"},
		"Tags.member.1.Value": {"platform"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the stack completes rather than failing on a 501 from
	// CreateEventSourceMapping…
	waitForStackStatus(t, srv, "lambda-esm-stack-tags", "CREATE_COMPLETE")

	// …and the mapping carries the stack tag.
	mapping := listSingleEventSourceMapping(t, srv)
	if mapping.EventSourceMappingArn == "" {
		t.Fatalf("event source mapping has no ARN: %+v", mapping)
	}
	tags := listLambdaResourceTags(t, srv, mapping.EventSourceMappingArn)
	if tags["team"] != "platform" {
		t.Errorf("event source mapping tags = %v, want team=platform", tags)
	}
}

func listLambdaResourceTags(t *testing.T, srv *helpers.TestServer, resourceARN string) map[string]string {
	t.Helper()
	resp := lambdaRequest(t, srv, http.MethodGet, "/2017-03-31/tags/"+url.PathEscape(resourceARN), nil)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var list struct {
		Tags map[string]string `json:"Tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatalf("decode ListTags: %v", err)
	}
	return list.Tags
}
