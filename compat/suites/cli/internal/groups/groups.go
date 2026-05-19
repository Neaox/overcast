// Package groups assembles all service group implementations for the CLI suite.
package groups

import (
	"context"

	"github.com/Neaox/overcast-compat-cli/internal/harness"
)

// ServiceGroup bundles the impls, setup, and teardown maps for one service.
type ServiceGroup struct {
	Impls    map[string]harness.TestFn
	Setup    map[string]func(context.Context, *harness.TestContext) error
	Teardown map[string]func(context.Context, *harness.TestContext) error
}

// All returns all service groups.
func All() []ServiceGroup {
	return []ServiceGroup{
		S3(),
		SQS(),
		DynamoDB(),
		SNS(),
		Lambda(),
		CloudWatchLogs(),
		SES(),
		IAM(),
		STS(),
		SecretsManager(),
		KMS(),
		SSM(),
		Kinesis(),
		EventBridge(),
		CloudFormation(),
		EC2(),
		ECS(),
		Cognito(),
		AppSync(),
		APIGateway(),
		CloudFront(),
		RDS(),
		StepFunctions(),
		WAF(),
		Shield(),
		ElastiCache(),
	}
}
