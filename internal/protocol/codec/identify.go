package codec

import (
	"context"
	"net/http"
	"strings"
)

// Identifier inspects a request and, if it matches the protocol's
// identification rules, returns the matching Codec and the AWS operation
// name encoded in the request.
//
// Identifiers generally MUST NOT consume the request body — only headers,
// method, path, and (for query-protocol form bodies) the URL query string.
// Body consumption belongs to the codec's Decode pass.
//
// identifyQuery is the sole, documented exception: the AWS Query protocol
// encodes Action into the POST body for the overwhelming majority of real
// SDK traffic, so resolving an operation name at all requires reading the
// body. It does so via r.FormValue, which parses (and caches on the
// request) exactly once via the standard library's idempotent
// r.ParseForm — every later read of Action/other fields, whether by the
// router's own QueryDispatcher resolution, a legacy handler's
// r.FormValue calls, or the typed codec's Decode pass, reuses that same
// cached r.Form instead of re-reading (and re-draining) r.Body. See
// docs/plans/level2-codegen.md Track 1.1.
//
// The order in which identifiers are tried matters; see the Smithy AWS
// service protocol precision rules:
// https://smithy.io/2.0/guides/wire-protocol-selection.html#aws-service-protocol-precision
type Identifier interface {
	// Claim returns (codec, operationName, true) on a match, or
	// (nil, "", false) if the request does not match this protocol.
	Claim(r *http.Request) (Codec, string, bool)
}

// DefaultIdentifiers returns the built-in identifiers in precision
// order. CBOR is intentionally omitted in Phase 1 (introduced in
// Phase 4 with the cbor codec).
//
// REST-XML has no reliable header/path-only identification heuristic
// — services using it (S3, CloudFront) are routed by their own router
// today and stay on the bespoke path per docs/plans/smithy.md §10.
// It is therefore not in this list.
func DefaultIdentifiers() []Identifier {
	return []Identifier{
		identifyRPCv2CBOR{},
		identifyJSON10{},
		identifyJSON11{},
		identifyQuery{},
	}
}

// --- Smithy RPC v2 CBOR -----------------------------------------------

type identifyRPCv2CBOR struct{}

func (identifyRPCv2CBOR) Claim(r *http.Request) (Codec, string, bool) {
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Smithy-Protocol")), smithyProtocolRPCv2CBOR) {
		return nil, "", false
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "service" || parts[2] != "operation" || parts[3] == "" {
		return nil, "", false
	}
	return RPCv2CBOR, parts[3], true
}

// --- AWS JSON 1.0 ----------------------------------------------------

type identifyJSON10 struct{}

func (identifyJSON10) Claim(r *http.Request) (Codec, string, bool) {
	if !hasContentTypePrefix(r, "application/x-amz-json-1.0") {
		return nil, "", false
	}
	op, ok := operationFromAmzTarget(r)
	if !ok {
		return nil, "", false
	}
	return JSON10, op, true
}

// --- AWS JSON 1.1 ----------------------------------------------------

type identifyJSON11 struct{}

func (identifyJSON11) Claim(r *http.Request) (Codec, string, bool) {
	if !hasContentTypePrefix(r, "application/x-amz-json-1.1") {
		return nil, "", false
	}
	op, ok := operationFromAmzTarget(r)
	if !ok {
		return nil, "", false
	}
	return JSON11, op, true
}

// --- AWS Query --------------------------------------------------------

type identifyQuery struct{}

func (identifyQuery) Claim(r *http.Request) (Codec, string, bool) {
	if !hasContentTypePrefix(r, "application/x-www-form-urlencoded") {
		return nil, "", false
	}
	// r.FormValue parses the URL query string and, for POST bodies, the
	// form-encoded body — via the standard library's r.ParseForm, which
	// caches its result on the request (r.Form) and is a no-op on any
	// later call. Content-Type is already confirmed above, so the body
	// read here is exactly the parse the codec's Decode pass (and any
	// legacy handler) needs anyway; nothing downstream re-reads r.Body
	// directly, so there is no double-consumption hazard.
	return QueryXML, r.FormValue("Action"), true
}

// --- helpers ----------------------------------------------------------

// hasContentTypePrefix returns true if the request's Content-Type starts
// with prefix (case-insensitive, ignoring any "; charset=..." suffix).
func hasContentTypePrefix(r *http.Request, prefix string) bool {
	ct := r.Header.Get("Content-Type")
	if ct == "" {
		return false
	}
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.EqualFold(strings.TrimSpace(ct), prefix)
}

// operationFromAmzTarget extracts the operation name from the
// X-Amz-Target header, which has the form "{ServiceName}.{Operation}"
// (or sometimes "{ServiceName}_{Version}.{Operation}").
func operationFromAmzTarget(r *http.Request) (string, bool) {
	target := r.Header.Get("X-Amz-Target")
	if target == "" {
		return "", false
	}
	dot := strings.LastIndexByte(target, '.')
	if dot < 0 || dot == len(target)-1 {
		return "", false
	}
	return target[dot+1:], true
}

// --- request-context plumbing ----------------------------------------

type ctxKey int

const (
	ctxKeyCodec ctxKey = iota
	ctxKeyOperation
)

// WithDispatch returns a derived context carrying the picked codec and
// operation name. Used by the protocol-detection middleware.
func WithDispatch(ctx context.Context, c Codec, op string) context.Context {
	ctx = context.WithValue(ctx, ctxKeyCodec, c)
	ctx = context.WithValue(ctx, ctxKeyOperation, op)
	return ctx
}

// FromContext returns the codec and operation name stashed by the
// protocol-detection middleware, or (nil, "") if no codec was
// identified for this request (e.g. legacy services, or
// protocol dispatch middleware).
func FromContext(ctx context.Context) (Codec, string) {
	c, _ := ctx.Value(ctxKeyCodec).(Codec)
	op, _ := ctx.Value(ctxKeyOperation).(string)
	return c, op
}
