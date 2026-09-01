//go:build dev

package route53

import "github.com/overcast-sh/overcast/internal/capabilities"

// Category names as constants — any typo is a compile error, not a silent new category.
const (
	catHostedZones      = "Hosted Zones"
	catResourceRecords  = "Resource Records"
	catChangeManagement = "Change Management"
	catTags             = "Tags"
	catHealthChecks     = "Health Checks"
	catTrafficPolicies  = "Traffic Policies"
	catVPCAssociations  = "VPC Associations"
)

func init() {
	capabilities.Default.RegisterForService(serviceName,
		// Hosted Zones
		capabilities.Capability{Operation: "CreateHostedZone", Category: catHostedZones, Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "ListHostedZones", Category: catHostedZones, Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "ListHostedZonesByName", Category: catHostedZones, Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "GetHostedZone", Category: catHostedZones, Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "GetHostedZoneCount", Category: catHostedZones, Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "UpdateHostedZoneComment", Category: catHostedZones, Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "DeleteHostedZone", Category: catHostedZones, Status: capabilities.StatusSupported},

		// Resource Records
		capabilities.Capability{Operation: "ChangeResourceRecordSets", Category: catResourceRecords, Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "ListResourceRecordSets", Category: catResourceRecords, Status: capabilities.StatusSupported},

		// Change Management
		capabilities.Capability{Operation: "GetChange", Category: catChangeManagement, Status: capabilities.StatusSupported},

		// Tags
		capabilities.Capability{Operation: "ChangeTagsForResource", Category: catTags, Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "ListTagsForResource", Category: catTags, Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "ListTagsForResources", Category: catTags, Status: capabilities.StatusSupported},

		// Health Checks
		capabilities.Capability{Operation: "CreateHealthCheck", Category: catHealthChecks, Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "GetHealthCheck", Category: catHealthChecks, Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "ListHealthChecks", Category: catHealthChecks, Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "GetHealthCheckCount", Category: catHealthChecks, Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "UpdateHealthCheck", Category: catHealthChecks, Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "DeleteHealthCheck", Category: catHealthChecks, Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "GetHealthCheckStatus", Category: catHealthChecks, Status: capabilities.StatusUnsupported},
		capabilities.Capability{Operation: "GetHealthCheckLastFailureReason", Category: catHealthChecks, Status: capabilities.StatusUnsupported},

		// Traffic Policies
		capabilities.Capability{Operation: "CreateTrafficPolicy", Category: catTrafficPolicies, Status: capabilities.StatusUnsupported},

		// VPC Associations
		capabilities.Capability{Operation: "AssociateVPCWithHostedZone", Category: catVPCAssociations, Status: capabilities.StatusUnsupported},
		capabilities.Capability{Operation: "DisassociateVPCFromHostedZone", Category: catVPCAssociations, Status: capabilities.StatusUnsupported},
		capabilities.Capability{Operation: "ListHostedZonesByVPC", Category: catVPCAssociations, Status: capabilities.StatusUnsupported},
	)
}
