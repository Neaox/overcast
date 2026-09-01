package scheduler

import "github.com/overcast-sh/overcast/internal/capabilities"

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
			Status: capabilities.StatusSupported, Notes: "`POST /schedules/{Name}`; `GroupName` in the body, defaulting to `default`; rejects a target type Overcast cannot fire"},
		capabilities.Capability{Operation: "GetSchedule", Category: catSchedules,
			Status: capabilities.StatusSupported, Notes: "`GET /schedules/{Name}`; `?groupName` selects the group"},
		capabilities.Capability{Operation: "UpdateSchedule", Category: catSchedules,
			Status: capabilities.StatusSupported, Notes: "`PUT /schedules/{Name}`; `GroupName` in the body; replaces the whole schedule, so an omitted member is unset; rejects a target type Overcast cannot fire"},
		capabilities.Capability{Operation: "DeleteSchedule", Category: catSchedules,
			Status: capabilities.StatusSupported, Notes: "`DELETE /schedules/{Name}`; `?groupName` selects the group"},
		capabilities.Capability{Operation: "ListSchedules", Category: catSchedules,
			Status: capabilities.StatusSupported, Notes: "`GET /schedules`; optional `?ScheduleGroup` filter"},

		// Tags
		capabilities.Capability{Operation: "TagResource", Category: catTags,
			Status: capabilities.StatusSupported, Notes: "Merges tags on ARN, at the shared `/tags/{ResourceArn}` path; refuses the keys and values AWS refuses"},
		capabilities.Capability{Operation: "UntagResource", Category: catTags,
			Status: capabilities.StatusSupported, Notes: "Removes `?TagKeys` from ARN"},
		capabilities.Capability{Operation: "ListTagsForResource", Category: catTags,
			Status: capabilities.StatusSupported, Notes: "Returns the modeled `TagList`, ordered by key"},
	)
}
