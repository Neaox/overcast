package cloudformation_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// Issue #538: sqsQueueAttributesFromProps forwarded twelve queue attributes
// and RedrivePolicy, but silently dropped Tags and RedriveAllowPolicy — a
// queue deployed from a CDK template with either property came out on the
// other side with neither, even though the SQS service itself already
// supports both (CreateQueue/TagQueue for Tags, SetQueueAttributes for
// RedriveAllowPolicy's JSON blob).
const sqsPropertiesTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "SourceQueue": {
      "Type": "AWS::SQS::Queue",
      "Properties": {"QueueName": "cfn-props-source"}
    },
    "DeadLetterQueue": {
      "Type": "AWS::SQS::Queue",
      "Properties": {
        "QueueName": "cfn-props-dlq",
        "RedriveAllowPolicy": {
          "redrivePermission": "byQueue",
          "sourceQueueArns": [{"Fn::GetAtt": ["SourceQueue", "Arn"]}]
        },
        "Tags": [
          {"Key": "team", "Value": "platform"},
          {"Key": "tier", "Value": "dlq"}
        ]
      }
    }
  },
  "Outputs": {
    "DeadLetterQueueUrl": {"Value": {"Ref": "DeadLetterQueue"}}
  }
}`

func TestCreateStack_SQSQueueTagsAndRedriveAllowPolicy(t *testing.T) {
	// Given: a CDK-shaped stack whose DLQ carries both Tags and a
	// RedriveAllowPolicy restricting which source queue may target it.
	srv := helpers.NewTestServer(t)

	// When: the stack is deployed.
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"sqs-props-stack"},
		"TemplateBody": {sqsPropertiesTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "sqs-props-stack", "CREATE_COMPLETE")

	queueURL := srv.Config.ExternalBaseURL() + "/000000000000/cfn-props-dlq"

	// Then: GetQueueAttributes reports the RedriveAllowPolicy CDK sent, not
	// silence.
	attrsResp := sqsJSONCall(t, srv, "GetQueueAttributes", map[string]any{
		"QueueUrl":       queueURL,
		"AttributeNames": []string{"All"},
	})
	defer attrsResp.Body.Close()
	helpers.AssertStatus(t, attrsResp, http.StatusOK)
	var attrsResult struct {
		Attributes map[string]string `json:"Attributes"`
	}
	helpers.DecodeJSON(t, attrsResp, &attrsResult)
	rawPolicy, ok := attrsResult.Attributes["RedriveAllowPolicy"]
	if !ok {
		t.Fatalf("expected RedriveAllowPolicy attribute, got %#v", attrsResult.Attributes)
	}
	var policy struct {
		RedrivePermission string   `json:"redrivePermission"`
		SourceQueueArns   []string `json:"sourceQueueArns"`
	}
	if err := json.Unmarshal([]byte(rawPolicy), &policy); err != nil {
		t.Fatalf("RedriveAllowPolicy is not valid JSON: %v (%s)", err, rawPolicy)
	}
	if policy.RedrivePermission != "byQueue" {
		t.Errorf("RedriveAllowPolicy.redrivePermission = %q, want byQueue", policy.RedrivePermission)
	}
	if len(policy.SourceQueueArns) != 1 {
		t.Fatalf("RedriveAllowPolicy.sourceQueueArns = %#v, want exactly one entry", policy.SourceQueueArns)
	}

	// And: ListQueueTags reports the Tags CDK sent, not silence.
	tags := listQueueTags(t, srv, queueURL)
	want := map[string]string{"team": "platform", "tier": "dlq"}
	for k, v := range want {
		if tags[k] != v {
			t.Errorf("queue tags[%q] = %q, want %q (full: %#v)", k, tags[k], v, tags)
		}
	}
}

// The Definition of Done in #538 calls for Tags to be reconciled — added,
// changed, and removed — on Update, not just forwarded on Create.
func TestUpdateStack_SQSQueueTagsReconciled(t *testing.T) {
	// Given: a queue created with two tags.
	srv := helpers.NewTestServer(t)
	template := func(tags string) string {
		return `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Queue": {
      "Type": "AWS::SQS::Queue",
      "Properties": {
        "QueueName": "cfn-props-reconcile-queue",
        "Tags": ` + tags + `
      }
    }
  }
}`
	}
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"sqs-tags-reconcile-stack"},
		"TemplateBody": {template(`[{"Key": "keep", "Value": "same"}, {"Key": "drop", "Value": "gone-after-update"}]`)},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "sqs-tags-reconcile-stack", "CREATE_COMPLETE")

	queueURL := srv.Config.ExternalBaseURL() + "/000000000000/cfn-props-reconcile-queue"
	before := listQueueTags(t, srv, queueURL)
	if before["keep"] != "same" || before["drop"] != "gone-after-update" {
		t.Fatalf("tags after create = %#v, want keep=same and drop=gone-after-update", before)
	}

	// When: the template drops "drop", keeps "keep" unchanged, and adds "add".
	ur := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {"sqs-tags-reconcile-stack"},
		"TemplateBody": {template(`[{"Key": "keep", "Value": "same"}, {"Key": "add", "Value": "new-after-update"}]`)},
	})
	defer ur.Body.Close()
	helpers.AssertStatus(t, ur, http.StatusOK)
	waitForStackStatus(t, srv, "sqs-tags-reconcile-stack", "UPDATE_COMPLETE")

	// Then: ListQueueTags reflects exactly the new set — added, kept, removed.
	after := listQueueTags(t, srv, queueURL)
	if after["keep"] != "same" {
		t.Errorf("tags[keep] = %q, want same (unchanged key must survive)", after["keep"])
	}
	if after["add"] != "new-after-update" {
		t.Errorf("tags[add] = %q, want new-after-update", after["add"])
	}
	if _, stillPresent := after["drop"]; stillPresent {
		t.Errorf("tags still contain %q after it was removed from the template: %#v", "drop", after)
	}
}

func listQueueTags(t *testing.T, srv *helpers.TestServer, queueURL string) map[string]string {
	t.Helper()
	resp := sqsJSONCall(t, srv, "ListQueueTags", map[string]any{"QueueUrl": queueURL})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Tags map[string]string `json:"Tags"`
	}
	helpers.DecodeJSON(t, resp, &result)
	return result.Tags
}
