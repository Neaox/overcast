package cloudformation_test

// lambda_dead_letter_test.go — AWS::Lambda::Function's DeadLetterConfig, at the
// altitude where the refusal actually hurt.
//
// The provisioner forwards DeadLetterConfig into both CreateFunction and
// UpdateFunctionConfiguration, and both used to answer 501 for it. Under
// CloudFormation that failed the resource, and with it the stack and the whole
// `cdk deploy`. This is the same coverage gap lambda_tracing_test.go was
// written for, with one addition it did not need: the update path is exercised
// too. Implementing only create would let the first deploy succeed and fail the
// redeploy that touches the function's configuration — a trap that reads as a
// new bug rather than as half a fix.

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

const lambdaDeadLetterTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "FailureQueue": {
      "Type": "AWS::SQS::Queue",
      "Properties": {"QueueName": "cfn-lambda-dlq"}
    },
    "DeadLetteredFunction": {
      "Type": "AWS::Lambda::Function",
      "Properties": {
        "FunctionName": "cfn-lambda-dead-lettered",
        "Runtime": "python3.11",
        "Handler": "index.handler",
        "Role": "arn:aws:iam::000000000000:role/lambda-role",
        "Code": {"ZipFile": "def handler(event, context): return {}"},
        "MemorySize": 512,
        "DeadLetterConfig": {"TargetArn": "arn:aws:sqs:us-east-1:000000000000:cfn-lambda-dlq"}
      }
    }
  }
}`

type cfnLambdaDeadLetterConfig struct {
	DeadLetterConfig *struct {
		TargetArn string `json:"TargetArn"`
	} `json:"DeadLetterConfig"`
	MemorySize int `json:"MemorySize"`
}

func TestCreateStack_LambdaFunctionWithADeadLetterQueueDeploysAndRedeploys(t *testing.T) {
	// Given: a fresh server.
	srv := helpers.NewTestServer(t)
	const stackName = "lambda-dlq-stack"
	const targetARN = "arn:aws:sqs:us-east-1:000000000000:cfn-lambda-dlq"

	// When: a stack whose function names a dead-letter queue is created.
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {lambdaDeadLetterTemplate},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)

	// Then: the stack completes rather than failing the resource.
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	if got := stackLambdaDeadLetterConfig(t, srv); got.DeadLetterConfig == nil || got.DeadLetterConfig.TargetArn != targetARN {
		t.Fatalf("DeadLetterConfig after create = %+v, want TargetArn %s", got.DeadLetterConfig, targetARN)
	}

	// When: the stack is redeployed with an unrelated configuration change, so
	// the provisioner sends UpdateFunctionConfiguration — carrying
	// DeadLetterConfig with it, as it does for every forwarded property.
	update := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {strings.Replace(lambdaDeadLetterTemplate, `"MemorySize": 512`, `"MemorySize": 1024`, 1)},
	})
	defer update.Body.Close()
	helpers.AssertStatus(t, update, http.StatusOK)

	// Then: the update completes too, and the target survives it.
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")
	got := stackLambdaDeadLetterConfig(t, srv)
	if got.MemorySize != 1024 {
		t.Errorf("MemorySize after update = %d, want 1024", got.MemorySize)
	}
	if got.DeadLetterConfig == nil || got.DeadLetterConfig.TargetArn != targetARN {
		t.Errorf("DeadLetterConfig after update = %+v, want TargetArn %s", got.DeadLetterConfig, targetARN)
	}
}

func stackLambdaDeadLetterConfig(t *testing.T, srv *helpers.TestServer) cfnLambdaDeadLetterConfig {
	t.Helper()
	get := lambdaRequest(t, srv, http.MethodGet, "/2015-03-31/functions/cfn-lambda-dead-lettered/configuration", nil)
	defer get.Body.Close()
	helpers.AssertStatus(t, get, http.StatusOK)
	var config cfnLambdaDeadLetterConfig
	helpers.DecodeJSON(t, get, &config)
	return config
}
