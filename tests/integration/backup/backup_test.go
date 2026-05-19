package backup_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

const backupTargetPrefix = "AWSBackup."

func backupCall(t *testing.T, srv *helpers.TestServer, action string, body any) *http.Response {
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
	req.Header.Set("X-Amz-Target", backupTargetPrefix+action)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request %s: %v", action, err)
	}
	return resp
}

func decodeMap(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	out := map[string]any{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return out
}

func TestBackupVaultLifecycle(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithServices("backup"))

	createResp := backupCall(t, srv, "CreateBackupVault", map[string]any{"BackupVaultName": "vault-a"})
	if createResp.StatusCode != http.StatusOK {
		t.Fatalf("CreateBackupVault status: got %d want 200", createResp.StatusCode)
	}
	_ = decodeMap(t, createResp)

	listResp := backupCall(t, srv, "ListBackupVaults", map[string]any{})
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("ListBackupVaults status: got %d want 200", listResp.StatusCode)
	}
	listBody := decodeMap(t, listResp)
	vaults, ok := listBody["BackupVaultList"].([]any)
	if !ok || len(vaults) != 1 {
		t.Fatalf("expected one vault in list, got %#v", listBody)
	}

	delResp := backupCall(t, srv, "DeleteBackupVault", map[string]any{"BackupVaultName": "vault-a"})
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("DeleteBackupVault status: got %d want 200", delResp.StatusCode)
	}
	_ = decodeMap(t, delResp)
}

func TestBackupPlanLifecycle(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithServices("backup"))

	createResp := backupCall(t, srv, "CreateBackupPlan", map[string]any{
		"BackupPlan": map[string]any{
			"BackupPlanName": "plan-a",
			"Rules":          []any{},
		},
	})
	if createResp.StatusCode != http.StatusOK {
		t.Fatalf("CreateBackupPlan status: got %d want 200", createResp.StatusCode)
	}
	createBody := decodeMap(t, createResp)
	planID, _ := createBody["BackupPlanId"].(string)
	if planID == "" {
		t.Fatalf("BackupPlanId missing from create response: %#v", createBody)
	}

	getResp := backupCall(t, srv, "GetBackupPlan", map[string]any{"BackupPlanId": planID})
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("GetBackupPlan status: got %d want 200", getResp.StatusCode)
	}
	_ = decodeMap(t, getResp)

	updateResp := backupCall(t, srv, "UpdateBackupPlan", map[string]any{
		"BackupPlanId": planID,
		"BackupPlan": map[string]any{
			"BackupPlanName": "plan-a-updated",
			"Rules":          []any{},
		},
	})
	if updateResp.StatusCode != http.StatusOK {
		t.Fatalf("UpdateBackupPlan status: got %d want 200", updateResp.StatusCode)
	}
	_ = decodeMap(t, updateResp)

	delResp := backupCall(t, srv, "DeleteBackupPlan", map[string]any{"BackupPlanId": planID})
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("DeleteBackupPlan status: got %d want 200", delResp.StatusCode)
	}
	_ = decodeMap(t, delResp)
}
