//go:build dev

package dynamodbstreams

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		capabilities.Capability{Service: "dynamodbstreams", Operation: "DescribeStream", Category: "General", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "dynamodbstreams", Operation: "GetRecords", Category: "General", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "dynamodbstreams", Operation: "GetShardIterator", Category: "General", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "dynamodbstreams", Operation: "ListStreams", Category: "General", Status: capabilities.StatusSupported},
	)
}
