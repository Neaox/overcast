// Package trace provides request-tracing infrastructure for the debug
// namespace. When OVERCAST_DEBUG is enabled, every HTTP request is captured
// into an in-memory ring buffer and made queryable through /_debug/trace/*
// endpoints.
//
// When OVERCAST_DEBUG is off this package has zero overhead: the Recorder is
// never injected into context, the Buffer is nil, and AddHop / AddLog are
// no-ops on a nil receiver.
package trace

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Entry is the complete trace of one HTTP request through the system.
type Entry struct {
	RequestID string        `json:"requestId"`
	Timestamp time.Time     `json:"timestamp"`
	Duration  time.Duration `json:"duration"`
	Method    string        `json:"method"`
	Path      string        `json:"path"`
	Host      string        `json:"host"`
	Query     string        `json:"query,omitempty"`
	Service   string        `json:"service"`
	Operation string        `json:"operation,omitempty"`
	Region    string        `json:"region"`
	Stack     string        `json:"stack,omitempty"`

	RequestHeaders       http.Header `json:"requestHeaders"`
	RequestBody          []byte      `json:"requestBody,omitempty"`
	RequestBodyTruncated bool        `json:"requestBodyTruncated,omitempty"`
	RequestSize          int64       `json:"requestSize,omitempty"`

	ResponseHeaders       http.Header `json:"responseHeaders"`
	ResponseBody          []byte      `json:"responseBody,omitempty"`
	ResponseBodyTruncated bool        `json:"responseBodyTruncated,omitempty"`
	StatusCode            int         `json:"statusCode"`
	Streaming             bool        `json:"streaming,omitempty"`

	Hops       []Hop      `json:"hops,omitempty"`
	LogEntries []LogEntry `json:"logEntries,omitempty"`

	AWSErrorCode    string `json:"awsErrorCode,omitempty"`
	AWSErrorMessage string `json:"awsErrorMessage,omitempty"`

	RemoteAddr      string `json:"remoteAddr,omitempty"`
	UserAgent       string `json:"userAgent,omitempty"`
	Referer         string `json:"referer,omitempty"`
	ParentRequestID string `json:"parentRequestId,omitempty"`

	XRayTraceID string         `json:"xrayTraceId,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// MaxHopBody is the largest request or response body stored on a Hop.
// AddHop copies and truncates anything larger so an oversized internal
// dispatch cannot pin its full body in the ring buffer.
const MaxHopBody = 1 << 20 // 1 MiB

// MaxHopStacks bounds how many goroutine stacks one trace captures for its
// hops. A CloudFormation or CDK deploy dispatches hundreds of internal calls
// through a single trace, and runtime.Stack costs both CPU and a multi-KiB
// string every time — so capturing per hop makes the largest traces the most
// expensive ones and pins megabytes in the ring buffer. The first hops already
// show where the dispatch loop lives, which is what the stack is read for;
// hops past the budget record no stack.
const MaxHopStacks = 20

// MaxFailedHopStacks is a second, independent budget for hops that failed.
// A failure at hop 300 of a deploy is exactly the one worth a stack, so it
// must not be starved by the 300 successful hops ahead of it.
const MaxFailedHopStacks = 20

// Hop records one internal service-to-service call made during request
// processing.
type Hop struct {
	ID                    string        `json:"id"`
	Parent                string        `json:"parent,omitempty"`
	RequestID             string        `json:"requestId,omitempty"`
	Order                 int           `json:"order"`
	CallerService         string        `json:"callerService"`
	CallerOperation       string        `json:"callerOperation,omitempty"`
	Service               string        `json:"service"`
	Operation             string        `json:"operation"`
	TargetURI             string        `json:"targetUri,omitempty"`
	RequestHeaders        http.Header   `json:"requestHeaders,omitempty"`
	RequestBody           []byte        `json:"requestBody,omitempty"`
	RequestBodyTruncated  bool          `json:"requestBodyTruncated,omitempty"`
	ResponseStatus        int           `json:"responseStatus"`
	ResponseBody          []byte        `json:"responseBody,omitempty"`
	ResponseBodyTruncated bool          `json:"responseBodyTruncated,omitempty"`
	Duration              time.Duration `json:"duration"`
	Error                 string        `json:"error,omitempty"`
	Timestamp             time.Time     `json:"timestamp"`
	Noisy                 bool          `json:"noisy,omitempty"`
	Stack                 string        `json:"stack,omitempty"`
}

// LogEntry is one structured log line captured for this request.
type LogEntry struct {
	Level     string         `json:"level"`
	Message   string         `json:"message"`
	Fields    map[string]any `json:"fields,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
	HopID     string         `json:"hopId,omitempty"`
}

// Recorder is the per-request trace builder, injected into the request context
// by the DebugTrace middleware. It is nil when OVERCAST_DEBUG is off.
// All methods are safe for concurrent use.
type Recorder struct {
	mu       sync.Mutex
	entry    Entry
	hopOrder int

	// Stack-capture budgets. These are atomics rather than fields under mu so
	// that the decision to capture — and the capture itself, which is the
	// expensive part — happen outside the lock, as they did when every hop
	// captured unconditionally.
	stackTaken      atomic.Bool
	hopStacks       atomic.Int64
	failedHopStacks atomic.Int64
}

// NewRecorder creates a Recorder primed with the request metadata known before
// the handler runs. callers may set additional fields (RequestBody, Region,
// etc.) after creation.
//
// requestHeaders must be a snapshot the caller no longer mutates — the
// Recorder retains it rather than cloning. Handlers do rewrite request headers
// (SQS's Query adapter resets Content-Type), so pass a clone of r.Header, not
// r.Header itself.
func NewRecorder(requestID string, timestamp time.Time, method, path, host, query string, requestHeaders http.Header) *Recorder {
	return &Recorder{
		entry: Entry{
			RequestID:      requestID,
			Timestamp:      timestamp,
			Method:         method,
			Path:           path,
			Host:           host,
			Query:          query,
			RequestHeaders: requestHeaders,
		},
	}
}

var stackBufPool = sync.Pool{
	New: func() any { b := make([]byte, 4096); return &b },
}

// CaptureStack returns the calling goroutine's stack trace as a string.
// Uses a pooled 4 KiB buffer; falls back to a larger allocation if truncated.
func CaptureStack() string {
	bufp := stackBufPool.Get().(*[]byte)
	buf := *bufp
	defer stackBufPool.Put(bufp)
	n := runtime.Stack(buf, false)
	if n < len(buf) {
		return string(buf[:n])
	}
	big := make([]byte, 16384)
	n = runtime.Stack(big, false)
	return string(big[:n])
}

// CaptureStackOnce records the calling goroutine's stack as the entry's stack,
// the first time it is called for this request. Later calls neither capture
// nor overwrite.
//
// Two response-writer wrappers sit in the chain (Logger's and RequestEvents'),
// they nest, and both want the stack at WriteHeader time. Capturing on every
// call meant paying runtime.Stack twice and keeping the *outer* one — the
// wrapper further from the handler, so the less informative of the two.
// First-write-wins keeps the innermost stack and makes the second call an
// atomic load.
func (r *Recorder) CaptureStackOnce() {
	if r == nil || !r.stackTaken.CompareAndSwap(false, true) {
		return
	}
	s := CaptureStack()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entry.Stack = s
}

// SetRequestBody stores an already-bounded copy of the request body. The
// caller owns bounding and copying — it is the only party that can decide how
// much of a body to read off the wire in the first place. truncated reports
// whether bytes were dropped; size is the full request size, or -1 when it is
// unknown (a chunked upload with no Content-Length), in which case the
// captured length stands in.
func (r *Recorder) SetRequestBody(body []byte, truncated bool, size int64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entry.RequestBody = body
	r.entry.RequestBodyTruncated = truncated
	if size < 0 {
		size = int64(len(body))
	}
	r.entry.RequestSize = size
}

// SetResponse populates response fields after the handler returns.
func (r *Recorder) SetResponse(headers http.Header, body []byte, status int, maxBody int, streaming bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entry.ResponseHeaders = headers.Clone()
	r.entry.StatusCode = status
	r.entry.Streaming = streaming
	if streaming {
		r.entry.ResponseBodyTruncated = true
		return
	}
	if len(body) > maxBody {
		// Copy: see SetRequestBody. The middleware's tee buffer is already
		// capped, but this method must bound whatever it is handed.
		r.entry.ResponseBody = append([]byte(nil), body[:maxBody]...)
		r.entry.ResponseBodyTruncated = true
	} else {
		r.entry.ResponseBody = body
	}
}

// SetResponseBodyTruncated marks the response body as truncated. Called by
// the middleware when the traceResponseWriter detects truncation at write
// time (before the final response body is available).
func (r *Recorder) SetResponseBodyTruncated() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entry.ResponseBodyTruncated = true
}

// SetServiceInfo records the detected service, operation, and region.
func (r *Recorder) SetServiceInfo(service, operation, region string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entry.Service = service
	r.entry.Operation = operation
	r.entry.Region = region
}

// SetMeta records request-level metadata (remote addr, user agent, referer, AWS error).
func (r *Recorder) SetMeta(remoteAddr, userAgent, referer, awsErrorCode, awsErrorMessage string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entry.RemoteAddr = remoteAddr
	r.entry.UserAgent = userAgent
	r.entry.Referer = referer
	r.entry.AWSErrorCode = awsErrorCode
	r.entry.AWSErrorMessage = awsErrorMessage
}

// SetParentRequestID records the request ID of the parent trace that
// triggered this one (for internal service-to-service calls).
func (r *Recorder) SetParentRequestID(parentID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entry.ParentRequestID = parentID
}

// SetDuration records the total request duration.
func (r *Recorder) SetDuration(d time.Duration) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entry.Duration = d
}

// AddHop appends an internal service-to-service hop. Safe to call from any
// goroutine. Returns the assigned Hop ID.
//
// A stack is captured for the hop only while this trace still has budget —
// see MaxHopStacks and MaxFailedHopStacks. A hop that arrives with a Stack
// already set keeps it and spends no budget.
func (r *Recorder) AddHop(h Hop) string {
	if r == nil {
		return ""
	}
	if h.Stack == "" && r.hopStackBudget(hopFailed(h)) {
		h.Stack = CaptureStack()
	}
	if len(h.RequestBody) > MaxHopBody {
		h.RequestBody = append([]byte(nil), h.RequestBody[:MaxHopBody]...)
		h.RequestBodyTruncated = true
	}
	if len(h.ResponseBody) > MaxHopBody {
		h.ResponseBody = append([]byte(nil), h.ResponseBody[:MaxHopBody]...)
		h.ResponseBodyTruncated = true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hopOrder++
	h.Order = r.hopOrder
	if h.ID == "" {
		h.ID = hopID(r.hopOrder)
	}
	r.entry.Hops = append(r.entry.Hops, h)
	return h.ID
}

func hopID(order int) string {
	return "hop-" + strconv.Itoa(order)
}

// hopFailed reports whether a hop is one a reader would go looking for.
func hopFailed(h Hop) bool {
	return h.Error != "" || h.ResponseStatus >= 400
}

// hopStackBudget claims one stack capture from the appropriate per-trace
// budget, reporting whether the claim succeeded.
func (r *Recorder) hopStackBudget(failed bool) bool {
	if failed {
		return r.failedHopStacks.Add(1) <= MaxFailedHopStacks
	}
	return r.hopStacks.Add(1) <= MaxHopStacks
}

// AddLog appends a structured log entry. Safe to call from any goroutine.
// Entries beyond 500 are dropped to prevent unbounded growth from polling loops.
func (r *Recorder) AddLog(le LogEntry) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.entry.LogEntries) >= 500 {
		return
	}
	r.entry.LogEntries = append(r.entry.LogEntries, le)
}

// AddMeta stores arbitrary key-value metadata.
func (r *Recorder) AddMeta(key string, value any) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entry.Metadata == nil {
		r.entry.Metadata = make(map[string]any)
	}
	r.entry.Metadata[key] = value
}

// RequestID returns the entry's request ID without deep-copying the whole
// trace (unlike Entry, which copies every hop body and log line).
func (r *Recorder) RequestID() string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.entry.RequestID
}

// Entry returns a copy of the captured trace data.
func (r *Recorder) Entry() Entry {
	if r == nil {
		return Entry{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.entry
	e.Hops = make([]Hop, len(r.entry.Hops))
	for i := range r.entry.Hops {
		e.Hops[i] = r.entry.Hops[i]
		e.Hops[i].RequestBody = append([]byte(nil), r.entry.Hops[i].RequestBody...)
		e.Hops[i].ResponseBody = append([]byte(nil), r.entry.Hops[i].ResponseBody...)
		if r.entry.Hops[i].RequestHeaders != nil {
			e.Hops[i].RequestHeaders = r.entry.Hops[i].RequestHeaders.Clone()
		}
	}
	e.LogEntries = make([]LogEntry, len(r.entry.LogEntries))
	copy(e.LogEntries, r.entry.LogEntries)
	if r.entry.Metadata != nil {
		e.Metadata = make(map[string]any, len(r.entry.Metadata))
		for k, v := range r.entry.Metadata {
			e.Metadata[k] = v
		}
	}
	return e
}

// ---- context helpers --------------------------------------------------------

type contextKey int

const recorderKey contextKey = 0

// ContextWithRecorder returns a new context carrying rec.
func ContextWithRecorder(ctx context.Context, rec *Recorder) context.Context {
	return context.WithValue(ctx, recorderKey, rec)
}

// RecorderFromContext retrieves the Recorder stored in ctx, or nil if debug is
// off (or the middleware hasn't run yet).
func RecorderFromContext(ctx context.Context) *Recorder {
	rec, _ := ctx.Value(recorderKey).(*Recorder)
	return rec
}

// Summary is a lightweight trace entry for list endpoints — no bodies, headers,
// hops, or log entries.
type Summary struct {
	RequestID  string        `json:"requestId"`
	Timestamp  time.Time     `json:"timestamp"`
	Method     string        `json:"method"`
	Path       string        `json:"path"`
	Service    string        `json:"service"`
	Operation  string        `json:"operation,omitempty"`
	StatusCode int           `json:"statusCode"`
	Duration   time.Duration `json:"duration"`
	Region     string        `json:"region,omitempty"`
	HopCount   int           `json:"hopCount,omitempty"`
	LogCount   int           `json:"logCount,omitempty"`
	Internal   bool          `json:"internal,omitempty"`
}

// ToSummary returns a lightweight summary of this entry.
func (e Entry) ToSummary() Summary {
	return Summary{
		RequestID:  e.RequestID,
		Timestamp:  e.Timestamp,
		Method:     e.Method,
		Path:       e.Path,
		Service:    e.Service,
		Operation:  e.Operation,
		StatusCode: e.StatusCode,
		Duration:   e.Duration,
		Region:     e.Region,
		HopCount:   len(e.Hops),
		LogCount:   len(e.LogEntries),
		Internal:   isInternalPath(e.Path),
	}
}

var internalPaths = map[string]bool{
	"/_health":          true,
	"/_metrics":         true,
	"/_events":          true,
	"/_events/request":  true,
	"/_events/request/": true,
	"/_overcast/inbox":  true,
	"/_overcast/inbox/": true,
	"/_/info":           true,
}

// isInternalPath reports whether p is polled by infrastructure or the web UI
// rather than driven by a real AWS client. It must stay aligned with
// middleware's isOperationalPollPath: the entire /_debug/* namespace counts
// as internal so UI polling (traces, state, metrics) never consumes the
// user-trace budget in the ring buffer.
func isInternalPath(p string) bool {
	if p == "/_debug" || strings.HasPrefix(p, "/_debug/") {
		return true
	}
	if internalPaths[p] {
		return true
	}
	// Match prefix paths like /_overcast/inbox/messages or /_events/request/<id>
	for prefix := range internalPaths {
		if len(prefix) > 1 && prefix[len(prefix)-1] == '/' && len(p) > len(prefix) && p[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// MarshalJSON overrides the default []byte → base64 encoding so that body
// fields appear as plain strings in the JSON output.
func (e Entry) MarshalJSON() ([]byte, error) {
	type shadow struct {
		RequestID             string         `json:"requestId"`
		Timestamp             time.Time      `json:"timestamp"`
		Duration              time.Duration  `json:"duration"`
		Method                string         `json:"method"`
		Path                  string         `json:"path"`
		Host                  string         `json:"host"`
		Query                 string         `json:"query,omitempty"`
		Service               string         `json:"service"`
		Operation             string         `json:"operation,omitempty"`
		Region                string         `json:"region"`
		Stack                 string         `json:"stack,omitempty"`
		RequestHeaders        http.Header    `json:"requestHeaders"`
		RequestBody           string         `json:"requestBody,omitempty"`
		RequestBodyTruncated  bool           `json:"requestBodyTruncated,omitempty"`
		RequestSize           int64          `json:"requestSize,omitempty"`
		ResponseHeaders       http.Header    `json:"responseHeaders"`
		ResponseBody          string         `json:"responseBody,omitempty"`
		ResponseBodyTruncated bool           `json:"responseBodyTruncated,omitempty"`
		StatusCode            int            `json:"statusCode"`
		Streaming             bool           `json:"streaming,omitempty"`
		Hops                  []Hop          `json:"hops,omitempty"`
		LogEntries            []LogEntry     `json:"logEntries,omitempty"`
		AWSErrorCode          string         `json:"awsErrorCode,omitempty"`
		AWSErrorMessage       string         `json:"awsErrorMessage,omitempty"`
		RemoteAddr            string         `json:"remoteAddr,omitempty"`
		UserAgent             string         `json:"userAgent,omitempty"`
		Referer               string         `json:"referer,omitempty"`
		ParentRequestID       string         `json:"parentRequestId,omitempty"`
		XRayTraceID           string         `json:"xrayTraceId,omitempty"`
		Metadata              map[string]any `json:"metadata,omitempty"`
	}
	return json.Marshal(shadow{
		RequestID:             e.RequestID,
		Timestamp:             e.Timestamp,
		Duration:              e.Duration,
		Method:                e.Method,
		Path:                  e.Path,
		Host:                  e.Host,
		Query:                 e.Query,
		Service:               e.Service,
		Operation:             e.Operation,
		Region:                e.Region,
		Stack:                 e.Stack,
		RequestHeaders:        e.RequestHeaders,
		RequestBody:           string(e.RequestBody),
		RequestBodyTruncated:  e.RequestBodyTruncated,
		RequestSize:           e.RequestSize,
		ResponseHeaders:       e.ResponseHeaders,
		ResponseBody:          string(e.ResponseBody),
		ResponseBodyTruncated: e.ResponseBodyTruncated,
		StatusCode:            e.StatusCode,
		Streaming:             e.Streaming,
		Hops:                  e.Hops,
		LogEntries:            e.LogEntries,
		AWSErrorCode:          e.AWSErrorCode,
		AWSErrorMessage:       e.AWSErrorMessage,
		RemoteAddr:            e.RemoteAddr,
		UserAgent:             e.UserAgent,
		Referer:               e.Referer,
		ParentRequestID:       e.ParentRequestID,
		XRayTraceID:           e.XRayTraceID,
		Metadata:              e.Metadata,
	})
}

// MarshalJSON overrides the default []byte → base64 encoding for Hop body
// fields.
func (h Hop) MarshalJSON() ([]byte, error) {
	type shadow struct {
		ID                    string        `json:"id"`
		Parent                string        `json:"parent,omitempty"`
		RequestID             string        `json:"requestId,omitempty"`
		Order                 int           `json:"order"`
		CallerService         string        `json:"callerService"`
		CallerOperation       string        `json:"callerOperation,omitempty"`
		Service               string        `json:"service"`
		Operation             string        `json:"operation"`
		TargetURI             string        `json:"targetUri,omitempty"`
		RequestHeaders        http.Header   `json:"requestHeaders,omitempty"`
		RequestBody           string        `json:"requestBody,omitempty"`
		RequestBodyTruncated  bool          `json:"requestBodyTruncated,omitempty"`
		ResponseStatus        int           `json:"responseStatus"`
		ResponseBody          string        `json:"responseBody,omitempty"`
		ResponseBodyTruncated bool          `json:"responseBodyTruncated,omitempty"`
		Duration              time.Duration `json:"duration"`
		Error                 string        `json:"error,omitempty"`
		Timestamp             time.Time     `json:"timestamp"`
		Noisy                 bool          `json:"noisy,omitempty"`
		Stack                 string        `json:"stack,omitempty"`
	}
	return json.Marshal(shadow{
		ID:                    h.ID,
		Parent:                h.Parent,
		RequestID:             h.RequestID,
		Order:                 h.Order,
		CallerService:         h.CallerService,
		CallerOperation:       h.CallerOperation,
		Service:               h.Service,
		Operation:             h.Operation,
		TargetURI:             h.TargetURI,
		RequestHeaders:        h.RequestHeaders,
		RequestBody:           string(h.RequestBody),
		RequestBodyTruncated:  h.RequestBodyTruncated,
		ResponseStatus:        h.ResponseStatus,
		ResponseBody:          string(h.ResponseBody),
		ResponseBodyTruncated: h.ResponseBodyTruncated,
		Duration:              h.Duration,
		Error:                 h.Error,
		Timestamp:             h.Timestamp,
		Noisy:                 h.Noisy,
		Stack:                 h.Stack,
	})
}
