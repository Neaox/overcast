package middleware

import (
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/your-org/overcast/internal/protocol"
	"github.com/your-org/overcast/internal/serviceutil"
)

// detectService infers the AWS service from a request using the same signals
// the real AWS SDKs embed: X-Amz-Target prefix (JSON services), well-known URL
// prefixes (Lambda REST API), and falling back to S3 for everything else.
func detectService(r *http.Request) string {
	if t := r.Header.Get("X-Amz-Target"); t != "" {
		switch {
		case strings.HasPrefix(t, "AmazonSQS."):
			return "sqs"
		case strings.HasPrefix(t, "DynamoDB_"):
			return "dynamodb"
		case strings.HasPrefix(t, "AmazonSNS"):
			return "sns"
		}
	}
	if strings.HasPrefix(r.URL.Path, "/2015-03-31/") {
		return "lambda"
	}
	if r.URL.Path == "/_events" {
		return "events"
	}
	return "s3"
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

	// 3. Heuristic from method + path depth for S3 / Lambda
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	depth := len(parts)
	q := r.URL.Query()

	switch {
	// Lambda
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

// Logger logs every request at INFO level with structured fields.
// When stdout is a terminal, each line is prefixed with the service badge and
// (when known) an operation badge so log lines are easy to scan at a glance.
// Failed requests (5xx) are logged at ERROR level.
func Logger(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rw, r)

			reqID := protocol.RequestIDFromContext(r.Context())
			duration := time.Since(start)
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
