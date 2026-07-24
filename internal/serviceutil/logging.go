package serviceutil

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/mattn/go-isatty"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/Neaox/overcast/internal/protocol"
)

// ── Console-mode detection ───────────────────────────────────────────────────

var (
	ttyOnce sync.Once
	tty     bool
)

// consoleMode reports whether stdout is an interactive terminal.
// The result is cached after the first call.
func consoleMode() bool {
	ttyOnce.Do(func() {
		tty = isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd())
	})
	return tty
}

// ANSI helpers. Badges use 256-colour codes for solid backgrounds so they
// render as coloured pills: solid bg + complementary muted fg + space padding.
const ansiReset = "\033[0m"

// badge renders a pill-shaped label using Unicode half-block characters as
// rounded caps so the badge reads as a rounded rectangle even in plain fonts.
//
//	▐ LABEL ▌
//
// The left cap (▐) and right cap (▌) are painted in the badge background
// colour against the terminal's default background, giving the illusion of
// curved ends. The interior is a solid colour block with the text in fg.
func badge(label string, bg, fg int) string {
	// left rounded cap: fg = badge bg, no bg set
	leftCap := fmt.Sprintf("\033[38;5;%dm▐", bg)
	// solid interior: bg = badge bg, fg = text colour
	interior := fmt.Sprintf("\033[48;5;%d;38;5;%dm %s ", bg, fg, label)
	// right rounded cap: reset bg, fg = badge bg again
	rightCap := fmt.Sprintf("\033[0;38;5;%dm▌%s", bg, ansiReset)
	return leftCap + interior + rightCap
}

// serviceBadge colours — bg chosen to evoke AWS console brand tints;
// fg chosen to be readable but not glaring (off-white / near-black).
//
//	S3        forest-green   bg=64  fg=194  (pale mint on green)
//	SQS       amber/gold     bg=136 fg=222  (warm cream on amber)
//	DynamoDB  steel-blue     bg=25  fg=153  (pale blue on blue)
//	SNS       mauve          bg=125 fg=218  (pink-white on plum)
//	Lambda    burnt-orange   bg=166 fg=229  (pale yellow on orange)
var serviceBadges = map[string][2]int{
	"s3":       {64, 194},
	"sqs":      {136, 222},
	"dynamodb": {25, 153},
	"sns":      {125, 218},
	"lambda":   {166, 229},
}

func serviceTag(service string) string {
	colors, ok := serviceBadges[strings.ToLower(service)]
	if !ok {
		colors = [2]int{240, 255} // dark-gray bg, white fg fallback
	}
	return badge(strings.ToUpper(service), colors[0], colors[1])
}

// operationTag renders a secondary pill in dark-charcoal / medium-gray —
// visually lighter than the service badge so it reads as subordinate context.
func operationTag(op string) string {
	return badge(op, 237, 246) // charcoal bg, slate-gray fg
}

// ── tagCore ──────────────────────────────────────────────────────────────────

// tagCore wraps a zapcore.Core and prepends a fixed string to every log message.
// Nested tagCores stack naturally: the outermost prefix is written last, so the
// resulting message reads [SERVICE] [Operation] original message.
type tagCore struct {
	zapcore.Core
	tag string
}

func (c *tagCore) With(fields []zapcore.Field) zapcore.Core {
	return &tagCore{Core: c.Core.With(fields), tag: c.tag}
}

func (c *tagCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Core.Enabled(ent.Level) {
		return ce.AddCore(ent, c)
	}
	return ce
}

func (c *tagCore) Write(ent zapcore.Entry, fields []zapcore.Field) error {
	ent.Message = c.tag + " " + ent.Message
	return c.Core.Write(ent, fields)
}

// wrapTag wraps l's core with a tagCore that prepends tag to every message.
func wrapTag(l *zap.Logger, tag string) *zap.Logger {
	return l.WithOptions(zap.WrapCore(func(core zapcore.Core) zapcore.Core {
		return &tagCore{Core: core, tag: tag}
	}))
}

// ServiceLogger wraps a zap.Logger with service-scoped context.
// All log calls automatically include the service name as a structured field,
// so log lines are filterable by service without adding the field at every call.
//
// Create one per service in service.go and pass it to the handler:
//
//	type Service struct {
//	    log *serviceutil.ServiceLogger
//	    ...
//	}
//
//	func New(cfg *config.Config, store state.Store, logger *zap.Logger) *Service {
//	    return &Service{
//	        log: serviceutil.NewServiceLogger(logger, "s3"),
//	        ...
//	    }
//	}
type ServiceLogger struct {
	log     *zap.Logger
	service string
}

// ZapLogger returns the underlying *zap.Logger so callers that require the raw
// logger (e.g. Docker GC) can create named children from the service-scoped logger.
func (l *ServiceLogger) ZapLogger() *zap.Logger { return l.log }

// NewServiceLogger returns a ServiceLogger scoped to the named service.
// When stdout is an interactive terminal, log messages are prefixed with a
// coloured [SERVICE] tag so different services are instantly distinguishable
// when running locally. In non-interactive environments (CI, Docker) the tag
// is omitted and the structured "service" field carries the same information.
func NewServiceLogger(logger *zap.Logger, service string) *ServiceLogger {
	log := logger.With(zap.String("service", service))
	if consoleMode() {
		log = wrapTag(log, serviceTag(service))
	}
	return &ServiceLogger{log: log, service: service}
}

// Debug logs at DEBUG level — fine-grained detail useful during development.
// Use for: parsed request parameters, individual state reads/writes.
// Do NOT use for: every middleware step (already logged by Logger middleware).
func (l *ServiceLogger) Debug(msg string, fields ...zap.Field) {
	l.log.Debug(msg, fields...)
}

// Info logs at INFO level — significant lifecycle events.
// Use for: resource created/deleted, queue purged, bucket emptied.
// Do NOT use for: individual requests (already logged by Logger middleware).
func (l *ServiceLogger) Info(msg string, fields ...zap.Field) {
	l.log.Info(msg, fields...)
}

// Warn logs at WARN level — handled but unexpected conditions.
// Use for: oversized payloads, deprecated parameters, known limitations hit.
func (l *ServiceLogger) Warn(msg string, fields ...zap.Field) {
	l.log.Warn(msg, fields...)
}

// Error logs at ERROR level — failures producing 5xx responses.
// Use for: state backend failures, serialisation errors, panics.
// Always include zap.Error(err) — never log just the message without the error.
func (l *ServiceLogger) Error(msg string, fields ...zap.Field) {
	l.log.Error(msg, fields...)
}

// LogStateError logs a state backend failure at ERROR level with standard fields.
// This is the canonical way to log storage failures in service store.go files:
//
//	if aerr := s.store.putObject(ctx, obj); aerr != nil {
//	    s.log.LogStateError(r, "put object", aerr, zap.String("bucket", bucket), zap.String("key", key))
//	    protocol.WriteXMLError(w, r, aerr)
//	    return
//	}
func (l *ServiceLogger) LogStateError(r *http.Request, op string, aerr *protocol.AWSError, fields ...zap.Field) {
	base := []zap.Field{
		zap.String("operation", op),
		zap.String("request_id", protocol.RequestIDFromContext(r.Context())),
		zap.Error(aerr), // includes cause via aerr.Error() which calls Unwrap chain
	}
	l.log.Error("state operation failed", append(base, fields...)...)
}

// With returns a new ServiceLogger with the additional fields attached to every
// subsequent log call. Useful for adding operation-scoped context:
//
//	log := h.log.With(zap.String("bucket", bucket), zap.String("key", key))
//	log.Debug("fetching object")
func (l *ServiceLogger) With(fields ...zap.Field) *ServiceLogger {
	return &ServiceLogger{
		log:     l.log.With(fields...),
		service: l.service,
	}
}

// WithOperation returns a child ServiceLogger scoped to the named operation
// (e.g. "CreateQueue", "PutObject"). All log calls on the returned logger
// automatically include an "operation" structured field.
//
// In console mode, messages are also prefixed with a dim [Operation] tag so
// it is easy to trace which handler produced each log line:
//
//	10:42:03  DEBUG  [S3] [PutObject]  stored object  {bucket=foo key=bar}
//
// Typical usage at the top of a handler:
//
//	log := h.log.WithOperation("PutObject")
//	log.Debug("decoded request", zap.String("bucket", bucket))
func (l *ServiceLogger) WithOperation(op string) *ServiceLogger {
	log := l.log.With(zap.String("operation", op))
	if consoleMode() {
		log = wrapTag(log, operationTag(op))
	}
	return &ServiceLogger{log: log, service: l.service}
}

// Logger returns the underlying zap.Logger for cases where raw zap is needed.
func (l *ServiceLogger) Logger() *zap.Logger {
	return l.log
}
