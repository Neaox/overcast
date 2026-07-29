// Package cloudformation_test — stack outputs that name a host-routed AWS
// endpoint are re-hosted onto an origin the caller can reach.
//
// CDK composes an API Gateway invoke URL in the template itself:
//
//	{"Fn::Join": ["", ["https://", {"Ref": "Api"}, ".execute-api.us-east-1.",
//	                   {"Ref": "AWS::URLSuffix"}, "/", {"Ref": "Stage"}, "/"]]}
//
// The scheme is a literal and there is no port, so AWS::URLSuffix cannot be
// substituted into something dialable — see docs/plans/host-routing-precedence.md
// §8. The correction belongs where Overcast assembles the finished string: at
// output emission, the URL is parsed with the same grammar that routes inbound
// requests and re-minted through the same helper every service uses.
//
// Outputs that are not host-routed AWS endpoints must pass through untouched.
package cloudformation_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

const apiEndpointOutputTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Topic": {
      "Type": "AWS::SNS::Topic",
      "Properties": {}
    }
  },
  "Outputs": {
    "ApiUrl": {
      "Value": { "Fn::Join": ["", [
        "https://", "abc123", ".execute-api.us-east-1.",
        { "Ref": "AWS::URLSuffix" }, "/", "prod", "/"
      ]]}
    },
    "RepoUri": {
      "Value": { "Fn::Join": ["", [
        "000000000000.dkr.ecr.us-east-1.", { "Ref": "AWS::URLSuffix" }
      ]]}
    },
    "PlainValue": {
      "Value": "not-a-url-at-all"
    },
    "TopicArn": {
      "Value": { "Ref": "Topic" }
    }
  }
}`

func TestDescribeStacks_hostRoutedOutputIsRehostedOntoAReachableOrigin(t *testing.T) {
	// Given: a stack whose output composes an API Gateway invoke URL the way
	// CDK does — literal https, no port, AWS::URLSuffix for the domain
	const hostname = "localhost.overcast.sh"
	srv := helpers.NewTestServer(t, helpers.WithHostname(hostname))
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"output-rehost-stack"},
		"TemplateBody": []string{apiEndpointOutputTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "output-rehost-stack", "CREATE_COMPLETE")

	// When: the stack is described
	desc := cfnQuery(t, srv, "DescribeStacks", url.Values{
		"StackName": []string{"output-rehost-stack"},
	})
	defer desc.Body.Close()
	body := string(readBody(t, desc))

	// Then: the API Gateway URL is re-hosted onto an origin the caller can
	// reach — right scheme, right hostname, right port. AWS::URLSuffix itself
	// is untouched at the template level; the correction happens here.
	if !strings.Contains(body, "abc123.execute-api.us-east-1."+hostname) {
		t.Errorf("ApiUrl output was not re-hosted onto %q; body:\n%s", hostname, body)
	}
	if strings.Contains(body, "abc123.execute-api.us-east-1.amazonaws.com") {
		t.Errorf("ApiUrl output still points at real AWS; body:\n%s", body)
	}
	if !strings.Contains(body, "http://abc123.execute-api") {
		t.Errorf("ApiUrl scheme was not corrected to http for a non-TLS server; body:\n%s", body)
	}
	// The path must survive the re-mint.
	if !strings.Contains(body, "/prod/") {
		t.Errorf("ApiUrl lost its stage path; body:\n%s", body)
	}
}

func TestDescribeStacks_nonHostRoutedOutputsAreUntouched(t *testing.T) {
	// Given: the same stack, whose other outputs are not host-routed AWS
	// endpoints
	srv := helpers.NewTestServer(t, helpers.WithHostname("localhost.overcast.sh"))
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"output-untouched-stack"},
		"TemplateBody": []string{apiEndpointOutputTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "output-untouched-stack", "CREATE_COMPLETE")

	// When: the stack is described
	desc := cfnQuery(t, srv, "DescribeStacks", url.Values{
		"StackName": []string{"output-untouched-stack"},
	})
	defer desc.Body.Close()
	body := string(readBody(t, desc))

	// Then: an ECR registry URI is left alone. "dkr"/"ecr" are not registered
	// host-route labels, so the grammar does not claim it — Overcast does not
	// serve that hostname and must not pretend to.
	if !strings.Contains(body, "000000000000.dkr.ecr.us-east-1.amazonaws.com") {
		t.Errorf("ECR URI output was rewritten but should have been left alone; body:\n%s", body)
	}
	// ...and a plain string output is not mangled by URL parsing.
	if !strings.Contains(body, "not-a-url-at-all") {
		t.Errorf("plain output value was altered; body:\n%s", body)
	}
	// ...and an ARN passes through.
	if !strings.Contains(body, "arn:aws:sns:") {
		t.Errorf("ARN output was altered; body:\n%s", body)
	}
}
