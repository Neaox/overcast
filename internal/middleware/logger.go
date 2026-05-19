package middleware

import (
	"bufio"
	"net"
	"net/http"
	"strings"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// detectService infers the AWS service from a request using the same signals
// the real AWS SDKs embed: X-Amz-Target prefix (JSON services), well-known URL
// prefixes (Lambda REST API), the Authorization Credential scope (Query-protocol
// services such as IAM, STS, SNS, EC2), and finally S3 as a fallback.
func detectService(r *http.Request) string {
	// 1. X-Amz-Target — covers all JSON-protocol services.
	if t := r.Header.Get("X-Amz-Target"); t != "" {
		switch {
		case strings.HasPrefix(t, "AmazonSQS."):
			return "sqs"
		case strings.HasPrefix(t, "DynamoDB_"):
			return "dynamodb"
		case strings.HasPrefix(t, "DynamoDBStreams_"):
			return "dynamodbstreams"
		case strings.HasPrefix(t, "AmazonSNS"):
			return "sns"
		case strings.HasPrefix(t, "Logs_"):
			return "logs"
		case strings.HasPrefix(t, "secretsmanager."):
			return "secretsmanager"
		case strings.HasPrefix(t, "AmazonSSM."):
			return "ssm"
		case strings.HasPrefix(t, "TrentService."):
			return "kms"
		case strings.HasPrefix(t, "AmazonStates."), strings.HasPrefix(t, "AWSStepFunctions."):
			return "stepfunctions"
		case strings.HasPrefix(t, "AWSShield_"):
			return "shield"
		case strings.HasPrefix(t, "AWSWAF_"):
			return "waf"
		case strings.HasPrefix(t, "AWSCognitoIdentityProviderService."):
			return "cognito"
		case strings.HasPrefix(t, "AmazonEC2ContainerServiceV"):
			return "ecs"
		case strings.HasPrefix(t, "AWSEvents."):
			return "events"
		}
	}

	// 2. Well-known URL prefixes — covers REST-protocol services.
	switch {
	case strings.HasPrefix(r.URL.Path, "/2015-03-31/"),
		strings.HasPrefix(r.URL.Path, "/2018-10-31/"),
		strings.HasPrefix(r.URL.Path, "/2021-11-15/"):
		return "lambda"
	case strings.HasPrefix(r.URL.Path, "/v1/pipes"):
		return "pipes"
	case strings.HasPrefix(r.URL.Path, "/v2/email/"):
		return "ses"
	case strings.HasPrefix(r.URL.Path, "/2020-05-31/"):
		return "cloudfront"
	case strings.HasPrefix(r.URL.Path, "/restapis"),
		strings.HasPrefix(r.URL.Path, "/v2/apis"),
		strings.HasPrefix(r.URL.Path, "/apikeys"),
		strings.HasPrefix(r.URL.Path, "/usageplans"):
		return "apigateway"
	case strings.HasPrefix(r.URL.Path, "/applications"):
		return "appregistry"
	case r.URL.Path == "/_events":
		return "events"
	case r.URL.Path == "/_metrics":
		return "metrics"
	}

	// 2b. Emulator-internal /_-prefixed paths — S3 bucket names cannot start
	// with '_', so any /_* path is definitively not S3. Map known service
	// prefixes to their owner; everything else is "internal".
	if strings.HasPrefix(r.URL.Path, "/_") {
		return internalService(r.URL.Path)
	}

	// 3. Authorization Credential scope — covers Query-protocol services
	// (IAM, STS, SNS, EC2, CloudFormation, RDS, …) where there is no
	// X-Amz-Target header and no distinguishing URL path.
	// Format: AWS4-HMAC-SHA256 Credential=AKID/DATE/REGION/SERVICE/aws4_request
	if svc := serviceFromAuthCredential(r); svc != "" && svc != "s3" {
		return svc
	}

	// 4. S3 is the final fallback: S3 uses plain HTTP verbs on path-style or
	// virtual-hosted URLs with no distinguishing header, so there is no
	// positive signal to match on.
	return "s3"
}

// internalService maps an emulator-internal /_-prefixed path to the service
// that owns it. S3 bucket names cannot start with '_', so any /_* path is
// definitively not an S3 request and must not fall through to the S3 fallback.
func internalService(path string) string {
	switch {
	case strings.HasPrefix(path, "/_cognito"):
		return "cognito"
	case strings.HasPrefix(path, "/_ecs"):
		return "ecs"
	case strings.HasPrefix(path, "/_lambda"):
		return "lambda"
	case strings.HasPrefix(path, "/_cloudfront"):
		return "cloudfront"
	case strings.HasPrefix(path, "/_overcast/cognito"):
		return "cognito"
	case strings.HasPrefix(path, "/_overcast/secretsmanager"):
		return "secretsmanager"
	case strings.HasPrefix(path, "/_overcast/inbox"):
		return "ses"
	default:
		return "internal"
	}
}

// serviceFromAuthCredential extracts the service name from the SigV4
// Authorization header's Credential scope component.
func serviceFromAuthCredential(r *http.Request) string {
	parts := credentialScope(r)
	if len(parts) >= 4 {
		return parts[3]
	}
	return ""
}

// detectOperation infers the operation name from the request.
//
// Priority:
//  1. X-Amz-Target suffix  ("AmazonSQS.CreateQueue" → "CreateQueue")
//  2. x-id query param     ("?x-id=ListBuckets"    → "ListBuckets")
//  3. Method + path shape  (PUT /{bucket}/{key}     → "PutObject")
func detectOperation(r *http.Request) string {
	// 1. Target-based (SQS / DynamoDB / SNS)
	if t := r.Header.Get("X-Amz-Target"); t != "" {
		if i := strings.LastIndex(t, "."); i >= 0 {
			return t[i+1:]
		}
		return t
	}

	// 2. x-id query param (S3 SDK sends this for several operations)
	if xid := r.URL.Query().Get("x-id"); xid != "" {
		return xid
	}

	if r.URL.Path == "/_events" {
		return "Subscribe"
	}

	if r.URL.Path == "/_metrics" {
		return "GetMetrics"
	}

	// Emulator-internal paths — don't fall through to S3/Lambda heuristics.
	if strings.HasPrefix(r.URL.Path, "/_") {
		return ""
	}

	// AppSync, CloudFront, API Gateway v1/v2 — REST services where operation
	// names cannot be reliably inferred from path/method without full routing.
	// Return "" (no operation) rather than misidentifying as an S3 operation.
	if strings.HasPrefix(r.URL.Path, "/v1/apis") ||
		strings.HasPrefix(r.URL.Path, "/2020-05-31/") ||
		strings.HasPrefix(r.URL.Path, "/restapis") ||
		strings.HasPrefix(r.URL.Path, "/v2/apis") {
		return ""
	}

	// Pipes REST API
	if strings.HasPrefix(r.URL.Path, "/v1/pipes") {
		trimmed := strings.TrimPrefix(r.URL.Path, "/v1/pipes")
		trimmed = strings.Trim(trimmed, "/")
		switch {
		case trimmed == "" && r.Method == http.MethodGet:
			return "ListPipes"
		case trimmed != "" && r.Method == http.MethodPost:
			return "CreatePipe"
		case trimmed != "" && r.Method == http.MethodGet:
			return "DescribePipe"
		case trimmed != "" && r.Method == http.MethodDelete:
			return "DeletePipe"
		case trimmed != "" && r.Method == http.MethodPatch:
			return "UpdatePipe"
		}
	}

	// 3. Heuristic from method + path depth for S3 / Lambda
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	depth := len(parts)
	q := r.URL.Query()

	switch {
	// Lambda
	case r.URL.Path == "/2015-03-31/functions" && r.Method == http.MethodGet:
		return "ListFunctions"
	case r.URL.Path == "/2015-03-31/event-source-mappings" && r.Method == http.MethodGet:
		return "ListEventSourceMappings"
	case strings.HasPrefix(r.URL.Path, "/2015-03-31/functions") && r.Method == http.MethodPost:
		return "InvokeFunction"
	case strings.HasPrefix(r.URL.Path, "/2015-03-31/functions") && r.Method == http.MethodGet:
		return "GetFunction"

	// S3 bucket-level
	case depth == 1 && r.Method == http.MethodGet && r.URL.Path == "/":
		return "ListBuckets"
	case depth == 1 && r.Method == http.MethodPut && q.Has("versioning"):
		return "PutBucketVersioning"
	case depth == 1 && r.Method == http.MethodGet && q.Has("location"):
		return "GetBucketLocation"
	case depth == 1 && r.Method == http.MethodGet && (q.Has("list-type") || q.Has("prefix")):
		return "ListObjectsV2"
	case depth == 1 && r.Method == http.MethodPut:
		return "CreateBucket"
	case depth == 1 && r.Method == http.MethodDelete:
		return "DeleteBucket"
	case depth == 1 && r.Method == http.MethodHead:
		return "HeadBucket"

	// S3 object-level
	case depth >= 2 && r.Method == http.MethodPut && r.Header.Get("X-Amz-Copy-Source") != "":
		return "CopyObject"
	case depth >= 2 && r.Method == http.MethodPut && q.Has("uploadId"):
		return "UploadPart"
	case depth >= 2 && r.Method == http.MethodPut:
		return "PutObject"
	case depth >= 2 && r.Method == http.MethodGet && q.Has("uploadId"):
		return "ListParts"
	case depth >= 2 && r.Method == http.MethodGet:
		return "GetObject"
	case depth >= 2 && r.Method == http.MethodHead:
		return "HeadObject"
	case depth >= 2 && r.Method == http.MethodDelete && q.Has("uploadId"):
		return "AbortMultipartUpload"
	case depth >= 2 && r.Method == http.MethodDelete:
		return "DeleteObject"
	case depth >= 2 && r.Method == http.MethodPost && q.Has("uploads"):
		return "CreateMultipartUpload"
	case depth >= 2 && r.Method == http.MethodPost && q.Has("delete"):
		return "DeleteObjects"
	}

	return ""
}

// responseWriter wraps http.ResponseWriter to capture the status code written
// by the handler. Go's standard ResponseWriter doesn't expose the status after
// the fact, so we intercept WriteHeader to record it.
//
// Flush is forwarded so that SSE handlers (/_events) can still call Flush
// through the middleware chain — without this the Flusher type assertion in
// eventsHandler would fail and the request would panic with a 500.
type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(status int) {
	rw.status = status
	rw.ResponseWriter.WriteHeader(status)
}

// Flush forwards to the underlying ResponseWriter if it supports http.Flusher.
func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets middleware stacks (and http.ResponseController) reach the
// underlying ResponseWriter for interface detection (Hijacker, Pusher, etc.).
func (rw *responseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

// Hijack implements http.Hijacker by delegating to the underlying writer.
// Required for WebSocket upgrades (e.g. AppSync real-time subscriptions).
func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := rw.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// Logger logs every request at INFO level with structured fields.
// When stdout is a terminal, each line is prefixed with the service badge and
// (when known) an operation badge so log lines are easy to scan at a glance.
// Failed requests (5xx) are logged at ERROR level.
func Logger(logger *zap.Logger, clk clock.Clock) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := clk.Now()
			rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rw, r)

			reqID := protocol.RequestIDFromContext(r.Context())
			duration := clk.Since(start)
			svc := detectService(r)
			op := detectOperation(r)

			log := serviceutil.NewServiceLogger(logger, svc)
			if op != "" {
				log = log.WithOperation(op)
			}

			fields := []zap.Field{
				zap.String("request_id", reqID),
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.String("query", r.URL.RawQuery),
				zap.Int("status", rw.status),
				zap.Duration("duration", duration),
				zap.String("remote_addr", r.RemoteAddr),
			}
			if t := r.Header.Get("X-Amz-Target"); t != "" {
				fields = append(fields, zap.String("target", t))
			}

			if rw.status >= 500 {
				log.Error("request failed", fields...)
			} else {
				log.Info("request", fields...)
			}
		})
	}
}
