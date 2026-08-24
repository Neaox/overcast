package lambda

// logging_json.go — AWS-shaped JSON logging for Lambda's LoggingConfig.
//
// A function whose LogFormat is JSON gets two behaviours that Text does not:
//
//   - System (platform) records are emitted as Telemetry-API-shaped JSON events
//     instead of the plain-text START / END / REPORT lines, and are filtered by
//     SystemLogLevel.
//   - Application records — the function's own stdout/stderr — are filtered by
//     ApplicationLogLevel, using the "level" key of each JSON record.
//
// Both are pinned to AWS's published references rather than inferred:
//
//   - Record shapes and field names: Lambda Telemetry API Event schema
//     reference. Overcast emits the subset of event types it genuinely
//     observes; a record it cannot populate honestly is not invented.
//   - Level assignment for each platform event: the "System log level event
//     mapping" table in the developer guide's log-level filtering page.
//
// Filtering applies to what reaches CloudWatch Logs and the X-Amz-Log-Result
// tail only. Telemetry/Logs API subscribers always see the complete set, which
// is what AWS documents: "Configuring the level of the system logs Lambda sends
// to CloudWatch doesn't affect Lambda Telemetry API behavior."

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Neaox/overcast/internal/services/lambda/initproto"
)

// Log formats, as modeled by com.amazonaws.lambda#LogFormat.
const (
	logFormatText = "Text"
	logFormatJSON = "JSON"
)

// Status values shared by the invoke and init phase records
// (com.amazonaws.lambda Telemetry API Status object).
const (
	invokeStatusSuccess = "success"
	invokeStatusFailure = "failure"
	invokeStatusError   = "error"
	invokeStatusTimeout = "timeout"
)

// functionVersionLatest is the unpublished version every invocation Overcast
// runs is served by.
const functionVersionLatest = "$LATEST"

// initPhaseInit is the Telemetry API InitPhase value for an ordinary
// initialization. The enum's other members describe things Overcast does not
// do: "invoke" is AWS's suppressed init (initialization code re-run inside an
// invocation after an error) and "snap-start" is SnapStart's restore.
const initPhaseInit = "init"

// logLevel is a Lambda log level. The zero value is deliberately the most
// detailed level so that an unparsed level can never silently filter more than
// intended; callers that need AWS's default use resolveLogLevels.
type logLevel int

const (
	logLevelTrace logLevel = iota
	logLevelDebug
	logLevelInfo
	logLevelWarn
	logLevelError
	logLevelFatal
)

var logLevelNames = [...]string{"TRACE", "DEBUG", "INFO", "WARN", "ERROR", "FATAL"}

func (l logLevel) String() string {
	if l < 0 || int(l) >= len(logLevelNames) {
		return "UNKNOWN"
	}
	return logLevelNames[l]
}

// parseLogLevel resolves a level name. Matching is case-insensitive because
// function output carries whatever casing the runtime's logger chose — AWS's
// own example of the custom-runtime contract uses lower case — while the API
// enum is validated separately, upper-case only.
func parseLogLevel(name string) (logLevel, bool) {
	for i, candidate := range logLevelNames {
		if strings.EqualFold(name, candidate) {
			return logLevel(i), true
		}
	}
	return 0, false
}

// resolveLogFormat returns the function's effective log format. Functions
// stored before LoggingConfig existed carry an empty string, which is Text.
func resolveLogFormat(fn *Function) string {
	if fn != nil && fn.LogFormat == logFormatJSON {
		return logFormatJSON
	}
	return logFormatText
}

// resolveLogLevels returns the function's effective application and system log
// levels. AWS defaults both to INFO when the format is JSON and no level was
// set; in Text mode neither is settable and no filtering happens, so the values
// returned there are the same INFO defaults and simply go unused.
func resolveLogLevels(fn *Function) (application, system logLevel) {
	application, system = logLevelInfo, logLevelInfo
	if fn == nil {
		return application, system
	}
	if parsed, ok := parseLogLevel(fn.ApplicationLogLevel); ok {
		application = parsed
	}
	if parsed, ok := parseLogLevel(fn.SystemLogLevel); ok {
		system = parsed
	}
	return application, system
}

// platformRecord is one system log record: its Telemetry API event type and the
// level AWS assigns it. Implementations are the record payloads below, and each
// one's systemLogLevel is a row of AWS's mapping table.
type platformRecord interface {
	eventType() string
	systemLogLevel() logLevel
}

// platformEvent is the envelope every system log record is wrapped in.
type platformEvent struct {
	Time   string         `json:"time"`
	Type   string         `json:"type"`
	Record platformRecord `json:"record"`
}

// The init-phase records. All three are sourced from the in-container init,
// which is the only thing that can see either end of the phase honestly: it is
// PID 1 in the execution environment and the proxy in front of the Runtime API,
// so it observes the runtime being spawned and the runtime's first GET /next
// without inferring either. They arrive as initproto.Records on the same
// seq-ordered frame stream as the container's output — see platformInitRecord
// and container_logs.go — which is what puts them in the right place in
// CloudWatch without any synchronisation.
//
// Overcast populates the members it genuinely knows and omits the optional ones
// it does not: runtimeVersion and runtimeVersionArn name a managed runtime
// build and its regional ARN, neither of which is observable here (and neither
// of which exists at all for a container-image function), and instanceId /
// instanceMaxMemory describe an AWS execution environment's identity and
// instance size. runtimeVersion being absent is also what makes initStart a
// DEBUG record rather than an INFO one, per AWS's system-log-level event
// mapping.

// platformInitStartRecord reports that the INIT phase began.
type platformInitStartRecord struct {
	InitializationType string `json:"initializationType"`
	Phase              string `json:"phase"`
	FunctionName       string `json:"functionName"`
	FunctionVersion    string `json:"functionVersion"`
}

func (platformInitStartRecord) eventType() string { return "platform.initStart" }

// systemLogLevel is the "initStart / runtimeVersion is not set" row of AWS's
// mapping table. Overcast never sets runtimeVersion, so this is never INFO.
func (platformInitStartRecord) systemLogLevel() logLevel { return logLevelDebug }

// platformInitRuntimeDoneRecord reports that the INIT phase finished — the
// runtime asked for its first invocation, or died without ever asking.
type platformInitRuntimeDoneRecord struct {
	InitializationType string `json:"initializationType"`
	Phase              string `json:"phase"`
	Status             string `json:"status"`
}

func (platformInitRuntimeDoneRecord) eventType() string { return "platform.initRuntimeDone" }

func (r platformInitRuntimeDoneRecord) systemLogLevel() logLevel {
	if r.Status == invokeStatusSuccess {
		return logLevelDebug
	}
	return logLevelWarn
}

// initReportMetrics is the Telemetry API InitReportMetrics object.
type initReportMetrics struct {
	DurationMs float64 `json:"durationMs"`
}

// platformInitReportRecord is the summary of the INIT phase.
//
// Its durationMs is measured inside the execution environment: from the init
// starting the phase — before it spawns the extensions or the runtime — to the
// runtime's first GET /next, as the init observes it. That is a different span
// from the Init Duration on the text REPORT line and in platform.report's
// metrics.initDurationMs, which the host measures from its own StartContainer
// call returning to its own handling of that same /next. They differ by a few
// milliseconds, usually with this one the larger, because the container is
// already running by the time the Docker API answers.
//
// It is measured here rather than reused from the host's pair for two reasons:
// a proactively initialized environment records no Init Duration at all (see
// takeInitDuration) and would otherwise have nothing to report, and the record
// arrives on a stream that races the host's own bookkeeping of the first /next,
// so a value read at ingest would not be deterministic.
type platformInitReportRecord struct {
	InitializationType string            `json:"initializationType"`
	Phase              string            `json:"phase"`
	Status             string            `json:"status"`
	Metrics            initReportMetrics `json:"metrics"`
}

func (platformInitReportRecord) eventType() string { return "platform.initReport" }

// systemLogLevel is the three "initReport" rows of AWS's mapping table, in the
// order they resolve: a failed phase is WARN whatever initialized it, a
// provisioned environment's report is INFO, and an on-demand one's is DEBUG.
func (r platformInitReportRecord) systemLogLevel() logLevel {
	switch {
	case r.Status != invokeStatusSuccess:
		return logLevelWarn
	case r.InitializationType != initTypeOnDemand:
		return logLevelInfo
	default:
		return logLevelDebug
	}
}

// platformInitRecord turns one observation the init reported into the Telemetry
// API record for it, filling in what only the host knows: the function it is,
// the version it serves and how its execution environment was initialized. It
// reports false for a record type or status this build does not recognise,
// which cannot happen — host and init are compiled together — but is not worth
// answering with an invented record if it ever does.
func platformInitRecord(rec initproto.Record, functionName, initType string) (platformRecord, bool) {
	var status string
	switch rec.Status {
	case initproto.StatusSuccess:
		status = invokeStatusSuccess
	case initproto.StatusError:
		status = invokeStatusError
	}
	switch rec.Type {
	case initproto.RecInitStart:
		return platformInitStartRecord{
			InitializationType: initType,
			Phase:              initPhaseInit,
			FunctionName:       functionName,
			FunctionVersion:    functionVersionLatest,
		}, true
	case initproto.RecInitRuntimeDone:
		if status == "" {
			return nil, false
		}
		return platformInitRuntimeDoneRecord{
			InitializationType: initType,
			Phase:              initPhaseInit,
			Status:             status,
		}, true
	case initproto.RecInitReport:
		if status == "" {
			return nil, false
		}
		return platformInitReportRecord{
			InitializationType: initType,
			Phase:              initPhaseInit,
			Status:             status,
			Metrics:            initReportMetrics{DurationMs: rec.DurationMs},
		}, true
	default:
		return nil, false
	}
}

// platformStartRecord reports that an invocation began. It replaces the plain
// text START line.
// traceContext is the Telemetry API TraceContext object
// (https://docs.aws.amazon.com/lambda/latest/dg/telemetry-schema-reference.html#TraceContext).
// Its value is the X-Amzn-Trace-Id header exactly as this invocation's runtime
// received it — Overcast mints one (unsampled) for every invocation, so the
// record carries the real correlation key rather than an invented one. spanId
// is optional and never set: Overcast does not mint spans. AWS's examples all
// show tracing present; whether it omits the member for a function without
// active tracing is not documented, so Overcast includes the header it
// genuinely handed over.
type traceContext struct {
	SpanID string `json:"spanId,omitempty"`
	Type   string `json:"type"`
	Value  string `json:"value"`
}

// tracingTypeXAmzn is the one TracingType value AWS documents.
const tracingTypeXAmzn = "X-Amzn-Trace-Id"

// traceContextFor wraps a trace header for a record, or nil when there is
// none to report.
func traceContextFor(traceID string) *traceContext {
	if traceID == "" {
		return nil
	}
	return &traceContext{Type: tracingTypeXAmzn, Value: traceID}
}

type platformStartRecord struct {
	RequestID string        `json:"requestId"`
	Version   string        `json:"version,omitempty"`
	Tracing   *traceContext `json:"tracing,omitempty"`
}

func (platformStartRecord) eventType() string        { return "platform.start" }
func (platformStartRecord) systemLogLevel() logLevel { return logLevelInfo }

// runtimeDoneMetrics is the Telemetry API RuntimeDoneMetrics object.
type runtimeDoneMetrics struct {
	DurationMs    float64 `json:"durationMs"`
	ProducedBytes int     `json:"producedBytes"`
}

// platformSpan is the Telemetry API Span object
// (https://docs.aws.amazon.com/lambda/latest/dg/telemetry-schema-reference.html#Span):
// a named slice of the phase, when it began, how long it took. Overcast emits
// the one span its in-container init can measure whole — responseLatency,
// the invocation being handed to the runtime to the runtime starting to send
// its answer. responseDuration ends only when the answer has finished
// streaming through the init's unbuffered proxy, and runtimeOverhead only at
// the runtime's next poll — both after platform.runtimeDone (which Overcast,
// like its END line, writes when the answer arrives) is already on its way,
// so neither is invented.
type platformSpan struct {
	Name       string  `json:"name"`
	Start      string  `json:"start"`
	DurationMs float64 `json:"durationMs"`
}

// platformSpansFor renders the init's measured spans, or nil when it
// measured none.
func platformSpansFor(measured []initproto.RecSpan) []platformSpan {
	if len(measured) == 0 {
		return nil
	}
	spans := make([]platformSpan, 0, len(measured))
	for _, m := range measured {
		spans = append(spans, platformSpan{
			Name:       m.Name,
			Start:      time.UnixMilli(m.StartMs).UTC().Format(platformEventTimeFormat),
			DurationMs: m.DurationMs,
		})
	}
	return spans
}

// platformRuntimeDoneRecord reports that the runtime finished the invocation.
// It replaces the plain text END line, and carries the status and response size
// that END has nowhere to put.
//
// ErrorType is set for the one outcome whose AWS name is documented — a
// runtime that exited (Runtime.ExitError; see logOutcome). A handler error
// and a timeout leave it absent, exactly as AWS's own runtimeDone examples
// for those statuses do.
type platformRuntimeDoneRecord struct {
	RequestID string              `json:"requestId"`
	Status    string              `json:"status"`
	ErrorType string              `json:"errorType,omitempty"`
	Metrics   *runtimeDoneMetrics `json:"metrics,omitempty"`
	Spans     []platformSpan      `json:"spans,omitempty"`
	Tracing   *traceContext       `json:"tracing,omitempty"`
}

func (platformRuntimeDoneRecord) eventType() string { return "platform.runtimeDone" }

func (r platformRuntimeDoneRecord) systemLogLevel() logLevel {
	if r.Status == invokeStatusSuccess {
		return logLevelDebug
	}
	return logLevelWarn
}

// reportMetrics is the Telemetry API ReportMetrics object.
type reportMetrics struct {
	DurationMs       float64  `json:"durationMs"`
	BilledDurationMs int64    `json:"billedDurationMs"`
	MemorySizeMB     int      `json:"memorySizeMB"`
	MaxMemoryUsedMB  int      `json:"maxMemoryUsedMB"`
	InitDurationMs   *float64 `json:"initDurationMs,omitempty"`
}

// platformReportRecord is the per-invocation report. It replaces the plain text
// REPORT line.
type platformReportRecord struct {
	RequestID string        `json:"requestId"`
	Status    string        `json:"status"`
	ErrorType string        `json:"errorType,omitempty"`
	Metrics   reportMetrics `json:"metrics"`
	Tracing   *traceContext `json:"tracing,omitempty"`
}

func (platformReportRecord) eventType() string { return "platform.report" }

func (r platformReportRecord) systemLogLevel() logLevel {
	if r.Status == invokeStatusSuccess {
		return logLevelInfo
	}
	return logLevelWarn
}

// platformEventTimeFormat is the millisecond-precision UTC form AWS uses for
// the "time" field of every system log record.
const platformEventTimeFormat = "2006-01-02T15:04:05.000Z"

// renderPlatformEvent marshals one system log record as the single JSON line
// that goes to CloudWatch Logs.
func renderPlatformEvent(now time.Time, rec platformRecord) string {
	encoded, err := json.Marshal(platformEvent{
		Time:   now.UTC().Format(platformEventTimeFormat),
		Type:   rec.eventType(),
		Record: rec,
	})
	if err != nil {
		// Every record type here is a plain struct of strings and numbers, so
		// this is unreachable; returning empty keeps a hypothetical failure
		// from writing a half-formed line into a customer's log stream.
		return ""
	}
	return string(encoded)
}

// emitPlatformRecord writes one system log record in the function's configured
// log format: textLine in Text mode, the Telemetry-API-shaped JSON event in
// JSON mode. An empty textLine means the record has no plain-text counterpart
// and is skipped entirely in Text mode.
//
// Telemetry/Logs API subscribers get the JSON event whichever format is
// configured, and get it unfiltered: "the Telemetry API always emits platform
// events such as START and REPORT in JSON format. Configuring the format of the
// system logs Lambda sends to CloudWatch doesn't affect Lambda Telemetry API
// behavior", and the same page says the same of the level. SystemLogLevel
// therefore filters the CloudWatch and tail copies only.
func (ci *containerInstance) emitPlatformRecord(requestID, textLine string, rec platformRecord) {
	var event string
	line, deliver := textLine, true
	switch {
	case ci.logFormat == logFormatJSON:
		event = renderPlatformEvent(ci.clk.Now(), rec)
		line = event
		deliver = rec.systemLogLevel() >= ci.sysLogLevel
	case ci.hasPlatformSubscribers():
		// Text mode: CloudWatch gets the plain-text line, but a subscriber
		// still gets the JSON event. Marshalled only when there is somebody to
		// send it to, because this runs three times per invocation on the warm
		// path and almost nothing subscribes.
		event = renderPlatformEvent(ci.clk.Now(), rec)
	}
	if event != "" {
		ci.publishPlatformRecord(event)
	}
	if line == "" {
		return
	}
	if deliver {
		// Into the invocation's own buffer: the tail for a request is START,
		// its lines, END, REPORT, and nothing else — which is only true because
		// the request ID travels with the record rather than being inferred
		// from what happens to be current.
		ci.deliverSynthLine(requestID, line)
	}
}

// emitInvocationStart writes the record that opens an invocation. traceID is
// the X-Amzn-Trace-Id header the runtime is handed for it, "" when the caller
// has none.
func (ci *containerInstance) emitInvocationStart(requestID, traceID string) {
	ci.emitPlatformRecord(
		requestID,
		"START RequestId: "+requestID+" Version: "+functionVersionLatest,
		platformStartRecord{
			RequestID: requestID,
			Version:   functionVersionLatest,
			Tracing:   traceContextFor(traceID),
		},
	)
}

// logOutcome pairs the Telemetry API Status of an invocation with the
// suffix AWS appends to its plain-text REPORT line. The two are not the same
// thing: a handler that returns an error is not a successful invocation in the
// JSON record, but AWS's text REPORT names a status only when the execution
// environment itself ended the invocation — a handler exception leaves the
// text REPORT unadorned, and changing that would be a Text-mode regression.
type logOutcome struct {
	status     string
	textStatus string
	// errorType is the AWS error-type name for the outcome, on the one
	// outcome whose name AWS actually documents: a runtime that exited is
	// Runtime.ExitError — the platform fault log's own format is
	// "Status: error<TAB>ErrorType: Runtime.ExitError"
	// (https://docs.aws.amazon.com/lambda/latest/dg/runtimes-logs-api.html,
	// the platform fault log). The others stay absent on the same page's
	// evidence: AWS's runtimeDone examples for both failure and timeout
	// carry no errorType at all, and inventing a name would be worse than
	// omitting an optional member.
	errorType string
}

var (
	outcomeSuccess      = logOutcome{status: invokeStatusSuccess}
	outcomeHandlerError = logOutcome{status: invokeStatusFailure}
	outcomeCrashed      = logOutcome{status: invokeStatusError, textStatus: "\tStatus: error", errorType: "Runtime.ExitError"}
	outcomeTimedOut     = logOutcome{status: invokeStatusTimeout, textStatus: "\tStatus: timeout"}
)

// emitInvocationEnd writes the two records that close an invocation: the
// runtime-done record (plain text END) and the report (plain text REPORT).
// producedBytes is the size of the response the handler returned, zero when it
// never produced one.
func (ci *containerInstance) emitInvocationEnd(requestID string, outcome logOutcome, elapsed time.Duration, maxMemoryMB, producedBytes int, traceID string) {
	initField, initMetric := "", (*float64)(nil)
	if initDur, ok := ci.takeInitDuration(); ok {
		ms := durationMillis(initDur)
		initField = fmt.Sprintf("\tInit Duration: %.2f ms", ms)
		initMetric = &ms
	}

	// runtimeDone's metrics prefer what the execution environment measured
	// about itself: the init's RecInvokeDone span (the runtime being handed
	// the event to its answer arriving back at the proxy) and the payload
	// size the runtime declared. The host's own pair is the fallback, and
	// the only source on the paths where the runtime never answered — a
	// crash, a timeout. platform.report keeps the host's span throughout:
	// AWS's runtimeDone and report durations are two different measurements
	// of two different things, and so are these.
	doneMetrics := &runtimeDoneMetrics{
		DurationMs:    durationMillis(elapsed),
		ProducedBytes: producedBytes,
	}
	var doneSpans []platformSpan
	if ci.logSink != nil {
		if measured, ok := ci.logSink.invokeDoneFor(requestID); ok {
			doneMetrics.DurationMs = measured.DurationMs
			if measured.ProducedBytes != nil {
				doneMetrics.ProducedBytes = int(*measured.ProducedBytes)
			}
			doneSpans = platformSpansFor(measured.Spans)
		}
	}
	ci.emitPlatformRecord(
		requestID,
		"END RequestId: "+requestID,
		platformRuntimeDoneRecord{
			RequestID: requestID,
			Status:    outcome.status,
			ErrorType: outcome.errorType,
			Metrics:   doneMetrics,
			Spans:     doneSpans,
			Tracing:   traceContextFor(traceID),
		},
	)
	ci.emitPlatformRecord(
		requestID,
		fmt.Sprintf("REPORT RequestId: %s\tDuration: %.2f ms\tBilled Duration: %d ms\tMemory Size: %d MB\tMax Memory Used: %d MB%s%s",
			requestID, durationMillis(elapsed), billedDuration(elapsed), ci.memorySize, maxMemoryMB, initField, outcome.textStatus),
		platformReportRecord{
			RequestID: requestID,
			Status:    outcome.status,
			ErrorType: outcome.errorType,
			Metrics: reportMetrics{
				DurationMs:       durationMillis(elapsed),
				BilledDurationMs: billedDuration(elapsed),
				MemorySizeMB:     ci.memorySize,
				MaxMemoryUsedMB:  maxMemoryMB,
				InitDurationMs:   initMetric,
			},
			Tracing: traceContextFor(traceID),
		},
	)
}

// applicationLogLevel returns the level Lambda assigns to one line of function
// output. A JSON object with a recognised "level" member uses it; everything
// else — unstructured text, malformed JSON, a non-string or unknown level — is
// INFO, per "If the 'level' value field is invalid or missing, Lambda assigns
// the log output the level INFO".
func applicationLogLevel(line string) logLevel {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "{") {
		// Cheap reject before paying for a parse: the overwhelming majority of
		// function output is not a JSON object, and this runs per log line.
		return logLevelInfo
	}
	var record struct {
		Level string `json:"level"`
	}
	if err := json.Unmarshal([]byte(trimmed), &record); err != nil {
		return logLevelInfo
	}
	if parsed, ok := parseLogLevel(record.Level); ok {
		return parsed
	}
	return logLevelInfo
}
