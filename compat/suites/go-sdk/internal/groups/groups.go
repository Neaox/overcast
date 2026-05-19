// Package groups assembles all service group implementations for the Go SDK suite.
package groups

import (
	"context"

	"github.com/Neaox/overcast-compat-go-sdk/internal/clients"
	"github.com/Neaox/overcast-compat-go-sdk/internal/harness"
)

// ServiceGroup bundles the impls, setup, and teardown maps for one service.
type ServiceGroup struct {
	Impls    map[string]harness.TestFn
	Setup    map[string]func(context.Context, *harness.TestContext) error
	Teardown map[string]func(context.Context, *harness.TestContext) error
}

// All returns all service groups.
func All(c *clients.Clients) []ServiceGroup {
	return []ServiceGroup{
		S3(c),
		SQS(c),
		DynamoDB(c),
		SNS(c),
		Lambda(c),
		CloudWatchLogs(c),
		SES(c),
		IAM(c),
		STS(c),
		SecretsManager(c),
		KMS(c),
		SSM(c),
		Kinesis(c),
		EventBridge(c),
		CloudFormation(c),
		EC2(c),
		ECS(c),
		Cognito(c),
		AppSync(c),
		APIGateway(c),
		CloudFront(c),
		RDS(c),
		StepFunctions(c),
		WAF(c),
		Shield(c),
	}
}
