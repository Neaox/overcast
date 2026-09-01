package serviceutil

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/overcast-sh/overcast/internal/logging"
)

// TraceLevel/ParseLevel/WrapLevelEncoder themselves are unit-tested in
// internal/logging (their actual home — see that package's doc comment for
// why the level definition lives there instead of here). These tests cover
// only ServiceLogger.Trace, i.e. that serviceutil wires the shared TraceLevel
// through correctly.

// TestServiceLogger_TraceEmittedAtTraceThreshold proves a ServiceLogger.Trace
// call is emitted when the underlying core's minimum level is TraceLevel...
func TestServiceLogger_TraceEmittedAtTraceThreshold(t *testing.T) {
	core, logs := observer.New(logging.TraceLevel)
	log := NewServiceLogger(zap.New(core), "test")

	log.Trace("trace message", zap.String("k", "v"))

	entries := logs.FilterMessage("trace message").All()
	if len(entries) != 1 {
		t.Fatalf("trace message entries = %d, want 1", len(entries))
	}
	if entries[0].Level != logging.TraceLevel {
		t.Fatalf("logged level = %v, want TraceLevel", entries[0].Level)
	}
}

// ...and suppressed when the underlying core's minimum level is DebugLevel
// (one step above TraceLevel) — proving OVERCAST_LOG_LEVEL=debug keeps trace
// chatter out, matching the trace-vs-debug policy.
func TestServiceLogger_TraceSuppressedAtDebugThreshold(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	log := NewServiceLogger(zap.New(core), "test")

	log.Trace("trace message", zap.String("k", "v"))
	log.Debug("debug message")

	if n := logs.FilterMessage("trace message").Len(); n != 0 {
		t.Fatalf("trace message entries = %d, want 0 (suppressed at debug threshold)", n)
	}
	if n := logs.FilterMessage("debug message").Len(); n != 1 {
		t.Fatalf("debug message entries = %d, want 1 (debug still passes at debug threshold)", n)
	}
}

// TestParseLevelOrDefault pins the invalid-level fallback: bad values fall
// back to info with ok=false (the caller warns, naming the effective level)
// instead of failing startup; valid values incl. "trace" parse; empty means
// the default.
func TestParseLevelOrDefault(t *testing.T) {
	cases := []struct {
		in     string
		want   zapcore.Level
		wantOK bool
	}{
		{"", zapcore.InfoLevel, true},
		{"trace", logging.TraceLevel, true},
		{"debug", zapcore.DebugLevel, true},
		{"warn", zapcore.WarnLevel, true},
		{"verbose", zapcore.InfoLevel, false},
		{"nonsense", zapcore.InfoLevel, false},
	}
	for _, c := range cases {
		got, ok := ParseLevelOrDefault(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("ParseLevelOrDefault(%q) = (%v,%v), want (%v,%v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}
