package cloudformation_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// A Lambda whose S3 deployment package cannot be fetched must fail its
// CreateFunction, and CloudFormation must surface that as an ordinary resource
// failure — no S3 validation of its own, no stack that reports CREATE_COMPLETE
// over a function with no code.
func TestCreateStack_LambdaMissingS3PackageFailsStack(t *testing.T) {
	// Given: a bucket that holds a different object than the template names.
	srv := helpers.NewTestServer(t)
	s3PutObject(t, srv, "cfn-lambda-missing-package", "present.zip", "unused-code")
	stackName := "lambda-missing-package"
	template := `{"Resources":{"Function":{"Type":"AWS::Lambda::Function","Properties":{"FunctionName":"lambda-missing-package-fn","Runtime":"python3.11","Handler":"index.handler","Role":"arn:aws:iam::000000000000:role/lambda-role","Code":{"S3Bucket":"cfn-lambda-missing-package","S3Key":"absent.zip"}}}}}`

	// When: the stack is created.
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{template},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the stack rolls back rather than completing.
	waitForStackStatus(t, srv, stackName, "ROLLBACK_COMPLETE")

	// And: the Lambda failure reaches the stack events verbatim.
	events := cfnQuery(t, srv, "DescribeStackEvents", url.Values{"StackName": []string{stackName}})
	defer events.Body.Close()
	helpers.AssertStatus(t, events, http.StatusOK)
	body := string(readBody(t, events))
	if !strings.Contains(body, "CreateFunction") {
		t.Fatalf("stack events do not mention the failing Lambda call:\n%s", body)
	}

	// And: no function was left behind.
	fnResp := lambdaRequest(t, srv, http.MethodGet, "/2015-03-31/functions/lambda-missing-package-fn", nil)
	defer fnResp.Body.Close()
	helpers.AssertStatus(t, fnResp, http.StatusNotFound)
}
