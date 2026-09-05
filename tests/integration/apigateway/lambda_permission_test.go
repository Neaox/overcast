package apigateway_test

// lambda_permission_test.go — API Gateway → Lambda under
// OVERCAST_ENFORCE_LAMBDA_RESOURCE_POLICY.
//
// "If the Lambda API rejects the invocation request, API Gateway returns a 500
// error code… the body of the response from API Gateway is
// {"message": "Internal server error"}", which is what a missing
// apigateway.amazonaws.com statement produces — the single most common cause of
// a 500 from an integration built by anything other than the console.
//
// https://docs.aws.amazon.com/lambda/latest/dg/services-apigateway-errors.html
// https://docs.aws.amazon.com/apigateway/latest/developerguide/http-api-troubleshooting-lambda.html

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// seedLambdaFunction writes a function record straight into the store: these
// assert on whether the integration is authorised to invoke, not on what the
// function returns, so no container runtime is involved.
func seedLambdaFunction(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	arn := "arn:aws:lambda:us-east-1:000000000000:function:" + name
	fn := map[string]any{
		"name": name, "arn": arn, "runtime": "nodejs20.x", "handler": "index.handler",
		"state": "Active", "timeout": 30, "memory_size": 128,
	}
	encoded, err := json.Marshal(fn)
	if err != nil {
		t.Fatalf("marshalling the seeded function: %v", err)
	}
	if err := srv.Store.Set(context.Background(), "lambda:functions", "us-east-1/"+name, string(encoded)); err != nil {
		t.Fatalf("seeding the function: %v", err)
	}
	return arn
}

func addLambdaPermission(t *testing.T, srv *helpers.TestServer, functionName string, body map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshalling the permission: %v", err)
	}
	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+"/2015-03-31/functions/"+functionName+"/policy", bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("AddPermission: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusCreated)
}

// deployLambdaProxyAPI builds a REST API whose /hello GET is an AWS_PROXY
// integration on the named function, deployed to the "test" stage, and returns
// its API ID.
func deployLambdaProxyAPI(t *testing.T, srv *helpers.TestServer, apiName, functionARN string) string {
	t.Helper()
	apiID, rootID := createRestAPIWithRoot(t, srv, apiName)
	resourceID := createResource(t, srv, apiID, rootID, "hello")
	putMethod(t, srv, apiID, resourceID, "GET")
	putIntegration(t, srv, apiID, resourceID, "GET", "AWS_PROXY",
		"arn:aws:apigateway:us-east-1:lambda:path/2015-03-31/functions/"+functionARN+"/invocations")
	deploymentID := createDeployment(t, srv, apiID)
	createStage(t, srv, apiID, deploymentID, "test")
	return apiID
}

func TestExecuteRestAPI_lambdaPermissionMissing(t *testing.T) {
	// Given: enforcement on and an integration whose function grants nothing
	srv := helpers.NewTestServer(t, helpers.WithEnforceLambdaResourcePolicy(true))
	arn := seedLambdaFunction(t, srv, "apigw-unpermitted")
	apiID := deployLambdaProxyAPI(t, srv, "exec-unpermitted", arn)

	// When: the deployed API is called
	resp := apiCall(t, srv, http.MethodGet, "/restapis/"+apiID+"/test/_user_request_/hello", nil)
	defer resp.Body.Close()

	// Then: API Gateway answers 500 with its own configuration-error body
	helpers.AssertStatus(t, resp, http.StatusInternalServerError)
	if body := helpers.ReadBody(t, resp); body != `{"message":"Internal server error"}` {
		t.Errorf("body = %s, want {\"message\":\"Internal server error\"}", body)
	}
}

func TestExecuteRestAPI_lambdaPermissionGranted(t *testing.T) {
	// Given: the permission `aws lambda add-permission` writes for an API
	srv := helpers.NewTestServer(t, helpers.WithEnforceLambdaResourcePolicy(true))
	arn := seedLambdaFunction(t, srv, "apigw-permitted")
	addLambdaPermission(t, srv, "apigw-permitted", map[string]any{
		"StatementId": "apigateway-invoke", "Action": "lambda:InvokeFunction",
		"Principal": "apigateway.amazonaws.com",
		"SourceArn": "arn:aws:execute-api:us-east-1:000000000000:*/*/*/*",
	})
	apiID := deployLambdaProxyAPI(t, srv, "exec-permitted", arn)

	// When: the deployed API is called
	resp := apiCall(t, srv, http.MethodGet, "/restapis/"+apiID+"/test/_user_request_/hello", nil)
	defer resp.Body.Close()

	// Then: the request reaches the integration. The only thing that differs
	// from the refused case above is the permission, so "not 500" is the whole
	// assertion: with no container runtime the function itself then fails, and
	// AWS spells that 502 rather than 500 — "If the function runs but returns
	// an error… API Gateway returns a 502."
	if resp.StatusCode == http.StatusInternalServerError {
		t.Fatalf("authorised integration answered the configuration-error 500: %s", helpers.ReadBody(t, resp))
	}
}

func TestExecuteRestAPI_lambdaPermissionForAnotherAPI(t *testing.T) {
	// Given: a permission scoped to a different API's execute-api ARN
	srv := helpers.NewTestServer(t, helpers.WithEnforceLambdaResourcePolicy(true))
	arn := seedLambdaFunction(t, srv, "apigw-other-api")
	addLambdaPermission(t, srv, "apigw-other-api", map[string]any{
		"StatementId": "apigateway-invoke", "Action": "lambda:InvokeFunction",
		"Principal": "apigateway.amazonaws.com",
		"SourceArn": "arn:aws:execute-api:us-east-1:000000000000:someotherapi/*/*/*",
	})
	apiID := deployLambdaProxyAPI(t, srv, "exec-other-api", arn)

	// When: this API is called
	resp := apiCall(t, srv, http.MethodGet, "/restapis/"+apiID+"/test/_user_request_/hello", nil)
	defer resp.Body.Close()

	// Then: the SourceArn condition does not match
	helpers.AssertStatus(t, resp, http.StatusInternalServerError)
}

func TestExecuteRestAPI_lambdaEnforcementOffNeedsNoPermission(t *testing.T) {
	// Given: the default server, with enforcement off
	srv := helpers.NewTestServer(t)
	arn := seedLambdaFunction(t, srv, "apigw-unenforced")
	apiID := deployLambdaProxyAPI(t, srv, "exec-unenforced", arn)

	// When: the deployed API is called with no permission anywhere
	resp := apiCall(t, srv, http.MethodGet, "/restapis/"+apiID+"/test/_user_request_/hello", nil)
	defer resp.Body.Close()

	// Then: nothing is refused for want of a permission
	if resp.StatusCode == http.StatusInternalServerError {
		t.Fatalf("enforcement is off but the integration answered 500: %s", helpers.ReadBody(t, resp))
	}
}
