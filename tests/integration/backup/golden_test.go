// golden_test.go contains wire-byte golden tests for Backup — see
// docs/plans/wire-byte-goldens.md for the harness design.
//
// The fixtures pin the bytes of Backup's REST-JSON responses at the bindings
// the pinned model gives them (backup-2018-11-15). They were re-recorded by
// #815, which moved the service onto those bindings: the previous fixtures
// were recorded through an `X-Amz-Target: AWSBackup.*` dispatcher for a
// namespace AWS does not have, and pinned CreationDate as an RFC 3339 string
// where restJson1 binds an epoch-seconds number.
//
// Coverage — all nine implemented operations:
//
//	CreateBackupVault, DescribeBackupVault, ListBackupVaults, DeleteBackupVault,
//	CreateBackupPlan, GetBackupPlan, ListBackupPlans, UpdateBackupPlan, DeleteBackupPlan
//
// No Backup operations are excluded.
//
// Determinism: every timestamp comes from the mock clock, and the one
// remaining non-deterministic value — BackupPlanId, a UUID as it is on AWS —
// is normalised out by normalisePlanIDs before comparison. Pinning a UUID
// would make these fixtures unrecordable; pinning a clock-derived id, as this
// file did before, made two plans created at one instant collide.
//
// Record: go test ./tests/integration/backup/ -run TestGolden -record
// Assert: go test ./tests/integration/backup/ -run TestGolden
package backup_test

import (
	"net/http"
	"regexp"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

const backupGoldenDir = "goldens"

// backupDo, createVault, createPlan and decodeMap are defined in
// backup_test.go and reused here.

// uuidPattern matches the BackupPlanId minted by CreateBackupPlan, wherever it
// appears — on its own and inside BackupPlanArn.
var uuidPattern = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// normalisePlanIDs replaces every minted plan id with a fixed placeholder so
// the fixture pins the shape of the response rather than the value of a UUID.
func normalisePlanIDs(body []byte) []byte {
	return uuidPattern.ReplaceAll(body, []byte("00000000-0000-0000-0000-000000000000"))
}

func TestGolden_CreateBackupVault(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())

	resp := backupDo(t, srv, http.MethodPut, pathVaults+"/golden-vault", defaultRegion, map[string]any{})
	helpers.GoldenTest(t, backupGoldenDir, "CreateBackupVault", resp, nil)
}

func TestGolden_DescribeBackupVault(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createVault(t, srv, "golden-vault")

	resp := backupDo(t, srv, http.MethodGet, pathVaults+"/golden-vault", defaultRegion, nil)
	helpers.GoldenTest(t, backupGoldenDir, "DescribeBackupVault", resp, nil)
}

func TestGolden_ListBackupVaults(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createVault(t, srv, "golden-vault-a")
	createVault(t, srv, "golden-vault-b")

	resp := backupDo(t, srv, http.MethodGet, pathVaults, defaultRegion, nil)
	helpers.GoldenTest(t, backupGoldenDir, "ListBackupVaults", resp, nil)
}

func TestGolden_DeleteBackupVault(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createVault(t, srv, "golden-vault")

	resp := backupDo(t, srv, http.MethodDelete, pathVaults+"/golden-vault", defaultRegion, nil)
	helpers.GoldenTest(t, backupGoldenDir, "DeleteBackupVault", resp, nil)
}

func TestGolden_CreateBackupPlan(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())

	resp := backupDo(t, srv, http.MethodPut, pathPlans, defaultRegion, map[string]any{
		"BackupPlan": map[string]any{
			"BackupPlanName": "golden-plan",
			"Rules": []map[string]any{
				{"RuleName": "daily", "TargetBackupVaultName": "default"},
			},
		},
	})
	helpers.GoldenTest(t, backupGoldenDir, "CreateBackupPlan", resp, normalisePlanIDs)
}

func TestGolden_GetBackupPlan(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	planID, _ := createPlan(t, srv, "golden-plan")["BackupPlanId"].(string)

	resp := backupDo(t, srv, http.MethodGet, pathPlans+"/"+planID, defaultRegion, nil)
	helpers.GoldenTest(t, backupGoldenDir, "GetBackupPlan", resp, normalisePlanIDs)
}

func TestGolden_ListBackupPlans(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createPlan(t, srv, "golden-plan-a")
	createPlan(t, srv, "golden-plan-b")

	resp := backupDo(t, srv, http.MethodGet, pathPlans, defaultRegion, nil)
	helpers.GoldenTest(t, backupGoldenDir, "ListBackupPlans", resp, normalisePlanIDs)
}

func TestGolden_UpdateBackupPlan(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	planID, _ := createPlan(t, srv, "golden-plan")["BackupPlanId"].(string)

	resp := backupDo(t, srv, http.MethodPost, pathPlans+"/"+planID, defaultRegion, map[string]any{
		"BackupPlan": map[string]any{
			"BackupPlanName": "golden-plan-renamed",
			"Rules": []map[string]any{
				{"RuleName": "weekly", "TargetBackupVaultName": "default"},
			},
		},
	})
	helpers.GoldenTest(t, backupGoldenDir, "UpdateBackupPlan", resp, normalisePlanIDs)
}

func TestGolden_DeleteBackupPlan(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	planID, _ := createPlan(t, srv, "golden-plan")["BackupPlanId"].(string)

	resp := backupDo(t, srv, http.MethodDelete, pathPlans+"/"+planID, defaultRegion, nil)
	helpers.GoldenTest(t, backupGoldenDir, "DeleteBackupPlan", resp, normalisePlanIDs)
}
