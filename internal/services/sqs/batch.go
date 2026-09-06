package sqs

// batch.go holds the request-level validation SendMessageBatch,
// DeleteMessageBatch and ChangeMessageVisibilityBatch share. AWS applies the
// same three limits and the same three error shapes to all of them, so the
// check lives here once and each typed operation calls it before touching the
// store — a batch that fails validation must fail whole, never half-applied.
//
// The codes below are the modeled shape names, which is what the JSON
// protocol's __type carries; queryerror.go maps each to the
// "AWS.SimpleQueueService."-prefixed code the shape's
// aws.protocols#awsQueryError trait binds for the Query protocol, and
// queryerror_model_test.go checks that mapping against the pinned model.

import (
	"net/http"
	"strconv"

	"github.com/overcast-sh/overcast/internal/protocol"
)

// maxBatchEntries is AWS's documented maximum number of entries in any SQS
// batch request. It is the constraint client-side chunking is written around,
// so accepting more locally hides the off-by-one that loses the whole batch on
// real AWS.
const maxBatchEntries = 10

// Entry-type names AWS uses in its EmptyBatchRequest message, one per batch
// operation. They are the same names the Query protocol numbers its entries
// with (see query.go's schemas).
const (
	sendMessageBatchEntryName             = "SendMessageBatchRequestEntry"
	deleteMessageBatchEntryName           = "DeleteMessageBatchRequestEntry"
	changeMessageVisibilityBatchEntryName = "ChangeMessageVisibilityBatchRequestEntry"
)

// validateBatchEntries applies AWS's three batch-request limits to entries: at
// least one entry, at most maxBatchEntries of them, and distinct entry ids.
// entryName is the operation's own entry-type name, which AWS quotes in the
// empty-batch message; id reads the Id member out of the operation's entry
// type, which is the only part of the three entry shapes this check needs.
//
// Duplicate ids are rejected because the Id is how a caller correlates a
// per-entry result back to its own record: two entries sharing one make the
// response ambiguous.
func validateBatchEntries[E any](entryName string, entries []E, id func(E) string) *protocol.AWSError {
	if len(entries) == 0 {
		return errEmptyBatchRequest(entryName)
	}
	if len(entries) > maxBatchEntries {
		return errTooManyEntriesInBatchRequest(len(entries))
	}
	seen := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		entryID := id(e)
		if _, duplicate := seen[entryID]; duplicate {
			return errBatchEntryIdsNotDistinct(entryID)
		}
		seen[entryID] = struct{}{}
	}
	return nil
}

func errEmptyBatchRequest(entryName string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "EmptyBatchRequest",
		Message:    "There should be at least one " + entryName + " in the request.",
		HTTPStatus: http.StatusBadRequest,
	}
}

func errTooManyEntriesInBatchRequest(sent int) *protocol.AWSError {
	return &protocol.AWSError{
		Code: "TooManyEntriesInBatchRequest",
		Message: "Maximum number of entries per request are " + strconv.Itoa(maxBatchEntries) +
			". You have sent " + strconv.Itoa(sent) + ".",
		HTTPStatus: http.StatusBadRequest,
	}
}

func errBatchEntryIdsNotDistinct(id string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "BatchEntryIdsNotDistinct",
		Message:    "Id " + id + " repeated.",
		HTTPStatus: http.StatusBadRequest,
	}
}
