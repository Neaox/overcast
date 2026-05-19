//go:build dev

package backup

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.RegisterForService(serviceName,
		capabilities.Capability{Operation: "CreateBackupVault", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "DeleteBackupVault", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "DescribeBackupVault", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "ListBackupVaults", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "CreateBackupPlan", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "DeleteBackupPlan", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "GetBackupPlan", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "ListBackupPlans", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "UpdateBackupPlan", Status: capabilities.StatusInert},
	)
}
