// golden_test.go contains wire-byte golden tests for Backup — see
// docs/plans/wire-byte-goldens.md for the harness design.
//
// Backup is one of the "switch-based five" hybrid services flagged in
// docs/plans/level2-codegen.md §2 Debt D2 (backup, cognito, eventbridge,
// organizations, transfer): JSON traffic always runs through
// (*Service).dispatchLegacy's inline switch (internal/services/backup/service.go),
// while CBOR runs the fully-typed twin registered in typed_ops.go. These
// goldens pin the legacy switch's exact response bytes for every operation
// so the P2 delegation sweep (level2-codegen.md §3 Track 2) can convert each
// switch case into a decode-shim onto the typed implementation and prove
// byte-identical output with this same, unmodified test.
//
// Coverage — every Backup operation is deterministic under a mock clock: all
// nine registered operations produce no UUIDs; the one non-clock-driven ID
// (BackupPlanId, minted as fmt.Sprintf("plan-%d", clk.Now().UnixNano()) in
// service.go) is fully reproducible as long as the mock clock's value at
// each CreateBackupPlan call is fixed — this test file advances srv.Clock by
// a fixed step between successive creates so two plans never collide on the
// same nanosecond-derived ID. All ops covered:
//
//	CreateBackupVault, DescribeBackupVault, ListBackupVaults, DeleteBackupVault,
//	CreateBackupPlan, GetBackupPlan, ListBackupPlans, UpdateBackupPlan, DeleteBackupPlan
//
// No Backup operations are excluded.
//
// Record: go test ./tests/integration/backup/ -run TestGolden -record
// Assert: go test ./tests/integration/backup/ -run TestGolden
package backup_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/Neaox/overcast/tests/helpers"
)

const backupGoldenDir = "goldens"

// backupCall (the X-Amz-Target JSON request helper for the legacy dispatch
// path) and decodeMap are defined in backup_test.go and reused here.

func createVault(t *testing.T, srv *helpers.TestServer, name string) {
	t.Helper()
	resp := backupCall(t, srv, "CreateBackupVault", map[string]any{"BackupVaultName": name})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// createPlan creates a backup plan and returns its BackupPlanId.
func createPlan(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	resp := backupCall(t, srv, "CreateBackupPlan", map[string]any{
		"BackupPlan": map[string]any{
			"BackupPlanName": name,
			"Rules": []map[string]any{
				{"RuleName": "daily", "TargetBackupVaultName": "default"},
			},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		BackupPlanId string `json:"BackupPlanId"`
	}
	helpers.DecodeJSON(t, resp, &out)
	return out.BackupPlanId
}

func TestGolden_CreateBackupVault(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())

	resp := backupCall(t, srv, "CreateBackupVault", map[string]any{"BackupVaultName": "golden-vault"})
	helpers.GoldenTest(t, backupGoldenDir, "CreateBackupVault", resp, nil)
}

func TestGolden_DescribeBackupVault(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createVault(t, srv, "golden-vault")

	resp := backupCall(t, srv, "DescribeBackupVault", map[string]any{"BackupVaultName": "golden-vault"})
	helpers.GoldenTest(t, backupGoldenDir, "DescribeBackupVault", resp, nil)
}

func TestGolden_ListBackupVaults(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createVault(t, srv, "golden-vault-a")
	createVault(t, srv, "golden-vault-b")

	resp := backupCall(t, srv, "ListBackupVaults", map[string]any{})
	helpers.GoldenTest(t, backupGoldenDir, "ListBackupVaults", resp, nil)
}

func TestGolden_DeleteBackupVault(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createVault(t, srv, "golden-vault")

	resp := backupCall(t, srv, "DeleteBackupVault", map[string]any{"BackupVaultName": "golden-vault"})
	helpers.GoldenTest(t, backupGoldenDir, "DeleteBackupVault", resp, nil)
}

func TestGolden_CreateBackupPlan(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())

	resp := backupCall(t, srv, "CreateBackupPlan", map[string]any{
		"BackupPlan": map[string]any{
			"BackupPlanName": "golden-plan",
			"Rules": []map[string]any{
				{"RuleName": "daily", "TargetBackupVaultName": "default"},
			},
		},
	})
	helpers.GoldenTest(t, backupGoldenDir, "CreateBackupPlan", resp, nil)
}

func TestGolden_GetBackupPlan(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	planID := createPlan(t, srv, "golden-plan")

	resp := backupCall(t, srv, "GetBackupPlan", map[string]any{"BackupPlanId": planID})
	helpers.GoldenTest(t, backupGoldenDir, "GetBackupPlan", resp, nil)
}

func TestGolden_ListBackupPlans(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createPlan(t, srv, "golden-plan-a")
	// Advance the mock clock so the second plan gets a distinct
	// nanosecond-derived BackupPlanId (see file-level doc comment).
	srv.Clock.Add(time.Second)
	createPlan(t, srv, "golden-plan-b")

	resp := backupCall(t, srv, "ListBackupPlans", map[string]any{})
	helpers.GoldenTest(t, backupGoldenDir, "ListBackupPlans", resp, nil)
}

func TestGolden_UpdateBackupPlan(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	planID := createPlan(t, srv, "golden-plan")
	srv.Clock.Add(time.Second)

	resp := backupCall(t, srv, "UpdateBackupPlan", map[string]any{
		"BackupPlanId": planID,
		"BackupPlan": map[string]any{
			"BackupPlanName": "golden-plan-renamed",
			"Rules": []map[string]any{
				{"RuleName": "weekly", "TargetBackupVaultName": "default"},
			},
		},
	})
	helpers.GoldenTest(t, backupGoldenDir, "UpdateBackupPlan", resp, nil)
}

func TestGolden_DeleteBackupPlan(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	planID := createPlan(t, srv, "golden-plan")

	resp := backupCall(t, srv, "DeleteBackupPlan", map[string]any{"BackupPlanId": planID})
	helpers.GoldenTest(t, backupGoldenDir, "DeleteBackupPlan", resp, nil)
}
