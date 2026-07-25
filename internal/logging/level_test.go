package logging

import (
	"testing"

	"go.uber.org/zap/zapcore"
)

// TestParseLevel_Trace proves OVERCAST_LOG_LEVEL=trace (in any case, with
// surrounding whitespace) parses to TraceLevel, since zapcore.ParseLevel has
// no knowledge of Overcast's custom fifth level.
func TestParseLevel_Trace(t *testing.T) {
	for _, in := range []string{"trace", "TRACE", "Trace", " trace ", "\ttrace\n"} {
		got, err := ParseLevel(in)
		if err != nil {
			t.Fatalf("ParseLevel(%q): unexpected error: %v", in, err)
		}
		if got != TraceLevel {
			t.Fatalf("ParseLevel(%q) = %v, want TraceLevel", in, got)
		}
	}
}

// TestParseLevel_StandardLevelsDelegate proves every standard zap level
// still parses correctly — ParseLevel must not break the existing levels
// while adding "trace".
func TestParseLevel_StandardLevelsDelegate(t *testing.T) {
	tests := map[string]zapcore.Level{
		"debug": zapcore.DebugLevel,
		"info":  zapcore.InfoLevel,
		"warn":  zapcore.WarnLevel,
		"error": zapcore.ErrorLevel,
	}
	for in, want := range tests {
		got, err := ParseLevel(in)
		if err != nil {
			t.Fatalf("ParseLevel(%q): unexpected error: %v", in, err)
		}
		if got != want {
			t.Fatalf("ParseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestParseLevel_Invalid proves an unrecognised level string is still
// rejected (ParseLevel must not silently swallow typos like "trrace").
func TestParseLevel_Invalid(t *testing.T) {
	if _, err := ParseLevel("not-a-level"); err == nil {
		t.Fatal("ParseLevel(\"not-a-level\") = nil error, want an error")
	}
}

// TestWrapLevelEncoder_RendersTraceLabel proves the wrapped encoder renders
// TraceLevel as the given label instead of zapcore's zero-value fallback
// ("Level(-2)"), while every other level still renders exactly as the base
// encoder would render it.
func TestWrapLevelEncoder_RendersTraceLabel(t *testing.T) {
	enc := WrapLevelEncoder(zapcore.LowercaseLevelEncoder, "trace")

	var captured string
	capture := capturingEncoder{append: func(s string) { captured = s }}
	enc(TraceLevel, &capture)
	if captured != "trace" {
		t.Fatalf("wrapped encoder for TraceLevel = %q, want %q", captured, "trace")
	}

	capture = capturingEncoder{append: func(s string) { captured = s }}
	enc(zapcore.InfoLevel, &capture)
	if captured != "info" {
		t.Fatalf("wrapped encoder for InfoLevel = %q, want %q (should delegate to base)", captured, "info")
	}
}

// TestWrapLevelEncoder_NilBaseDefaultsToLowercase proves a nil base encoder
// doesn't panic and falls back to lowercase rendering for non-trace levels.
func TestWrapLevelEncoder_NilBaseDefaultsToLowercase(t *testing.T) {
	enc := WrapLevelEncoder(nil, "trace")
	var captured string
	capture := capturingEncoder{append: func(s string) { captured = s }}
	enc(zapcore.WarnLevel, &capture)
	if captured != "warn" {
		t.Fatalf("nil-base wrapped encoder for WarnLevel = %q, want %q", captured, "warn")
	}
}

// capturingEncoder is a minimal zapcore.PrimitiveArrayEncoder that only
// implements AppendString, sufficient for exercising a LevelEncoder in
// isolation without pulling in a full array/object encoder.
type capturingEncoder struct {
	zapcore.PrimitiveArrayEncoder
	append func(string)
}

func (c *capturingEncoder) AppendString(s string) { c.append(s) }
