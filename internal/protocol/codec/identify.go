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
// Identifiers MUST NOT consume the request body — only headers, method,
// path, and (for query-protocol form bodies) the parsed form. Body
// consumption belongs to the codec's Decode pass.
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
	// PostForm requires a prior ParseForm. Doing it here would consume
	// the request body, which the contract forbids. The middleware
	// either parses the form earlier (downstream services already do
	// this for legacy handlers) or the codec parses it during Decode.
	//
	// For identification, we look at the URL query string (Action= can
	// appear there for some SDKs) and fall back to peeking at the body
	// only if the body is form-encoded AND already buffered by an
	// earlier middleware. We deliberately do NOT call r.ParseForm here.
	if action := r.URL.Query().Get("Action"); action != "" {
		return QueryXML, action, true
	}
	// No header-only operation hint available. The middleware will
	// still record the codec; the operation name will be resolved
	// later when the body is parsed (Phase 6 query decoder).
	return QueryXML, "", true
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
