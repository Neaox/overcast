package sqs

import (
	"net/http"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/internal/protocol"
)

// entry is a minimal stand-in for the three concrete *BatchRequestEntry types,
// proving validateBatchEntries reads the Id through the accessor it is given
// rather than through any one operation's struct.
type entry struct{ id string }

func entries(ids ...string) []entry {
	out := make([]entry, 0, len(ids))
	for _, id := range ids {
		out = append(out, entry{id: id})
	}
	return out
}

func entryID(e entry) string { return e.id }

func TestValidateBatchEntries_withinLimits(t *testing.T) {
	// Given: a batch of ten distinct entry ids — AWS's maximum, and the
	// boundary an off-by-one in the limit check would break.
	ten := make([]string, 0, maxBatchEntries)
	for i := range maxBatchEntries {
		ten = append(ten, string(rune('a'+i)))
	}

	// When: the shared batch validation runs over it.
	aerr := validateBatchEntries(sendMessageBatchEntryName, entries(ten...), entryID)

	// Then: the batch is accepted.
	if aerr != nil {
		t.Fatalf("validateBatchEntries(10 distinct entries) = %v, want nil", aerr)
	}
}

func TestValidateBatchEntries_empty(t *testing.T) {
	// Given: a request carrying no entries at all.
	// When: the shared batch validation runs over it.
	aerr := validateBatchEntries(deleteMessageBatchEntryName, entries(), entryID)

	// Then: AWS's EmptyBatchRequest, naming the operation's own entry type.
	requireBatchError(t, aerr, "EmptyBatchRequest", deleteMessageBatchEntryName)
}

func TestValidateBatchEntries_tooManyEntries(t *testing.T) {
	// Given: eleven entries — one past AWS's documented per-batch maximum.
	ids := make([]string, 0, maxBatchEntries+1)
	for i := range maxBatchEntries + 1 {
		ids = append(ids, string(rune('a'+i)))
	}

	// When: the shared batch validation runs over it.
	aerr := validateBatchEntries(sendMessageBatchEntryName, entries(ids...), entryID)

	// Then: AWS's TooManyEntriesInBatchRequest, reporting how many were sent.
	requireBatchError(t, aerr, "TooManyEntriesInBatchRequest", "11")
}

func TestValidateBatchEntries_duplicateIds(t *testing.T) {
	// Given: two entries sharing an Id, which would make the per-entry results
	// ambiguous to correlate.
	// When: the shared batch validation runs over it.
	aerr := validateBatchEntries(
		changeMessageVisibilityBatchEntryName,
		entries("a", "b", "a"),
		entryID,
	)

	// Then: AWS's BatchEntryIdsNotDistinct, naming the repeated Id.
	requireBatchError(t, aerr, "BatchEntryIdsNotDistinct", "a")
}

// requireBatchError asserts the shared validation produced a client-fault AWS
// error with the modeled shape name as its code and the given substring in its
// message.
func requireBatchError(t *testing.T, aerr *protocol.AWSError, wantCode, wantInMessage string) {
	t.Helper()
	if aerr == nil {
		t.Fatalf("validateBatchEntries = nil, want %s", wantCode)
	}
	if aerr.Code != wantCode {
		t.Errorf("code = %q, want %q", aerr.Code, wantCode)
	}
	if aerr.HTTPStatus != http.StatusBadRequest {
		t.Errorf("HTTPStatus = %d, want %d", aerr.HTTPStatus, http.StatusBadRequest)
	}
	if !strings.Contains(aerr.Message, wantInMessage) {
		t.Errorf("message = %q, want it to contain %q", aerr.Message, wantInMessage)
	}
}

// TestBatchErrorsCarryLegacyQueryCode proves each batch error's wire __type
// (the modeled shape name) maps to the "AWS.SimpleQueueService."-prefixed
// legacy Query-protocol code, which is what the x-amzn-query-error header and
// the Query-XML <Code> element must carry. The table itself is checked against
// the pinned model by queryerror_model_test.go.
func TestBatchErrorsCarryLegacyQueryCode(t *testing.T) {
	for code, want := range map[string]string{
		"EmptyBatchRequest":            "AWS.SimpleQueueService.EmptyBatchRequest",
		"TooManyEntriesInBatchRequest": "AWS.SimpleQueueService.TooManyEntriesInBatchRequest",
		"BatchEntryIdsNotDistinct":     "AWS.SimpleQueueService.BatchEntryIdsNotDistinct",
	} {
		if got := sqsLegacyQueryErrorCode(code); got != want {
			t.Errorf("sqsLegacyQueryErrorCode(%q) = %q, want %q", code, got, want)
		}
	}
}
