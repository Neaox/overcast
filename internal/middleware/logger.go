package middleware

import (
	"bufio"
	"bytes"
	"net"
	"net/http"
	"net/url"
	"strings"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/awsapi"
	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/trace"
)

// detectService infers the AWS service from a request using the same signals
// the real AWS SDKs embed: X-Amz-Target prefix (JSON services), well-known URL
// prefixes (Lambda REST API), the Authorization Credential scope (Query-protocol
// services such as IAM, STS, SNS, EC2), and finally S3 as a fallback.
// An optional body parameter enables Query-protocol Action-param detection.
func detectService(r *http.Request, body ...[]byte) string {
	// 1. X-Amz-Target — use the generated AWS operation registry for
	// accurate service mapping across all JSON-protocol services.
	if t := r.Header.Get("X-Amz-Target"); t != "" {
		if claim, ok := awsapi.NewRegistry().ClaimTarget(t); ok && claim.Service != "" {
			return middlewareServiceKey(claim.Service)
		}
	}

	// 2. Well-known URL prefixes — covers REST-protocol services.
	switch {
	case isLambdaAPIVersionPrefix(r.URL.Path):
		return "lambda"
	case strings.HasPrefix(r.URL.Path, "/2015-02-01/"):
		return "efs"
	case strings.HasPrefix(r.URL.Path, "/v1/pipes"):
		return "pipes"
	case strings.HasPrefix(r.URL.Path, "/v1/apis"),
		strings.HasPrefix(r.URL.Path, "/v1/tags"),
		strings.HasPrefix(r.URL.Path, "/v1/domainnames"),
		strings.HasPrefix(r.URL.Path, "/v1/mergedApis"),
		strings.HasPrefix(r.URL.Path, "/v1/sourceApis"):
		return "appsync"
	case strings.HasPrefix(r.URL.Path, "/v2/email/"):
		return "ses"
	case strings.HasPrefix(r.URL.Path, "/2020-05-31/"):
		return "cloudfront"
	case strings.HasPrefix(r.URL.Path, "/2013-04-01/"):
		return "route53"
	case strings.HasPrefix(r.URL.Path, "/v2/apis"):
		if svc := serviceFromAuthCredential(r); svc == "appsync" {
			return "appsync"
		}
		return "apigateway"
	case strings.HasPrefix(r.URL.Path, "/restapis"),
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

	// 2b. Host-routed AWS-style addresses (execute-api / lambda-url /
	// appsync-api Host subdomains — see hostroute.go). Read from the claim
	// HostAddressing stamped on this request, so the label is what actually
	// routed it rather than a re-derivation that could disagree — notably
	// when OVERCAST_HOSTNAME itself contains a registered label, where S3
	// virtual-hosted addressing legitimately wins.
	if claim, ok := HostClaimFromContext(r.Context()); ok && claim.Kind == HostClaimHostRoute {
		if svc, found := HostRouteServiceFor(claim.Route); found {
			return svc
		}
	}

	// 2c. Emulator-internal /_-prefixed paths — S3 bucket names cannot start
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

	// 3b. Query-protocol Action parameter — use the generated AWS operation
	// registry for accurate service/operation mapping.
	if len(body) > 0 && len(body[0]) > 0 && bytes.Contains(body[0][:min(len(body[0]), 256)], []byte("Action=")) {
		values, err := url.ParseQuery(string(body[0]))
		if err == nil {
			if claim, ok := awsapi.NewRegistry().ClaimQuery(values.Get("Version"), values.Get("Action")); ok {
				return middlewareServiceKey(claim.Service)
			}
		}
	}

	// 4. S3 is the final fallback: S3 uses plain HTTP verbs on path-style or
	// virtual-hosted URLs with no distinguishing header, so there is no
	// positive signal to match on.
	return "s3"
}

// isLambdaAPIVersionPrefix reports whether a path is under one of Lambda's
// dated API version prefixes. Lambda is the one service that spreads its REST
// surface across many of them, and a prefix missing here sends that whole
// operation family into the S3 fallback's bucket/object shapes.
//
// The list is every version the pinned AWS models bind a Lambda operation to,
// not only the ones Overcast routes today: an unrouted Lambda path still gets
// a protocol-correct 501 from the generated registry, so labelling it lambda
// is what actually happened to it. TestDetectServiceCoversModeledLambdaVersions
// fails if a model refresh introduces one that is missing. No other modeled
// service shares any of these prefixes (only Lambda MicroVMs, which Overcast
// does not implement, shares /2017-03-31).
func isLambdaAPIVersionPrefix(path string) bool {
	switch {
	case strings.HasPrefix(path, "/2014-11-13/"),
		strings.HasPrefix(path, "/2015-03-31/"),
		strings.HasPrefix(path, "/2016-08-19/"),
		strings.HasPrefix(path, "/2017-03-31/"),
		strings.HasPrefix(path, "/2017-10-31/"),
		strings.HasPrefix(path, "/2018-10-31/"),
		strings.HasPrefix(path, "/2019-09-25/"),
		strings.HasPrefix(path, "/2019-09-30/"),
		strings.HasPrefix(path, "/2020-04-22/"),
		strings.HasPrefix(path, "/2020-06-30/"),
		strings.HasPrefix(path, "/2021-07-20/"),
		strings.HasPrefix(path, "/2021-10-31/"),
		strings.HasPrefix(path, "/2021-11-15/"),
		strings.HasPrefix(path, "/2024-08-31/"),
		strings.HasPrefix(path, "/2025-11-30/"),
		strings.HasPrefix(path, "/2025-12-01/"):
		return true
	}
	return false
}

// middlewareServiceKey translates the generated registry's service identity
// to the key middleware has always used where the two differ. CloudWatch Logs
// is modeled as "cloudwatch-logs" (its capability key), but its SigV4 signing
// name and real IAM action prefix are "logs" (logs:PutLogEvents), and every
// downstream switch — IAM enforcement, log labels, trace service badges —
// keys on "logs".
func middlewareServiceKey(s string) string {
	if s == "cloudwatch-logs" {
		return "logs"
	}
	return s
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
	case strings.HasPrefix(path, "/_appsync"):
		return "appsync"
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

// detectOperation infers the operation name from the request. It classifies
// the service first; callers that already have one should use
// detectOperationForService rather than paying for it twice.
func detectOperation(r *http.Request, body ...[]byte) string {
	return detectOperationForService(r, detectService(r, body...), body...)
}

// detectOperationForService infers the AWS operation name from a request that
// has already been classified as belonging to svc.
//
// Priority:
//  1. X-Amz-Target suffix  ("AmazonSQS.CreateQueue" → "CreateQueue")
//  2. x-id query param     ("?x-id=ListBuckets"     → "ListBuckets")
//  3. Query-protocol Action parameter (needs the body)
//  4. Method + path, resolved against svc
//
// Step 4 is where this used to go wrong. It ran one flat switch whose Lambda
// arm handled two methods under one path prefix and whose S3 arm was reachable
// by anything the arms above it failed to claim, so every other Lambda method
// and API version was labelled — and metered — as an S3 object operation.
// Resolution is now scoped to the classified service throughout: restOperation
// answers for a REST-routed service from the pinned Smithy models, the S3
// shape rules are reachable only when the request is S3's, and a path neither
// recognises yields "" instead of borrowing a name from whichever service's
// heuristics happened to sit lower in the switch.
func detectOperationForService(r *http.Request, svc string, body ...[]byte) string {
	// 1. Target-based (all JSON-protocol services).
	if t := r.Header.Get("X-Amz-Target"); t != "" {
		if claim, ok := awsapi.NewRegistry().ClaimTarget(t); ok && claim.Operation != "" {
			return claim.Operation
		}
	}

	// 2. x-id query param (S3 SDK sends this for several operations)
	if xid := rawQueryValue(r.URL.RawQuery, "x-id"); xid != "" {
		return xid
	}

	// 3. Query-protocol Action parameter.
	if len(body) > 0 && len(body[0]) > 0 && bytes.Contains(body[0][:min(len(body[0]), 256)], []byte("Action=")) {
		values, err := url.ParseQuery(string(body[0]))
		if err == nil {
			if claim, ok := awsapi.NewRegistry().ClaimQuery(values.Get("Version"), values.Get("Action")); ok {
				return claim.Operation
			}
		}
	}

	switch r.URL.Path {
	case "/_events":
		return "Subscribe"
	case "/_metrics":
		return "GetMetrics"
	}

	// Emulator-internal paths — never a modeled AWS operation, and S3 bucket
	// names cannot begin with '_', so nothing below can apply.
	if strings.HasPrefix(r.URL.Path, "/_") {
		return ""
	}

	// Host-routed data-plane traffic (execute-api, lambda-url, appsync-api,
	// CloudFront, ELB). The path belongs to the customer's own API, not to an
	// AWS control-plane operation, so there is nothing to name — and without
	// this the request would be scored against whatever service the host claim
	// named it.
	if claim, ok := HostClaimFromContext(r.Context()); ok && claim.Kind == HostClaimHostRoute {
		return ""
	}

	// 4. Method + path, resolved against the classified service.
	if svc != "s3" {
		return restOperation(svc, r.Method, r.URL.Path, r.URL.RawQuery)
	}
	return s3ShapeOperation(r)
}

// s3ShapeOperation names an S3 request from its method, path depth and
// sub-resource query parameters. S3 alone needs shape rules rather than the
// generated model bindings: it has no distinguishing header or path prefix,
// and in the shared model trie its own `/{Bucket}/{Key+}` bindings sit behind
// other services' greedy bindings, so a lookup for "/my-bucket/key" answers
// with MediaStore Data's GetObject long before it reaches S3's.
//
// Reachable only for requests detectService classified as S3 — which is the
// same determination the router makes when it decides S3 keeps a path.
func s3ShapeOperation(r *http.Request) string {
	depth := pathDepth(r.URL.Path)
	query := r.URL.RawQuery

	switch {
	// Bucket-level
	case depth == 1 && r.Method == http.MethodGet && r.URL.Path == "/":
		return "ListBuckets"
	case depth == 1 && r.Method == http.MethodPut && rawQueryHas(query, "versioning"):
		return "PutBucketVersioning"
	case depth == 1 && r.Method == http.MethodGet && rawQueryHas(query, "location"):
		return "GetBucketLocation"
	case depth == 1 && r.Method == http.MethodGet && (rawQueryHas(query, "list-type") || rawQueryHas(query, "prefix")):
		return "ListObjectsV2"
	case depth == 1 && r.Method == http.MethodPut:
		return "CreateBucket"
	case depth == 1 && r.Method == http.MethodDelete:
		return "DeleteBucket"
	case depth == 1 && r.Method == http.MethodHead:
		return "HeadBucket"

	// Object-level
	case depth >= 2 && r.Method == http.MethodPut && r.Header.Get("X-Amz-Copy-Source") != "":
		return "CopyObject"
	case depth >= 2 && r.Method == http.MethodPut && rawQueryHas(query, "uploadId"):
		return "UploadPart"
	case depth >= 2 && r.Method == http.MethodPut:
		return "PutObject"
	case depth >= 2 && r.Method == http.MethodGet && rawQueryHas(query, "uploadId"):
		return "ListParts"
	case depth >= 2 && r.Method == http.MethodGet:
		return "GetObject"
	case depth >= 2 && r.Method == http.MethodHead:
		return "HeadObject"
	case depth >= 2 && r.Method == http.MethodDelete && rawQueryHas(query, "uploadId"):
		return "AbortMultipartUpload"
	case depth >= 2 && r.Method == http.MethodDelete:
		return "DeleteObject"
	case depth >= 2 && r.Method == http.MethodPost && rawQueryHas(query, "uploads"):
		return "CreateMultipartUpload"
	case depth >= 2 && r.Method == http.MethodPost && rawQueryHas(query, "delete"):
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
	status          int
	awsErrorCode    string
	awsErrorMessage string
	awsErrorCause   error
	req             *http.Request
}

func (rw *responseWriter) WriteHeader(status int) {
	rw.status = status
	// Two of these wrappers nest in the chain (Logger's and RequestEvents'),
	// so this runs twice per response. CaptureStackOnce keeps the innermost
	// stack — the one closest to the handler — and makes the second call free.
	trace.RecorderFromContext(rw.req.Context()).CaptureStackOnce()
	rw.ResponseWriter.WriteHeader(status)
}

// RecordAWSError lets protocol writers attach AWS error details to the request
// log without exposing internal causes in client-facing response bodies.
func (rw *responseWriter) RecordAWSError(aerr *protocol.AWSError) {
	if aerr == nil {
		return
	}
	rw.awsErrorCode = aerr.Code
	rw.awsErrorMessage = aerr.Message
	rw.awsErrorCause = protocol.Cause(aerr)
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

// isOperationalPollPath reports whether path belongs to an internal
// health/readiness probe or the /_debug/* namespace polled by container
// orchestrators (Docker HEALTHCHECK, Kubernetes probes) or the web UI's
// auto-refreshing debug views. These fire purely because time passed and
// infrastructure polled — never because of anything a real AWS client did —
// so per the trace-vs-debug policy (CONTRIBUTING.md § Log levels) they belong
// at TRACE: polling intervals of a few seconds would otherwise drown genuine
// request activity even at DEBUG.
func isOperationalPollPath(path string) bool {
	return path == "/_health" || strings.HasPrefix(path, "/_debug/") || path == "/_debug"
}

// Logger logs every request with structured fields: real AWS API calls and
// other requests at INFO, internal health/readiness and /_debug/* polling at
// TRACE (see isOperationalPollPath). When stdout is a terminal, each line is
// prefixed with the service badge and (when known) an operation badge so log
// lines are easy to scan at a glance. Failed requests (5xx) are logged at
// ERROR level regardless of path.
func Logger(logger *zap.Logger, clk clock.Clock) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := clk.Now()
			// The request snapshot is only ever printed for a 5xx, so nothing
			// is read up front: DebugTrace's capture is reused when debug is
			// on — which also means the failure log gets that capture's larger
			// bound — and otherwise a lazy tee records only what the handler
			// itself reads. See bodycapture.go.
			capture := requestCaptureFromContext(r.Context())
			if capture == nil {
				capture = teeRequestBody(r, maxLoggedRequestBody)
			}
			rw := &responseWriter{ResponseWriter: w, status: http.StatusOK, req: r}

			next.ServeHTTP(rw, r)
			capture.seal()

			reqID := protocol.RequestIDFromContext(r.Context())
			duration := clk.Since(start)
			svc := detectService(r)
			op := detectOperationForService(r, svc)

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
				requestBody := capture.body()
				fields = append(fields,
					zap.String("request_uri", r.RequestURI),
					zap.String("request_proto", r.Proto),
					zap.String("request_host", r.Host),
					zap.Int64("request_content_length", r.ContentLength),
					zap.Any("request_headers", capture.headers),
					zap.ByteString("request_body", requestBody),
				)
				if capture.truncated {
					fields = append(fields, zap.Bool("request_body_truncated", true))
				}
				if capture.err != nil {
					fields = append(fields, zap.Error(capture.err))
				}
			}
			if rw.awsErrorCode != "" {
				fields = append(fields,
					zap.String("aws_error_code", rw.awsErrorCode),
					zap.String("aws_error_message", rw.awsErrorMessage),
				)
				if rw.awsErrorCause != nil {
					fields = append(fields, zap.String("aws_error_cause", rw.awsErrorCause.Error()))
				}
			}

			if rec := trace.RecorderFromContext(r.Context()); rec != nil {
				rec.AddLog(trace.LogEntry{
					Level:     logLevel(rw.status),
					Message:   "request",
					Timestamp: start,
					Fields:    trace.ZapFieldsToMap(fields),
				})
				if rw.awsErrorCode != "" {
					rec.SetMeta(r.RemoteAddr, r.UserAgent(), r.Header.Get("Referer"), rw.awsErrorCode, rw.awsErrorMessage)
				}
			}

			switch {
			case rw.status >= 500:
				log.Error("request failed", fields...)
			case isOperationalPollPath(r.URL.Path):
				log.Trace("request", fields...)
			default:
				log.Info("request", fields...)
			}
		})
	}
}

func logLevel(status int) string {
	if status >= 500 {
		return "ERROR"
	}
	return "INFO"
}
