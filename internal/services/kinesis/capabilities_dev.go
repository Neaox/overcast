//go:build dev

package kinesis

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		capabilities.Capability{Service: "kinesis", Operation: "AddTagsToStream", Category: "General", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "kinesis", Operation: "CreateStream", Category: "General", Status: capabilities.StatusSupported, Notes: "Stream becomes ACTIVE immediately"},
		capabilities.Capability{Service: "kinesis", Operation: "DecreaseStreamRetentionPeriod", Category: "General", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "kinesis", Operation: "DeleteStream", Category: "General", Status: capabilities.StatusSupported, Notes: "Also removes all stored records"},
		capabilities.Capability{Service: "kinesis", Operation: "DescribeStream", Category: "General", Status: capabilities.StatusSupported, Notes: "Returns full Shards list"},
		capabilities.Capability{Service: "kinesis", Operation: "DescribeStreamSummary", Category: "General", Status: capabilities.StatusSupported, Notes: "Lightweight summary without shard detail"},
		capabilities.Capability{Service: "kinesis", Operation: "GetRecords", Category: "General", Status: capabilities.StatusSupported, Notes: "Returns stored records and a valid NextShardIterator"},
		capabilities.Capability{Service: "kinesis", Operation: "GetShardIterator", Category: "General", Status: capabilities.StatusSupported, Notes: "Supports TRIM_HORIZON, LATEST, AT/AFTER_SEQUENCE_NUMBER"},
		capabilities.Capability{Service: "kinesis", Operation: "IncreaseStreamRetentionPeriod", Category: "General", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "kinesis", Operation: "ListShards", Category: "General", Status: capabilities.StatusSupported, Notes: "Returns active (open) shards only; no pagination"},
		capabilities.Capability{Service: "kinesis", Operation: "ListStreams", Category: "General", Status: capabilities.StatusSupported, Notes: "Returns all stream names; no pagination"},
		capabilities.Capability{Service: "kinesis", Operation: "ListTagsForStream", Category: "General", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "kinesis", Operation: "MergeShards", Category: "General", Status: capabilities.StatusSupported, Notes: "Closes both parents, creates merged child shard"},
		capabilities.Capability{Service: "kinesis", Operation: "PutRecord", Category: "General", Status: capabilities.StatusSupported, Notes: "Routes by partition key hash"},
		capabilities.Capability{Service: "kinesis", Operation: "PutRecords", Category: "General", Status: capabilities.StatusSupported, Notes: "Returns FailedRecordCount=0 for all records"},
		capabilities.Capability{Service: "kinesis", Operation: "RemoveTagsFromStream", Category: "General", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "kinesis", Operation: "SplitShard", Category: "General", Status: capabilities.StatusSupported, Notes: "Closes parent, creates two children at NewStartingHashKey"},
	)
}
