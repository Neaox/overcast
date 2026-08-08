package cloudformation_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// CloudFormation dispatches AWS::Lambda::Function through the Lambda API, so
// the runtime catalog governs it too: a runtime CreateFunction accepts must
// provision, and one AWS has blocked must fail the stack with the same message
// rather than a CloudFormation-specific invention.
func TestCreateStack_LambdaRuntimeLifecycleMatchesLambdaAPI(t *testing.T) {
	t.Run("current runtime provisions", func(t *testing.T) {
		srv := helpers.NewTestServer(t)
		template := `{"Resources":{"Function":{"Type":"AWS::Lambda::Function","Properties":{` +
			`"FunctionName":"cfn-runtime-current","Runtime":"python3.14","Handler":"index.handler",` +
			`"Role":"arn:aws:iam::000000000000:role/lambda-role",` +
			`"Code":{"ZipFile":"def handler(event, context): return {}"}}}}}`
		createLambdaStack(t, srv, "lambda-runtime-current", template)

		fn := lambdaRequest(t, srv, http.MethodGet, "/2015-03-31/functions/cfn-runtime-current", nil)
		defer fn.Body.Close()
		helpers.AssertStatus(t, fn, http.StatusOK)
		if body := string(readBody(t, fn)); !strings.Contains(body, `"Runtime":"python3.14"`) {
			t.Errorf("provisioned function does not report python3.14: %s", body)
		}
	})

	t.Run("create-blocked runtime fails the stack with the Lambda message", func(t *testing.T) {
		srv := helpers.NewTestServer(t)
		stackName := "lambda-runtime-blocked"
		template := `{"Resources":{"Function":{"Type":"AWS::Lambda::Function","Properties":{` +
			`"FunctionName":"cfn-runtime-blocked","Runtime":"nodejs14.x","Handler":"index.handler",` +
			`"Role":"arn:aws:iam::000000000000:role/lambda-role",` +
			`"Code":{"ZipFile":"exports.handler = async () => ({})"}}}}}`

		resp := cfnQuery(t, srv, "CreateStack", url.Values{
			"StackName":    []string{stackName},
			"TemplateBody": []string{template},
		})
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusOK)
		waitForStackStatus(t, srv, stackName, "ROLLBACK_COMPLETE")

		events := cfnQuery(t, srv, "DescribeStackEvents", url.Values{"StackName": []string{stackName}})
		defer events.Body.Close()
		helpers.AssertStatus(t, events, http.StatusOK)
		if body := string(readBody(t, events)); !strings.Contains(body, "is no longer supported for creating or updating AWS Lambda functions") {
			t.Fatalf("stack events do not carry the Lambda deprecation message:\n%s", body)
		}

		fn := lambdaRequest(t, srv, http.MethodGet, "/2015-03-31/functions/cfn-runtime-blocked", nil)
		defer fn.Body.Close()
		helpers.AssertStatus(t, fn, http.StatusNotFound)
	})
}
