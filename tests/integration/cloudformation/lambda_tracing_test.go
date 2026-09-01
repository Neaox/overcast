package cloudformation_test

// lambda_tracing_test.go — AWS::Lambda::Function properties the provisioner
// forwards verbatim into CreateFunction/UpdateFunctionConfiguration.
//
// This is the coverage gap that let a 501 regression ship: the provisioner
// gained TracingConfig/EphemeralStorage/KmsKeyArn forwarding in the same change
// that made Lambda reject those members, and no test deployed a template that
// set one. A CDK stack with `tracing` enabled failed with
// `Create Failed lambda CreateFunction: HTTP 501 NotImplemented`.

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

const lambdaTracingTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "TracedFunction": {
      "Type": "AWS::Lambda::Function",
      "Properties": {
        "FunctionName": "cfn-lambda-traced",
        "Runtime": "python3.11",
        "Handler": "index.handler",
        "Role": "arn:aws:iam::000000000000:role/lambda-role",
        "Code": {"ZipFile": "def handler(event, context): return {}"},
        "MemorySize": 1024,
        "Timeout": 120,
        "TracingConfig": {"Mode": "Active"},
        "EphemeralStorage": {"Size": 2048},
        "KmsKeyArn": "arn:aws:kms:us-east-1:000000000000:key/00000000-0000-0000-0000-000000000000"
      }
    }
  }
}`

type cfnLambdaAdvancedConfig struct {
	TracingConfig *struct {
		Mode string `json:"Mode"`
	} `json:"TracingConfig"`
	EphemeralStorage *struct {
		Size int `json:"Size"`
	} `json:"EphemeralStorage"`
	KMSKeyArn string `json:"KMSKeyArn"`
}

// TestCreateStack_LambdaFunctionWithTracingReachesCreateComplete is the
// stack-level guard: a template that enables X-Ray tracing must deploy, and the
// forwarded properties must land on the function.
func TestCreateStack_LambdaFunctionWithTracingReachesCreateComplete(t *testing.T) {
	// Given: a fresh server.
	srv := helpers.NewTestServer(t)

	// When: a stack whose function sets TracingConfig is created.
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"lambda-tracing-stack"},
		"TemplateBody": []string{lambdaTracingTemplate},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the stack completes rather than failing the resource.
	waitForStackStatus(t, srv, "lambda-tracing-stack", "CREATE_COMPLETE")

	// And: the provisioner's forwarded properties reached the function.
	get := lambdaRequest(t, srv, http.MethodGet, "/2015-03-31/functions/cfn-lambda-traced/configuration", nil)
	defer get.Body.Close()
	helpers.AssertStatus(t, get, http.StatusOK)
	var config cfnLambdaAdvancedConfig
	helpers.DecodeJSON(t, get, &config)
	if config.TracingConfig == nil || config.TracingConfig.Mode != "Active" {
		t.Errorf("TracingConfig = %+v, want Mode=Active", config.TracingConfig)
	}
	if config.EphemeralStorage == nil || config.EphemeralStorage.Size != 2048 {
		t.Errorf("EphemeralStorage = %+v, want Size=2048", config.EphemeralStorage)
	}
	if want := "arn:aws:kms:us-east-1:000000000000:key/00000000-0000-0000-0000-000000000000"; config.KMSKeyArn != want {
		t.Errorf("KMSKeyArn = %q, want %q", config.KMSKeyArn, want)
	}
}
