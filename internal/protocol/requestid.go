package protocol

import (
	"context"

	"github.com/google/uuid"
)

// contextKey is an unexported type for context keys in this package.
// Using a named type prevents collisions with keys from other packages.
// (TypeScript equivalent: a Symbol used as a Map key.)
type contextKey int

const requestIDKey contextKey = 0

// NewRequestID generates a new unique AWS-style request ID.
// AWS uses UUID v4 format without hyphens in some services, with hyphens in
// others. We always use with-hyphens — both forms are accepted by SDKs.
func NewRequestID() string {
	return uuid.New().String()
}

// ContextWithRequestID returns a new context carrying the given request ID.
// Called by the request-ID middleware on every incoming request.
func ContextWithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestIDFromContext retrieves the request ID stored in ctx.
// Returns a freshly generated ID if none was set — this is a safety net and
// should not happen in normal operation (the middleware always sets one).
func RequestIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey).(string); ok && id != "" {
		return id
	}
	return NewRequestID()
}
