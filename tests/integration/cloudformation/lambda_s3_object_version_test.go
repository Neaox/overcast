package cloudformation_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func TestCreateStack_LambdaS3ObjectVersionFailsLoudlyBeforeCreation(t *testing.T) {
	// Given: a Lambda resource whose code selects an S3 object version that
	// Overcast cannot resolve until S3 version-history support exists (#645).
	srv := helpers.NewTestServer(t)
	template := `{"Resources":{"Function":{"Type":"AWS::Lambda::Function","Properties":{"FunctionName":"cfn-versioned-code","Runtime":"python3.11","Handler":"index.handler","Role":"arn:aws:iam::000000000000:role/lambda-role","Code":{"S3Bucket":"code-bucket","S3Key":"function.zip","S3ObjectVersion":"version-1"}}}}}`

	// When: CloudFormation forwards the property through its thin adapter.
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":       {"lambda-versioned-code-stack"},
		"TemplateBody":    {template},
		"DisableRollback": {"true"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, "lambda-versioned-code-stack", "CREATE_FAILED")

	// Then: the unsupported service request is visible in stack events, and no
	// partially configured function remains behind.
	events := cfnQuery(t, srv, "DescribeStackEvents", url.Values{
		"StackName": {"lambda-versioned-code-stack"},
	})
	defer events.Body.Close()
	helpers.AssertStatus(t, events, http.StatusOK)
	if body := string(readBody(t, events)); !strings.Contains(body, "HTTP 501") || !strings.Contains(body, "NotImplemented") {
		t.Fatalf("stack events do not expose unsupported Lambda code version: %s", body)
	}

	configuration := lambdaRequest(t, srv, http.MethodGet,
		"/2015-03-31/functions/cfn-versioned-code/configuration", nil)
	defer configuration.Body.Close()
	helpers.AssertStatus(t, configuration, http.StatusNotFound)
}
