package cloudformation_test

// cfn_tags_1197_test.go covers issue #1197: CloudFormation's Tags property
// used to be silently dropped for AWS::CloudTrail::Trail, AWS::Transfer::Server,
// AWS::Transfer::User, AWS::IAM::ManagedPolicy and AWS::IAM::InstanceProfile —
// the five of the issue's eight resource types that were not already fixed
// (AWS::Kinesis::Stream, AWS::IAM::Role and AWS::IAM::User already thread
// Tags; see kinesis_properties_test.go and iam_properties_test.go).
//
// Each resource type gets a create test (tags stick from the moment the
// stack completes, read back through the service's own tag-list operation)
// and an update test (a tags-only template change reconciles in place
// without replacing the resource).

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// ── CloudTrail::Trail ────────────────────────────────────────────────────────

const cloudTrailTargetPrefix1197 = "com.amazonaws.cloudtrail.v20131101.CloudTrail_20131101."

func ctCall1197(t *testing.T, srv *helpers.TestServer, action string, body any) *http.Response {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s body: %v", action, err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new request %s: %v", action, err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", cloudTrailTargetPrefix1197+action)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request %s: %v", action, err)
	}
	return resp
}

func ctListTags1197(t *testing.T, srv *helpers.TestServer, arn string) map[string]string {
	t.Helper()
	resp := ctCall1197(t, srv, "ListTags", map[string]any{"ResourceIdList": []string{arn}})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ListTags status: got %d want 200", resp.StatusCode)
	}
	var out struct {
		ResourceTagList []struct {
			TagsList []struct {
				Key   string `json:"Key"`
				Value string `json:"Value"`
			} `json:"TagsList"`
		} `json:"ResourceTagList"`
	}
	helpers.DecodeJSON(t, resp, &out)
	if len(out.ResourceTagList) != 1 {
		t.Fatalf("ListTags: expected 1 resource entry, got %d", len(out.ResourceTagList))
	}
	got := make(map[string]string, len(out.ResourceTagList[0].TagsList))
	for _, tag := range out.ResourceTagList[0].TagsList {
		got[tag.Key] = tag.Value
	}
	return got
}

func TestCreateStack_CloudTrailTrail_TagsAppliedAtCreate(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const template = `{
  "Resources": {
    "Trail": {
      "Type": "AWS::CloudTrail::Trail",
      "Properties": {
        "Name": "cfn-tagged-trail",
        "S3BucketName": "cfn-tagged-trail-bucket",
        "Tags": [{"Key": "env", "Value": "prod"}]
      }
    }
  }
}`
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"cloudtrail-tags-create-stack"},
		"TemplateBody": {template},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, "cloudtrail-tags-create-stack", "CREATE_COMPLETE")

	arn := "arn:aws:cloudtrail:us-east-1:000000000000:trail/cfn-tagged-trail"
	if got := ctListTags1197(t, srv, arn); !reflect.DeepEqual(got, map[string]string{"env": "prod"}) {
		t.Fatalf("trail tags = %#v", got)
	}
}

func TestUpdateStack_CloudTrailTrailTags_reconciledInPlace(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "cloudtrail-tags-update-stack"
	const initialTemplate = `{
  "Resources": {
    "Trail": {
      "Type": "AWS::CloudTrail::Trail",
      "Properties": {
        "Name": "update-tags-trail",
        "S3BucketName": "update-tags-trail-bucket",
        "Tags": [{"Key": "env", "Value": "dev"}, {"Key": "owner", "Value": "platform"}]
      }
    }
  }
}`
	const updatedTemplate = `{
  "Resources": {
    "Trail": {
      "Type": "AWS::CloudTrail::Trail",
      "Properties": {
        "Name": "update-tags-trail",
        "S3BucketName": "update-tags-trail-bucket",
        "Tags": [{"Key": "env", "Value": "prod"}, {"Key": "project", "Value": "overcast"}]
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

	arn := "arn:aws:cloudtrail:us-east-1:000000000000:trail/update-tags-trail"
	if got := ctListTags1197(t, srv, arn); !reflect.DeepEqual(got, map[string]string{"env": "dev", "owner": "platform"}) {
		t.Fatalf("initial tags = %#v", got)
	}

	updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {updatedTemplate},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	// Not replaced: the trail is still describable under the same name.
	describeResp := ctCall1197(t, srv, "DescribeTrails", map[string]any{"trailNameList": []string{"update-tags-trail"}})
	defer describeResp.Body.Close()
	helpers.AssertStatus(t, describeResp, http.StatusOK)

	if got := ctListTags1197(t, srv, arn); !reflect.DeepEqual(got, map[string]string{"env": "prod", "project": "overcast"}) {
		t.Fatalf("updated tags = %#v", got)
	}
}

// ── Transfer::Server / Transfer::User ───────────────────────────────────────

const transferTargetPrefix1197 = "TransferService."

func transferCall1197(t *testing.T, srv *helpers.TestServer, action string, body any) *http.Response {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s body: %v", action, err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new request %s: %v", action, err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", transferTargetPrefix1197+action)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request %s: %v", action, err)
	}
	return resp
}

func transferListTags1197(t *testing.T, srv *helpers.TestServer, arn string) map[string]string {
	t.Helper()
	resp := transferCall1197(t, srv, "ListTagsForResource", map[string]any{"Arn": arn})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ListTagsForResource status: got %d want 200", resp.StatusCode)
	}
	var out struct {
		Tags []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"Tags"`
	}
	helpers.DecodeJSON(t, resp, &out)
	got := make(map[string]string, len(out.Tags))
	for _, tag := range out.Tags {
		got[tag.Key] = tag.Value
	}
	return got
}

func TestCreateStack_TransferServer_TagsAppliedAtCreate(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const template = `{
  "Resources": {
    "Server": {
      "Type": "AWS::Transfer::Server",
      "Properties": {
        "Tags": [{"Key": "env", "Value": "prod"}]
      }
    }
  },
  "Outputs": {"ServerId": {"Value": {"Fn::GetAtt": ["Server", "ServerId"]}}}
}`
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"transfer-server-tags-create-stack"},
		"TemplateBody": {template},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, "transfer-server-tags-create-stack", "CREATE_COMPLETE")

	serverID := stackOutput(t, srv, "transfer-server-tags-create-stack", "ServerId")
	arn := "arn:aws:transfer:us-east-1:000000000000:server/" + serverID
	if got := transferListTags1197(t, srv, arn); !reflect.DeepEqual(got, map[string]string{"env": "prod"}) {
		t.Fatalf("server tags = %#v", got)
	}
}

// A stack update that only changes Transfer::Server's Tags must reconcile in
// place via TagResource/UntagResource rather than forcing a replacement —
// tags never force replacement on real AWS, unlike EndpointType/
// IdentityProviderType/Protocols changes on this emulator, which still do.
func TestUpdateStack_TransferServerTags_reconciledInPlace(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "transfer-server-tags-update-stack"
	const initialTemplate = `{
  "Resources": {
    "Server": {
      "Type": "AWS::Transfer::Server",
      "Properties": {
        "Tags": [{"Key": "env", "Value": "dev"}, {"Key": "owner", "Value": "platform"}]
      }
    }
  },
  "Outputs": {"ServerId": {"Value": {"Fn::GetAtt": ["Server", "ServerId"]}}}
}`
	const updatedTemplate = `{
  "Resources": {
    "Server": {
      "Type": "AWS::Transfer::Server",
      "Properties": {
        "Tags": [{"Key": "env", "Value": "prod"}, {"Key": "project", "Value": "overcast"}]
      }
    }
  },
  "Outputs": {"ServerId": {"Value": {"Fn::GetAtt": ["Server", "ServerId"]}}}
}`
	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {initialTemplate},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	serverID := stackOutput(t, srv, stackName, "ServerId")
	arn := "arn:aws:transfer:us-east-1:000000000000:server/" + serverID
	if got := transferListTags1197(t, srv, arn); !reflect.DeepEqual(got, map[string]string{"env": "dev", "owner": "platform"}) {
		t.Fatalf("initial tags = %#v", got)
	}

	updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {updatedTemplate},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	// Not replaced: the same ServerId is still describable.
	afterID := stackOutput(t, srv, stackName, "ServerId")
	if afterID != serverID {
		t.Fatalf("server was replaced: before=%s after=%s", serverID, afterID)
	}
	if got := transferListTags1197(t, srv, arn); !reflect.DeepEqual(got, map[string]string{"env": "prod", "project": "overcast"}) {
		t.Fatalf("updated tags = %#v", got)
	}
}

func TestCreateStack_TransferUser_TagsAppliedAtCreate(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const template = `{
  "Resources": {
    "Server": {
      "Type": "AWS::Transfer::Server"
    },
    "User": {
      "Type": "AWS::Transfer::User",
      "Properties": {
        "ServerId": {"Fn::GetAtt": ["Server", "ServerId"]},
        "UserName": "cfn-tagged-user",
        "Role": "arn:aws:iam::000000000000:role/transfer-access",
        "Tags": [{"Key": "env", "Value": "prod"}]
      }
    }
  },
  "Outputs": {"ServerId": {"Value": {"Fn::GetAtt": ["Server", "ServerId"]}}}
}`
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"transfer-user-tags-create-stack"},
		"TemplateBody": {template},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, "transfer-user-tags-create-stack", "CREATE_COMPLETE")

	serverID := stackOutput(t, srv, "transfer-user-tags-create-stack", "ServerId")
	arn := "arn:aws:transfer:us-east-1:000000000000:user/" + serverID + "/cfn-tagged-user"
	if got := transferListTags1197(t, srv, arn); !reflect.DeepEqual(got, map[string]string{"env": "prod"}) {
		t.Fatalf("user tags = %#v", got)
	}
}

func TestUpdateStack_TransferUserTags_reconciledInPlace(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "transfer-user-tags-update-stack"
	const initialTemplate = `{
  "Resources": {
    "Server": {"Type": "AWS::Transfer::Server"},
    "User": {
      "Type": "AWS::Transfer::User",
      "Properties": {
        "ServerId": {"Fn::GetAtt": ["Server", "ServerId"]},
        "UserName": "update-tags-user",
        "Role": "arn:aws:iam::000000000000:role/transfer-access",
        "Tags": [{"Key": "env", "Value": "dev"}]
      }
    }
  },
  "Outputs": {"ServerId": {"Value": {"Fn::GetAtt": ["Server", "ServerId"]}}}
}`
	const updatedTemplate = `{
  "Resources": {
    "Server": {"Type": "AWS::Transfer::Server"},
    "User": {
      "Type": "AWS::Transfer::User",
      "Properties": {
        "ServerId": {"Fn::GetAtt": ["Server", "ServerId"]},
        "UserName": "update-tags-user",
        "Role": "arn:aws:iam::000000000000:role/transfer-access",
        "Tags": [{"Key": "env", "Value": "prod"}, {"Key": "team", "Value": "ops"}]
      }
    }
  },
  "Outputs": {"ServerId": {"Value": {"Fn::GetAtt": ["Server", "ServerId"]}}}
}`
	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {initialTemplate},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	serverID := stackOutput(t, srv, stackName, "ServerId")
	arn := "arn:aws:transfer:us-east-1:000000000000:user/" + serverID + "/update-tags-user"
	if got := transferListTags1197(t, srv, arn); !reflect.DeepEqual(got, map[string]string{"env": "dev"}) {
		t.Fatalf("initial tags = %#v", got)
	}

	updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {updatedTemplate},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	if got := transferListTags1197(t, srv, arn); !reflect.DeepEqual(got, map[string]string{"env": "prod", "team": "ops"}) {
		t.Fatalf("updated tags = %#v", got)
	}
}

// ── IAM::ManagedPolicy ───────────────────────────────────────────────────────

func TestCreateStack_IAMManagedPolicy_TagsAppliedAtCreate(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const template = `{
  "Resources": {
    "Policy": {
      "Type": "AWS::IAM::ManagedPolicy",
      "Properties": {
        "ManagedPolicyName": "cfn-tagged-policy",
        "PolicyDocument": {"Version": "2012-10-17", "Statement": [{"Effect": "Allow", "Action": "s3:GetObject", "Resource": "*"}]},
        "Tags": [{"Key": "env", "Value": "prod"}]
      }
    }
  }
}`
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"iam-managed-policy-tags-create-stack"},
		"TemplateBody": {template},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, "iam-managed-policy-tags-create-stack", "CREATE_COMPLETE")

	policyArn := "arn:aws:iam::000000000000:policy/cfn-tagged-policy"
	assertIAMTags(t, srv, "ListPolicyTags", "PolicyArn", policyArn, map[string]string{"env": "prod"})
}

func TestUpdateStack_IAMManagedPolicyTags_reconciledInPlace(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "iam-managed-policy-tags-update-stack"
	const initialTemplate = `{
  "Resources": {
    "Policy": {
      "Type": "AWS::IAM::ManagedPolicy",
      "Properties": {
        "ManagedPolicyName": "update-tags-policy",
        "PolicyDocument": {"Version": "2012-10-17", "Statement": [{"Effect": "Allow", "Action": "s3:GetObject", "Resource": "*"}]},
        "Tags": [{"Key": "env", "Value": "dev"}, {"Key": "owner", "Value": "platform"}]
      }
    }
  }
}`
	const updatedTemplate = `{
  "Resources": {
    "Policy": {
      "Type": "AWS::IAM::ManagedPolicy",
      "Properties": {
        "ManagedPolicyName": "update-tags-policy",
        "PolicyDocument": {"Version": "2012-10-17", "Statement": [{"Effect": "Allow", "Action": "s3:GetObject", "Resource": "*"}]},
        "Tags": [{"Key": "env", "Value": "prod"}, {"Key": "project", "Value": "overcast"}]
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

	policyArn := "arn:aws:iam::000000000000:policy/update-tags-policy"
	assertIAMTags(t, srv, "ListPolicyTags", "PolicyArn", policyArn, map[string]string{"env": "dev", "owner": "platform"})

	updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {updatedTemplate},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	assertIAMTags(t, srv, "ListPolicyTags", "PolicyArn", policyArn, map[string]string{"env": "prod", "project": "overcast"})
}

// ── IAM::InstanceProfile ─────────────────────────────────────────────────────

func TestCreateStack_IAMInstanceProfile_TagsAppliedAtCreate(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const template = `{
  "Resources": {
    "Profile": {
      "Type": "AWS::IAM::InstanceProfile",
      "Properties": {
        "InstanceProfileName": "cfn-tagged-profile",
        "Tags": [{"Key": "env", "Value": "prod"}]
      }
    }
  }
}`
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"iam-instance-profile-tags-create-stack"},
		"TemplateBody": {template},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, "iam-instance-profile-tags-create-stack", "CREATE_COMPLETE")

	assertIAMTags(t, srv, "ListInstanceProfileTags", "InstanceProfileName", "cfn-tagged-profile", map[string]string{"env": "prod"})
}

func TestUpdateStack_IAMInstanceProfileTags_reconciledInPlace(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "iam-instance-profile-tags-update-stack"
	const initialTemplate = `{
  "Resources": {
    "Profile": {
      "Type": "AWS::IAM::InstanceProfile",
      "Properties": {
        "InstanceProfileName": "update-tags-profile",
        "Tags": [{"Key": "env", "Value": "dev"}, {"Key": "owner", "Value": "platform"}]
      }
    }
  }
}`
	const updatedTemplate = `{
  "Resources": {
    "Profile": {
      "Type": "AWS::IAM::InstanceProfile",
      "Properties": {
        "InstanceProfileName": "update-tags-profile",
        "Tags": [{"Key": "env", "Value": "prod"}, {"Key": "project", "Value": "overcast"}]
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

	assertIAMTags(t, srv, "ListInstanceProfileTags", "InstanceProfileName", "update-tags-profile", map[string]string{"env": "dev", "owner": "platform"})

	updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {updatedTemplate},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	assertIAMTags(t, srv, "ListInstanceProfileTags", "InstanceProfileName", "update-tags-profile", map[string]string{"env": "prod", "project": "overcast"})
}
