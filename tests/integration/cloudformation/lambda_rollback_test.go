package cloudformation_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

func TestUpdateStack_LaterResourceFailureRestoresLambdaInPlaceUpdate(t *testing.T) {
	// Given: Lambda is updated before a dependent S3 resource.
	srv := helpers.NewTestServer(t)
	stackName := "lambda-stack-rollback"
	createTemplate := `{"Resources":{"Function":{"Type":"AWS::Lambda::Function","Properties":{"FunctionName":"lambda-stack-rollback-fn","Runtime":"python3.11","Handler":"index.handler","Role":"arn:aws:iam::000000000000:role/lambda-role","Code":{"ZipFile":"def handler(event, context): return {}"},"Description":"original"}},"Later":{"Type":"AWS::S3::Bucket","DependsOn":"Function","Properties":{"BucketName":"lambda-stack-rollback-later","VersioningConfiguration":{"Status":"Enabled"}}}}}`
	createLambdaStack(t, srv, stackName, createTemplate)

	// When: Lambda changes successfully but the later S3 update is invalid.
	updateTemplate := `{"Resources":{"Function":{"Type":"AWS::Lambda::Function","Properties":{"FunctionName":"lambda-stack-rollback-fn","Runtime":"python3.11","Handler":"index.handler","Role":"arn:aws:iam::000000000000:role/lambda-role","Code":{"ZipFile":"def handler(event, context): return {}"},"Description":"attempted"}},"Later":{"Type":"AWS::S3::Bucket","DependsOn":"Function","Properties":{"BucketName":"lambda-stack-rollback-later","VersioningConfiguration":{"Status":"Invalid"}}}}}`
	resp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {updateTemplate},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_ROLLBACK_COMPLETE")

	// Then: shared CloudFormation rollback replays Lambda's updater in reverse.
	configResp := lambdaRequest(t, srv, http.MethodGet,
		"/2015-03-31/functions/lambda-stack-rollback-fn/configuration", nil)
	defer configResp.Body.Close()
	helpers.AssertStatus(t, configResp, http.StatusOK)
	var config struct {
		Description string `json:"Description"`
	}
	helpers.DecodeJSON(t, configResp, &config)
	if config.Description != "original" {
		t.Fatalf("Description after stack rollback = %q, want original", config.Description)
	}
}
