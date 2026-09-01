package trace

import (
	"fmt"
	"math"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/overcast-sh/overcast/internal/clock"
)

// ZapFieldsToMap converts a slice of zapcore.Field into a plain map, making
// structured log entries usable outside the zap ecosystem. Duplicate code
// in middleware/logger.go is removed in favour of this shared function.
func ZapFieldsToMap(fields []zapcore.Field) map[string]any {
	if len(fields) == 0 {
		return nil
	}
	m := make(map[string]any, len(fields))
	for _, f := range fields {
		m[f.Key] = ZapFieldValue(f)
	}
	return m
}

// ZapFieldValue converts a single zapcore.Field into its Go value,
// handling all zap field types including nested arrays and objects.
func ZapFieldValue(f zapcore.Field) any {
	switch f.Type {
	case zapcore.StringType:
		return f.String
	case zapcore.Int64Type, zapcore.Int32Type, zapcore.Int16Type, zapcore.Int8Type:
		return f.Integer
	case zapcore.Uint64Type, zapcore.Uint32Type, zapcore.Uint16Type, zapcore.Uint8Type, zapcore.UintptrType:
		return f.Integer
	case zapcore.DurationType:
		return f.Integer
	case zapcore.BoolType:
		return f.Integer == 1
	case zapcore.Float64Type:
		return math.Float64frombits(uint64(f.Integer))
	case zapcore.Float32Type:
		return float32(math.Float64frombits(uint64(f.Integer)))
	case zapcore.TimeType, zapcore.TimeFullType:
		if f.Interface != nil {
			return f.Interface
		}
		return f.Integer
	case zapcore.ByteStringType:
		return string(f.Interface.([]byte))
	case zapcore.ErrorType:
		if f.Interface != nil {
			return f.Interface.(error).Error()
		}
		return nil
	case zapcore.StringerType:
		if f.Interface != nil {
			return f.Interface.(fmt.Stringer).String()
		}
		return nil
	case zapcore.ArrayMarshalerType:
		return f.Interface
	case zapcore.ObjectMarshalerType:
		return f.Interface
	case zapcore.BinaryType:
		return f.Interface
	case zapcore.ReflectType, zapcore.InlineMarshalerType, zapcore.Complex128Type, zapcore.Complex64Type,
		zapcore.NamespaceType, zapcore.SkipType, zapcore.UnknownType:
		if f.Interface != nil {
			return f.Interface
		}
		return f.String
	}
	return nil
}

// recorderCore wraps a zapcore.Core, capturing every log entry into a
// per-request Recorder before delegating to the inner core. Unlike the
// removed TracingCore, there is no shared global state: each *zap.Logger
// that wants trace capture creates its own recorderCore via
// zap.WrapCore, and that core holds a direct *Recorder reference
// (not a context). This is safe for concurrent use because the *Recorder
// is per-request and the core is per-logger.
type recorderCore struct {
	zapcore.Core
	rec *Recorder
	clk clock.Clock
}

// NewRecorderCore returns a zap.Option that wraps inner with a core that
// captures log entries into rec. When rec is nil the original core is
// returned unchanged — use this as a no-op guard at the call site.
func NewRecorderCore(rec *Recorder, clk clock.Clock) func(zapcore.Core) zapcore.Core {
	if rec == nil {
		return func(c zapcore.Core) zapcore.Core { return c }
	}
	return func(inner zapcore.Core) zapcore.Core {
		return &recorderCore{Core: inner, rec: rec, clk: clk}
	}
}

func (c *recorderCore) Check(entry zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(entry.Level) {
		return ce.AddCore(entry, c)
	}
	return ce
}

func (c *recorderCore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	c.rec.AddLog(LogEntry{
		Level:     entry.Level.String(),
		Message:   entry.Message,
		Timestamp: c.clk.Now(),
		Fields:    ZapFieldsToMap(fields),
	})
	return c.Core.Write(entry, fields)
}

// WithRecorder returns a zap.Logger that captures log entries into rec.
// When rec is nil the original logger is returned unchanged.
func WithRecorder(base *zap.Logger, rec *Recorder, clk clock.Clock) *zap.Logger {
	if rec == nil {
		return base
	}
	return base.WithOptions(zap.WrapCore(NewRecorderCore(rec, clk)))
}
