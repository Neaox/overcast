package scheduler

import "github.com/Neaox/overcast/internal/capabilities"

const (
	catScheduleGroups = "Schedule Groups"
	catSchedules      = "Schedules"
	catTags           = "Tags"
)

func init() {
	capabilities.Default.RegisterForService(serviceName,
		// Schedule Groups
		capabilities.Capability{Operation: "CreateScheduleGroup", Category: catScheduleGroups,
			Status: capabilities.StatusSupported, Notes: "Creates a named group"},
		capabilities.Capability{Operation: "GetScheduleGroup", Category: catScheduleGroups,
			Status: capabilities.StatusSupported, Notes: "Returns group metadata"},
		capabilities.Capability{Operation: "ListScheduleGroups", Category: catScheduleGroups,
			Status: capabilities.StatusSupported, Notes: "Lists groups in region"},
		capabilities.Capability{Operation: "DeleteScheduleGroup", Category: catScheduleGroups,
			Status: capabilities.StatusSupported, Notes: "Deletes group (except `default`)"},

		// Schedules
		capabilities.Capability{Operation: "CreateSchedule", Category: catSchedules,
			Status: capabilities.StatusSupported, Notes: "Group-specific or default group path"},
		capabilities.Capability{Operation: "GetSchedule", Category: catSchedules,
			Status: capabilities.StatusSupported, Notes: "Returns full schedule definition"},
		capabilities.Capability{Operation: "UpdateSchedule", Category: catSchedules,
			Status: capabilities.StatusSupported, Notes: "Updates expression/target/state fields"},
		capabilities.Capability{Operation: "DeleteSchedule", Category: catSchedules,
			Status: capabilities.StatusSupported, Notes: "Deletes schedule"},
		capabilities.Capability{Operation: "ListSchedules", Category: catSchedules,
			Status: capabilities.StatusSupported, Notes: "Optional `ScheduleGroup` filter"},

		// Tags
		capabilities.Capability{Operation: "TagResource", Category: catTags,
			Status: capabilities.StatusSupported, Notes: "Merges tags on ARN"},
		capabilities.Capability{Operation: "UntagResource", Category: catTags,
			Status: capabilities.StatusSupported, Notes: "Removes keys from ARN"},
		capabilities.Capability{Operation: "ListTagsForResource", Category: catTags,
			Status: capabilities.StatusSupported, Notes: "Returns tag map"},
	)
}
