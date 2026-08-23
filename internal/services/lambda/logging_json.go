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

// The init-phase records (platform.initStart, platform.initReport) are not
// emitted yet. Both need a hook at the moment the runtime first polls /next,
// which is where Overcast learns that init finished, and both are DEBUG at the
// default system log level — so their absence is invisible to a caller who has
// not asked for debug detail, and inventing timings to fill the gap would not
// be.
//
// TODO(priority:P3): emit platform.initStart and platform.initReport records for Lambda cold starts under JSON log format.

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

// platformStartRecord reports that an invocation began. It replaces the plain
// text START line.
type platformStartRecord struct {
	RequestID string `json:"requestId"`
	Version   string `json:"version,omitempty"`
}

func (platformStartRecord) eventType() string        { return "platform.start" }
func (platformStartRecord) systemLogLevel() logLevel { return logLevelInfo }

// runtimeDoneMetrics is the Telemetry API RuntimeDoneMetrics object.
type runtimeDoneMetrics struct {
	DurationMs    float64 `json:"durationMs"`
	ProducedBytes int     `json:"producedBytes"`
}

// platformRuntimeDoneRecord reports that the runtime finished the invocation.
// It replaces the plain text END line, and carries the status and response size
// that END has nowhere to put.
//
// ErrorType is modeled but never set. AWS puts one of its own error-type names
// there (Runtime.ExitError, Sandbox.Timedout and so on); Overcast knows the
// invocation failed but not which of those AWS would have chosen, and an
// invented name is worse than an absent optional field.
//
// TODO(priority:P3): populate errorType on Lambda platform.runtimeDone and platform.report records for failed invocations.
type platformRuntimeDoneRecord struct {
	RequestID string              `json:"requestId"`
	Status    string              `json:"status"`
	ErrorType string              `json:"errorType,omitempty"`
	Metrics   *runtimeDoneMetrics `json:"metrics,omitempty"`
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
// SystemLogLevel filters the CloudWatch and tail copies only; subscribers see
// every record either way.
func (ci *containerInstance) emitPlatformRecord(requestID, textLine string, rec platformRecord) {
	line, deliver := textLine, true
	if ci.logFormat == logFormatJSON {
		line = renderPlatformEvent(ci.clk.Now(), rec)
		deliver = rec.systemLogLevel() >= ci.sysLogLevel
	}
	if line == "" {
		return
	}
	ci.publishRuntimeLog("platform", line)
	if deliver {
		// Into the invocation's own buffer: the tail for a request is START,
		// its lines, END, REPORT, and nothing else — which is only true because
		// the request ID travels with the record rather than being inferred
		// from what happens to be current.
		ci.deliverSynthLine(requestID, line)
	}
}

// emitInvocationStart writes the record that opens an invocation.
func (ci *containerInstance) emitInvocationStart(requestID string) {
	ci.emitPlatformRecord(
		requestID,
		"START RequestId: "+requestID+" Version: "+functionVersionLatest,
		platformStartRecord{RequestID: requestID, Version: functionVersionLatest},
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
}

var (
	outcomeSuccess      = logOutcome{status: invokeStatusSuccess}
	outcomeHandlerError = logOutcome{status: invokeStatusFailure}
	outcomeCrashed      = logOutcome{status: invokeStatusError, textStatus: "\tStatus: error"}
	outcomeTimedOut     = logOutcome{status: invokeStatusTimeout, textStatus: "\tStatus: timeout"}
)

// emitInvocationEnd writes the two records that close an invocation: the
// runtime-done record (plain text END) and the report (plain text REPORT).
// producedBytes is the size of the response the handler returned, zero when it
// never produced one.
func (ci *containerInstance) emitInvocationEnd(requestID string, outcome logOutcome, elapsed time.Duration, maxMemoryMB, producedBytes int) {
	initField, initMetric := "", (*float64)(nil)
	if initDur, ok := ci.takeInitDuration(); ok {
		ms := durationMillis(initDur)
		initField = fmt.Sprintf("\tInit Duration: %.2f ms", ms)
		initMetric = &ms
	}

	ci.emitPlatformRecord(
		requestID,
		"END RequestId: "+requestID,
		platformRuntimeDoneRecord{
			RequestID: requestID,
			Status:    outcome.status,
			Metrics: &runtimeDoneMetrics{
				DurationMs:    durationMillis(elapsed),
				ProducedBytes: producedBytes,
			},
		},
	)
	ci.emitPlatformRecord(
		requestID,
		fmt.Sprintf("REPORT RequestId: %s\tDuration: %.2f ms\tBilled Duration: %d ms\tMemory Size: %d MB\tMax Memory Used: %d MB%s%s",
			requestID, durationMillis(elapsed), billedDuration(elapsed), ci.memorySize, maxMemoryMB, initField, outcome.textStatus),
		platformReportRecord{
			RequestID: requestID,
			Status:    outcome.status,
			Metrics: reportMetrics{
				DurationMs:       durationMillis(elapsed),
				BilledDurationMs: billedDuration(elapsed),
				MemorySizeMB:     ci.memorySize,
				MaxMemoryUsedMB:  maxMemoryMB,
				InitDurationMs:   initMetric,
			},
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
