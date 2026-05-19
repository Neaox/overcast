package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Neaox/overcast/internal/state"
)

func BenchmarkRequestIAMResource_SQSCreateQueue(b *testing.B) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"QueueName":"bench-queue"}`))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.CreateQueue")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test/20260423/us-east-1/sqs/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc")
	req.Header.Set("X-Amz-Date", "20260423T000000Z")

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = requestIAMResource(req)
	}
}

func BenchmarkRequestIAMResource_ECSDeleteCluster(b *testing.B) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"cluster":"bench-cluster"}`))
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "AmazonEC2ContainerServiceV20141113.DeleteCluster")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test/20260423/us-east-1/ecs/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc")
	req.Header.Set("X-Amz-Date", "20260423T000000Z")

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = requestIAMResource(req)
	}
}

func BenchmarkEvaluateIAMDecision_SQSAllow(b *testing.B) {
	st := state.NewMemoryStore()
	seedIAMUserForBench(b, st, "test", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:CreateQueue","Resource":"*"}]}`)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"QueueName":"bench-queue"}`))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.CreateQueue")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test/20260423/us-east-1/sqs/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc")
	req.Header.Set("X-Amz-Date", "20260423T000000Z")

	b.ReportAllocs()
	var cacheMu sync.RWMutex
	var cache map[string]*iamEnforceCacheEntry
	for i := 0; i < b.N; i++ {
		_ = evaluateIAMDecision(req, st, "test", "sqs:CreateQueue", "arn:aws:sqs:us-east-1:000000000000:bench-queue", &cacheMu, &cache)
	}
}

func seedIAMUserForBench(b *testing.B, st state.Store, accessKey, userDoc string) {
	b.Helper()
	ctx := context.Background()
	user := map[string]any{
		"UserName":       accessKey,
		"AccessKeys":     []map[string]string{{"AccessKeyId": accessKey}},
		"InlinePolicies": map[string]string{"inline-1": userDoc},
	}
	raw, err := json.Marshal(user)
	if err != nil {
		b.Fatalf("marshal user seed: %v", err)
	}
	if err := st.Set(ctx, "iam:users", accessKey, string(raw)); err != nil {
		b.Fatalf("seed user: %v", err)
	}
}
