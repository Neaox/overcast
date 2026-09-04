package s3

// conditional_write.go implements S3's conditional writes: the If-Match and
// If-None-Match request headers on PutObject and CompleteMultipartUpload.
//
// Per AWS docs (https://docs.aws.amazon.com/AmazonS3/latest/userguide/conditional-writes.html
// § "Conditional write behavior", and the If-Match / If-None-Match header
// descriptions on
// https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObject.html and
// https://docs.aws.amazon.com/AmazonS3/latest/API/API_CompleteMultipartUpload.html):
//
//   - If-None-Match: "If there's no existing object with the same key name in
//     the bucket, the write operation succeeds, resulting in a 200 OK response.
//     If there's an existing object, the write operation fails, resulting in a
//     412 Precondition Failed response." On a versioning-enabled bucket, "if
//     there's no current object version with the same name, or if the current
//     object version is a delete marker, the write operation succeeds."
//     The header "expects the '*' (asterisk) character" — an entity tag asks
//     for RFC 7232's compare-and-fail-on-match semantics, which S3 does not
//     implement on a write, so Overcast refuses it with S3's NotImplemented
//     rather than ignoring it and silently overwriting.
//
//   - If-Match: "If there's an existing object with the same key name and
//     matching ETag, the write operation succeeds… If the ETag doesn't match,
//     the write operation fails with a 412 Precondition Failed response…
//     If there's no current object version with the same name, or if the
//     current object version is a delete marker, the operation fails with a
//     404 Not Found error."
//
// AWS's 409 ConditionalRequestConflict has no counterpart here: it reports a
// concurrent delete racing an in-flight conditional write inside a distributed
// store, and Overcast evaluates and commits under one per-key lock, so the
// window it describes does not exist.

import (
	"context"
	"net/http"
	"strings"

	"github.com/overcast-sh/overcast/internal/protocol"
)

// conditionalWrite is the pair of preconditions a write request carries.
// The zero value means an unconditional write, which is the hot path: nothing
// is looked up and no lock is taken for it.
type conditionalWrite struct {
	// ifMatch is the raw If-Match header value, or "" when it was absent.
	ifMatch string
	// ifNoneMatch records an If-None-Match: * header. Only '*' is legal on a
	// write, so the value itself carries no further information.
	ifNoneMatch bool
}

// active reports whether the request asked for any precondition at all.
func (c conditionalWrite) active() bool { return c.ifMatch != "" || c.ifNoneMatch }

// parseConditionalWrite reads the conditional-write headers off a request.
//
// An If-None-Match value other than '*' is refused here rather than at
// evaluation time: it is a malformed request, not an unmet condition, and it
// must not reach the store.
func parseConditionalWrite(r *http.Request) (conditionalWrite, *protocol.AWSError) {
	var c conditionalWrite
	c.ifMatch = strings.TrimSpace(r.Header.Get("If-Match"))

	switch raw := strings.TrimSpace(r.Header.Get("If-None-Match")); raw {
	case "":
	case "*":
		c.ifNoneMatch = true
	default:
		return conditionalWrite{}, errConditionalWriteNotImplemented()
	}
	return c, nil
}

// evaluate reports whether the preconditions hold against the key's current
// version. current is the record s3:objects holds for the key, or nil when the
// key has none.
//
// Both conditions are evaluated when both are present, If-Match first, which
// is RFC 7232 § 6's precedence order. AWS documents no combined form, and the
// pair is unsatisfiable for an existing object — If-Match requires a current
// version, If-None-Match: * requires there to be none — so the honest answer
// is the failure of whichever holds, not a silent success.
func (c conditionalWrite) evaluate(key string, current *Object) *protocol.AWSError {
	// A delete marker is the absence of a current object as far as both
	// conditions are concerned, which is exactly what AWS documents.
	if current != nil && current.DeleteMarker {
		current = nil
	}

	if c.ifMatch != "" {
		if current == nil {
			return errNoSuchKey(key)
		}
		if !etagMatches(current.ETag, c.ifMatch) {
			return errPreconditionFailed()
		}
	}
	if c.ifNoneMatch && current != nil {
		return errPreconditionFailed()
	}
	return nil
}

// checkConditionalWrite evaluates c against the key's current version, reading
// it only when there is a condition to evaluate — an unconditional write costs
// no extra store round trip.
//
// The caller must already hold the key's write lock: the read this performs
// and the write that follows it are one step, and releasing between them is
// the race that makes "only one of N concurrent writers wins" untrue.
func (h *Handler) checkConditionalWrite(ctx context.Context, c conditionalWrite, bucket, key string) *protocol.AWSError {
	if !c.active() {
		return nil
	}
	current, aerr := h.store.lookupObjectMeta(ctx, bucket, key)
	if aerr != nil {
		return aerr
	}
	return c.evaluate(key, current)
}

// ---- Conditional-write errors ----------------------------------------------

// errPreconditionFailed is S3's answer to a conditional write whose condition
// did not hold. The wording is S3's own.
func errPreconditionFailed() *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "PreconditionFailed",
		Message:    "At least one of the pre-conditions you specified did not hold",
		HTTPStatus: http.StatusPreconditionFailed,
	}
}

// errConditionalWriteNotImplemented is the answer to an If-None-Match carrying
// an entity tag on a write. S3 documents the header as expecting '*' and has no
// entity-tag form of it on the write path; NotImplemented is the error S3 uses
// for a header whose implied functionality it does not provide, and it is what
// S3 answers a CopyObject that carries a conditional header into a bucket whose
// policy enforces conditional writes
// (https://docs.aws.amazon.com/AmazonS3/latest/userguide/conditional-writes-enforce.html).
// Ignoring the header instead would silently overwrite, which is the whole bug
// this file exists to fix.
func errConditionalWriteNotImplemented() *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "NotImplemented",
		Message:    "A header you provided implies functionality that is not implemented",
		HTTPStatus: http.StatusNotImplemented,
	}
}
