package cloudformation_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// backupStackTemplate provisions a vault and a plan that stores into it — the
// shape a CDK "backup" stack produces.
const backupStackTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Vault": {
      "Type": "AWS::Backup::BackupVault",
      "Properties": {"BackupVaultName": "cfn-vault"}
    },
    "Plan": {
      "Type": "AWS::Backup::BackupPlan",
      "Properties": {
        "BackupPlan": {
          "BackupPlanName": "cfn-plan",
          "Rules": [{"RuleName": "daily", "TargetBackupVaultName": "cfn-vault"}]
        }
      }
    }
  },
  "Outputs": {
    "VaultArn": {"Value": {"Fn::GetAtt": ["Vault", "BackupVaultArn"]}},
    "PlanArn":  {"Value": {"Fn::GetAtt": ["Plan", "BackupPlanArn"]}}
  }
}`

// backupGet issues a Backup REST-JSON GET at one of the service's modeled
// bindings.
func backupGet(t *testing.T, srv *helpers.TestServer, path string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatalf("Backup GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("Backup GET %s: decode: %v", path, err)
	}
	return resp.StatusCode, body
}

// TestCreateStack_BackupVaultAndPlan covers the provisioner's dispatch into
// Backup end to end.
//
// It exists because #815 removed the `AWSBackup.` X-Amz-Target namespace those
// two handlers provisioned through, and nothing failed when it went: both
// handlers ignore the dispatch result on delete and fall back to a synthesised
// ARN on create, so a stack would still have reported CREATE_COMPLETE with
// nothing behind it. The delete half is the sharper case — the plan handler
// used to send the whole ARN where DeleteBackupPlan binds the plan id, so a
// stack delete never removed the plan and no test noticed.
func TestCreateStack_BackupVaultAndPlan(t *testing.T) {
	// Given: a CloudFormation stack with a backup vault and a backup plan.
	srv := helpers.NewTestServer(t)

	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"backup-stack"},
		"TemplateBody": []string{backupStackTemplate},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the stack completes.
	waitForStackStatus(t, srv, "backup-stack", "CREATE_COMPLETE")

	// And: the vault really exists at its modeled binding.
	status, vault := backupGet(t, srv, "/backup-vaults/cfn-vault")
	if status != http.StatusOK {
		t.Fatalf("DescribeBackupVault: HTTP %d: %#v", status, vault)
	}
	vaultArn, _ := vault["BackupVaultArn"].(string)
	if vaultArn == "" {
		t.Fatalf("vault has no ARN: %#v", vault)
	}

	// And: so does the plan, with the rule the template gave it.
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
	if plan["BackupPlanName"] != "cfn-plan" || planArn == "" {
		t.Fatalf("unexpected plan: %#v", plan)
	}

	// And: the stack outputs carry the ARNs the service minted, not ones the
	// provisioner synthesised for itself.
	dsResp := cfnQuery(t, srv, "DescribeStacks", url.Values{"StackName": []string{"backup-stack"}})
	defer dsResp.Body.Close()
	stackBody := string(readBody(t, dsResp))
	for name, want := range map[string]string{"VaultArn": vaultArn, "PlanArn": planArn} {
		if !strings.Contains(stackBody, want) {
			t.Fatalf("output %s: expected %q in DescribeStacks output, body=%s", name, want, stackBody)
		}
	}

	// When: the stack is deleted.
	del := cfnQuery(t, srv, "DeleteStack", url.Values{"StackName": []string{"backup-stack"}})
	defer del.Body.Close()
	helpers.AssertStatus(t, del, http.StatusOK)

	// Then: both the vault and the plan are removed with it.
	helpers.Eventually(t, 5*time.Second, 20*time.Millisecond, func() bool {
		_, vaults := backupGet(t, srv, "/backup-vaults")
		vaultList, _ := vaults["BackupVaultList"].([]any)
		_, remaining := backupGet(t, srv, "/backup/plans")
		remainingPlans, _ := remaining["BackupPlansList"].([]any)
		return len(vaultList) == 0 && len(remainingPlans) == 0
	}, "timed out waiting for the backup vault and plan to be deleted with the stack")
}
