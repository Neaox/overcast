package serviceutil

import (
	"net/http"

	"github.com/overcast-sh/overcast/internal/awsapi"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/protocol/codec"
)

// operationRegistry is the immutable generated AWS operation registry. It holds
// no state, so one package-level value serves every caller and no lookup
// allocates.
var operationRegistry = awsapi.NewRegistry()

// WriteNotImplemented writes the standard not-implemented response for a
// modeled AWS operation, in the error envelope that operation's wire protocol
// expects. The 501 status, the x-emulator-unsupported marker, and the request
// ID are all applied by the protocol layer — never set them by hand.
//
// The router uses this for operations no service handler claimed; a service
// that dispatches its own operations uses NotImplementedTarget.
func WriteNotImplemented(w http.ResponseWriter, r *http.Request, claim awsapi.Claim) {
	switch claim.ErrorProfile {
	case awsapi.ErrorProfileJSON:
		protocol.NotImplementedJSON(w, r)
	case awsapi.ErrorProfileEC2QueryXML:
		protocol.NotImplementedEC2QueryXML(w, r)
	case awsapi.ErrorProfileQueryXML:
		protocol.NotImplementedQueryXML(w, r)
	case awsapi.ErrorProfileXML:
		protocol.NotImplementedXML(w, r)
	case awsapi.ErrorProfileRPCV2CBOR:
		codec.RPCv2CBOR.WriteError(w, r, protocol.ErrNotImplemented)
	case awsapi.ErrorProfileRPCV2JSON:
		codec.RPCv2JSON.WriteError(w, r, protocol.ErrNotImplemented)
	default:
		// Keep a JSON fallback for a malformed zero-value Claim while every
		// declared ErrorProfile remains explicit for exhaustive checking.
		protocol.NotImplementedJSON(w, r)
	}
}

// NotImplementedTarget answers an X-Amz-Target that names a modeled AWS
// operation the calling service has not implemented, and reports whether it
// did. It reports false without writing anything when the target names no
// modeled operation, leaving the service free to answer with the
// unknown-operation error AWS uses for a target it does not recognise.
//
// This is the DRY way for any X-Amz-Target dispatcher to satisfy the repo rule
// that a recognized AWS operation returns 501 rather than a 400: the operation
// list comes from the generated registry in internal/awsapi, so it cannot rot
// the way a hand-maintained list of stubs does. Services other than DynamoDB
// still answer their own unknown targets directly and could adopt this the
// same way.
func NotImplementedTarget(w http.ResponseWriter, r *http.Request, target string) bool {
	claim, ok := operationRegistry.ClaimTarget(target)
	if !ok {
		return false
	}
	WriteNotImplemented(w, r, claim)
	return true
}

// WriteUnhandledOperation answers an operation name the calling service has
// no handler for, in the wire format c writes, separating two claims that
// used to share one 400:
//
//   - A name the pinned AWS models carry for service is a real operation
//     Overcast has not implemented. It gets protocol.ErrNotImplemented (501),
//     the house rule cloudwatch/service.go's dispatchJSON states: the caller
//     reads "not emulated here" and goes to the support matrix, rather than
//     "no such operation" and off to check their SDK version or spelling.
//   - Any other name gets unknown — whatever unknown-operation error the
//     service has always answered with, unchanged, since AWS itself refuses
//     a target it does not recognise that way.
//
// The real-operation list is the generated model corpus (awsapi.HasOperation),
// so it cannot rot the way a hand-maintained list of stubs does. The corpus
// scan is linear, which is fine on this path: it runs once per refused
// request, after every handler lookup has already missed.
//
// c is the codec the request was identified as, so an RPC v2 CBOR caller gets
// a CBOR 501 where the fixed-format NotImplementedJSON would hand it JSON. A
// legacy dispatcher running without a codec in context passes the codec of
// the one protocol it serves; the JSON codecs' WriteError is
// protocol.WriteJSONError and QueryXML's is protocol.WriteQueryXMLError, so a
// service that adopts this keeps its unknown-operation bytes exactly as they
// were.
func WriteUnhandledOperation(w http.ResponseWriter, r *http.Request, c codec.Codec, service, operation string, unknown *protocol.AWSError) {
	if awsapi.HasOperation(service, operation) {
		c.WriteError(w, r, protocol.ErrNotImplemented)
		return
	}
	c.WriteError(w, r, unknown)
}
