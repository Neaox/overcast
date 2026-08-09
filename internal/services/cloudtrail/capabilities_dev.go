//go:build dev

package cloudtrail

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.RegisterForService(serviceName,
		capabilities.Capability{Operation: "CreateTrail", Status: capabilities.StatusInert, Notes: "Inline `TagsList` applied at creation"},
		capabilities.Capability{Operation: "DescribeTrails", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "UpdateTrail", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "DeleteTrail", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "ListTrails", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "GetTrailStatus", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "StartLogging", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "StopLogging", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "LookupEvents", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "AddTags", Status: capabilities.StatusInert, Notes: "Trail ARNs; event data stores and channels are not emulated"},
		capabilities.Capability{Operation: "RemoveTags", Status: capabilities.StatusInert, Notes: "Matches each `TagsList` entry on its `Key`, ignoring the value"},
		capabilities.Capability{Operation: "ListTags", Status: capabilities.StatusInert, Notes: "One `ResourceTagList` entry per requested ARN; no pagination"},
	)
}
