//go:build dev

package dynamodbstreams

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		capabilities.Capability{Service: "dynamodbstreams", Operation: "DescribeStream", Category: "General", Status: capabilities.StatusSupported, Notes: "A stream ARN from another region is a `ResourceNotFoundException`, as on AWS's regional endpoints"},
		capabilities.Capability{Service: "dynamodbstreams", Operation: "GetRecords", Category: "General", Status: capabilities.StatusSupported, Notes: "Reads the stream in the region its shard iterator names; each record carries `awsRegion`"},
		capabilities.Capability{Service: "dynamodbstreams", Operation: "GetShardIterator", Category: "General", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "dynamodbstreams", Operation: "ListStreams", Category: "General", Status: capabilities.StatusSupported, Notes: "Region-scoped — reports only streams for tables in the request's region"},
	)
}
