//go:build dev

package backup

import "github.com/Neaox/overcast/internal/capabilities"

// The nine rows below were StatusInert before #864 flipped them to StatusWIP,
// and #815 makes them reachable. Neither of those grades is the honest one now:
//
//   - StatusInert says an operation "is intentionally accepted but has no side
//     effects". That describes the *tier* — router.TierInert records that
//     nothing runs behind Backup's control plane — not these operations, which
//     really do create, store, list and delete vaults and plans.
//   - StatusPartial is what a served operation gets when "core behaviour works;
//     edge cases or optional fields may differ", which is exactly the state of
//     every operation whose modeled input or output carries a member Overcast
//     drops. Each note below names the member rather than saying "partial".
//
// The two Deletes serve their modeled shapes in full and are StatusSupported.
// Both model an InvalidRequestException for a resource that still has contents
// — recovery points, backup selections — and Overcast implements neither, so
// no reachable state can produce one.
func init() {
	capabilities.Default.RegisterForService(serviceName,
		capabilities.Capability{Operation: "CreateBackupVault", Status: capabilities.StatusPartial,
			Notes: "PUT /backup-vaults/{BackupVaultName}; `BackupVaultTags` is accepted and dropped — Backup has no tag operations yet (#815)"},
		capabilities.Capability{Operation: "DeleteBackupVault", Status: capabilities.StatusSupported,
			Notes: "DELETE /backup-vaults/{BackupVaultName}; empty 200, the modeled Unit output"},
		capabilities.Capability{Operation: "DescribeBackupVault", Status: capabilities.StatusPartial,
			Notes: "GET /backup-vaults/{BackupVaultName}; honours `backupVaultAccountId`. Vault lock and MPA approval are not emulated, so `Locked` is always false and the retention members are absent"},
		capabilities.Capability{Operation: "ListBackupVaults", Status: capabilities.StatusPartial,
			Notes: "GET /backup-vaults; `vaultType`, `shared`, `maxResults` and `nextToken` query params. Every vault is a standard unshared vault, so `shared=true` lists none"},
		capabilities.Capability{Operation: "CreateBackupPlan", Status: capabilities.StatusPartial,
			Notes: "PUT /backup/plans; `BackupPlanTags` is accepted and dropped (#815), and `AdvancedBackupSettings` is not stored or returned"},
		capabilities.Capability{Operation: "DeleteBackupPlan", Status: capabilities.StatusSupported,
			Notes: "DELETE /backup/plans/{BackupPlanId}; returns the modeled id, ARN, VersionId and DeletionDate"},
		capabilities.Capability{Operation: "GetBackupPlan", Status: capabilities.StatusPartial,
			Notes: "GET /backup/plans/{BackupPlanId}; only the current version is kept, so any other `versionId` is not found, and `MaxScheduledRunsPreview` yields no `ScheduledRunsPreview`"},
		capabilities.Capability{Operation: "ListBackupPlans", Status: capabilities.StatusPartial,
			Notes: "GET /backup/plans; `maxResults` and `nextToken` query params. Plans are deleted outright rather than tombstoned, so `includeDeleted` adds nothing"},
		capabilities.Capability{Operation: "UpdateBackupPlan", Status: capabilities.StatusPartial,
			Notes: "POST /backup/plans/{BackupPlanId}; mints a new VersionId per update. Prior versions are not retained and `AdvancedBackupSettings` is not stored"},
	)
}
