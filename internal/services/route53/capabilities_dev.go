//go:build dev

package route53

import "github.com/Neaox/overcast/internal/capabilities"

// Category names as constants — any typo is a compile error, not a silent new category.
const (
	catHostedZones      = "Hosted Zones"
	catResourceRecords  = "Resource Records"
	catChangeManagement = "Change Management"
	catTrafficPolicies  = "Traffic Policies"
	catHealthChecks     = "Health Checks"
)

func init() {
	capabilities.Default.RegisterForService(serviceName,
		// Hosted Zones
		capabilities.Capability{Operation: "CreateHostedZone", Category: catHostedZones, Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "ListHostedZones", Category: catHostedZones, Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "GetHostedZone", Category: catHostedZones, Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "DeleteHostedZone", Category: catHostedZones, Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "ListHostedZonesByName", Category: catHostedZones, Status: capabilities.StatusUnsupported},

		// Resource Records
		capabilities.Capability{Operation: "ChangeResourceRecordSets", Category: catResourceRecords, Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "ListResourceRecordSets", Category: catResourceRecords, Status: capabilities.StatusSupported},

		// Change Management
		capabilities.Capability{Operation: "GetChange", Category: catChangeManagement, Status: capabilities.StatusSupported},

		// Traffic Policies
		capabilities.Capability{Operation: "CreateTrafficPolicy", Category: catTrafficPolicies, Status: capabilities.StatusUnsupported},

		// Health Checks
		capabilities.Capability{Operation: "CreateHealthCheck", Category: catHealthChecks, Status: capabilities.StatusUnsupported},
	)
}
