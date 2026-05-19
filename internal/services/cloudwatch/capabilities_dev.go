//go:build dev

package cloudwatch

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		capabilities.Capability{Service: "cloudwatch", Operation: "DeleteAlarms", Category: "General", Status: capabilities.StatusSupported, Notes: "Deletes one or more alarms by name"},
		capabilities.Capability{Service: "cloudwatch", Operation: "DescribeAlarms", Category: "General", Status: capabilities.StatusSupported, Notes: "Lists alarms, supports filtering"},
		capabilities.Capability{Service: "cloudwatch", Operation: "DescribeAlarmsForMetric", Category: "General", Status: capabilities.StatusSupported, Notes: "Lists alarms for a specific metric"},
		capabilities.Capability{Service: "cloudwatch", Operation: "GetMetricData", Category: "General", Status: capabilities.StatusSupported, Notes: "Returns query-based metric values over time ranges"},
		capabilities.Capability{Service: "cloudwatch", Operation: "GetMetricStatistics", Category: "General", Status: capabilities.StatusSupported, Notes: "Returns aggregated datapoints by period"},
		capabilities.Capability{Service: "cloudwatch", Operation: "ListMetrics", Category: "General", Status: capabilities.StatusSupported, Notes: "Lists available metrics"},
		capabilities.Capability{Service: "cloudwatch", Operation: "ListTagsForResource", Category: "General", Status: capabilities.StatusSupported, Notes: "Lists tags for an alarm"},
		capabilities.Capability{Service: "cloudwatch", Operation: "PutMetricAlarm", Category: "General", Status: capabilities.StatusSupported, Notes: "Creates or updates an alarm"},
		capabilities.Capability{Service: "cloudwatch", Operation: "PutMetricData", Category: "General", Status: capabilities.StatusSupported, Notes: "Publishes metric data points"},
		capabilities.Capability{Service: "cloudwatch", Operation: "SetAlarmState", Category: "General", Status: capabilities.StatusSupported, Notes: "Manually sets the state of an alarm"},
		capabilities.Capability{Service: "cloudwatch", Operation: "TagResource", Category: "General", Status: capabilities.StatusSupported, Notes: "Adds or updates tags on an alarm"},
		capabilities.Capability{Service: "cloudwatch", Operation: "UntagResource", Category: "General", Status: capabilities.StatusSupported, Notes: "Removes tags from an alarm"},
	)
}
