package cloudformation_test

import (
	"encoding/xml"
	"net/url"
	"slices"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func TestUpdateStack_unchangedResourceKeepsCreateStatus(t *testing.T) {
	// Given: a stack with one resource that will change and one that will not
	srv := helpers.NewTestServer(t)
	stackName := "upd-unchanged-resource"
	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName": {stackName},
		"TemplateBody": {`{
  "Resources": {
    "UnchangedQueue": {
      "Type": "AWS::SQS::Queue",
      "Properties": {"QueueName": "upd-unchanged-resource-static"}
    },
    "ChangedQueue": {
      "Type": "AWS::SQS::Queue",
      "Properties": {"QueueName": "upd-unchanged-resource-v1"}
    }
  }
}`},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, 200)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	// When: the stack is updated with only one resource changed
	updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName": {stackName},
		"TemplateBody": {`{
  "Resources": {
    "UnchangedQueue": {
      "Type": "AWS::SQS::Queue",
      "Properties": {"QueueName": "upd-unchanged-resource-static"}
    },
    "ChangedQueue": {
      "Type": "AWS::SQS::Queue",
      "Properties": {"QueueName": "upd-unchanged-resource-v2"}
    }
  }
}`},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, 200)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	// Then: the unchanged resource remains in its previous CREATE_COMPLETE state
	resources := describeStackResourceStatuses(t, srv, stackName)
	if got := resources["UnchangedQueue"]; got != "CREATE_COMPLETE" {
		t.Fatalf("unchanged resource status: got %q, want CREATE_COMPLETE", got)
	}
	if got := resources["ChangedQueue"]; got != "UPDATE_COMPLETE" {
		t.Fatalf("changed resource status: got %q, want UPDATE_COMPLETE", got)
	}

	// And: update events are emitted only for the resource CloudFormation updated
	events := describeStackEventsByLogicalID(t, srv, stackName)
	if slices.Contains(events["UnchangedQueue"], "UPDATE_IN_PROGRESS") || slices.Contains(events["UnchangedQueue"], "UPDATE_COMPLETE") {
		t.Fatalf("unchanged resource had update events: %v", events["UnchangedQueue"])
	}
	if !slices.Contains(events["ChangedQueue"], "UPDATE_IN_PROGRESS") || !slices.Contains(events["ChangedQueue"], "UPDATE_COMPLETE") {
		t.Fatalf("changed resource missing update events: %v", events["ChangedQueue"])
	}
}

func TestUpdateStack_IAMPolicy_inPlaceUpdate(t *testing.T) {
	srv := helpers.NewTestServer(t)

	stackName := "upd-iam-policy"
	params := url.Values{
		"Action":    {"CreateStack"},
		"StackName": {stackName},
		"Version":   {"2010-05-15"},
		"TemplateBody": {`{
  "Resources": {
    "TestRole": {
      "Type": "AWS::IAM::Role",
      "Properties": {
        "AssumeRolePolicyDocument": {"Version": "2012-10-17", "Statement": [{"Effect": "Allow", "Principal": {"Service": "lambda.amazonaws.com"}, "Action": "sts:AssumeRole"}]}
      }
    },
    "TestPolicy": {
      "Type": "AWS::IAM::Policy",
      "Properties": {
        "PolicyName": "tpol",
        "PolicyDocument": {"Version": "2012-10-17", "Statement": [{"Effect": "Allow", "Action": "s3:ListBucket", "Resource": "*"}]},
        "Roles": [{"Ref": "TestRole"}]
      }
    }
  }
}`},
	}
	resp := cfnQuery(t, srv, "CreateStack", params)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, 200)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	params2 := url.Values{
		"Action":    {"UpdateStack"},
		"StackName": {stackName},
		"Version":   {"2010-05-15"},
		"TemplateBody": {`{
  "Resources": {
    "TestRole": {
      "Type": "AWS::IAM::Role",
      "Properties": {
        "AssumeRolePolicyDocument": {"Version": "2012-10-17", "Statement": [{"Effect": "Allow", "Principal": {"Service": "lambda.amazonaws.com"}, "Action": "sts:AssumeRole"}]}
      }
    },
    "TestPolicy": {
      "Type": "AWS::IAM::Policy",
      "Properties": {
        "PolicyName": "tpol",
        "PolicyDocument": {"Version": "2012-10-17", "Statement": [{"Effect": "Allow", "Action": "s3:GetObject", "Resource": "*"}]},
        "Roles": [{"Ref": "TestRole"}]
      }
    }
  }
}`},
	}
	resp2 := cfnQuery(t, srv, "UpdateStack", params2)
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, 200)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")
}

func TestUpdateStack_IAMPolicy_replacementOnPolicyNameChange(t *testing.T) {
	srv := helpers.NewTestServer(t)

	stackName := "upd-polreplace"
	params := url.Values{
		"Action":    {"CreateStack"},
		"StackName": {stackName},
		"Version":   {"2010-05-15"},
		"TemplateBody": {`{
  "Resources": {
    "TestRole": {
      "Type": "AWS::IAM::Role",
      "Properties": {
        "AssumeRolePolicyDocument": {"Version": "2012-10-17", "Statement": [{"Effect": "Allow", "Principal": {"Service": "lambda.amazonaws.com"}, "Action": "sts:AssumeRole"}]}
      }
    },
    "TestPolicy": {
      "Type": "AWS::IAM::Policy",
      "Properties": {
        "PolicyName": "tpolv1",
        "PolicyDocument": {"Version": "2012-10-17", "Statement": [{"Effect": "Allow", "Action": "s3:ListBucket", "Resource": "*"}]},
        "Roles": [{"Ref": "TestRole"}]
      }
    }
  }
}`},
	}
	resp := cfnQuery(t, srv, "CreateStack", params)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, 200)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	params2 := url.Values{
		"Action":    {"UpdateStack"},
		"StackName": {stackName},
		"Version":   {"2010-05-15"},
		"TemplateBody": {`{
  "Resources": {
    "TestRole": {
      "Type": "AWS::IAM::Role",
      "Properties": {
        "AssumeRolePolicyDocument": {"Version": "2012-10-17", "Statement": [{"Effect": "Allow", "Principal": {"Service": "lambda.amazonaws.com"}, "Action": "sts:AssumeRole"}]}
      }
    },
    "TestPolicy": {
      "Type": "AWS::IAM::Policy",
      "Properties": {
        "PolicyName": "tpolv2",
        "PolicyDocument": {"Version": "2012-10-17", "Statement": [{"Effect": "Allow", "Action": "s3:ListBucket", "Resource": "*"}]},
        "Roles": [{"Ref": "TestRole"}]
      }
    }
  }
}`},
	}
	resp2 := cfnQuery(t, srv, "UpdateStack", params2)
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, 200)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")
}

func describeStackResourceStatuses(t *testing.T, srv *helpers.TestServer, stackName string) map[string]string {
	t.Helper()
	resp := cfnQuery(t, srv, "DescribeStackResources", url.Values{"StackName": {stackName}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, 200)
	body := readBody(t, resp)
	var result struct {
		Resources []struct {
			LogicalID string `xml:"LogicalResourceId"`
			Status    string `xml:"ResourceStatus"`
		} `xml:"DescribeStackResourcesResult>StackResources>member"`
	}
	if err := xml.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal DescribeStackResourcesResponse: %v\nbody: %s", err, body)
	}
	statuses := make(map[string]string, len(result.Resources))
	for _, res := range result.Resources {
		statuses[res.LogicalID] = res.Status
	}
	return statuses
}

func describeStackEventsByLogicalID(t *testing.T, srv *helpers.TestServer, stackName string) map[string][]string {
	t.Helper()
	resp := cfnQuery(t, srv, "DescribeStackEvents", url.Values{"StackName": {stackName}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, 200)
	body := readBody(t, resp)
	var result struct {
		Events []struct {
			LogicalID string `xml:"LogicalResourceId"`
			Status    string `xml:"ResourceStatus"`
		} `xml:"DescribeStackEventsResult>StackEvents>member"`
	}
	if err := xml.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal DescribeStackEventsResponse: %v\nbody: %s", err, body)
	}
	events := make(map[string][]string)
	for _, event := range result.Events {
		events[event.LogicalID] = append(events[event.LogicalID], event.Status)
	}
	return events
}
