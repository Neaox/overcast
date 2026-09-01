package pipes

// The record shape each source produces.
//
// A pipe always hands its enrichment and target a JSON array of records, and
// the shape of a record is the source's, not the target's. These builders
// reproduce the shapes AWS documents under "Amazon EventBridge Pipes sources"
// — the same envelopes a Lambda event source mapping produces, minus the
// `Records` wrapper Lambda adds.

import (
	"fmt"
	"time"

	"github.com/overcast-sh/overcast/internal/events"
)

// dynamoDBStreamRecord builds one DynamoDB Streams record.
//
// AWS docs: https://docs.aws.amazon.com/eventbridge/latest/userguide/eb-pipes-dynamodb.html
func dynamoDBStreamRecord(sourceARN string, payload events.DynamoDBStreamPayload) map[string]any {
	// SequenceNumber is zero-padded to 21 digits, matching DynamoDB Streams.
	seqNum := fmt.Sprintf("%021d", payload.SequenceNumber)
	dynamo := map[string]any{
		"ApproximateCreationDateTime": float64(payload.CreatedAt) / 1000.0,
		"SequenceNumber":              seqNum,
		"StreamViewType":              "NEW_AND_OLD_IMAGES",
	}
	if payload.Keys != nil {
		dynamo["Keys"] = payload.Keys
	}
	if payload.NewImage != nil {
		dynamo["NewImage"] = payload.NewImage
	}
	if payload.OldImage != nil {
		dynamo["OldImage"] = payload.OldImage
	}
	return map[string]any{
		"eventID":        seqNum,
		"eventVersion":   "1.1",
		"eventSource":    "aws:dynamodb",
		"eventSourceARN": sourceARN,
		"awsRegion":      regionOrDefault(regionFromARN(sourceARN), "us-east-1"),
		"eventName":      payload.EventName,
		"dynamodb":       dynamo,
	}
}

// kinesisRecord builds one Kinesis stream record.
//
// `data` stays base64 on the wire, as AWS sends it — a []byte marshals that
// way, so a consumer decodes it exactly as it would a real record.
//
// AWS docs: https://docs.aws.amazon.com/eventbridge/latest/userguide/eb-pipes-kinesis.html
func kinesisRecord(sourceARN, roleARN string, rec events.StreamRecord) map[string]any {
	return map[string]any{
		"kinesisSchemaVersion":        "1.0",
		"partitionKey":                rec.PartitionKey,
		"sequenceNumber":              rec.SequenceNumber,
		"data":                        rec.Data,
		"approximateArrivalTimestamp": epochSeconds(rec.ApproximateArrivalTimestamp),
		"eventSource":                 "aws:kinesis",
		"eventVersion":                "1.0",
		"eventID":                     rec.ShardID + ":" + rec.SequenceNumber,
		"eventName":                   "aws:kinesis:record",
		"invokeIdentityArn":           roleARN,
		"awsRegion":                   regionOrDefault(regionFromARN(sourceARN), "us-east-1"),
		"eventSourceARN":              sourceARN,
	}
}

// sqsRecord builds one SQS queue record.
//
// AWS docs: https://docs.aws.amazon.com/eventbridge/latest/userguide/eb-pipes-sqs.html
func sqsRecord(sourceARN string, msg events.ReceivedMessage) map[string]any {
	attributes := msg.Attributes
	if attributes == nil {
		attributes = map[string]string{}
	}
	return map[string]any{
		"messageId":         msg.MessageID,
		"receiptHandle":     msg.ReceiptHandle,
		"body":              msg.Body,
		"attributes":        attributes,
		"messageAttributes": map[string]any{},
		"md5OfBody":         msg.MD5OfBody,
		"eventSource":       "aws:sqs",
		"eventSourceARN":    sourceARN,
		"awsRegion":         regionOrDefault(regionFromARN(sourceARN), "us-east-1"),
	}
}

// epochSeconds renders a timestamp the way AWS renders
// approximateArrivalTimestamp: epoch seconds with millisecond precision.
func epochSeconds(t time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	return float64(t.UnixMilli()) / 1000.0
}
