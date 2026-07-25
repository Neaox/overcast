package serviceutil

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/Neaox/overcast/internal/logging"
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
