package cloudformation_test

import (
	"net/url"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

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
