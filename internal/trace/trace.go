// Package trace provides request-tracing infrastructure for the debug
// namespace. When OVERCAST_DEBUG is enabled, every HTTP request is captured
// into an in-memory ring buffer and made queryable through /_overcast/debug/trace/*
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
	"unicode/utf8"
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

	RequestHeaders     http.Header `json:"requestHeaders"`
	RequestBody        []byte      `json:"requestBody,omitempty"`
	RequestBodyOmitted OmitReason  `json:"requestBodyOmitted,omitempty"`
	RequestSize        int64       `json:"requestSize,omitempty"`

	ResponseHeaders     http.Header `json:"responseHeaders"`
	ResponseBody        []byte      `json:"responseBody,omitempty"`
	ResponseBodyOmitted OmitReason  `json:"responseBodyOmitted,omitempty"`
	StatusCode          int         `json:"statusCode"`
	Streaming           bool        `json:"streaming,omitempty"`

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

// MaxInlinedHopBodies bounds the hop bodies one materialised Entry carries.
//
// A hop stores no bodies. Every hop is a router-dispatched request with a trace
// of its own — one AddHop call site, always through router.ServeHTTP, always
// setting RequestID — so a copy on the parent would be a second copy of bodies
// the buffer already holds. They are resolved from those traces when an Entry
// is materialised, which moves the bound from what the ring *retains* to what
// one response *carries*.
//
// The bound is still needed there. A deploy dispatches thousands of hops
// through a single trace and a reader opens them one at a time, so inlining
// every body would spend megabytes to render a table. Past this, a hop reports
// OmitTraceBudget and the UI links to the hop's own trace, where the body is
// intact — the difference from the old per-trace budget being that nothing has
// actually been deleted.
const MaxInlinedHopBodies = 8 << 20 // 8 MiB

// Hop records one internal service-to-service call made during request
// processing.
type Hop struct {
	ID                  string        `json:"id"`
	RequestID           string        `json:"requestId,omitempty"`
	Order               int           `json:"order"`
	CallerService       string        `json:"callerService"`
	CallerOperation     string        `json:"callerOperation,omitempty"`
	Service             string        `json:"service"`
	Operation           string        `json:"operation"`
	TargetURI           string        `json:"targetUri,omitempty"`
	RequestHeaders      http.Header   `json:"requestHeaders,omitempty"`
	RequestBody         []byte        `json:"requestBody,omitempty"`
	RequestBodyOmitted  OmitReason    `json:"requestBodyOmitted,omitempty"`
	ResponseStatus      int           `json:"responseStatus"`
	ResponseBody        []byte        `json:"responseBody,omitempty"`
	ResponseBodyOmitted OmitReason    `json:"responseBodyOmitted,omitempty"`
	Duration            time.Duration `json:"duration"`
	Error               string        `json:"error,omitempty"`
	Timestamp           time.Time     `json:"timestamp"`
	Noisy               bool          `json:"noisy,omitempty"`
	Stack               string        `json:"stack,omitempty"`
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
//
// The ring buffer holds the Recorder itself rather than a snapshot of it, so a
// Recorder stays live for as long as its trace is retained. That is what makes
// asynchronous work observable: CloudFormation provisions a stack on a
// goroutine that outlives the request, and every AddHop it makes after the
// response was written is visible to readers immediately. Readers materialise
// an Entry (or the cheaper Summary) on demand — writers never copy.
type Recorder struct {
	// Immutable after construction, so readers may take them without the
	// lock. The buffer sorts by timestamp, classifies by path and filters by
	// method on every list call; none of that should contend with a deploy
	// hammering AddHop.
	requestID string
	timestamp time.Time
	method    string
	path      string
	host      string
	query     string
	internal  bool

	mu       sync.RWMutex
	entry    Entry
	hopOrder int
	// hopRequestIDs indexes the request IDs of this trace's hops so the
	// HopsFor list filter — "which request originated this one?" — is an O(1)
	// lookup instead of a scan of every hop of every trace.
	hopRequestIDs map[string]struct{}

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
		requestID: requestID,
		timestamp: timestamp,
		method:    method,
		path:      path,
		host:      host,
		query:     query,
		internal:  isInternalPath(path),
		entry: Entry{
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
	if truncated {
		r.entry.RequestBodyOmitted = OmitSize
	}
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
		// Nothing was captured, so there is nothing to store — and the reason
		// must not be OmitSize: a streamed body was never held at all, which
		// reads differently from one we cut short.
		r.entry.ResponseBodyOmitted = OmitStreaming
		return
	}
	if len(body) > maxBody {
		// Copy: see SetRequestBody. The middleware's tee buffer is already
		// capped, but this method must bound whatever it is handed.
		r.entry.ResponseBody = append([]byte(nil), body[:maxBody]...)
		r.entry.ResponseBodyOmitted = OmitSize
	} else {
		r.entry.ResponseBody = body
	}
}

// SetResponseBodyOmitted records why the response body is not intact. Called by
// the middleware when the traceResponseWriter detects truncation at write time
// (before the final response body is available), which SetResponse cannot see
// because it is handed the already-capped tee buffer.
func (r *Recorder) SetResponseBodyOmitted(reason OmitReason) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entry.ResponseBodyOmitted = reason
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
// already set keeps it and spends no budget. No hop is ever dropped.
//
// **Bodies are deliberately not retained.** The hop's RequestID names a trace
// that already holds them, so keeping a copy here would double what the ring
// carries for a deploy; they are resolved from that trace on read. Callers may
// keep passing bodies — the provisioner has them to hand — and they are simply
// not stored. See MaxInlinedHopBodies.
func (r *Recorder) AddHop(h Hop) string {
	if r == nil {
		return ""
	}
	if h.Stack == "" && r.hopStackBudget(hopFailed(h)) {
		h.Stack = CaptureStack()
	}
	h.RequestBody, h.ResponseBody = nil, nil
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hopOrder++
	h.Order = r.hopOrder
	if h.ID == "" {
		h.ID = hopID(r.hopOrder)
	}
	if h.RequestID != "" {
		if r.hopRequestIDs == nil {
			r.hopRequestIDs = make(map[string]struct{})
		}
		r.hopRequestIDs[h.RequestID] = struct{}{}
	}
	r.entry.Hops = append(r.entry.Hops, h)
	return h.ID
}

// bodies returns copies of the request and response bodies and their omission
// reasons. It is what a parent's hop resolves against, so it copies for the
// same reason Entry does: the caller holds the result while the recorder is
// still live.
func (r *Recorder) bodies() (req []byte, reqOmit OmitReason, resp []byte, respOmit OmitReason) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]byte(nil), r.entry.RequestBody...), r.entry.RequestBodyOmitted,
		append([]byte(nil), r.entry.ResponseBody...), r.entry.ResponseBodyOmitted
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

// RequestID returns the request ID this trace was opened with. It is fixed at
// construction, so this takes no lock and stays correct for the whole life of
// the trace — including asynchronous work that outlives the request and needs
// to name its originating request.
func (r *Recorder) RequestID() string {
	if r == nil {
		return ""
	}
	return r.requestID
}

// MatchesSearch reports whether the trace's short scalar fields contain
// lowered, which the caller must already have lowercased — ListSummaries
// compiles the query once and then asks this of every retained trace.
//
// These are the fields that cost nothing to search: each is a short string
// already held on the recorder, so the whole check is one read lock and a
// handful of substring tests, no matter how much the trace has recorded. That
// is what keeps the list's 1 Hz poll cheap while a deploy is in flight.
//
// Bodies, log entries and hop errors are deliberately absent. Searching those
// means scanning up to MaxHopBodyBytes per trace, which is not something a list
// call can do inline; it is the deep search's job, and a test pins the
// separation so it is changed on purpose rather than by accident.
func (r *Recorder) MatchesSearch(lowered string) bool {
	if r == nil {
		return false
	}
	if lowered == "" {
		return true
	}
	// Fixed at construction, so no lock is needed for these two.
	if containsFold(r.requestID, lowered) || containsFold(r.path, lowered) {
		return true
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return containsFold(r.entry.Service, lowered) ||
		containsFold(r.entry.Operation, lowered) ||
		containsFold(r.entry.AWSErrorCode, lowered) ||
		containsFold(r.entry.AWSErrorMessage, lowered)
}

// containsFold reports whether field contains the already-lowercased query,
// ignoring case. The query is lowered once per list call by compileFilter.
//
// The two guards before the fallback are what keep this off the allocator.
// Lowering every field of every retained trace cost one allocation per field
// per list call — a thousand of them on a full buffer, once a second while
// someone types in the search box. A direct match settles a query typed in the
// field's own case, and a field with no uppercase in it cannot match any
// differently once lowered, which between them cover paths, request IDs and
// service names. Only a mixed-case field that did not match outright — an
// operation name, mostly — reaches the copy.
func containsFold(field, lowered string) bool {
	// Case folding never changes a string's byte length for the ASCII these
	// fields hold, and a query longer than the field cannot be inside it. This
	// is what keeps a query that matches nothing — every keystroke of a long
	// one — off the allocator entirely.
	if field == "" || len(lowered) > len(field) {
		return false
	}
	if strings.Contains(field, lowered) {
		return true
	}
	if !hasUpper(field) {
		return false
	}
	return strings.Contains(strings.ToLower(field), lowered)
}

// hasUpper reports whether s holds anything ToLower would change. Non-ASCII is
// reported as upper so the answer is never wrong for a script this cannot
// reason about byte-wise — the cost of being conservative is one allocation on
// a field that was not going to match anyway.
func hasUpper(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 'A' && c <= 'Z' || c >= utf8.RuneSelf {
			return true
		}
	}
	return false
}

// HasHop reports whether this trace dispatched a hop carrying the given
// request ID. O(1): the index is maintained by AddHop.
func (r *Recorder) HasHop(requestID string) bool {
	if r == nil || requestID == "" {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.hopRequestIDs[requestID]
	return ok
}

// Summary returns a lightweight view of the trace for list endpoints. It
// copies no bodies, headers, hops or log entries, so a list of a full ring
// buffer costs one read-lock and a handful of string headers per trace rather
// than a deep copy of every trace.
func (r *Recorder) Summary() Summary {
	if r == nil {
		return Summary{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.summaryLocked()
}

// summaryLocked is Summary for a caller that already holds the read lock.
//
// sync.RWMutex is not reentrant in any way worth relying on: a second RLock
// taken while a writer is queued between the two deadlocks. Deep search's
// snapshot needs the summary from inside its own read lock, so the body lives
// here and Summary is the locking wrapper.
// Callers must hold r.mu.
func (r *Recorder) summaryLocked() Summary {
	return Summary{
		RequestID:  r.requestID,
		Timestamp:  r.timestamp,
		Method:     r.method,
		Path:       r.path,
		Service:    r.entry.Service,
		Operation:  r.entry.Operation,
		StatusCode: r.entry.StatusCode,
		Duration:   r.entry.Duration,
		Region:     r.entry.Region,
		HopCount:   len(r.entry.Hops),
		LogCount:   len(r.entry.LogEntries),
		Internal:   r.internal,
	}
}

// Entry returns a deep copy of the captured trace data, current as of the
// moment it is called.
func (r *Recorder) Entry() Entry {
	if r == nil {
		return Entry{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	e := r.entry
	e.RequestID = r.requestID
	e.Timestamp = r.timestamp
	e.Method = r.method
	e.Path = r.path
	e.Host = r.host
	e.Query = r.query
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

var internalPaths = map[string]bool{
	"/_overcast/health":          true,
	"/_overcast/metrics":         true,
	"/_overcast/events":          true,
	"/_overcast/events/request":  true,
	"/_overcast/events/request/": true,
	"/_overcast/ses/inbox":       true,
	"/_overcast/ses/inbox/":      true,
	"/_overcast/info":            true,
}

// isInternalPath reports whether p is polled by infrastructure or the web UI
// rather than driven by a real AWS client. It must stay aligned with
// middleware's isOperationalPollPath: the entire /_overcast/debug/* namespace counts
// as internal so UI polling (traces, state, metrics) never consumes the
// user-trace budget in the ring buffer.
func isInternalPath(p string) bool {
	if p == "/_overcast/debug" || strings.HasPrefix(p, "/_overcast/debug/") {
		return true
	}
	if internalPaths[p] {
		return true
	}
	// Match prefix paths like /_overcast/ses/inbox/messages or /_overcast/events/request/<id>
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
		RequestBodyOmitted    OmitReason     `json:"requestBodyOmitted,omitempty"`
		RequestBodyTruncated  bool           `json:"requestBodyTruncated,omitempty"`
		RequestSize           int64          `json:"requestSize,omitempty"`
		ResponseHeaders       http.Header    `json:"responseHeaders"`
		ResponseBody          string         `json:"responseBody,omitempty"`
		ResponseBodyOmitted   OmitReason     `json:"responseBodyOmitted,omitempty"`
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
		RequestBodyOmitted:    e.RequestBodyOmitted,
		RequestBodyTruncated:  e.RequestBodyOmitted.Omitted(),
		RequestSize:           e.RequestSize,
		ResponseHeaders:       e.ResponseHeaders,
		ResponseBody:          string(e.ResponseBody),
		ResponseBodyOmitted:   e.ResponseBodyOmitted,
		ResponseBodyTruncated: e.ResponseBodyOmitted.Omitted(),
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
		RequestID             string        `json:"requestId,omitempty"`
		Order                 int           `json:"order"`
		CallerService         string        `json:"callerService"`
		CallerOperation       string        `json:"callerOperation,omitempty"`
		Service               string        `json:"service"`
		Operation             string        `json:"operation"`
		TargetURI             string        `json:"targetUri,omitempty"`
		RequestHeaders        http.Header   `json:"requestHeaders,omitempty"`
		RequestBody           string        `json:"requestBody,omitempty"`
		RequestBodyOmitted    OmitReason    `json:"requestBodyOmitted,omitempty"`
		RequestBodyTruncated  bool          `json:"requestBodyTruncated,omitempty"`
		ResponseStatus        int           `json:"responseStatus"`
		ResponseBody          string        `json:"responseBody,omitempty"`
		ResponseBodyOmitted   OmitReason    `json:"responseBodyOmitted,omitempty"`
		ResponseBodyTruncated bool          `json:"responseBodyTruncated,omitempty"`
		Duration              time.Duration `json:"duration"`
		Error                 string        `json:"error,omitempty"`
		Timestamp             time.Time     `json:"timestamp"`
		Noisy                 bool          `json:"noisy,omitempty"`
		Stack                 string        `json:"stack,omitempty"`
	}
	return json.Marshal(shadow{
		ID:                    h.ID,
		RequestID:             h.RequestID,
		Order:                 h.Order,
		CallerService:         h.CallerService,
		CallerOperation:       h.CallerOperation,
		Service:               h.Service,
		Operation:             h.Operation,
		TargetURI:             h.TargetURI,
		RequestHeaders:        h.RequestHeaders,
		RequestBody:           string(h.RequestBody),
		RequestBodyOmitted:    h.RequestBodyOmitted,
		RequestBodyTruncated:  h.RequestBodyOmitted.Omitted(),
		ResponseStatus:        h.ResponseStatus,
		ResponseBody:          string(h.ResponseBody),
		ResponseBodyOmitted:   h.ResponseBodyOmitted,
		ResponseBodyTruncated: h.ResponseBodyOmitted.Omitted(),
		Duration:              h.Duration,
		Error:                 h.Error,
		Timestamp:             h.Timestamp,
		Noisy:                 h.Noisy,
		Stack:                 h.Stack,
	})
}
