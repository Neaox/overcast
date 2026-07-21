//go:build dev

package logs

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		// Log groups
		capabilities.Capability{Service: "cloudwatch-logs", Operation: "CreateLogGroup", Category: "Log groups", Status: capabilities.StatusSupported, Notes: "Validates name; returns error on duplicate"},
		capabilities.Capability{Service: "cloudwatch-logs", Operation: "DescribeLogGroups", Category: "Log groups", Status: capabilities.StatusSupported, Notes: "Optional `logGroupNamePrefix` filter"},
		capabilities.Capability{Service: "cloudwatch-logs", Operation: "DeleteLogGroup", Category: "Log groups", Status: capabilities.StatusSupported, Notes: "Deletes group and all streams/events"},
		// Log streams
		capabilities.Capability{Service: "cloudwatch-logs", Operation: "CreateLogStream", Category: "Log streams", Status: capabilities.StatusSupported, Notes: "Validates group exists; returns error on duplicate"},
		capabilities.Capability{Service: "cloudwatch-logs", Operation: "DescribeLogStreams", Category: "Log streams", Status: capabilities.StatusSupported, Notes: "Optional `logStreamNamePrefix` filter"},
		capabilities.Capability{Service: "cloudwatch-logs", Operation: "DeleteLogStream", Category: "Log streams", Status: capabilities.StatusSupported, Notes: "Deletes stream and all its events"},
		// Log events
		capabilities.Capability{Service: "cloudwatch-logs", Operation: "PutLogEvents", Category: "Log events", Status: capabilities.StatusSupported, Notes: "Accepts batch of events; sets ingestion time"},
		capabilities.Capability{Service: "cloudwatch-logs", Operation: "GetLogEvents", Category: "Log events", Status: capabilities.StatusSupported, Notes: "startTime/endTime filtering; startFromHead"},
		capabilities.Capability{Service: "cloudwatch-logs", Operation: "FilterLogEvents", Category: "Log events", Status: capabilities.StatusSupported, Notes: "Text patterns (AND, quoted, ?OR), JSON patterns (`{ $.field op value }` with `&&`/`||`, EXISTS, IS NULL), space-delimited patterns (`[col, col = val, ...]` with `*` glob, `%regex%`, numeric ops, `&&`/`||`, ellipsis); time range, stream name/prefix"},
		capabilities.Capability{Service: "cloudwatch-logs", Operation: "StartLiveTail", Category: "Log events", Status: capabilities.StatusSupported, Notes: "AWS event-stream response with sessionStart/sessionUpdate; supports group identifiers, stream names/prefixes, and filter patterns"},
		// Insights
		capabilities.Capability{Service: "cloudwatch-logs", Operation: "StartQuery", Category: "Insights", Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "cloudwatch-logs", Operation: "GetQueryResults", Category: "Insights", Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "cloudwatch-logs", Operation: "PutMetricFilter", Category: "Insights", Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		// Retention
		capabilities.Capability{Service: "cloudwatch-logs", Operation: "PutRetentionPolicy", Category: "Retention", Status: capabilities.StatusSupported, Notes: "Sets retentionInDays on log group"},
		capabilities.Capability{Service: "cloudwatch-logs", Operation: "DeleteRetentionPolicy", Category: "Retention", Status: capabilities.StatusSupported, Notes: "Clears retention (sets to 0)"},
		capabilities.Capability{Service: "cloudwatch-logs", Operation: "PutSubscriptionFilter", Category: "Retention", Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		// Tagging
		capabilities.Capability{Service: "cloudwatch-logs", Operation: "TagLogGroup", Category: "Tagging", Status: capabilities.StatusSupported, Notes: "Adds tags to a log group"},
		capabilities.Capability{Service: "cloudwatch-logs", Operation: "UntagLogGroup", Category: "Tagging", Status: capabilities.StatusSupported, Notes: "Removes tags from a log group"},
		capabilities.Capability{Service: "cloudwatch-logs", Operation: "ListTagsLogGroup", Category: "Tagging", Status: capabilities.StatusSupported, Notes: "Returns tags for a log group"},
	)
}
