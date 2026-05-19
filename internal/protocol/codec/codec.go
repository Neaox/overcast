// Package codec defines the wire-protocol abstraction used by Overcast's
// typed operation dispatcher.
//
// A Codec is a stateless (de)serialiser for one AWS wire protocol. It does
// not know the operation, the service, or any business logic — it only
// turns request bodies into typed input structs and typed output structs
// (or AWSErrors) into response bytes.
//
// This package is part of Phase 0 of the Smithy-aligned wire-protocol plan.
// See docs/plans/smithy.md.
//
// IMPORTANT: codecs in this package wrap the existing protocol.WriteJSON /
// protocol.WriteXML / protocol.WriteQueryXML helpers. They MUST produce
// byte-identical responses to the legacy code paths so that migrating a
// service to the typed dispatcher is a no-op on the wire.
package codec

import (
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
)

// Smithy protocol shape IDs. These are the canonical names used by the
// Smithy 2.0 spec and are stable identifiers we use in logs, telemetry,
// and for the Smithy-Protocol response header (rpcv2Cbor only).
//
// Source:
//   - https://smithy.io/2.0/aws/protocols/aws-json-1_0-protocol.html
//   - https://smithy.io/2.0/aws/protocols/aws-json-1_1-protocol.html
//   - https://smithy.io/2.0/aws/protocols/aws-query-protocol.html
//   - https://smithy.io/2.0/aws/protocols/aws-restxml-protocol.html
//   - https://smithy.io/2.0/additional-specs/protocols/smithy-rpc-v2.html
const (
	NameAWSJSON10 = "aws.protocols#awsJson1_0"
	NameAWSJSON11 = "aws.protocols#awsJson1_1"
	NameAWSQuery  = "aws.protocols#awsQuery"
	NameRESTXML   = "aws.protocols#restXml"
	NameRPCv2CBOR = "smithy.protocols#rpcv2Cbor"
)

// Codec serialises requests and responses for one AWS wire protocol.
//
// Implementations MUST be stateless and safe for concurrent use. They
// never inspect the operation name, never know which service they're
// serving, and never invoke business logic.
//
// Methods on Codec are intentionally narrow:
//   - Decode reads the request body into a pointer-to-struct.
//   - WriteResponse serialises a successful response.
//   - WriteError serialises an AWSError in the codec's native envelope.
//
// All concrete codecs in Phase 0 are thin wrappers over the existing
// helpers in package protocol; the wire bytes are byte-identical.
type Codec interface {
	// Name returns the Smithy protocol shape ID, e.g. NameAWSJSON10.
	// Used for diagnostics, logging, and the Smithy-Protocol response
	// header (rpcv2Cbor only).
	Name() string

	// Decode reads r.Body into into. into MUST be a non-nil pointer to a
	// typed input struct. Returns a *protocol.AWSError populated with a
	// 4xx code on malformed input, or nil on success.
	//
	// Decode is responsible for draining and closing r.Body so the
	// HTTP/1.1 connection can be reused. Callers MUST NOT touch r.Body
	// after calling Decode.
	Decode(r *http.Request, into any) *protocol.AWSError

	// WriteResponse serialises v as a successful response body using the
	// codec's native format and writes it to w with status. v may be nil
	// or a typed pointer; nil is rendered as the codec's empty-response
	// representation (e.g. "{}" for JSON).
	WriteResponse(w http.ResponseWriter, r *http.Request, status int, v any)

	// WriteError serialises aerr in the codec's native error envelope.
	// The HTTP status comes from aerr.HTTPStatus.
	WriteError(w http.ResponseWriter, r *http.Request, aerr *protocol.AWSError)
}

// Supports reports whether c is in supported, treating JSON 1.0 and 1.1 as
// equivalent (the emulator accepts either for any JSON-tier service — see
// docs/smithy.md). Every service's Dispatch method should use this instead
// of writing its own supportsCodec loop.
func Supports(supported []Codec, c Codec) bool {
	name := c.Name()
	for _, s := range supported {
		if s.Name() == name {
			return true
		}
		// JSON 1.0 ↔ 1.1 equivalence.
		if (s.Name() == NameAWSJSON10 && name == NameAWSJSON11) ||
			(s.Name() == NameAWSJSON11 && name == NameAWSJSON10) {
			return true
		}
	}
	return false
}
