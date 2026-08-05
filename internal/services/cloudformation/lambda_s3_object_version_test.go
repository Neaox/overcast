package cloudformation

import (
	"context"
	"testing"
)

func TestLambdaProvisionerUpdateForwardsS3ObjectVersion(t *testing.T) {
	router := &captureRouter{}
	h := &lambdaFunctionHandler{}
	oldProps := map[string]any{
		"Runtime": "nodejs22.x", "Handler": "index.handler", "Role": "arn:aws:iam::000000000000:role/r",
		"Code": map[string]any{"S3Bucket": "assets", "S3Key": "function.zip", "S3ObjectVersion": "version-1"},
	}
	newProps := map[string]any{
		"Runtime": "nodejs22.x", "Handler": "index.handler", "Role": "arn:aws:iam::000000000000:role/r",
		"Code": map[string]any{"S3Bucket": "assets", "S3Key": "function.zip", "S3ObjectVersion": "version-2"},
	}

	if _, _, err := h.Update(context.Background(), router, nil, "Stack-Function", newProps, oldProps, &resolveContext{Region: "us-east-1"}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	for _, request := range router.requests {
		if request.path != "/2015-03-31/functions/Stack-Function/code" {
			continue
		}
		if got := request.body["S3ObjectVersion"]; got != "version-2" {
			t.Fatalf("S3ObjectVersion = %v, want version-2; body=%v", got, request.body)
		}
		return
	}
	t.Fatalf("UpdateFunctionCode request not dispatched: %v", router.requests)
}
