package middleware

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/protocol"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestDetectService(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		header map[string]string // optional headers
		want   string
	}{
		// X-Amz-Target — JSON-protocol services
		{name: "sqs target", method: "POST", path: "/", header: map[string]string{"X-Amz-Target": "AmazonSQS.CreateQueue"}, want: "sqs"},
		{name: "dynamodb target", method: "POST", path: "/", header: map[string]string{"X-Amz-Target": "DynamoDB_20120810.PutItem"}, want: "dynamodb"},
		{name: "cognito target", method: "POST", path: "/", header: map[string]string{"X-Amz-Target": "AWSCognitoIdentityProviderService.ListUsers"}, want: "cognito"},

		// Well-known URL prefixes — REST-protocol services
		{name: "lambda path", method: "GET", path: "/2015-03-31/functions", want: "lambda"},
		{name: "pipes path", method: "GET", path: "/v1/pipes", want: "pipes"},
		{name: "appsync apis", method: "GET", path: "/v1/apis", want: "appsync"},
		{name: "appsync graphql", method: "POST", path: "/_appsync/api-id/graphql", want: "appsync"},
		{name: "ses path", method: "GET", path: "/v2/email/identities", want: "ses"},
		{name: "cloudfront path", method: "GET", path: "/2020-05-31/distribution", want: "cloudfront"},
		{name: "apigateway restapis", method: "GET", path: "/restapis", want: "apigateway"},
		{name: "apigateway v2", method: "GET", path: "/v2/apis", want: "apigateway"},
		{name: "appsync events v2", method: "GET", path: "/v2/apis", header: map[string]string{"Authorization": "AWS4-HMAC-SHA256 Credential=AKID/20260623/us-east-1/appsync/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc"}, want: "appsync"},

		// Internal /_events and /_metrics
		{name: "events", method: "GET", path: "/_events", want: "events"},
		{name: "metrics", method: "GET", path: "/_metrics", want: "metrics"},

		// Emulator-internal /_-prefixed paths — must NOT fall through to S3
		{name: "health", method: "GET", path: "/_health", want: "internal"},
		{name: "topology", method: "GET", path: "/_topology", want: "internal"},
		{name: "info", method: "GET", path: "/_/info", want: "internal"},
		{name: "debug", method: "GET", path: "/_debug/store", want: "internal"},
		{name: "cognito oauth", method: "GET", path: "/_cognito/us-east-1_ABC/oauth2/authorize", want: "cognito"},
		{name: "cognito login", method: "POST", path: "/_cognito/us-east-1_ABC/login", want: "cognito"},
		{name: "ecs tasks", method: "GET", path: "/_ecs/clusters/default/tasks", want: "ecs"},
		{name: "lambda instances", method: "GET", path: "/_lambda/instances", want: "lambda"},
		{name: "cloudfront proxy", method: "GET", path: "/_cloudfront/EDIST123/index.html", want: "cloudfront"},
		{name: "secretsmanager internal", method: "GET", path: "/_overcast/secretsmanager/secrets", want: "secretsmanager"},
		{name: "mail internal", method: "GET", path: "/_overcast/inbox/messages", want: "ses"},

		// S3 fallback — plain paths without distinguishing signals
		{name: "s3 list buckets", method: "GET", path: "/", want: "s3"},
		{name: "s3 get object", method: "GET", path: "/my-bucket/key", want: "s3"},
		{name: "s3 put object", method: "PUT", path: "/my-bucket/key", want: "s3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, tt.path, nil)
			for k, v := range tt.header {
				r.Header.Set(k, v)
			}
			got := detectService(r)
			if got != tt.want {
				t.Errorf("detectService(%s %s) = %q, want %q", tt.method, tt.path, got, tt.want)
			}
		})
	}
}

func TestDetectOperation(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		header map[string]string
		want   string
	}{
		// X-Amz-Target
		{name: "sqs target", method: "POST", path: "/", header: map[string]string{"X-Amz-Target": "AmazonSQS.CreateQueue"}, want: "CreateQueue"},

		// x-id query param
		{name: "x-id param", method: "GET", path: "/bucket/key?x-id=GetObject", want: "GetObject"},

		// Internal endpoints with known operations
		{name: "events", method: "GET", path: "/_events", want: "Subscribe"},
		{name: "metrics", method: "GET", path: "/_metrics", want: "GetMetrics"},

		// Emulator-internal /_-prefixed paths — must return "" (no operation)
		{name: "health", method: "GET", path: "/_health", want: ""},
		{name: "topology", method: "GET", path: "/_topology", want: ""},
		{name: "info", method: "GET", path: "/_/info", want: ""},
		{name: "cognito oauth", method: "GET", path: "/_cognito/us-east-1_ABC/oauth2/authorize", want: ""},
		{name: "cognito debug token", method: "GET", path: "/_cognito/us-east-1_ABC/debug/token", want: ""},
		{name: "lambda instances", method: "GET", path: "/_lambda/instances", want: ""},
		{name: "debug store", method: "GET", path: "/_debug/store/s3", want: ""},

		// S3 heuristics — should still work for real S3 paths
		{name: "s3 list buckets", method: "GET", path: "/", want: "ListBuckets"},
		{name: "s3 create bucket", method: "PUT", path: "/my-bucket", want: "CreateBucket"},
		{name: "s3 get object", method: "GET", path: "/my-bucket/key", want: "GetObject"},
		{name: "s3 put object", method: "PUT", path: "/my-bucket/key", want: "PutObject"},
		{name: "s3 delete object", method: "DELETE", path: "/my-bucket/key", want: "DeleteObject"},
		{name: "s3 head object", method: "HEAD", path: "/my-bucket/key", want: "HeadObject"},
		{name: "s3 head bucket", method: "HEAD", path: "/my-bucket", want: "HeadBucket"},
		{name: "s3 delete bucket", method: "DELETE", path: "/my-bucket", want: "DeleteBucket"},
		{name: "s3 list objects v2", method: "GET", path: "/my-bucket?list-type=2", want: "ListObjectsV2"},
		{name: "s3 get bucket location", method: "GET", path: "/my-bucket?location", want: "GetBucketLocation"},
		{name: "s3 copy object", method: "PUT", path: "/my-bucket/key", header: map[string]string{"X-Amz-Copy-Source": "/src/obj"}, want: "CopyObject"},
		{name: "s3 create multipart", method: "POST", path: "/my-bucket/key?uploads", want: "CreateMultipartUpload"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, tt.path, nil)
			for k, v := range tt.header {
				r.Header.Set(k, v)
			}
			got := detectOperation(r)
			if got != tt.want {
				t.Errorf("detectOperation(%s %s) = %q, want %q", tt.method, tt.path, got, tt.want)
			}
		})
	}
}

func TestInternalService(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/_cognito/pool/oauth2/authorize", "cognito"},
		{"/_cognito/pool/login", "cognito"},
		{"/_ecs/clusters/default/tasks", "ecs"},
		{"/_ecs/tasks/arn/logs/main", "ecs"},
		{"/_lambda/instances", "lambda"},
		{"/_lambda/runtimes", "lambda"},
		{"/_appsync/api-id/graphql", "appsync"},
		{"/_cloudfront/EDIST/index.html", "cloudfront"},
		{"/_overcast/secretsmanager/secrets", "secretsmanager"},
		{"/_overcast/secretsmanager/secrets/id/value", "secretsmanager"},
		{"/_overcast/inbox/messages", "ses"},
		{"/_health", "internal"},
		{"/_topology", "internal"},
		{"/_/info", "internal"},
		{"/_debug/store", "internal"},
		{"/_metrics", "internal"}, // still "internal" via this helper; detectService handles the exact match earlier
		{"/_unknown/path", "internal"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := internalService(tt.path)
			if got != tt.want {
				t.Errorf("internalService(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestLogger_awsInternalError(t *testing.T) {
	// Given: a handler writes a wrapped AWS InternalError.
	core, logs := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)
	handler := Logger(logger, clock.New())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body in handler: %v", err)
		}
		if string(body) != `{"QueueUrl":"http://localhost:4566/000000000000/q"}` {
			t.Fatalf("handler body = %q", string(body))
		}
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, errors.New("state scan: database is locked")))
	}))
	req := httptest.NewRequest(http.MethodPost, "/?trace=1", strings.NewReader(`{"QueueUrl":"http://localhost:4566/000000000000/q"}`))
	req.Header.Set("X-Amz-Target", "AmazonSQS.ReceiveMessage")
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	rec := httptest.NewRecorder()

	// When: the request fails with a 500.
	handler.ServeHTTP(rec, req)

	// Then: the failure log includes the AWS error and its internal cause.
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	entries := logs.FilterMessage("request failed").All()
	if len(entries) != 1 {
		t.Fatalf("request failed log entries = %d, want 1", len(entries))
	}
	ctx := entries[0].ContextMap()
	if got := ctx["aws_error_code"]; got != "InternalError" {
		t.Fatalf("aws_error_code = %v, want InternalError", got)
	}
	if got := ctx["aws_error_cause"]; got != "state scan: database is locked" {
		t.Fatalf("aws_error_cause = %v, want state scan: database is locked", got)
	}
	if got := ctx["request_uri"]; got != "/?trace=1" {
		t.Fatalf("request_uri = %v, want /?trace=1", got)
	}
	if got := ctx["request_body"]; got != `{"QueueUrl":"http://localhost:4566/000000000000/q"}` {
		t.Fatalf("request_body = %v", got)
	}
	headers, ok := ctx["request_headers"].(http.Header)
	if !ok {
		t.Fatalf("request_headers type = %T, want http.Header", ctx["request_headers"])
	}
	if got := headers.Get("X-Amz-Target"); got != "AmazonSQS.ReceiveMessage" {
		t.Fatalf("X-Amz-Target header = %q", got)
	}
}
