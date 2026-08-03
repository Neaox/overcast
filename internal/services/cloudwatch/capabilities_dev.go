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
		capabilities.Capability{Service: "cloudwatch", Operation: "GetMetricData", Category: "General", Status: capabilities.StatusSupported, Notes: "Returns query-based metric values over time ranges"},
		capabilities.Capability{Service: "cloudwatch", Operation: "GetMetricStatistics", Category: "General", Status: capabilities.StatusSupported, Notes: "Returns aggregated datapoints by period"},
		capabilities.Capability{Service: "cloudwatch", Operation: "ListMetrics", Category: "General", Status: capabilities.StatusSupported, Notes: "Lists available metrics"},
		capabilities.Capability{Service: "cloudwatch", Operation: "ListTagsForResource", Category: "General", Status: capabilities.StatusSupported, Notes: "Lists tags for an alarm"},
		capabilities.Capability{Service: "cloudwatch", Operation: "PutMetricAlarm", Category: "General", Status: capabilities.StatusSupported, Notes: "Creates or updates a single-metric alarm, which is then evaluated automatically (Threshold, ComparisonOperator, Period, EvaluationPeriods, DatapointsToAlarm, Dimensions, TreatMissingData). Metric-math/multi-metric alarms, anomaly detection (ThresholdMetricId) and extended statistics are refused with 501 rather than created un-evaluated"},
		capabilities.Capability{Service: "cloudwatch", Operation: "PutMetricData", Category: "General", Status: capabilities.StatusSupported, Notes: "Publishes metric data points"},
		capabilities.Capability{Service: "cloudwatch", Operation: "SetAlarmState", Category: "General", Status: capabilities.StatusSupported, Notes: "Forces an alarm's state and fires that state's actions; the forced state is held against the evaluator for one evaluation range"},
		capabilities.Capability{Service: "cloudwatch", Operation: "PutAnomalyDetector", Category: "General", Status: capabilities.StatusUnsupported, Notes: "stub; returns 501 — there is no anomaly-detection model behind the emulator"},
		capabilities.Capability{Service: "cloudwatch", Operation: "PutCompositeAlarm", Category: "General", Status: capabilities.StatusUnsupported, Notes: "stub; returns 501 — composite alarm rules are not evaluated"},
		capabilities.Capability{Service: "cloudwatch", Operation: "TagResource", Category: "General", Status: capabilities.StatusSupported, Notes: "Adds or updates tags on an alarm"},
		capabilities.Capability{Service: "cloudwatch", Operation: "UntagResource", Category: "General", Status: capabilities.StatusSupported, Notes: "Removes tags from an alarm"},
	)
}
