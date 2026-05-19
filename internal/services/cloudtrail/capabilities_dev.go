//go:build dev

package cloudtrail

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.RegisterForService(serviceName,
		capabilities.Capability{Operation: "CreateTrail", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "DescribeTrails", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "UpdateTrail", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "DeleteTrail", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "ListTrails", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "GetTrailStatus", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "StartLogging", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "StopLogging", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "LookupEvents", Status: capabilities.StatusInert},
	)
}
