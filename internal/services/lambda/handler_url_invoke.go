package lambda

// handler_url_invoke.go — Host-routed Lambda function URL invocation.
//
// Registered at /_overcast/lambda/url-invoke/{urlId}/* (see Service.HostRouteRewrite
// in handler_url.go). Maps the incoming HTTP request to the Lambda function
// URL payload format 2.0 event (the only event format AWS uses for function
// URLs — there is no "1.0" option, unlike API Gateway) and maps the
// synchronous invoke result back to an HTTP response per the (buffered)
// function URL response contract:
// https://docs.aws.amazon.com/lambda/latest/dg/urls-invocation.html
//
// AuthType is intentionally not enforced (see handler_url.go doc) — every
// Host-routed invocation runs as if AuthType were NONE, regardless of what
// was configured. This matches Overcast's "not a security boundary" stance
// (AGENTS.md) and is called out in docs/networking.md.

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

// lambdaFunctionURLEvent is the Lambda function URL payload format 2.0
// request event (same shape as the API Gateway HTTP API v2 proxy event,
// minus routeKey/stage variability — both are always "$default").
type lambdaFunctionURLEvent struct {
	Version               string                    `json:"version"`
	RouteKey              string                    `json:"routeKey"`
	RawPath               string                    `json:"rawPath"`
	RawQueryString        string                    `json:"rawQueryString"`
	Cookies               []string                  `json:"cookies,omitempty"`
	Headers               map[string]string         `json:"headers"`
	QueryStringParameters map[string]string         `json:"queryStringParameters,omitempty"`
	RequestContext        functionURLRequestContext `json:"requestContext"`
	Body                  string                    `json:"body,omitempty"`
	IsBase64Encoded       bool                      `json:"isBase64Encoded"`
}

type functionURLRequestContext struct {
	AccountID    string          `json:"accountId"`
	APIID        string          `json:"apiId"`
	DomainName   string          `json:"domainName"`
	DomainPrefix string          `json:"domainPrefix"`
	HTTP         functionURLHTTP `json:"http"`
	RequestID    string          `json:"requestId"`
	RouteKey     string          `json:"routeKey"`
	Stage        string          `json:"stage"`
	Time         string          `json:"time"`
	TimeEpoch    int64           `json:"timeEpoch"`
}

type functionURLHTTP struct {
	Method    string `json:"method"`
	Path      string `json:"path"`
	Protocol  string `json:"protocol"`
	SourceIP  string `json:"sourceIp"`
	UserAgent string `json:"userAgent"`
}

// functionURLProxyResponse is the structured response shape a function may
// return to control status/headers/cookies explicitly.
type functionURLProxyResponse struct {
	StatusCode      int               `json:"statusCode"`
	Headers         map[string]string `json:"headers"`
	Cookies         []string          `json:"cookies"`
	Body            string            `json:"body"`
	IsBase64Encoded bool              `json:"isBase64Encoded"`
}

// InvokeFunctionURL handles Host-routed Lambda function URL invocations —
// see the file doc above.
func (h *Handler) InvokeFunctionURL(w http.ResponseWriter, r *http.Request) {
	log := h.log.WithRecorder(r.Context())
	urlID := chi.URLParam(r, "urlId")
	rawPath := "/" + strings.TrimPrefix(chi.URLParam(r, "*"), "/")
	ctx := r.Context()

	cfg, aerr := h.ls.getFunctionURLConfigByURLID(ctx, urlID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if cfg == nil {
		// Real AWS: an unrecognised function URL ID has nothing behind it to
		// attribute an error to — a plain 403 with no function context.
		writeFunctionURLPlainError(w, http.StatusForbidden, "Forbidden")
		return
	}

	fn, ferr := h.ls.getFunction(ctx, cfg.FunctionName)
	if ferr != nil {
		protocol.WriteJSONError(w, r, ferr)
		return
	}
	if fn == nil {
		writeFunctionURLPlainError(w, http.StatusNotFound, "Function not found")
		return
	}
	if aerr := checkInvokableState(fn); aerr != nil {
		writeFunctionURLPlainError(w, aerr.HTTPStatus, aerr.Message)
		return
	}

	rt := h.runtimes.runtimeFor(ctx, fn.Runtime)
	if rt == nil {
		log.Warn("function url invoke: no runtime available",
			zap.String("function", fn.Name), zap.String("runtime", fn.Runtime))
		writeFunctionURLPlainError(w, http.StatusInternalServerError,
			"No runtime available for "+fn.Runtime+". Only Node.js runtimes with inline source code can be invoked in the emulator.")
		return
	}

	event, err := h.buildFunctionURLEvent(r, rawPath, urlID)
	if err != nil {
		writeFunctionURLPlainError(w, http.StatusBadRequest, "Could not read request body")
		return
	}
	payload, err := json.Marshal(event)
	if err != nil {
		writeFunctionURLPlainError(w, http.StatusInternalServerError, "Failed to build invoke event")
		return
	}

	if err := h.ls.addInvocation(ctx, fn, payload); err != nil {
		log.Warn("function url invoke: record invocation", zap.String("function", fn.Name), zap.Error(err))
	}

	// A function URL response has no X-Amz-Log-Result field, so no tail.
	result := h.invokeSync(ctx, fn, rt, payload, fn.Name, InvokeOptions{})
	if result.throttle != nil {
		writeThrottleError(w, r, result.throttle)
		return
	}
	writeFunctionURLResponse(w, cfg, result)
}

// buildFunctionURLEvent maps r onto the Lambda function URL payload v2.0
// event shape. See the file doc for why format 2.0 is the only option.
func (h *Handler) buildFunctionURLEvent(r *http.Request, rawPath, urlID string) (*lambdaFunctionURLEvent, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 6*1024*1024)) // 6 MiB, matches the synchronous invoke limit elsewhere.
	if err != nil {
		return nil, err
	}

	headers := make(map[string]string, len(r.Header))
	for k, vals := range r.Header {
		headers[strings.ToLower(k)] = strings.Join(vals, ",")
	}

	var queryParams map[string]string
	if q := r.URL.Query(); len(q) > 0 {
		queryParams = make(map[string]string, len(q))
		for k, vals := range q {
			queryParams[k] = strings.Join(vals, ",")
		}
	}

	var cookies []string
	for _, c := range r.Cookies() {
		cookies = append(cookies, c.String())
	}

	bodyStr, isB64 := encodeFunctionURLBody(body)

	now := h.clk.Now()
	return &lambdaFunctionURLEvent{
		Version:               "2.0",
		RouteKey:              "$default",
		RawPath:               rawPath,
		RawQueryString:        r.URL.RawQuery,
		Cookies:               cookies,
		Headers:               headers,
		QueryStringParameters: queryParams,
		RequestContext: functionURLRequestContext{
			AccountID: h.accountID(),
			APIID:     urlID,
			// Folded: a Host is case-insensitive, so the domain reported to
			// handler code must not vary with how the caller typed it.
			DomainName:   serviceutil.FoldHostname(r.Host),
			DomainPrefix: serviceutil.DomainPrefix(r.Host),
			HTTP: functionURLHTTP{
				Method:    r.Method,
				Path:      rawPath,
				Protocol:  serviceutil.RequestProtocol(r),
				SourceIP:  serviceutil.ClientIP(r),
				UserAgent: r.Header.Get("User-Agent"),
			},
			RequestID: protocol.NewRequestID(),
			RouteKey:  "$default",
			Stage:     "$default",
			Time:      now.Format("02/Jan/2006:15:04:05 +0000"),
			TimeEpoch: now.UnixMilli(),
		},
		Body:            bodyStr,
		IsBase64Encoded: isB64,
	}, nil
}

// encodeFunctionURLBody returns body as a UTF-8 string when valid, otherwise
// base64-encoded — mirroring AWS's binary-vs-text handling for proxy event
// bodies without needing a media-type allowlist.
func encodeFunctionURLBody(body []byte) (string, bool) {
	if len(body) == 0 {
		return "", false
	}
	if utf8.Valid(body) {
		return string(body), false
	}
	return base64.StdEncoding.EncodeToString(body), true
}

// writeFunctionURLResponse maps a synchronous invoke result to an HTTP
// response per the function URL BUFFERED response contract: an unhandled
// function error becomes a 502 (matching real AWS function URLs); a
// structured {statusCode, headers, cookies, body, isBase64Encoded} JSON
// payload is honoured; anything else (bare JSON, plain text, empty) is
// returned verbatim with the AWS-documented fallback of 200 +
// application/json.
func writeFunctionURLResponse(w http.ResponseWriter, cfg *FunctionURLConfig, result *InvokeResult) {
	applyFunctionURLCors(w, cfg)

	if result.FunctionError != "" {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Amz-Function-Error", result.FunctionError)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write(result.Payload)
		return
	}

	var resp functionURLProxyResponse
	if err := json.Unmarshal(result.Payload, &resp); err == nil && resp.StatusCode != 0 {
		for k, v := range resp.Headers {
			w.Header().Set(k, v)
		}
		for _, c := range resp.Cookies {
			w.Header().Add("Set-Cookie", c)
		}
		w.WriteHeader(resp.StatusCode)
		if resp.Body == "" {
			return
		}
		if resp.IsBase64Encoded {
			if decoded, err := base64.StdEncoding.DecodeString(resp.Body); err == nil {
				_, _ = w.Write(decoded)
				return
			}
		}
		_, _ = w.Write([]byte(resp.Body))
		return
	}

	// Bare JSON (or anything that isn't the structured proxy shape): AWS's
	// documented fallback is 200 + application/json, body verbatim.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result.Payload)
}

// applyFunctionURLCors reflects the function URL config's stored Cors
// settings onto the response, when present. This is the one piece of Cors
// behaviour Overcast actually applies (as opposed to just storing/returning
// it) — cheap to do and meaningfully improves fidelity for browser-based
// testing against a function URL, which is the config's main use case.
func applyFunctionURLCors(w http.ResponseWriter, cfg *FunctionURLConfig) {
	if cfg == nil || cfg.Cors == nil {
		return
	}
	c := cfg.Cors
	if len(c.AllowOrigins) > 0 {
		w.Header().Set("Access-Control-Allow-Origin", strings.Join(c.AllowOrigins, ","))
	}
	if len(c.AllowMethods) > 0 {
		w.Header().Set("Access-Control-Allow-Methods", strings.Join(c.AllowMethods, ","))
	}
	if len(c.AllowHeaders) > 0 {
		w.Header().Set("Access-Control-Allow-Headers", strings.Join(c.AllowHeaders, ","))
	}
	if len(c.ExposeHeaders) > 0 {
		w.Header().Set("Access-Control-Expose-Headers", strings.Join(c.ExposeHeaders, ","))
	}
	if c.AllowCredentials != nil && *c.AllowCredentials {
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	}
	if c.MaxAge != nil {
		w.Header().Set("Access-Control-Max-Age", strconv.Itoa(*c.MaxAge))
	}
}

// writeFunctionURLPlainError writes a minimal JSON error body — function
// URLs don't share API Gateway's {message} shape in the AWS SDK sense (there
// is no SDK-side "invoke" for a function URL; it's just plain HTTP), so
// there's no wire-compatibility contract to match here beyond "returns the
// right status code with a readable body".
func writeFunctionURLPlainError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, _ := json.Marshal(struct {
		Message string `json:"message"`
	}{Message: message})
	_, _ = w.Write(body)
}
