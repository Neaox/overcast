package protocol

import "fmt"

// ARN builds an AWS ARN string.
// Format: arn:aws:<service>:<region>:<accountID>:<resource>
//
// Examples:
//
//	ARN("us-east-1", "000000000000", "s3", "my-bucket")
//	  → "arn:aws:s3:::my-bucket"  (S3 omits region and account)
//
//	ARN("us-east-1", "000000000000", "sqs", "my-queue")
//	  → "arn:aws:sqs:us-east-1:000000000000:my-queue"
func ARN(region, accountID, service, resource string) string {
	// S3 ARNs omit region and account ID — this is an AWS quirk.
	if service == "s3" {
		return fmt.Sprintf("arn:aws:s3:::%s", resource)
	}
	return fmt.Sprintf("arn:aws:%s:%s:%s:%s", service, region, accountID, resource)
}

// QueueARN builds an SQS queue ARN from its components.
func QueueARN(region, accountID, queueName string) string {
	return ARN(region, accountID, "sqs", queueName)
}

// TopicARN builds an SNS topic ARN.
func TopicARN(region, accountID, topicName string) string {
	return ARN(region, accountID, "sns", topicName)
}

// LambdaARN builds a Lambda function ARN.
func LambdaARN(region, accountID, functionName string) string {
	return ARN(region, accountID, "lambda", "function:"+functionName)
}

// LambdaVersionARN builds a Lambda function ARN with a numeric version qualifier.
// Format: arn:aws:lambda:{region}:{account}:function:{name}:{version}.
func LambdaVersionARN(region, accountID, functionName string, version int) string {
	return fmt.Sprintf("arn:aws:lambda:%s:%s:function:%s:%d", region, accountID, functionName, version)
}

// LayerVersionARN builds a Lambda layer version ARN.
// Format: arn:aws:lambda:{region}:{account}:layer:{name}:{version}.
func LayerVersionARN(region, accountID, layerName string, version int) string {
	return fmt.Sprintf("arn:aws:lambda:%s:%s:layer:%s:%d", region, accountID, layerName, version)
}

// LayerARN builds the unversioned Lambda layer ARN (no version suffix).
// Format: arn:aws:lambda:{region}:{account}:layer:{name}.
func LayerARN(region, accountID, layerName string) string {
	return fmt.Sprintf("arn:aws:lambda:%s:%s:layer:%s", region, accountID, layerName)
}

// TableARN builds a DynamoDB table ARN.
func TableARN(region, accountID, tableName string) string {
	return ARN(region, accountID, "dynamodb", "table/"+tableName)
}

// LogGroupARN builds a CloudWatch Logs log group ARN.
func LogGroupARN(region, accountID, groupName string) string {
	return fmt.Sprintf("arn:aws:logs:%s:%s:log-group:%s:*", region, accountID, groupName)
}

// LogStreamARN builds a CloudWatch Logs log stream ARN.
func LogStreamARN(region, accountID, groupName, streamName string) string {
	return fmt.Sprintf("arn:aws:logs:%s:%s:log-group:%s:log-stream:%s", region, accountID, groupName, streamName)
}

// RestAPIARN builds an API Gateway REST API ARN.
// API Gateway ARNs omit the account ID — this is an AWS quirk.
// Format: arn:aws:apigateway:{region}::/restapis/{apiId}.
func RestAPIARN(region, apiID string) string {
	return fmt.Sprintf("arn:aws:apigateway:%s::/restapis/%s", region, apiID)
}

// APIV2ARN builds an API Gateway v2 (HTTP/WebSocket) API ARN.
// Format: arn:aws:apigateway:{region}::/apis/{apiId}.
func APIV2ARN(region, apiID string) string {
	return fmt.Sprintf("arn:aws:apigateway:%s::/apis/%s", region, apiID)
}

// DistributionARN builds a CloudFront distribution ARN.
// CloudFront ARNs omit the region — this is an AWS quirk.
// Format: arn:aws:cloudfront::{accountID}:distribution/{distributionID}.
func DistributionARN(accountID, distributionID string) string {
	return fmt.Sprintf("arn:aws:cloudfront::%s:distribution/%s", accountID, distributionID)
}
