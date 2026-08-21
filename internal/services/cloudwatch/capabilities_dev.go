//go:build dev

package cloudwatch

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		capabilities.Capability{Service: "cloudwatch", Operation: "DeleteAlarms", Category: "General", Status: capabilities.StatusSupported, Notes: "Deletes one or more alarms by name, along with their history"},
		capabilities.Capability{Service: "cloudwatch", Operation: "DescribeAlarmHistory", Category: "General", Status: capabilities.StatusSupported, Notes: "Returns StateUpdate, ConfigurationUpdate and Action items; filters by alarm name, item type, date range, MaxRecords and ScanBy. The most recent 100 items per alarm are retained"},
		capabilities.Capability{Service: "cloudwatch", Operation: "DescribeAlarms", Category: "General", Status: capabilities.StatusSupported, Notes: "Lists alarms, supports filtering; reports the evaluator's live StateValue, StateReason and StateReasonData"},
		capabilities.Capability{Service: "cloudwatch", Operation: "DescribeAlarmsForMetric", Category: "General", Status: capabilities.StatusSupported, Notes: "Lists alarms for a specific metric"},
		capabilities.Capability{Service: "cloudwatch", Operation: "DisableAlarmActions", Category: "General", Status: capabilities.StatusSupported, Notes: "Clears ActionsEnabled so transitions stop firing actions"},
		capabilities.Capability{Service: "cloudwatch", Operation: "EnableAlarmActions", Category: "General", Status: capabilities.StatusSupported, Notes: "Sets ActionsEnabled so transitions fire actions again"},
		capabilities.Capability{Service: "cloudwatch", Operation: "GetMetricData", Category: "General", Status: capabilities.StatusSupported, Notes: "Returns query-based metric values over time ranges, over both the Query and JSON protocols — the pinned model makes awsJson1_0 primary, so an SDK reaching it that way now gets the same result the Query protocol always returned (#886)"},
		capabilities.Capability{Service: "cloudwatch", Operation: "GetMetricStatistics", Category: "General", Status: capabilities.StatusSupported, Notes: "Returns aggregated datapoints by period"},
		capabilities.Capability{Service: "cloudwatch", Operation: "ListMetrics", Category: "General", Status: capabilities.StatusSupported, Notes: "Lists available metrics"},
		capabilities.Capability{Service: "cloudwatch", Operation: "ListTagsForResource", Category: "General", Status: capabilities.StatusSupported, Notes: "Lists tags for an alarm, over both the Query and JSON protocols. Alarms are the only taggable CloudWatch resource Overcast emulates, so any other ResourceARN is ResourceNotFoundException — or InvalidParameterValue when it is not a CloudWatch ARN at all"},
		capabilities.Capability{Service: "cloudwatch", Operation: "PutMetricAlarm", Category: "General", Status: capabilities.StatusSupported, Notes: "Creates or updates a single-metric alarm, which is then evaluated automatically (Threshold, ComparisonOperator, Period, EvaluationPeriods, DatapointsToAlarm, Dimensions, TreatMissingData). Metric-math/multi-metric alarms, anomaly detection (ThresholdMetricId) and extended statistics are created but never evaluated, and say so in the alarm's StateReason and an x-overcast-emulation-limitation response header; PromQL alarms (EvaluationCriteria) are still refused with 501. Tags are applied at creation only and validated against the same rules as TagResource, so a create carrying an invalid tag set fails rather than leaving an untagged alarm"},
		capabilities.Capability{Service: "cloudwatch", Operation: "PutMetricData", Category: "General", Status: capabilities.StatusSupported, Notes: "Publishes metric data points"},
		capabilities.Capability{Service: "cloudwatch", Operation: "SetAlarmState", Category: "General", Status: capabilities.StatusSupported, Notes: "Forces an alarm's state and fires that state's actions; the forced state is held against the evaluator for one evaluation range"},
		capabilities.Capability{Service: "cloudwatch", Operation: "PutAnomalyDetector", Category: "General", Status: capabilities.StatusUnsupported, Notes: "stub; returns 501 — there is no anomaly-detection model behind the emulator"},
		capabilities.Capability{Service: "cloudwatch", Operation: "PutCompositeAlarm", Category: "General", Status: capabilities.StatusUnsupported, Notes: "stub; returns 501 — composite alarm rules are not evaluated"},
		capabilities.Capability{Service: "cloudwatch", Operation: "TagResource", Category: "General", Status: capabilities.StatusSupported, Notes: "Adds or updates tags on an alarm, over both the Query and JSON protocols. Tagging an alarm that does not exist is ResourceNotFoundException, as on AWS. Tag sets are validated — 50 tags per resource, keys 1-128 characters and not aws:-prefixed, values up to 256 — and a rejected set is not written"},
		capabilities.Capability{Service: "cloudwatch", Operation: "UntagResource", Category: "General", Status: capabilities.StatusSupported, Notes: "Removes tags from an alarm, over both the Query and JSON protocols. A key that is not present is ignored; an alarm that does not exist is ResourceNotFoundException"},
	)
}
