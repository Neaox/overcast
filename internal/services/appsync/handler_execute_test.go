package appsync

import "testing"

func TestLambdaFunctionNameFromARN_qualifiedAlias(t *testing.T) {
	// Given: a Lambda alias ARN stored in an AppSync data source.
	arn := "arn:aws:lambda:us-east-1:000000000000:function:lambda-function-l-ue1-digital-guides-namespace:live"

	// When: AppSync extracts the name to invoke Lambda.
	got := lambdaFunctionNameFromARN(arn)

	// Then: the qualifier is stripped and the underlying function is invoked.
	want := "lambda-function-l-ue1-digital-guides-namespace"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
