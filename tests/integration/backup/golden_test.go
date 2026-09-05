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
// Coverage — the fifteen vault, plan and access point operations:
//
//	CreateBackupVault, DescribeBackupVault, ListBackupVaults, DeleteBackupVault,
//	CreateBackupPlan, GetBackupPlan, ListBackupPlans, UpdateBackupPlan, DeleteBackupPlan,
//	CreateBackupAccessPoint, DescribeBackupAccessPoint, DeleteBackupAccessPoint,
//	ListBackupAccessPoints, ListBackupAccessPointsByRecoveryPoint,
//	ListBackupAccessPointsByResource
//
// TagResource, ListTags and UntagResource are excluded: the first two answer an
// empty JSON document, and ListTags' one member is asserted per resource type
// in tags_test.go.
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
// backup_test.go, and createAccessPoint, accessPointArn, arnPath and
// recoveryPointArn in access_points_test.go; both sets are reused here.

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

// ─── Access points ───────────────────────────────────────────────────────────
//
// Every value below is deterministic without normalisation: the ARN is minted
// from the name, and CreationTime comes from the mock clock — unlike a plan,
// an access point has no UUID.

func TestGolden_CreateBackupAccessPoint(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())

	resp := backupDo(t, srv, http.MethodPut, pathAccessPoints+"/create", defaultRegion, map[string]any{
		"Name":                "golden-access-point",
		"RecoveryPointArn":    recoveryPointArn,
		"AccessPointMetadata": map[string]any{"AccessPointInTime": "2021-11-27T03:30:27Z"},
	})
	helpers.GoldenTest(t, backupGoldenDir, "CreateBackupAccessPoint", resp, nil)
}

// TestGolden_DescribeBackupAccessPoint creates with AccessPointMetadata so the
// fixture pins the map's bytes; createAccessPoint sends none.
func TestGolden_DescribeBackupAccessPoint(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	seed := backupDo(t, srv, http.MethodPut, pathAccessPoints+"/create", defaultRegion, map[string]any{
		"Name":                "golden-access-point",
		"RecoveryPointArn":    recoveryPointArn,
		"AccessPointMetadata": map[string]any{"AccessPointInTime": "2021-11-27T03:30:27Z"},
	})
	seed.Body.Close()

	resp := backupDo(t, srv, http.MethodGet,
		pathAccessPoints+"/"+arnPath(accessPointArn("golden-access-point")), defaultRegion, nil)
	helpers.GoldenTest(t, backupGoldenDir, "DescribeBackupAccessPoint", resp, nil)
}

func TestGolden_ListBackupAccessPoints(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createAccessPoint(t, srv, "golden-access-point-a")
	createAccessPoint(t, srv, "golden-access-point-b")

	resp := backupDo(t, srv, http.MethodGet, pathAccessPoints, defaultRegion, nil)
	helpers.GoldenTest(t, backupGoldenDir, "ListBackupAccessPoints", resp, nil)
}

func TestGolden_ListBackupAccessPointsByRecoveryPoint(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createAccessPoint(t, srv, "golden-access-point")

	resp := backupDo(t, srv, http.MethodPost,
		pathAccessPoints+"/recovery-point/"+arnPath(recoveryPointArn), defaultRegion, nil)
	helpers.GoldenTest(t, backupGoldenDir, "ListBackupAccessPointsByRecoveryPoint", resp, nil)
}

// TestGolden_ListBackupAccessPointsByResource pins the empty page. It is the
// answer for every resource ARN, because no access point ever carries a
// ResourceArn to match — see handler_access_points.go.
func TestGolden_ListBackupAccessPointsByResource(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createAccessPoint(t, srv, "golden-access-point")

	resp := backupDo(t, srv, http.MethodPost,
		pathAccessPoints+"/resource/"+arnPath("arn:aws:s3:::golden-bucket"), defaultRegion, nil)
	helpers.GoldenTest(t, backupGoldenDir, "ListBackupAccessPointsByResource", resp, nil)
}

func TestGolden_DeleteBackupAccessPoint(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createAccessPoint(t, srv, "golden-access-point")

	resp := backupDo(t, srv, http.MethodDelete,
		pathAccessPoints+"/delete/"+arnPath(accessPointArn("golden-access-point")), defaultRegion, nil)
	helpers.GoldenTest(t, backupGoldenDir, "DeleteBackupAccessPoint", resp, nil)
}
