package cloudformation_test

// fidelity_test.go — issue #760: a stack can reach CREATE_COMPLETE while some
// of its resources are stubs or backed by an inert-tier service, and nothing
// told the user. This exercises the real deploy path a `cdk deploy` or the
// CLI actually sees: DescribeStackResources and DescribeStackEvents, both
// AWS-shaped responses whose ResourceStatusReason now carries the signal.

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

const fidelityTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "AppRole": {
      "Type": "AWS::IAM::Role",
      "Properties": {
        "AssumeRolePolicyDocument": {
          "Version": "2012-10-17",
          "Statement": [{"Effect": "Allow", "Principal": {"Service": "lambda.amazonaws.com"}, "Action": "sts:AssumeRole"}]
        }
      }
    },
    "CDKMetadata": {
      "Type": "AWS::CDK::Metadata",
      "Properties": {"Analytics": "v2:deflate64:test"}
    },
    "Queue": {
      "Type": "AWS::SQS::Queue",
      "Properties": {"QueueName": "fidelity-test-queue"}
    }
  }
}`

// TestCreateStack_fidelityReasonsOnDeployPath is the failing-test-first case
// for #760: a deploy containing a stub resource (AWS::CDK::Metadata) and an
// inert-tier real resource (AWS::IAM::Role — see internal/router.ServiceTiers)
// reaches CREATE_COMPLETE the way real CDK sees it, and both
// DescribeStackResources and DescribeStackEvents — the actual output `cdk
// deploy` polls and renders — say so on the resource they apply to. The fully
// real AWS::SQS::Queue gets no such reason at all.
func TestCreateStack_fidelityReasonsOnDeployPath(t *testing.T) {
	srv := helpers.NewTestServer(t)

	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"fidelity-stack"},
		"TemplateBody": []string{fidelityTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "fidelity-stack", "CREATE_COMPLETE")

	// DescribeStackResources: the resource-level record.
	resourcesResp := cfnQuery(t, srv, "DescribeStackResources", url.Values{
		"StackName": []string{"fidelity-stack"},
	})
	defer resourcesResp.Body.Close()
	helpers.AssertStatus(t, resourcesResp, http.StatusOK)
	resourcesBody := string(readBody(t, resourcesResp))

	if !strings.Contains(resourcesBody, "Overcast: created, but iam is emulated at inert tier") {
		t.Errorf("expected the IAM role's ResourceStatusReason to explain the inert tier, got: %s", resourcesBody)
	}
	if !strings.Contains(resourcesBody, "Overcast: AWS::CDK::Metadata is accepted as a no-op") {
		t.Errorf("expected the CDK metadata stub's ResourceStatusReason to say it is a no-op, got: %s", resourcesBody)
	}
	// Exactly those two resources — the SQS queue is fully backed and gets no
	// fidelity notice at all, which is the negative case the issue calls for.
	if got := strings.Count(resourcesBody, "Overcast:"); got != 2 {
		t.Errorf("expected exactly 2 Overcast-authored reasons (the role and the metadata stub), got %d in: %s",
			got, resourcesBody)
	}

	// DescribeStackEvents: the same signal on the CREATE_COMPLETE event, which
	// is what `cdk deploy` actually polls and renders while a deploy runs.
	eventsResp := cfnQuery(t, srv, "DescribeStackEvents", url.Values{
		"StackName": []string{"fidelity-stack"},
	})
	defer eventsResp.Body.Close()
	helpers.AssertStatus(t, eventsResp, http.StatusOK)
	eventsBody := string(readBody(t, eventsResp))

	if !strings.Contains(eventsBody, "Overcast: created, but iam is emulated at inert tier") {
		t.Errorf("expected a stack event carrying the IAM role's fidelity reason, got: %s", eventsBody)
	}
	if !strings.Contains(eventsBody, "Overcast: AWS::CDK::Metadata is accepted as a no-op") {
		t.Errorf("expected a stack event carrying the CDK metadata stub's fidelity reason, got: %s", eventsBody)
	}
}
