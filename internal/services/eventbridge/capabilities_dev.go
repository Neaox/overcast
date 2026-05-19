//go:build dev

package eventbridge

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		// Event buses
		capabilities.Capability{Service: "eventbridge", Operation: "CreateEventBus", Category: "Event buses", Status: capabilities.StatusSupported, Notes: "Creates a custom event bus"},
		capabilities.Capability{Service: "eventbridge", Operation: "DescribeEventBus", Category: "Event buses", Status: capabilities.StatusSupported, Notes: "Returns bus details; synthetic default bus"},
		capabilities.Capability{Service: "eventbridge", Operation: "ListEventBuses", Category: "Event buses", Status: capabilities.StatusSupported, Notes: "Always includes default bus"},
		capabilities.Capability{Service: "eventbridge", Operation: "DeleteEventBus", Category: "Event buses", Status: capabilities.StatusSupported},
		// Rules
		capabilities.Capability{Service: "eventbridge", Operation: "PutRule", Category: "Rules", Status: capabilities.StatusSupported, Notes: "Creates or updates a rule"},
		capabilities.Capability{Service: "eventbridge", Operation: "DescribeRule", Category: "Rules", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "eventbridge", Operation: "ListRules", Category: "Rules", Status: capabilities.StatusSupported, Notes: "Lists rules for a bus"},
		capabilities.Capability{Service: "eventbridge", Operation: "EnableRule", Category: "Rules", Status: capabilities.StatusSupported, Notes: "Sets rule state to ENABLED"},
		capabilities.Capability{Service: "eventbridge", Operation: "DisableRule", Category: "Rules", Status: capabilities.StatusSupported, Notes: "Sets rule state to DISABLED"},
		capabilities.Capability{Service: "eventbridge", Operation: "DeleteRule", Category: "Rules", Status: capabilities.StatusSupported},
		// Targets
		capabilities.Capability{Service: "eventbridge", Operation: "PutTargets", Category: "Targets", Status: capabilities.StatusSupported, Notes: "Adds targets to a rule"},
		capabilities.Capability{Service: "eventbridge", Operation: "ListTargetsByRule", Category: "Targets", Status: capabilities.StatusSupported, Notes: "Lists targets for a rule"},
		capabilities.Capability{Service: "eventbridge", Operation: "RemoveTargets", Category: "Targets", Status: capabilities.StatusSupported, Notes: "Removes targets from a rule"},
		// Events
		capabilities.Capability{Service: "eventbridge", Operation: "PutEvents", Category: "Events", Status: capabilities.StatusSupported, Notes: "Accepts events; returns event IDs (no routing)"},
		// Tags
		capabilities.Capability{Service: "eventbridge", Operation: "TagResource", Category: "Tags", Status: capabilities.StatusSupported, Notes: "Tag buses and rules"},
		capabilities.Capability{Service: "eventbridge", Operation: "ListTagsForResource", Category: "Tags", Status: capabilities.StatusSupported, Notes: "List tags for a resource"},
		capabilities.Capability{Service: "eventbridge", Operation: "UntagResource", Category: "Tags", Status: capabilities.StatusUnsupported, Notes: "Returns 501", DocOnly: true},
		// Archives
		capabilities.Capability{Service: "eventbridge", Operation: "CreateArchive", Category: "Archives", Status: capabilities.StatusUnsupported, Notes: "Returns 501", DocOnly: true},
		capabilities.Capability{Service: "eventbridge", Operation: "DescribeArchive", Category: "Archives", Status: capabilities.StatusUnsupported, Notes: "Returns 501", DocOnly: true},
		capabilities.Capability{Service: "eventbridge", Operation: "ListArchives", Category: "Archives", Status: capabilities.StatusUnsupported, Notes: "Returns 501", DocOnly: true},
		capabilities.Capability{Service: "eventbridge", Operation: "DeleteArchive", Category: "Archives", Status: capabilities.StatusUnsupported, Notes: "Returns 501", DocOnly: true},
		// Replays
		capabilities.Capability{Service: "eventbridge", Operation: "StartReplay", Category: "Replays", Status: capabilities.StatusUnsupported, Notes: "Returns 501", DocOnly: true},
		capabilities.Capability{Service: "eventbridge", Operation: "DescribeReplay", Category: "Replays", Status: capabilities.StatusUnsupported, Notes: "Returns 501", DocOnly: true},
		capabilities.Capability{Service: "eventbridge", Operation: "ListReplays", Category: "Replays", Status: capabilities.StatusUnsupported, Notes: "Returns 501", DocOnly: true},
		// Connections
		capabilities.Capability{Service: "eventbridge", Operation: "CreateConnection", Category: "Connections", Status: capabilities.StatusUnsupported, Notes: "Returns 501", DocOnly: true},
		capabilities.Capability{Service: "eventbridge", Operation: "DescribeConnection", Category: "Connections", Status: capabilities.StatusUnsupported, Notes: "Returns 501", DocOnly: true},
		capabilities.Capability{Service: "eventbridge", Operation: "ListConnections", Category: "Connections", Status: capabilities.StatusUnsupported, Notes: "Returns 501", DocOnly: true},
		capabilities.Capability{Service: "eventbridge", Operation: "DeleteConnection", Category: "Connections", Status: capabilities.StatusUnsupported, Notes: "Returns 501", DocOnly: true},
	)
}
