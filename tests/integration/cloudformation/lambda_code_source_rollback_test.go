package cloudformation_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

type lambdaCodeState struct {
	CodeSha256 string `json:"CodeSha256"`
}

func TestUpdateStack_RollbackRestoresInlineCodeSource(t *testing.T) {
	// Given: an inline Lambda function and an S3 object used by a later attempted update.
	srv := helpers.NewTestServer(t)
	s3PutObject(t, srv, "lambda-rollback-code", "function.zip", "attempted-s3-code")
	stackName := "lambda-code-source-rollback"
	createTemplate := `{"Resources":{"Function":{"Type":"AWS::Lambda::Function","Properties":{"FunctionName":"lambda-code-source-rollback-fn","Runtime":"python3.11","Handler":"index.handler","Role":"arn:aws:iam::000000000000:role/lambda-role","Code":{"ZipFile":"def handler(event, context): return 'original'"}}},"Later":{"Type":"AWS::S3::Bucket","DependsOn":"Function","Properties":{"BucketName":"lambda-code-source-rollback-later","VersioningConfiguration":{"Status":"Enabled"}}}}}`
	createLambdaStack(t, srv, stackName, createTemplate)
	original := getLambdaCodeState(t, srv, "lambda-code-source-rollback-fn")

	// When: the Lambda changes to S3 code before a later resource fails the stack update.
	updateTemplate := `{"Resources":{"Function":{"Type":"AWS::Lambda::Function","Properties":{"FunctionName":"lambda-code-source-rollback-fn","Runtime":"python3.11","Handler":"index.handler","Role":"arn:aws:iam::000000000000:role/lambda-role","Code":{"S3Bucket":"lambda-rollback-code","S3Key":"function.zip"}}},"Later":{"Type":"AWS::S3::Bucket","DependsOn":"Function","Properties":{"BucketName":"lambda-code-source-rollback-later","VersioningConfiguration":{"Status":"Invalid"}}}}}`
	updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {updateTemplate},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_ROLLBACK_COMPLETE")

	// Then: compensation restores the original inline package.
	restored := getLambdaCodeState(t, srv, "lambda-code-source-rollback-fn")
	if restored.CodeSha256 != original.CodeSha256 {
		t.Fatalf("CodeSha256 after rollback = %q, want original %q", restored.CodeSha256, original.CodeSha256)
	}
}

func getLambdaCodeState(t *testing.T, srv *helpers.TestServer, name string) lambdaCodeState {
	t.Helper()
	resp := lambdaRequest(t, srv, http.MethodGet, "/2015-03-31/functions/"+name+"/configuration", nil)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var state lambdaCodeState
	helpers.DecodeJSON(t, resp, &state)
	return state
}
