package cloudformation_test

// transfer_stack_tags_test.go — #1310: AWS::Transfer::Server and
// AWS::Transfer::User join the effective-stack-tag mechanism (#1143). Server
// already forwarded its own Tags at create but forced replacement on every
// update, tags included; User forwarded no tags at all. Neither merged the
// stack's own Tags.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

const transferTargetPrefix = "TransferService."

func transferJSONCall(t *testing.T, srv *helpers.TestServer, action string, body map[string]any) *http.Response {
	t.Helper()
	return awsJSONCall(t, srv, transferTargetPrefix, action, "application/x-amz-json-1.1", body)
}

func listTransferTags(t *testing.T, srv *helpers.TestServer, arn string) map[string]string {
	t.Helper()
	resp := transferJSONCall(t, srv, "ListTagsForResource", map[string]any{"Arn": arn})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Tags []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"Tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode ListTagsForResource: %v", err)
	}
	out := map[string]string{}
	for _, tag := range result.Tags {
		out[tag.Key] = tag.Value
	}
	return out
}

// transferServerTestARN mirrors transferServerARN in the provisioner: the
// test server's fixed account ID and default region.
func transferServerTestARN(serverID string) string {
	return fmt.Sprintf("arn:aws:transfer:us-east-1:000000000000:server/%s", serverID)
}

func transferUserTestARN(serverID, userName string) string {
	return fmt.Sprintf("arn:aws:transfer:us-east-1:000000000000:user/%s/%s", serverID, userName)
}

const transferServerStackTagsTemplate = `{
  "Resources": {
    "Server": {
      "Type": "AWS::Transfer::Server",
      "Properties": {
        "Tags": [{"Key": "owner", "Value": "resource"}]
      }
    }
  }
}`

func TestCreateStack_TransferServer_stackTagsMerge(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "transfer-server-stack-tags-create"

	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":           {stackName},
		"TemplateBody":        {transferServerStackTagsTemplate},
		"Tags.member.1.Key":   {"env"},
		"Tags.member.1.Value": {"dev"},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	serverID := describeStackResourceIDs(t, srv, stackName)["Server"]
	if got := listTransferTags(t, srv, transferServerTestARN(serverID)); got["env"] != "dev" || got["owner"] != "resource" {
		t.Fatalf("server tags = %#v, want env=dev and owner=resource merged", got)
	}
}

func TestUpdateStack_TransferServer_stackTagChangeReconcilesWithoutReplacement(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "transfer-server-stack-tags-update"

	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":           {stackName},
		"TemplateBody":        {transferServerStackTagsTemplate},
		"Tags.member.1.Key":   {"env"},
		"Tags.member.1.Value": {"dev"},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	serverID := describeStackResourceIDs(t, srv, stackName)["Server"]

	updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":           {stackName},
		"TemplateBody":        {transferServerStackTagsTemplate},
		"Tags.member.1.Key":   {"env"},
		"Tags.member.1.Value": {"prod"},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	// Then: the server is reconciled in place, not replaced — same ServerId —
	// and a tags-only change must not force replacement (#1308/#1310).
	newServerID := describeStackResourceIDs(t, srv, stackName)["Server"]
	if newServerID != serverID {
		t.Fatalf("Server physical ID changed (%q -> %q) on a tags-only update; expected in-place reconciliation", serverID, newServerID)
	}
	if got := listTransferTags(t, srv, transferServerTestARN(serverID)); got["env"] != "prod" || got["owner"] != "resource" {
		t.Fatalf("reconciled server tags = %#v, want env=prod (reconciled) and owner=resource (unchanged)", got)
	}
}

func transferUserStackTagsTemplate(serverLogicalRef string) string {
	return `{
  "Resources": {
    "Server": {
      "Type": "AWS::Transfer::Server",
      "Properties": {}
    },
    "User": {
      "Type": "AWS::Transfer::User",
      "Properties": {
        "ServerId": {"Ref": "` + serverLogicalRef + `"},
        "UserName": "cfn-stack-tags-user",
        "Role": "arn:aws:iam::000000000000:role/transfer-user-role",
        "Tags": [{"Key": "owner", "Value": "resource"}]
      }
    }
  }
}`
}

func TestCreateStack_TransferUser_stackTagsMerge(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "transfer-user-stack-tags-create"

	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":           {stackName},
		"TemplateBody":        {transferUserStackTagsTemplate("Server")},
		"Tags.member.1.Key":   {"env"},
		"Tags.member.1.Value": {"dev"},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	physicalIDs := describeStackResourceIDs(t, srv, stackName)
	arn := transferUserTestARN(physicalIDs["Server"], "cfn-stack-tags-user")
	if got := listTransferTags(t, srv, arn); got["env"] != "dev" || got["owner"] != "resource" {
		t.Fatalf("user tags = %#v, want env=dev and owner=resource merged", got)
	}
}

func TestUpdateStack_TransferUser_stackTagChangeReconciles(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "transfer-user-stack-tags-update"
	template := transferUserStackTagsTemplate("Server")

	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":           {stackName},
		"TemplateBody":        {template},
		"Tags.member.1.Key":   {"env"},
		"Tags.member.1.Value": {"dev"},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	serverID := describeStackResourceIDs(t, srv, stackName)["Server"]
	arn := transferUserTestARN(serverID, "cfn-stack-tags-user")

	updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":           {stackName},
		"TemplateBody":        {template},
		"Tags.member.1.Key":   {"env"},
		"Tags.member.1.Value": {"prod"},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	if got := listTransferTags(t, srv, arn); got["env"] != "prod" || got["owner"] != "resource" {
		t.Fatalf("reconciled user tags = %#v, want env=prod (reconciled) and owner=resource (unchanged)", got)
	}
}
