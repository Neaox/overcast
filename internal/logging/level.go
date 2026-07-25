// Package logging holds Overcast's custom log-level machinery: the TRACE
// level and its supporting parse/encode helpers.
//
// This lives in its own leaf package (depending on nothing but zap/zapcore)
// rather than in internal/serviceutil, which is where the rest of the
// logging helpers (ServiceLogger, etc.) live. internal/serviceutil imports
// internal/protocol, and internal/protocol imports internal/state (for
// AWSError wrapping) — so internal/state cannot import internal/serviceutil
// without an import cycle. internal/state's background maintenance/flush
// loops need TraceLevel directly (see internal/state/maintenance.go and
// hybrid.go), so the level definition itself has to sit below all of them.
// internal/serviceutil re-exports these symbols for convenience so existing
// callers (ServiceLogger.Trace, cmd/overcast) don't need to know about this
// split.
package logging

import (
	"strings"

	"go.uber.org/zap/zapcore"
)

// TraceLevel is Overcast's custom fifth log level, one step below zap's
// built-in DebugLevel. zap's level ladder is DebugLevel(-1) < InfoLevel(0) <
// WarnLevel(1) < ErrorLevel(2) < ...; TraceLevel(-2) extends it downward.
//
// The distinction (see CONTRIBUTING.md § Log levels for the full policy and
// its decision rule):
//   - DEBUG is event-driven and request-scoped: a line fires because a
//     specific client operation happened, and it explains Overcast's
//     reasoning about that operation (protocol identified, dispatch path
//     chosen, store decision made, replay/seed detail, migration step
//     detail).
//   - TRACE is time-driven or machinery-scoped: a line fires because time
//     passed or infrastructure cycled — health/readiness probe request logs,
//     /_debug/* polling request logs, per-tick flush/checkpoint/
//     maintenance/sweep cycle logs, buffer/pool internals — regardless of
//     what any client did.
//
// zapcore.Level has no built-in name for -2, so TraceLevel must be paired
// with WrapLevelEncoder (render "trace" instead of the zero-value
// "Level(-2)") and ParseLevel (accept the string "trace" from
// OVERCAST_LOG_LEVEL, which zapcore.ParseLevel does not recognise).
const TraceLevel = zapcore.Level(-2)

// ParseLevel parses a log-level string into a zapcore.Level, extending
// zapcore.ParseLevel with Overcast's "trace" level (case-insensitive,
// surrounding whitespace ignored). Every other input (including zap's
// standard "debug"/"info"/"warn"/"error"/"dpanic"/"panic"/"fatal", and their
// numeric forms) is delegated to zapcore.ParseLevel unchanged.
func ParseLevel(s string) (zapcore.Level, error) {
	if strings.EqualFold(strings.TrimSpace(s), "trace") {
		return TraceLevel, nil
	}
	return zapcore.ParseLevel(s)
}

// WrapLevelEncoder wraps an existing zapcore.LevelEncoder so that TraceLevel
// renders as label instead of the base encoder's zero-value fallback (which
// would otherwise print the unhelpful "Level(-2)"). Every other level is
// encoded exactly as base would encode it, so the rest of the level ladder is
// unaffected. Pass the label in whatever case matches the base encoder's
// convention — e.g. "trace" alongside zapcore.LowercaseLevelEncoder (used by
// the JSON/production encoder, which renders "info", "warn", ...) or "TRACE"
// alongside zapcore.CapitalLevelEncoder/CapitalColorLevelEncoder (used by the
// console/development encoder, which renders "INFO", "WARN", ...).
// A nil base defaults to zapcore.LowercaseLevelEncoder.
func WrapLevelEncoder(base zapcore.LevelEncoder, label string) zapcore.LevelEncoder {
	if base == nil {
		base = zapcore.LowercaseLevelEncoder
	}
	return func(l zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
		if l == TraceLevel {
			enc.AppendString(label)
			return
		}
		base(l, enc)
	}
}
