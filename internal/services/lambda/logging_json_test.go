package lambda

import (
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/services/lambda/initproto"
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

func TestFunctionInstanceIdentity_changesWithLoggingConfig(t *testing.T) {
	// Given: a function configured for JSON logging at explicit levels. The
	// three settings reach the container as AWS_LAMBDA_LOG_FORMAT and
	// AWS_LAMBDA_LOG_LEVEL, and a process exec'd with the old values can never
	// observe the new ones — so each has to move the execution environment's
	// fingerprint and retire the warm containers baked with it.
	base := &Function{
		Name:                "fn",
		Runtime:             "nodejs22.x",
		Handler:             "index.handler",
		Timeout:             3,
		MemorySize:          128,
		LogFormat:           logFormatJSON,
		ApplicationLogLevel: "INFO",
		SystemLogLevel:      "INFO",
	}
	baseID := functionInstanceIdentity(base)

	// When/Then: changing any of them produces a different identity.
	for _, tc := range []struct {
		name  string
		apply func(fn *Function)
	}{
		{"log format to text", func(fn *Function) { fn.LogFormat = logFormatText }},
		{"application log level", func(fn *Function) { fn.ApplicationLogLevel = "ERROR" }},
		{"system log level", func(fn *Function) { fn.SystemLogLevel = "DEBUG" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutated := *base
			tc.apply(&mutated)
			if got := functionInstanceIdentity(&mutated); got == baseID {
				t.Fatalf("identity unchanged after %s change — a warm container would keep serving the old environment", tc.name)
			}
		})
	}

	// And: an unset level is AWS's INFO, so a configuration that only spells
	// the defaults out is the same environment. Otherwise reading a function
	// back and writing it again unchanged would cost a cold start.
	implicit := *base
	implicit.ApplicationLogLevel, implicit.SystemLogLevel = "", ""
	if got := functionInstanceIdentity(&implicit); got != baseID {
		t.Fatal("identity changed when the default levels were spelled out — a pointless cold start")
	}
}

// The init-phase records, against the shapes in AWS's Telemetry API Event
// schema reference. Overcast fills in every member AWS marks as required and
// omits the four optional ones it cannot observe (runtimeVersion,
// runtimeVersionArn, instanceId, instanceMaxMemory) — see platformInitRecord.
func TestRenderPlatformEvent_initPhaseRecords(t *testing.T) {
	tests := []struct {
		name string
		rec  platformRecord
		want string
	}{
		{
			name: "initStart",
			rec: platformInitStartRecord{
				InitializationType: initTypeOnDemand,
				Phase:              initPhaseInit,
				FunctionName:       "myFunction",
				FunctionVersion:    functionVersionLatest,
			},
			want: `{"time":"2026-08-09T12:34:56.789Z","type":"platform.initStart","record":{"initializationType":"on-demand","phase":"init","functionName":"myFunction","functionVersion":"$LATEST"}}`,
		},
		{
			name: "initRuntimeDone",
			rec: platformInitRuntimeDoneRecord{
				InitializationType: initTypeProvisioned,
				Phase:              initPhaseInit,
				Status:             invokeStatusSuccess,
			},
			want: `{"time":"2026-08-09T12:34:56.789Z","type":"platform.initRuntimeDone","record":{"initializationType":"provisioned-concurrency","phase":"init","status":"success"}}`,
		},
		{
			name: "initReport",
			rec: platformInitReportRecord{
				InitializationType: initTypeOnDemand,
				Phase:              initPhaseInit,
				Status:             invokeStatusSuccess,
				Metrics:            initReportMetrics{DurationMs: 125.33},
			},
			want: `{"time":"2026-08-09T12:34:56.789Z","type":"platform.initReport","record":{"initializationType":"on-demand","phase":"init","status":"success","metrics":{"durationMs":125.33}}}`,
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

// The init-phase rows of AWS's system log level event mapping. The initStart
// rows are conditioned on runtimeVersion, which Overcast never sets, so only
// the DEBUG one is reachable; the three initReport rows resolve in the order
// AWS lists them, with a failed phase overriding the initialization type.
func TestPlatformRecordSystemLogLevel_initPhaseRecords(t *testing.T) {
	tests := []struct {
		name string
		rec  platformRecord
		want logLevel
	}{
		{"initStart, runtimeVersion is not set", platformInitStartRecord{}, logLevelDebug},
		{"initRuntimeDone success", platformInitRuntimeDoneRecord{Status: invokeStatusSuccess}, logLevelDebug},
		{"initRuntimeDone failure", platformInitRuntimeDoneRecord{Status: invokeStatusError}, logLevelWarn},
		{"initReport on-demand", platformInitReportRecord{Status: invokeStatusSuccess, InitializationType: initTypeOnDemand}, logLevelDebug},
		{"initReport provisioned", platformInitReportRecord{Status: invokeStatusSuccess, InitializationType: initTypeProvisioned}, logLevelInfo},
		{"initReport failed on-demand", platformInitReportRecord{Status: invokeStatusError, InitializationType: initTypeOnDemand}, logLevelWarn},
		{"initReport failed provisioned", platformInitReportRecord{Status: invokeStatusError, InitializationType: initTypeProvisioned}, logLevelWarn},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.rec.systemLogLevel(); got != tc.want {
				t.Errorf("systemLogLevel() = %v, want %v", got, tc.want)
			}
		})
	}
}

// What the init reports, plus what only the host knows, is the AWS record.
func TestPlatformInitRecord(t *testing.T) {
	tests := []struct {
		name  string
		in    initproto.Record
		want  platformRecord
		wantK bool
	}{
		{
			name:  "initStart",
			in:    initproto.Record{Type: initproto.RecInitStart},
			want:  platformInitStartRecord{InitializationType: initTypeOnDemand, Phase: initPhaseInit, FunctionName: "fn", FunctionVersion: functionVersionLatest},
			wantK: true,
		},
		{
			name:  "initRuntimeDone success",
			in:    initproto.Record{Type: initproto.RecInitRuntimeDone, Status: initproto.StatusSuccess},
			want:  platformInitRuntimeDoneRecord{InitializationType: initTypeOnDemand, Phase: initPhaseInit, Status: invokeStatusSuccess},
			wantK: true,
		},
		{
			name:  "initReport carries the init-measured duration",
			in:    initproto.Record{Type: initproto.RecInitReport, Status: initproto.StatusError, DurationMs: 12.5},
			want:  platformInitReportRecord{InitializationType: initTypeOnDemand, Phase: initPhaseInit, Status: invokeStatusError, Metrics: initReportMetrics{DurationMs: 12.5}},
			wantK: true,
		},
		{
			name: "an unknown record type is not invented",
			in:   initproto.Record{Type: "somethingElse", Status: initproto.StatusSuccess},
		},
		{
			name: "a closing record with no status is not invented",
			in:   initproto.Record{Type: initproto.RecInitReport},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := platformInitRecord(tc.in, "fn", initTypeOnDemand)
			if ok != tc.wantK {
				t.Fatalf("platformInitRecord ok = %v, want %v", ok, tc.wantK)
			}
			if !ok {
				return
			}
			if got != tc.want {
				t.Errorf("platformInitRecord() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestRenderPlatformEvent_tracing pins the TraceContext member on the three
// invocation records: type and value exactly as the schema reference shapes
// them, spanId absent because Overcast mints no spans, and the whole member
// absent when the caller had no trace to report.
func TestRenderPlatformEvent_tracing(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 34, 56, 789e6, time.UTC)
	trace := "Root=1-62e900b2-710d76f009d6e7785905449a;Parent=0efbd19962d95b05;Sampled=0"

	got := renderPlatformEvent(now, platformStartRecord{
		RequestID: "req-1",
		Version:   "$LATEST",
		Tracing:   traceContextFor(trace),
	})
	want := `{"time":"2026-08-09T12:34:56.789Z","type":"platform.start","record":{"requestId":"req-1","version":"$LATEST","tracing":{"type":"X-Amzn-Trace-Id","value":"` + trace + `"}}}`
	if got != want {
		t.Errorf("start with tracing:\n got %s\nwant %s", got, want)
	}

	// No trace: the optional member is omitted, not emitted empty.
	got = renderPlatformEvent(now, platformReportRecord{
		RequestID: "req-1",
		Status:    invokeStatusSuccess,
		Tracing:   traceContextFor(""),
	})
	if strings.Contains(got, "tracing") {
		t.Errorf("report without a trace still carries a tracing member: %s", got)
	}
}
