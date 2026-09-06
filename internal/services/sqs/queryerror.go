package sqs

// queryerror.go renders the x-amzn-query-error header SQS carries on every
// JSON-protocol error response.
//
// SQS's service shape in the pinned Smithy model carries
// aws.protocols#awsQueryCompatible: the JSON protocol (x-amz-target /
// application/x-amz-json-1.0) is layered over the same operations the older
// Query protocol (form-encoded Action=, XML response — see query.go) used to
// serve, and AWS keeps clients written against the Query protocol's error
// codes working by echoing the legacy code in this header alongside the
// JSON body's own __type. See #1810.
//
// The legacy code is an error shape's aws.protocols#awsQueryError trait
// (`code` field) where the shape has one; a shape with no such trait falls
// back to its own modeled name. Overcast's wire __type (the AWSError.Code
// used to build the JSON body) does not consistently hold either of those —
// e.g. errQueueNameExists below writes "QueueNameExists" (the modeled shape
// name) while errQueueNotFound writes "AWS.SimpleQueueService.NonExistentQueue"
// (already the legacy code) — so the header value cannot be derived from
// __type by any single rule. sqsQueryErrorCodes hard-codes the mapping this
// package's error definitions actually need instead, and
// queryerror_model_test.go cross-checks every entry against the pinned model
// so the two cannot silently drift apart.
//
// Wire __type is left exactly as-is: this file only ever adds the header, it
// never changes what a handler already puts in AWSError.Code.
import (
	"context"
	"net/http"

	"github.com/overcast-sh/overcast/internal/protocol"
)

// sqsQueryErrorMapping records one error's legacy Query-protocol code
// alongside the modeled shape it comes from, so queryerror_model_test.go can
// look the shape up against the pinned Smithy model's
// aws.protocols#awsQueryError traits and confirm LegacyCode is really what
// that shape's trait says (or, for a shape with no such trait, that
// LegacyCode is the shape's own name — AWS's documented fallback).
type sqsQueryErrorMapping struct {
	// Shape is the modeled shape name, per the pinned Smithy model, e.g.
	// "QueueDoesNotExist".
	Shape string
	// LegacyCode is the value AWS sends as the code half of
	// x-amzn-query-error for this error.
	LegacyCode string
}

// sqsQueryErrorCodes maps every Code this package places in an SQS
// JSON-protocol error's __type to the legacy Query-protocol error code AWS
// sends in x-amzn-query-error for the same condition. A Code absent from
// this table falls back to itself in sqsLegacyQueryErrorCode below — the
// right default for a Code that already doubles as its own shape's modeled
// name (InvalidAttributeName, InvalidAttributeValue: no awsQueryError trait
// on either shape) or that names no SQS-specific shape at all
// (InvalidParameterValue, MissingParameter, InvalidAction, ...): AWS's
// awsQueryCompatible fallback for "no trait" is the shape's own name, and
// Code is already standing in for that.
var sqsQueryErrorCodes = map[string]sqsQueryErrorMapping{
	// QueueDoesNotExist: errQueueNotFound (store.go) already writes the
	// legacy code as Code, so this is an identity mapping.
	"AWS.SimpleQueueService.NonExistentQueue": {
		Shape:      "QueueDoesNotExist",
		LegacyCode: "AWS.SimpleQueueService.NonExistentQueue",
	},
	// QueueNameExists: errQueueNameExists (handler_queue.go) writes the
	// modeled shape name as Code; the shape's awsQueryError code differs.
	"QueueNameExists": {
		Shape:      "QueueNameExists",
		LegacyCode: "QueueAlreadyExists",
	},
	// PurgeQueueInProgress: errPurgeQueueInProgress (store.go) writes the
	// modeled shape name as Code; the legacy code carries the
	// "AWS.SimpleQueueService." prefix the shape name itself lacks.
	"PurgeQueueInProgress": {
		Shape:      "PurgeQueueInProgress",
		LegacyCode: "AWS.SimpleQueueService.PurgeQueueInProgress",
	},
	// ReceiptHandleIsInvalid: shape name and legacy code are the same
	// string, so this is an identity mapping too.
	"ReceiptHandleIsInvalid": {
		Shape:      "ReceiptHandleIsInvalid",
		LegacyCode: "ReceiptHandleIsInvalid",
	},
	// The three batch-request limits (batch.go). Each writes the modeled
	// shape name as Code; the legacy code carries the
	// "AWS.SimpleQueueService." prefix the shape name itself lacks.
	"EmptyBatchRequest": {
		Shape:      "EmptyBatchRequest",
		LegacyCode: "AWS.SimpleQueueService.EmptyBatchRequest",
	},
	"TooManyEntriesInBatchRequest": {
		Shape:      "TooManyEntriesInBatchRequest",
		LegacyCode: "AWS.SimpleQueueService.TooManyEntriesInBatchRequest",
	},
	"BatchEntryIdsNotDistinct": {
		Shape:      "BatchEntryIdsNotDistinct",
		LegacyCode: "AWS.SimpleQueueService.BatchEntryIdsNotDistinct",
	},
}

// sqsLegacyQueryErrorCode returns the legacy Query-protocol error code for a
// wire Code, per sqsQueryErrorCodes, falling back to code itself when the
// table carries no more specific answer (see the table's doc comment for
// why that fallback is correct, not just convenient).
func sqsLegacyQueryErrorCode(code string) string {
	if mapping, ok := sqsQueryErrorCodes[code]; ok {
		return mapping.LegacyCode
	}
	return code
}

// withLegacyQueryError returns aerr with QueryErrorCode set from
// sqsLegacyQueryErrorCode, so protocol.WriteJSONError renders the
// x-amzn-query-error header. aerr is not mutated; nil passes through
// unchanged.
func withLegacyQueryError(aerr *protocol.AWSError) *protocol.AWSError {
	if aerr == nil {
		return nil
	}
	tagged := *aerr
	tagged.QueryErrorCode = sqsLegacyQueryErrorCode(aerr.Code)
	return &tagged
}

// writeJSONError is protocol.WriteJSONError plus the x-amzn-query-error
// header, for the legacy (non-typed) SQS handlers in this package that call
// it directly. Typed operations don't need a call-site change: they return
// their *protocol.AWSError through the typed dispatcher instead of writing
// it themselves, so sqsTypedOp below tags it in the one place every typed
// operation's error already passes through.
func writeJSONError(w http.ResponseWriter, r *http.Request, aerr *protocol.AWSError) {
	protocol.WriteJSONError(w, r, withLegacyQueryError(aerr))
}

// sqsTypedOp wraps a typed operation function so any *protocol.AWSError it
// returns carries the x-amzn-query-error mapping before op.Typed[In,
// Out].Invoke hands it to the codec's WriteError (codec/json10.go,
// codec/json11.go both funnel that into protocol.WriteJSONError; codec's
// Query-XML WriteError — used only by the legacy Query-protocol adapter in
// query.go, never by the typed dispatcher — writes its own legacy-shaped
// error code natively and needs no header). This is the typed-dispatch
// counterpart to writeJSONError above — see typed_ops.go, where every
// registered operation is wrapped with this.
func sqsTypedOp[In any, Out any](
	fn func(ctx context.Context, in *In) (*Out, *protocol.AWSError),
) func(ctx context.Context, in *In) (*Out, *protocol.AWSError) {
	return func(ctx context.Context, in *In) (*Out, *protocol.AWSError) {
		out, aerr := fn(ctx, in)
		if aerr != nil {
			return out, withLegacyQueryError(aerr)
		}
		return out, aerr
	}
}
