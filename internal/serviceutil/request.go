// Package serviceutil provides shared, generic utilities used across all
// service handlers. The goal is to ensure every service follows the same
// patterns for request parsing, response writing, error handling, and
// logging — so that reading any service handler feels immediately familiar.
//
// TypeScript analogy: this is the equivalent of a shared utils/ or lib/
// directory that every route handler imports from.
//
// Usage pattern in a handler:
//
//	func (h *Handler) CreateQueue(w http.ResponseWriter, r *http.Request) {
//	    var req createQueueRequest
//	    if !serviceutil.DecodeJSON(w, r, &req) {
//	        return // error already written
//	    }
//	    if !serviceutil.RequireFields(w, r, req.QueueName, "QueueName") {
//	        return
//	    }
//	    // ... implementation
//	}
package serviceutil

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/your-org/overcast/internal/protocol"
)

// DecodeJSON decodes the request body as JSON into dst.
// On failure, writes a well-formed AWS JSON error response and returns false.
// The caller should return immediately when false is returned — the response
// has already been written.
//
// This replaces the repetitive pattern:
//
//	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
//	    protocol.WriteJSONError(w, r, protocol.ErrInvalidArgument("invalid request body"))
//	    return
//	}
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInvalidArgument(
			"The request body could not be parsed as JSON: "+err.Error(),
		))
		return false
	}
	return true
}

// RequireString checks that a string field is non-empty.
// On failure, writes a MissingParameter error and returns false.
//
//	if !serviceutil.RequireString(w, r, req.QueueName, "QueueName") {
//	    return
//	}
func RequireString(w http.ResponseWriter, r *http.Request, value, paramName string) bool {
	if value == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter(paramName))
		return false
	}
	return true
}

// QueryInt extracts an integer query parameter. Returns defaultVal if the
// parameter is absent or unparseable. Does not write an error — call
// RequireQueryInt if the parameter is mandatory.
//
//	maxKeys := serviceutil.QueryInt(r, "max-keys", 1000)
func QueryInt(r *http.Request, param string, defaultVal int) int {
	s := r.URL.Query().Get(param)
	if s == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return defaultVal
	}
	return v
}

// QueryString extracts a string query parameter, returning defaultVal if absent.
//
//	prefix := serviceutil.QueryString(r, "prefix", "")
func QueryString(r *http.Request, param, defaultVal string) string {
	if v := r.URL.Query().Get(param); v != "" {
		return v
	}
	return defaultVal
}

// HasQueryParam returns true if the named query parameter is present in the URL,
// regardless of its value. Useful for S3-style action parameters like ?location.
//
//	if serviceutil.HasQueryParam(r, "location") { ... }
func HasQueryParam(r *http.Request, param string) bool {
	_, ok := r.URL.Query()[param]
	return ok
}

// ClampInt returns v clamped to [min, max].
//
//	maxMessages := serviceutil.ClampInt(req.MaxNumberOfMessages, 1, 10)
func ClampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// DefaultInt returns v if v > 0, otherwise defaultVal.
// Useful for optional integer request fields that default to a non-zero value.
//
//	timeout := serviceutil.DefaultInt(req.VisibilityTimeout, 30)
func DefaultInt(v, defaultVal int) int {
	if v > 0 {
		return v
	}
	return defaultVal
}

// ParseIntDefault parses a string as an integer.
// Returns defaultVal if the string is empty or unparseable.
//
//	timeout := serviceutil.ParseIntDefault(q.Attributes["VisibilityTimeout"], 30)
func ParseIntDefault(s string, defaultVal int) int {
	if s == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return defaultVal
	}
	return v
}

// HeaderPrefix extracts all headers whose names start with prefix and returns
// them as a map with the prefix stripped from each key name.
// Keys are lowercased for consistency.
//
// Used by S3 to extract x-amz-meta-* user metadata headers:
//
//	meta := serviceutil.HeaderPrefix(r, "X-Amz-Meta-")
//	// {"author": "alice", "version": "1.0"}
func HeaderPrefix(r *http.Request, prefix string) map[string]string {
	result := make(map[string]string)
	prefixLower := toLower(prefix)
	for k, v := range r.Header {
		kLower := toLower(k)
		if hasPrefix(kLower, prefixLower) {
			stripped := kLower[len(prefixLower):]
			if len(v) > 0 {
				result[stripped] = v[0]
			}
		}
	}
	return result
}

// toLower and hasPrefix avoid importing strings to keep this package lean.
func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
