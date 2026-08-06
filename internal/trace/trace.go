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
	"net/http"
	"strconv"
	"sync"
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

	RemoteAddr string `json:"remoteAddr,omitempty"`
	UserAgent  string `json:"userAgent,omitempty"`

	XRayTraceID string         `json:"xrayTraceId,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// Hop records one internal service-to-service call made during request
// processing.
type Hop struct {
	ID              string        `json:"id"`
	Parent          string        `json:"parent,omitempty"`
	Order           int           `json:"order"`
	CallerService   string        `json:"callerService"`
	CallerOperation string        `json:"callerOperation,omitempty"`
	Service         string        `json:"service"`
	Operation       string        `json:"operation"`
	TargetURI       string        `json:"targetUri,omitempty"`
	RequestHeaders  http.Header   `json:"requestHeaders,omitempty"`
	RequestBody     []byte        `json:"requestBody,omitempty"`
	ResponseStatus  int           `json:"responseStatus"`
	ResponseBody    []byte        `json:"responseBody,omitempty"`
	Duration        time.Duration `json:"duration"`
	Error           string        `json:"error,omitempty"`
	Timestamp       time.Time     `json:"timestamp"`
	Noisy           bool          `json:"noisy,omitempty"`
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
}

// NewRecorder creates a Recorder primed with the request metadata known before
// the handler runs. callers may set additional fields (RequestBody, Region,
// etc.) after creation.
func NewRecorder(requestID string, timestamp time.Time, method, path, host, query string, requestHeaders http.Header) *Recorder {
	return &Recorder{
		entry: Entry{
			RequestID:      requestID,
			Timestamp:      timestamp,
			Method:         method,
			Path:           path,
			Host:           host,
			Query:          query,
			RequestHeaders: requestHeaders.Clone(),
		},
	}
}

// SetRequestBody stores the request body, capping at maxBody.
func (r *Recorder) SetRequestBody(body []byte, maxBody int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(body) > maxBody {
		r.entry.RequestBody = body[:maxBody]
		r.entry.RequestBodyTruncated = true
	} else {
		r.entry.RequestBody = body
	}
	r.entry.RequestSize = int64(len(body))
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
		r.entry.ResponseBody = body[:maxBody]
		r.entry.ResponseBodyTruncated = true
	} else {
		r.entry.ResponseBody = body
	}
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

// SetMeta records request-level metadata (remote addr, user agent, AWS error).
func (r *Recorder) SetMeta(remoteAddr, userAgent, awsErrorCode, awsErrorMessage string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entry.RemoteAddr = remoteAddr
	r.entry.UserAgent = userAgent
	r.entry.AWSErrorCode = awsErrorCode
	r.entry.AWSErrorMessage = awsErrorMessage
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
func (r *Recorder) AddHop(h Hop) string {
	if r == nil {
		return ""
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

// AddLog appends a structured log entry. Safe to call from any goroutine.
func (r *Recorder) AddLog(le LogEntry) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
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

// Entry returns a copy of the captured trace data.
func (r *Recorder) Entry() Entry {
	if r == nil {
		return Entry{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.entry
	e.Hops = make([]Hop, len(r.entry.Hops))
	copy(e.Hops, r.entry.Hops)
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
