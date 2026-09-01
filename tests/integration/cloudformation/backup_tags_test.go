package cloudformation_test

import (
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// listBackupResourceTags reads a Backup vault/plan's tags at GET
// /tags/{ResourceArn} — the path Backup's TagResource/ListTags share with
// several other services (#1195; internal/services/backup/handler_tags.go).
func listBackupResourceTags(t *testing.T, srv *helpers.TestServer, resourceArn string) map[string]string {
	t.Helper()
	path := "/tags/" + strings.ReplaceAll(resourceArn, ":", "%3A")
	status, body := backupGet(t, srv, path)
	if status != http.StatusOK {
		t.Fatalf("ListTags %s: HTTP %d: %#v", resourceArn, status, body)
	}
	tags, _ := body["Tags"].(map[string]any)
	out := make(map[string]string, len(tags))
	for k, v := range tags {
		s, _ := v.(string)
		out[k] = s
	}
	return out
}

// CreateBackupVault/CreateBackupPlan's BackupVaultTags/BackupPlanTags members
// used to be accepted and dropped (#815's gap, closed by #1195). A CDK/CFN
// template setting them must see the tags stick from the moment the stack
// completes, with no separate TagResource call needed.
func TestCreateStack_BackupVaultAndPlan_TagsAppliedAtCreate(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const template = `{
  "Resources": {
    "Vault": {
      "Type": "AWS::Backup::BackupVault",
      "Properties": {
        "BackupVaultName": "cfn-tagged-vault",
        "BackupVaultTags": {"environment": "development"}
      }
    },
    "Plan": {
      "Type": "AWS::Backup::BackupPlan",
      "Properties": {
        "BackupPlan": {
          "BackupPlanName": "cfn-tagged-plan",
          "Rules": [{"RuleName": "daily", "TargetBackupVaultName": "cfn-tagged-vault"}]
        },
        "BackupPlanTags": {"environment": "development"}
      }
    }
  },
  "Outputs": {
    "VaultArn": {"Value": {"Fn::GetAtt": ["Vault", "BackupVaultArn"]}},
    "PlanArn":  {"Value": {"Fn::GetAtt": ["Plan", "BackupPlanArn"]}}
  }
}`

	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"backup-tags-create-stack"},
		"TemplateBody": {template},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, "backup-tags-create-stack", "CREATE_COMPLETE")

	vaultArn := "arn:aws:backup:us-east-1:000000000000:backup-vault:cfn-tagged-vault"
	if got := listBackupResourceTags(t, srv, vaultArn); !reflect.DeepEqual(got, map[string]string{"environment": "development"}) {
		t.Fatalf("vault tags = %#v", got)
	}

	status, plans := backupGet(t, srv, "/backup/plans")
	if status != http.StatusOK {
		t.Fatalf("ListBackupPlans: HTTP %d: %#v", status, plans)
	}
	planList, _ := plans["BackupPlansList"].([]any)
	if len(planList) != 1 {
		t.Fatalf("expected 1 backup plan, got %#v", plans)
	}
	plan, _ := planList[0].(map[string]any)
	planArn, _ := plan["BackupPlanArn"].(string)
	if planArn == "" {
		t.Fatalf("plan has no ARN: %#v", plan)
	}
	if got := listBackupResourceTags(t, srv, planArn); !reflect.DeepEqual(got, map[string]string{"environment": "development"}) {
		t.Fatalf("plan tags = %#v", got)
	}
}

// A stack update that only changes BackupVaultTags must reconcile the tags
// in place via TagResource/UntagResource rather than forcing a replacement —
// tags never force replacement on real AWS, and BackupVaultName (the vault's
// only replacement property) is unchanged here.
func TestUpdateStack_BackupVaultTags_reconciledInPlace(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "backup-vault-tags-update-stack"
	const initialTemplate = `{
  "Resources": {
    "Vault": {
      "Type": "AWS::Backup::BackupVault",
      "Properties": {
        "BackupVaultName": "update-tags-vault",
        "BackupVaultTags": {"environment": "development", "owner": "platform"}
      }
    }
  }
}`
	const updatedTemplate = `{
  "Resources": {
    "Vault": {
      "Type": "AWS::Backup::BackupVault",
      "Properties": {
        "BackupVaultName": "update-tags-vault",
        "BackupVaultTags": {"environment": "production", "project": "overcast"}
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

	vaultArn := "arn:aws:backup:us-east-1:000000000000:backup-vault:update-tags-vault"
	if got := listBackupResourceTags(t, srv, vaultArn); !reflect.DeepEqual(got, map[string]string{
		"environment": "development",
		"owner":       "platform",
	}) {
		t.Fatalf("initial tags = %#v", got)
	}

	updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {updatedTemplate},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	// The vault itself was not replaced: the ARN (and so its physical
	// resource) is unchanged, still reachable at the same name.
	status, vault := backupGet(t, srv, "/backup-vaults/update-tags-vault")
	if status != http.StatusOK {
		t.Fatalf("DescribeBackupVault after update: HTTP %d: %#v", status, vault)
	}

	if got := listBackupResourceTags(t, srv, vaultArn); !reflect.DeepEqual(got, map[string]string{
		"environment": "production",
		"project":     "overcast",
	}) {
		t.Fatalf("updated tags = %#v", got)
	}
}

// A stack update that only changes BackupPlanTags must reconcile in place —
// UpdateBackupPlan models no tags member either, so #1195's TagResource/
// UntagResource follow-up is the only way a plan's tags can ever change
// in-place at all.
func TestUpdateStack_BackupPlanTags_reconciledInPlace(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "backup-plan-tags-update-stack"
	const initialTemplate = `{
  "Resources": {
    "Plan": {
      "Type": "AWS::Backup::BackupPlan",
      "Properties": {
        "BackupPlan": {
          "BackupPlanName": "update-tags-plan",
          "Rules": [{"RuleName": "daily", "TargetBackupVaultName": "default"}]
        },
        "BackupPlanTags": {"environment": "development"}
      }
    }
  },
  "Outputs": {"PlanArn": {"Value": {"Fn::GetAtt": ["Plan", "BackupPlanArn"]}}}
}`
	const updatedTemplate = `{
  "Resources": {
    "Plan": {
      "Type": "AWS::Backup::BackupPlan",
      "Properties": {
        "BackupPlan": {
          "BackupPlanName": "update-tags-plan",
          "Rules": [{"RuleName": "daily", "TargetBackupVaultName": "default"}]
        },
        "BackupPlanTags": {"environment": "production", "team": "ops"}
      }
    }
  },
  "Outputs": {"PlanArn": {"Value": {"Fn::GetAtt": ["Plan", "BackupPlanArn"]}}}
}`
	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {initialTemplate},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	dsResp := cfnQuery(t, srv, "DescribeStacks", url.Values{"StackName": {stackName}})
	defer dsResp.Body.Close()
	stackBody := string(readBody(t, dsResp))
	planArn := extractPlanArnFromOutputs(t, stackBody)

	if got := listBackupResourceTags(t, srv, planArn); !reflect.DeepEqual(got, map[string]string{"environment": "development"}) {
		t.Fatalf("initial tags = %#v", got)
	}

	updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {updatedTemplate},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	if got := listBackupResourceTags(t, srv, planArn); !reflect.DeepEqual(got, map[string]string{
		"environment": "production",
		"team":        "ops",
	}) {
		t.Fatalf("updated tags = %#v", got)
	}
}

// extractPlanArnFromOutputs pulls the PlanArn output's value out of a
// DescribeStacks XML body. Good enough for one known output name; a real XML
// parse is not worth it for a single string.
func extractPlanArnFromOutputs(t *testing.T, stackXML string) string {
	t.Helper()
	const marker = "<OutputValue>arn:aws:backup:"
	idx := strings.Index(stackXML, marker)
	if idx < 0 {
		t.Fatalf("no PlanArn output found in stack body: %s", stackXML)
	}
	rest := stackXML[idx+len("<OutputValue>"):]
	end := strings.Index(rest, "</OutputValue>")
	if end < 0 {
		t.Fatalf("malformed OutputValue in stack body: %s", stackXML)
	}
	return rest[:end]
}
