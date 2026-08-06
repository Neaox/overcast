package trace

import (
	"context"
	"math"
	"sync/atomic"

	"go.uber.org/zap/zapcore"
)

// TracingCore wraps a zapcore.Core to capture every log entry into the
// per-request Recorder (when one is present in the context). It is created at
// startup and injected into the zap logger via zap.WrapCore. At request time,
// the DebugTrace middleware sets the current request context via SetContext /
// ClearContext, which the core reads on every Write call.
//
// When no Recorder is in context (debug off, or a non-request goroutine), log
// entries pass through to the inner core unchanged.
type TracingCore struct {
	ctx atomic.Pointer[context.Context]
}

// NewTracingCore creates a TracingCore with no active context.
func NewTracingCore() *TracingCore {
	return &TracingCore{}
}

// SetContext stores the current request context so that subsequent log calls
// within the same goroutine can find the Recorder.
func (tc *TracingCore) SetContext(ctx context.Context) {
	tc.ctx.Store(&ctx)
}

// ClearContext removes the current request context.
func (tc *TracingCore) ClearContext() {
	tc.ctx.Store(nil)
}

// context returns the currently stored context, or nil.
func (tc *TracingCore) context() context.Context {
	p := tc.ctx.Load()
	if p == nil {
		return nil
	}
	return *p
}

// Wrap returns a zapcore.Core that delegates to inner, appending log entries
// to the per-request Recorder when one is active.
func (tc *TracingCore) Wrap(inner zapcore.Core) zapcore.Core {
	return &tracingCoreWrapper{
		core: inner,
		tc:   tc,
	}
}

// tracingCoreWrapper wraps a zapcore.Core, adding trace capture.
type tracingCoreWrapper struct {
	core zapcore.Core
	tc   *TracingCore
}

func (w *tracingCoreWrapper) Enabled(lvl zapcore.Level) bool {
	return w.core.Enabled(lvl)
}

func (w *tracingCoreWrapper) With(fields []zapcore.Field) zapcore.Core {
	return &tracingCoreWrapper{
		core: w.core.With(fields),
		tc:   w.tc,
	}
}

func (w *tracingCoreWrapper) Check(entry zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if w.core.Enabled(entry.Level) {
		return ce.AddCore(entry, w)
	}
	return ce
}

func (w *tracingCoreWrapper) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	if ctx := w.tc.context(); ctx != nil {
		if rec := RecorderFromContext(ctx); rec != nil {
			rec.AddLog(LogEntry{
				Level:     entry.Level.String(),
				Message:   entry.Message,
				Timestamp: entry.Time,
				Fields:    coreFieldsToMap(fields),
			})
		}
	}
	return w.core.Write(entry, fields)
}

func (w *tracingCoreWrapper) Sync() error {
	return w.core.Sync()
}

func coreFieldsToMap(fields []zapcore.Field) map[string]any {
	if len(fields) == 0 {
		return nil
	}
	m := make(map[string]any, len(fields))
	for _, f := range fields {
		m[f.Key] = coreFieldValue(f)
	}
	return m
}

func coreFieldValue(f zapcore.Field) any {
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
	default:
		if f.Interface != nil {
			return f.Interface
		}
		return f.String
	}
}
