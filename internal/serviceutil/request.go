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
	"net/url"
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
//	    protocol.WriteJSONError(w, r, protocol.ErrSerialization("invalid request body"))
//	    return
//	}
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		// Drain and close so the connection can be reused.
		io.Copy(io.Discard, r.Body) //nolint:errcheck
		r.Body.Close()              //nolint:errcheck
		protocol.WriteJSONError(w, r, protocol.ErrSerialization(
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

// FoldHostname lowercases an ASCII hostname (a trailing :port is unaffected,
// being digits). A hostname is case-insensitive — RFC 4343 for DNS, RFC 3986
// §3.2.2 for the URI authority — so folding loses nothing, and lowercase is the
// canonical form both that section and RFC 3986 §6.2.2.1 tell producers to emit.
//
// Overcast needs this on both sides of a request. Inbound, middleware folds the
// Host before deciding who owns it, so casing cannot change which service
// answers. Outbound, the URLs and request-context fields minted here are folded,
// so what Overcast hands back does not carry the case of whichever caller
// happened to create the resource.
//
// Folding is pay-per-use: an already-lowercase host is returned unchanged with
// no copy, which is what keeps inbound classification allocation-free on every
// real request. ASCII-only by construction — DNS names are ASCII, an IDN arrives
// punycode-encoded, and Unicode case folding carries locale hazards a hostname
// comparison must not inherit. strings.ToLower has the same no-copy property but
// measured ~2.4x slower here, because its scan also tracks non-ASCII and cannot
// stop at the first upper-case byte.
func FoldHostname(hostname string) string {
	i := 0
	for ; i < len(hostname); i++ {
		if c := hostname[i]; c >= 'A' && c <= 'Z' {
			break
		}
	}
	if i == len(hostname) {
		return hostname
	}
	b := []byte(hostname)
	for ; i < len(b); i++ {
		if c := b[i]; c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// RequestBaseURL derives a base URL from proxy headers or the request host.
//
// The host is folded to its canonical lowercase form: this is the single funnel
// every client-facing URL is minted through (via ClientBaseURL), so folding here
// is what stops a mixed-case request baking its casing into a queue URL, an
// invoke endpoint or a stack output.
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
	host = FoldHostname(host)
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

// ClientIP returns the caller's IP address: the first entry of
// X-Forwarded-For when present and valid, otherwise the host portion of
// r.RemoteAddr. Used to populate AWS-shaped request contexts (API Gateway
// "identity.sourceIp", Lambda function URL "requestContext.http.sourceIp").
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			xff = xff[:i]
		}
		if ip := strings.TrimSpace(xff); ip != "" && net.ParseIP(ip) != nil {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return host
}

// RequestProtocol returns r.Proto, falling back to "HTTP/1.1". Used to
// populate AWS-shaped request contexts (API Gateway "identity.protocol",
// Lambda function URL "requestContext.http.protocol").
func RequestProtocol(r *http.Request) string {
	if r.Proto != "" {
		return r.Proto
	}
	return "HTTP/1.1"
}

// DomainPrefix returns the first label of host (e.g. "api" for
// "api.example.com"), stripping any port first. Falls back to "localhost"
// for an empty host. Used to populate AWS-shaped request contexts (API
// Gateway v2 / Lambda function URL "requestContext.domainPrefix").
func DomainPrefix(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if host == "" {
		return "localhost"
	}
	// requestContext.domainPrefix is data function code can branch on, so it
	// must not vary with how the caller typed the Host. See FoldHostname.
	host = FoldHostname(host)
	if i := strings.IndexByte(host, '.'); i > 0 {
		return host[:i]
	}
	return host
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

// HostRoutedURL mints the canonical AWS Host-routed URL for a resource:
//
//	{scheme}://{id}.{label}.{region}.{host}[:{port}]{path}
//
// It is the single place any service builds one, so a URL Overcast hands back
// is by construction one its own router can resolve — label must be a key of
// middleware's host-route table (use the LabelExecuteAPI / LabelLambdaURL /
// LabelAppSyncAPI constants, which that table is built from). serviceutil
// cannot import middleware, since middleware imports serviceutil, so the
// guarantee is enforced by round-trip tests that feed a minted URL back in as
// a Host header rather than by the type system.
//
// It builds on ClientBaseURL, not config.ExternalBaseURL: only ClientBaseURL
// falls back to the request port when cfg.Port is unset and honours
// cfg.TLSEnabled(). See docs/plans/harness-representativeness-audit.md finding 2.
//
// path is appended verbatim and may be empty (API Gateway v2's apiEndpoint has
// no path), "/" (Lambda function URLs), or a fixed route ("/graphql").
func HostRoutedURL(cfg *config.Config, r *http.Request, label, id, region, path string) string {
	return HostRoutedURLFromBase(ClientBaseURL(cfg, r), label, id, region, path)
}

// HostRoutedURLFromBase is HostRoutedURL for callers that have already resolved
// the client-facing base URL and have no *http.Request to hand — AppSync's
// typed handlers carry it on the context instead.
func HostRoutedURLFromBase(baseURL, label, id, region, path string) string {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return ""
	}
	host := id + "." + label
	if region != "" {
		host += "." + region
	}
	host += "." + u.Hostname()
	if port := u.Port(); port != "" {
		host = net.JoinHostPort(host, port)
	}
	u.Host = host
	u.Path = path
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// HostRoutedHostname is HostRoutedURL reduced to the bare DNS name, with no
// scheme, port or path — the shape AWS uses for fields that carry a hostname
// rather than a URL, such as AppSync's GraphqlApi.dns map.
//
// It is derived from HostRoutedURL rather than rebuilt, so the two forms cannot
// disagree about the grammar.
func HostRoutedHostname(cfg *config.Config, r *http.Request, label, id, region string) string {
	return HostRoutedHostnameFromBase(ClientBaseURL(cfg, r), label, id, region)
}

// HostRoutedHostnameFromBase is HostRoutedHostname for callers that already
// hold a resolved base URL.
func HostRoutedHostnameFromBase(baseURL, label, id, region string) string {
	u, err := url.Parse(HostRoutedURLFromBase(baseURL, label, id, region, ""))
	if err != nil {
		return ""
	}
	return u.Hostname()
}
