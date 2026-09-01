package codec

import (
	"context"
	"net/http"
	"strings"

	"github.com/overcast-sh/overcast/internal/protocol"
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
// body. It reads it through protocol.ParseFormPreservingBody, which caches
// the fields on r.Form — every later read of Action/other fields, whether by
// the router's own QueryDispatcher resolution, a legacy handler's r.FormValue
// calls, or the typed codec's Decode pass, reuses that cached r.Form — and
// puts the bytes back on r.Body so a request that only *looked* like Query
// traffic still reaches its own handler with its payload. Identifiers run
// ahead of routing, so "this content type means Query" is a guess, and a
// wrong guess must not cost the request its body. See
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

// DefaultIdentifiers returns the built-in identifiers in precision order.
// Explicit Smithy RPC protocol markers take precedence over the AWS JSON and
// Query heuristics.
//
// REST-XML has no reliable header/path-only identification heuristic
// — services using it (S3, CloudFront) are routed by their own router
// today and stay on the bespoke path per docs/plans/smithy.md §10.
// It is therefore not in this list.
func DefaultIdentifiers() []Identifier {
	return []Identifier{
		identifyRPCv2CBOR{},
		identifyRPCv2JSON{},
		identifyJSON10{},
		identifyJSON11{},
		identifyQuery{},
	}
}

type identifyRPCv2JSON struct{}

func (identifyRPCv2JSON) Claim(r *http.Request) (Codec, string, bool) {
	return claimRPCv2(r, smithyProtocolRPCv2JSON, RPCv2JSON)
}

// --- Smithy RPC v2 CBOR -----------------------------------------------

type identifyRPCv2CBOR struct{}

func (identifyRPCv2CBOR) Claim(r *http.Request) (Codec, string, bool) {
	return claimRPCv2(r, smithyProtocolRPCv2CBOR, RPCv2CBOR)
}

func claimRPCv2(r *http.Request, protocolMarker string, wireCodec Codec) (Codec, string, bool) {
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Smithy-Protocol")), protocolMarker) {
		return nil, "", false
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "service" || parts[1] == "" || parts[2] != "operation" || parts[3] == "" {
		return nil, "", false
	}
	return wireCodec, parts[3], true
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
	// The AWS Query protocol puts Action in the body only on POST. Every
	// other method carrying this content type is some other service's
	// request wearing a default header — an S3 PutObject above all, since
	// that is what a bare HTTP client sends when no content type is set —
	// so its body is the payload and must not be touched here.
	if r.Method != http.MethodPost {
		return QueryXML, r.URL.Query().Get("Action"), true
	}
	// ParseFormPreservingBody caches the parsed fields on r.Form, which the
	// codec's Decode pass and any legacy handler's r.FormValue calls reuse,
	// and it hands the body back readable so a request that turns out not to
	// be Query traffic still reaches its handler intact. An oversized body is
	// not a Query request at all; Action then comes from the URL, if anywhere.
	if err := protocol.ParseFormPreservingBody(r); err != nil {
		return QueryXML, r.URL.Query().Get("Action"), true
	}
	return QueryXML, r.Form.Get("Action"), true
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
