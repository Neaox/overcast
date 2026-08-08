package lambda

import (
	"testing"
	"time"
)

// logging_json_test.go pins the wire shapes and level mapping of JSON logging
// against AWS's published references, so a change to either has to be a
// deliberate edit of an expectation rather than a silent drift:
//
//   - record shapes: Lambda Telemetry API Event schema reference
//   - level mapping: "System log level event mapping" in the Lambda developer
//     guide's log-level filtering page
//
// Both are reproduced in the comments of logging_json.go.

var testRecordTime = time.Date(2026, 8, 9, 12, 34, 56, 789_000_000, time.UTC)

func TestParseLogLevel(t *testing.T) {
	// AWS accepts the level names case-insensitively in function output; the
	// API enum itself is upper-case only and is validated elsewhere.
	for _, tc := range []struct {
		in   string
		want logLevel
		ok   bool
	}{
		{"TRACE", logLevelTrace, true},
		{"DEBUG", logLevelDebug, true},
		{"debug", logLevelDebug, true},
		{"Info", logLevelInfo, true},
		{"WARN", logLevelWarn, true},
		{"ERROR", logLevelError, true},
		{"FATAL", logLevelFatal, true},
		{"NOTICE", 0, false},
		{"", 0, false},
	} {
		got, ok := parseLogLevel(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("parseLogLevel(%q) = (%v, %v), want (%v, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestLogLevelOrdering(t *testing.T) {
	// Lambda sends records at the configured level "and lower" in detail, i.e.
	// at or above it in severity. The ordering is what makes that a comparison.
	ordered := []logLevel{logLevelTrace, logLevelDebug, logLevelInfo, logLevelWarn, logLevelError, logLevelFatal}
	for i := 1; i < len(ordered); i++ {
		if !(ordered[i-1] < ordered[i]) {
			t.Fatalf("%v is not less severe than %v", ordered[i-1], ordered[i])
		}
	}
	if got := logLevelWarn.String(); got != "WARN" {
		t.Errorf("logLevelWarn.String() = %q, want %q", got, "WARN")
	}
}

func TestRenderPlatformEvent(t *testing.T) {
	initDur := 397.68
	tests := []struct {
		name string
		rec  platformRecord
		want string
	}{
		{
			name: "start",
			rec: platformStartRecord{
				RequestID: "6d68ca91-49c9-448d-89b8-7ca3e6dc66aa",
				Version:   "$LATEST",
			},
			want: `{"time":"2026-08-09T12:34:56.789Z","type":"platform.start","record":{"requestId":"6d68ca91-49c9-448d-89b8-7ca3e6dc66aa","version":"$LATEST"}}`,
		},
		{
			name: "runtimeDone success",
			rec: platformRuntimeDoneRecord{
				RequestID: "req-1",
				Status:    invokeStatusSuccess,
				Metrics:   &runtimeDoneMetrics{DurationMs: 140, ProducedBytes: 16},
			},
			want: `{"time":"2026-08-09T12:34:56.789Z","type":"platform.runtimeDone","record":{"requestId":"req-1","status":"success","metrics":{"durationMs":140,"producedBytes":16}}}`,
		},
		{
			name: "runtimeDone failure carries errorType",
			rec: platformRuntimeDoneRecord{
				RequestID: "req-1",
				Status:    invokeStatusError,
				ErrorType: "Runtime.ExitError",
			},
			want: `{"time":"2026-08-09T12:34:56.789Z","type":"platform.runtimeDone","record":{"requestId":"req-1","status":"error","errorType":"Runtime.ExitError"}}`,
		},
		{
			name: "report with init duration",
			rec: platformReportRecord{
				RequestID: "req-1",
				Status:    invokeStatusSuccess,
				Metrics: reportMetrics{
					DurationMs:       693.92,
					BilledDurationMs: 694,
					MemorySizeMB:     128,
					MaxMemoryUsedMB:  84,
					InitDurationMs:   &initDur,
				},
			},
			want: `{"time":"2026-08-09T12:34:56.789Z","type":"platform.report","record":{"requestId":"req-1","status":"success","metrics":{"durationMs":693.92,"billedDurationMs":694,"memorySizeMB":128,"maxMemoryUsedMB":84,"initDurationMs":397.68}}}`,
		},
		{
			name: "report without init duration omits it",
			rec: platformReportRecord{
				RequestID: "req-1",
				Status:    invokeStatusTimeout,
				Metrics: reportMetrics{
					DurationMs:       3000,
					BilledDurationMs: 3000,
					MemorySizeMB:     128,
					MaxMemoryUsedMB:  40,
				},
			},
			want: `{"time":"2026-08-09T12:34:56.789Z","type":"platform.report","record":{"requestId":"req-1","status":"timeout","metrics":{"durationMs":3000,"billedDurationMs":3000,"memorySizeMB":128,"maxMemoryUsedMB":40}}}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := renderPlatformEvent(testRecordTime, tc.rec); got != tc.want {
				t.Errorf("renderPlatformEvent()\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}

func TestPlatformRecordSystemLogLevel(t *testing.T) {
	// The AWS mapping table, row for row.
	tests := []struct {
		name string
		rec  platformRecord
		want logLevel
	}{
		{"start", platformStartRecord{}, logLevelInfo},
		{"runtimeDone success", platformRuntimeDoneRecord{Status: invokeStatusSuccess}, logLevelDebug},
		{"runtimeDone failure", platformRuntimeDoneRecord{Status: invokeStatusFailure}, logLevelWarn},
		{"report success", platformReportRecord{Status: invokeStatusSuccess}, logLevelInfo},
		{"report timeout", platformReportRecord{Status: invokeStatusTimeout}, logLevelWarn},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.rec.systemLogLevel(); got != tc.want {
				t.Errorf("systemLogLevel() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestApplicationLogLevel(t *testing.T) {
	// "If the 'level' value field is invalid or missing, Lambda assigns the log
	// output the level INFO" — so every fallback here is INFO, never a drop.
	for _, tc := range []struct {
		name string
		line string
		want logLevel
	}{
		{"structured error", `{"timestamp":"2026-08-09T12:34:56.789Z","level":"ERROR","message":"boom"}`, logLevelError},
		{"lower case level", `{"level":"debug","msg":"my debug log"}`, logLevelDebug},
		{"unknown level name", `{"level":"NOTICE","msg":"x"}`, logLevelInfo},
		{"non-string level", `{"level":3,"msg":"x"}`, logLevelInfo},
		{"missing level", `{"msg":"x"}`, logLevelInfo},
		{"unstructured text", `hello world`, logLevelInfo},
		{"malformed json", `{"level":"ERROR"`, logLevelInfo},
		{"json array", `["level","ERROR"]`, logLevelInfo},
		{"empty", ``, logLevelInfo},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := applicationLogLevel(tc.line); got != tc.want {
				t.Errorf("applicationLogLevel(%q) = %v, want %v", tc.line, got, tc.want)
			}
		})
	}
}

func TestResolvedLoggingConfigDefaults(t *testing.T) {
	// A function stored before LoggingConfig existed has empty strings; those
	// must resolve to AWS's documented defaults rather than a zero logLevel,
	// which would be TRACE and would let everything through.
	fn := &Function{Name: "fn"}
	if got := resolveLogFormat(fn); got != logFormatText {
		t.Errorf("resolveLogFormat(empty) = %q, want %q", got, logFormatText)
	}

	fn.LogFormat = logFormatJSON
	app, sys := resolveLogLevels(fn)
	if app != logLevelInfo || sys != logLevelInfo {
		t.Errorf("resolveLogLevels(JSON, unset) = (%v, %v), want (INFO, INFO)", app, sys)
	}

	fn.ApplicationLogLevel = "ERROR"
	fn.SystemLogLevel = "WARN"
	app, sys = resolveLogLevels(fn)
	if app != logLevelError || sys != logLevelWarn {
		t.Errorf("resolveLogLevels(JSON, set) = (%v, %v), want (ERROR, WARN)", app, sys)
	}
}
