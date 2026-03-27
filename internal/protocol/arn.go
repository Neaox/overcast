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

// TableARN builds a DynamoDB table ARN.
func TableARN(region, accountID, tableName string) string {
	return ARN(region, accountID, "dynamodb", "table/"+tableName)
}
