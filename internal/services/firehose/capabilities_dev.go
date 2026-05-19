//go:build dev

package firehose

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		// Delivery Streams
		capabilities.Capability{Service: "firehose", Operation: "CreateDeliveryStream", Category: "Delivery Streams", Status: capabilities.StatusSupported, Notes: "Creates a delivery stream"},
		capabilities.Capability{Service: "firehose", Operation: "DescribeDeliveryStream", Category: "Delivery Streams", Status: capabilities.StatusSupported, Notes: "Returns delivery stream details"},
		capabilities.Capability{Service: "firehose", Operation: "ListDeliveryStreams", Category: "Delivery Streams", Status: capabilities.StatusSupported, Notes: "Lists all delivery streams"},
		capabilities.Capability{Service: "firehose", Operation: "DeleteDeliveryStream", Category: "Delivery Streams", Status: capabilities.StatusSupported, Notes:
		// Records
		"Deletes a delivery stream"},

		capabilities.Capability{Service: "firehose", Operation: "PutRecord", Category: "Records", Status: capabilities.StatusSupported, Notes: "Writes a single record to the stream"},
		capabilities.Capability{Service: "firehose", Operation: "PutRecordBatch", Category: "Records", Status: capabilities.StatusSupported, Notes: "Writes multiple records to the stream"},
	)
}
