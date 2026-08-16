package mcp

// standard_headers.go — the request metadata 2026-07-28 mirrors into HTTP
// headers, and why it stops at HTTP.
//
// # What the headers are for
//
// `Mcp-Method`, and `Mcp-Name` on the three methods that name a thing, repeat
// values that are already in the body. The point is not the server, which is
// going to parse the body anyway — it is everything between:
//
//	"The Streamable HTTP transport mirrors selected JSON-RPC body fields into
//	 HTTP headers so that intermediaries (load balancers, gateways,
//	 observability tooling) can route and inspect requests without parsing the
//	 body."
//
// Which is also why the server must check they agree. A mirrored value that
// disagrees with the body means an intermediary routing on the header and a
// server acting on the body are handling different requests — the spec is
// explicit that this "prevents potential security vulnerabilities when
// different components in the network rely on different sources of truth".
//
// # Why stdio is exempt, rather than special-cased
//
// This is a property of one binding, not of the protocol: "All protocol
// metadata travels in the message body … A binding MAY additionally mirror
// selected body fields into envelope metadata." Stdio has no envelope to mirror
// into and no intermediaries to serve, so there is nothing to require.
//
// That matters here because ServeStdio has no dispatcher of its own — it
// synthesises an http.Request and pushes it through the same handler. Without a
// marker, a rule written for the HTTP binding would silently start rejecting
// every stdio request. The marker is a context value rather than a header
// precisely because a header could be forged by a real HTTP caller, and this
// decides whether a validation rule applies.
//
// # x-mcp-header is not implemented, deliberately
//
// The revision also lets a server mirror tool parameters into `Mcp-Param-*`
// headers by annotating them, and requires clients to support that. It does not
// require servers to use it: "While the use of `x-mcp-header` is optional for
// servers, clients MUST support this feature." None of Overcast's tools carries
// such an annotation, so there is nothing to mirror and nothing to validate.
// Implementing the machinery for zero annotations would be building a feature
// to have built it.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// stdioTransportKey marks a request as synthesised by the stdio loop.
// Unexported and of a private type, so nothing outside this package can set it
// and no HTTP caller can forge it.
type stdioTransportKey struct{}

// withStdioTransport marks a context as belonging to a stdio request.
func withStdioTransport(ctx context.Context) context.Context {
	return context.WithValue(ctx, stdioTransportKey{}, true)
}

// isStdioTransport reports whether this request came from the stdio loop rather
// than over HTTP.
func isStdioTransport(ctx context.Context) bool {
	marked, _ := ctx.Value(stdioTransportKey{}).(bool)
	return marked
}

// base64Sentinel wraps a header value that could not be sent as plain ASCII.
// Servers "MUST decode an encoded Mcp-Name or Mcp-Param-{Name} value before
// comparing it to the corresponding request body value" — so a name with a
// non-ASCII character in it must not read as a mismatch.
const (
	base64SentinelPrefix = "=?base64?"
	base64SentinelSuffix = "?="
)

// decodeHeaderValue undoes the sentinel encoding if present.
//
// A value that claims to be encoded but is not valid base64 is returned as it
// arrived, so it fails the comparison it was going to fail anyway rather than
// becoming a different error about encoding.
func decodeHeaderValue(value string) string {
	if !strings.HasPrefix(value, base64SentinelPrefix) || !strings.HasSuffix(value, base64SentinelSuffix) {
		return value
	}
	inner := value[len(base64SentinelPrefix) : len(value)-len(base64SentinelSuffix)]
	decoded, err := base64.StdEncoding.DecodeString(inner)
	if err != nil {
		return value
	}
	return string(decoded)
}

// namedMethods maps each method that must carry an Mcp-Name to the params field
// the header mirrors. The spec names exactly these three.
var namedMethods = map[string]string{
	"tools/call":     "name",
	"resources/read": "uri",
	"prompts/get":    "name",
}

// validateStandardHeaders checks the mirrored headers against the body.
//
// Applies only to a modern request over HTTP. A legacy request predates the
// requirement, and stdio has no headers to mirror — see the file comment.
func validateStandardHeaders(r *http.Request, req jsonRPCRequest, meta requestMeta) *rpcError {
	if !meta.modern || isStdioTransport(r.Context()) {
		return nil
	}

	method := strings.TrimSpace(r.Header.Get("Mcp-Method"))
	if method == "" {
		return headerMismatch("missing required Mcp-Method header")
	}
	if method != req.Method {
		return headerMismatch("Mcp-Method header value " + method +
			" does not match body method " + req.Method)
	}

	field, named := namedMethods[req.Method]
	if !named {
		return nil
	}
	want, err := stringParam(req.Params, field)
	if err != nil {
		// The body has no usable value to compare against. That is a malformed
		// request for the handler to reject on its own terms, with a message
		// about the parameter rather than about a header.
		return nil
	}
	got := decodeHeaderValue(strings.TrimSpace(r.Header.Get("Mcp-Name")))
	if got == "" {
		return headerMismatch("missing required Mcp-Name header for method " + req.Method)
	}
	if got != want {
		return headerMismatch("Mcp-Name header value " + got +
			" does not match body value " + want)
	}
	return nil
}

func headerMismatch(message string) *rpcError {
	return &rpcError{Code: RPCHeaderMismatch, Message: "header mismatch: " + message}
}

// stringParam reads one top-level string field out of a request's params.
func stringParam(params json.RawMessage, field string) (string, error) {
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(params, &decoded); err != nil {
		return "", err
	}
	raw, ok := decoded[field]
	if !ok {
		return "", errNoSuchParam
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return value, nil
}

// errNoSuchParam signals that a request carried no such field, which is
// different from carrying one that will not decode.
var errNoSuchParam = errors.New("no such param")
