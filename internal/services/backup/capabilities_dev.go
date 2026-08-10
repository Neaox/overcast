//go:build dev

package backup

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.RegisterForService(serviceName,
		capabilities.Capability{Operation: "CreateBackupVault", Status: capabilities.StatusWIP, Notes: "Overcast does not serve the binding AWS models, so no SDK reaches it (#815)"},
		capabilities.Capability{Operation: "DeleteBackupVault", Status: capabilities.StatusWIP, Notes: "Overcast does not serve the binding AWS models, so no SDK reaches it (#815)"},
		capabilities.Capability{Operation: "DescribeBackupVault", Status: capabilities.StatusWIP, Notes: "Overcast does not serve the binding AWS models, so no SDK reaches it (#815)"},
		capabilities.Capability{Operation: "ListBackupVaults", Status: capabilities.StatusWIP, Notes: "Overcast does not serve the binding AWS models, so no SDK reaches it (#815)"},
		capabilities.Capability{Operation: "CreateBackupPlan", Status: capabilities.StatusWIP, Notes: "Overcast does not serve the binding AWS models, so no SDK reaches it (#815)"},
		capabilities.Capability{Operation: "DeleteBackupPlan", Status: capabilities.StatusWIP, Notes: "Overcast does not serve the binding AWS models, so no SDK reaches it (#815)"},
		capabilities.Capability{Operation: "GetBackupPlan", Status: capabilities.StatusWIP, Notes: "Overcast does not serve the binding AWS models, so no SDK reaches it (#815)"},
		capabilities.Capability{Operation: "ListBackupPlans", Status: capabilities.StatusWIP, Notes: "Overcast does not serve the binding AWS models, so no SDK reaches it (#815)"},
		capabilities.Capability{Operation: "UpdateBackupPlan", Status: capabilities.StatusWIP, Notes: "Overcast does not serve the binding AWS models, so no SDK reaches it (#815)"},
	)
}
