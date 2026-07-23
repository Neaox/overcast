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
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol"
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
		// Drain and close so the connection can be reused.
		io.Copy(io.Discard, r.Body) //nolint:errcheck
		r.Body.Close()              //nolint:errcheck
		protocol.WriteJSONError(w, r, protocol.ErrInvalidArgument(
			"The request body could not be parsed as JSON: "+err.Error(),
		))
		return false
	}
	// Drain any trailing bytes (e.g. whitespace after the JSON value) so the
	// HTTP/1.1 connection can be reused by the SDK client.
	io.Copy(io.Discard, r.Body) //nolint:errcheck
	r.Body.Close()              //nolint:errcheck
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

// ClientBaseURL returns the base URL services should embed in client-facing
// responses. OVERCAST_HOSTNAME/OVERCAST_PORT are authoritative in normal
// runtime config; request headers provide the fallback for httptest servers and
// reverse-proxy deployments where no explicit external hostname is configured.
func ClientBaseURL(cfg *config.Config, r *http.Request) string {
	if cfg != nil && cfg.Hostname != "" {
		scheme := "http"
		if cfg.TLSEnabled() {
			scheme = "https"
		}
		port := cfg.Port
		if port <= 0 {
			port = requestPort(r)
		}
		if port > 0 {
			return scheme + "://" + net.JoinHostPort(cfg.Hostname, strconv.Itoa(port))
		}
		return scheme + "://" + cfg.Hostname
	}
	if base := RequestBaseURL(r); base != "" {
		return base
	}
	if cfg != nil {
		return cfg.ExternalBaseURL()
	}
	return "http://localhost:4566"
}

// RequestBaseURL derives a base URL from proxy headers or the request host.
func RequestBaseURL(r *http.Request) string {
	if r == nil {
		return ""
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	if host == "" {
		return ""
	}
	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	return scheme + "://" + host
}

func requestPort(r *http.Request) int {
	if r == nil {
		return 0
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	_, port, err := net.SplitHostPort(host)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return 0
	}
	return n
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

func toLower(s string) string {
	return strings.ToLower(s)
}

func hasPrefix(s, prefix string) bool {
	return strings.HasPrefix(s, prefix)
}
