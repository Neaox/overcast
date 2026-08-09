package cloudformation_test

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// A stack whose update changes the log group, adds a subscription that cannot
// be provisioned, and carries a new template, parameter set, tag set and output
// set with it. The update fails on the subscription, so everything the request
// brought with it has to be unwound — not just the resources.
const (
	rollbackMetadataBaseTemplate = `{
  "Parameters": {
    "Stage": {"Type": "String", "Default": "one"}
  },
  "Resources": {
    "Logs": {
      "Type": "AWS::Logs::LogGroup",
      "Properties": {"LogGroupName": "%s", "RetentionInDays": 7}
    }
  },
  "Outputs": {
    "Group": {"Value": {"Ref": "Logs"}}
  }
}`
	rollbackMetadataFailingTemplate = `{
  "Parameters": {
    "Stage": {"Type": "String", "Default": "one"}
  },
  "Resources": {
    "Logs": {
      "Type": "AWS::Logs::LogGroup",
      "Properties": {"LogGroupName": "%s", "RetentionInDays": 14}
    },
    "SubB": {
      "Type": "AWS::SNS::Subscription",
      "DependsOn": "Logs",
      "Properties": {
        "TopicArn": "arn:aws:sns:us-east-1:000000000000:nonexistent-topic-xyzzy",
        "Protocol": "sqs",
        "Endpoint": "arn:aws:sqs:us-east-1:000000000000:nonexistent-queue"
      }
    }
  },
  "Outputs": {
    "Group": {"Value": {"Ref": "Logs"}},
    "Added": {"Value": "added-by-the-failed-update"}
  }
}`
)

// UpdateStack persists the attempted template and parameters before
// provisioning starts, so a stack that rolls back has to be handed its previous
// generation back. Otherwise GetTemplate serves the template that failed and
// the next update resolves parameters from an attempt that was undone, while
// the resources describe the generation before it.
func TestUpdateStack_rollbackRestoresSupersededMetadata(t *testing.T) {
	// Given: a provisioned stack with a template, parameters and tags.
	srv := helpers.NewTestServer(t)
	stackName := "upd-rollback-metadata"
	logGroup := "upd-rollback-metadata-logs"
	baseTemplate := fmt.Sprintf(rollbackMetadataBaseTemplate, logGroup)

	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":                          {stackName},
		"TemplateBody":                       {baseTemplate},
		"Parameters.member.1.ParameterKey":   {"Stage"},
		"Parameters.member.1.ParameterValue": {"one"},
		"Tags.member.1.Key":                  {"stage"},
		"Tags.member.1.Value":                {"one"},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	before := describeStackMetadata(t, srv, stackName)

	// When: an update carrying a new template, parameters and tags fails.
	updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":                          {stackName},
		"TemplateBody":                       {fmt.Sprintf(rollbackMetadataFailingTemplate, logGroup)},
		"Parameters.member.1.ParameterKey":   {"Stage"},
		"Parameters.member.1.ParameterValue": {"two"},
		"Tags.member.1.Key":                  {"stage"},
		"Tags.member.1.Value":                {"two"},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_ROLLBACK_COMPLETE")

	// Then: every AWS-observable description of the stack is the generation the
	// resources were rolled back to.
	assertRolledBackToBaseGeneration(t, srv, stackName, baseTemplate, before)
}

// assertRolledBackToBaseGeneration checks the whole observable generation —
// template, parameters, tags, outputs and resources — rather than one field, so
// that a restoration that puts back only part of it is still a failure.
func assertRolledBackToBaseGeneration(t *testing.T, srv *helpers.TestServer, stackName, baseTemplate string, before stackMetadataRecord) {
	t.Helper()
	if got := describeStackStatus(t, srv, stackName); got != "UPDATE_ROLLBACK_COMPLETE" {
		t.Fatalf("stack status = %q, want UPDATE_ROLLBACK_COMPLETE", got)
	}
	if got := getStackTemplate(t, srv, stackName); got != baseTemplate {
		t.Errorf("GetTemplate returned the failed update's template:\n got: %s\nwant: %s", got, baseTemplate)
	}
	after := describeStackMetadata(t, srv, stackName)
	if got := after.Parameters["Stage"]; got != before.Parameters["Stage"] {
		t.Errorf("parameter Stage = %q, want %q", got, before.Parameters["Stage"])
	}
	if got := after.Tags["stage"]; got != before.Tags["stage"] {
		t.Errorf("tag stage = %q, want %q", got, before.Tags["stage"])
	}
	if got := after.Outputs["Group"]; got != before.Outputs["Group"] {
		t.Errorf("output Group = %q, want %q", got, before.Outputs["Group"])
	}
	if _, ok := after.Outputs["Added"]; ok {
		t.Errorf("output Added survived the rollback: %+v", after.Outputs)
	}
	resources := describeStackResources(t, srv, stackName)
	if _, ok := resources["SubB"]; ok {
		t.Errorf("SubB survived the rollback: %+v", resources)
	}
	if _, ok := resources["Logs"]; !ok {
		t.Errorf("Logs missing after the rollback: %+v", resources)
	}
}

// ---- Test helpers ----------------------------------------------------------

type stackMetadataRecord struct {
	Parameters map[string]string
	Tags       map[string]string
	Outputs    map[string]string
}

func describeStackMetadata(t *testing.T, srv *helpers.TestServer, stackName string) stackMetadataRecord {
	t.Helper()
	resp := cfnQuery(t, srv, "DescribeStacks", url.Values{"StackName": {stackName}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := readBody(t, resp)
	var result struct {
		Parameters []struct {
			Key   string `xml:"ParameterKey"`
			Value string `xml:"ParameterValue"`
		} `xml:"DescribeStacksResult>Stacks>member>Parameters>member"`
		Tags []struct {
			Key   string `xml:"Key"`
			Value string `xml:"Value"`
		} `xml:"DescribeStacksResult>Stacks>member>Tags>member"`
		Outputs []struct {
			Key   string `xml:"OutputKey"`
			Value string `xml:"OutputValue"`
		} `xml:"DescribeStacksResult>Stacks>member>Outputs>member"`
	}
	if err := xml.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal DescribeStacksResponse: %v\nbody: %s", err, body)
	}
	record := stackMetadataRecord{
		Parameters: make(map[string]string, len(result.Parameters)),
		Tags:       make(map[string]string, len(result.Tags)),
		Outputs:    make(map[string]string, len(result.Outputs)),
	}
	for _, p := range result.Parameters {
		record.Parameters[p.Key] = p.Value
	}
	for _, tag := range result.Tags {
		record.Tags[tag.Key] = tag.Value
	}
	for _, out := range result.Outputs {
		record.Outputs[out.Key] = out.Value
	}
	return record
}

func getStackTemplate(t *testing.T, srv *helpers.TestServer, stackName string) string {
	t.Helper()
	resp := cfnQuery(t, srv, "GetTemplate", url.Values{"StackName": {stackName}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := readBody(t, resp)
	var result struct {
		TemplateBody string `xml:"GetTemplateResult>TemplateBody"`
	}
	if err := xml.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal GetTemplateResponse: %v\nbody: %s", err, body)
	}
	return result.TemplateBody
}
