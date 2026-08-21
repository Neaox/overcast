// Package partialbatch reads the partial-batch failure response a function
// returns when a poll-based event source asks it to report which records of a
// batch failed.
//
// The shape is AWS's:
//
//	{"batchItemFailures": [{"itemIdentifier": "<id>"}, ...]}
//
// where the identifier is the SQS message ID for a queue source and the record
// sequence number for a stream source. Both Lambda event source mappings and
// EventBridge Pipes consume it, which is why it lives here rather than in
// either service.
//
// The rules Parse implements are the ones AWS documents for a Lambda event
// source mapping with FunctionResponseTypes: ["ReportBatchItemFailures"]:
//
//   - An empty response, a JSON null, an object with no batchItemFailures
//     member, or a null batchItemFailures member is a complete success — the
//     whole batch is acknowledged. This is what a function that never opted in
//     returns, so honouring the flag cannot change its behaviour.
//   - An empty batchItemFailures list is also a complete success.
//   - A response that is not valid JSON, an entry that is not an object, an
//     entry with no recognisable identifier, and an empty or null identifier
//     are all a complete *failure* — the whole batch is retried. AWS is
//     deliberately conservative here: a response it cannot read must never be
//     taken as permission to delete records.
//
// One deliberate divergence, for an emulator's benefit: the member names are
// matched case-insensitively, so a handler written against a strongly typed SDK
// that serialises `BatchItemFailures` is honoured rather than being read as a
// malformed response. AWS accepts only the casing above from most runtimes; a
// name that is neither spelling is still a malformed response, which is the
// case that matters.
package partialbatch

import (
	"encoding/json"
	"fmt"
	"strings"
)

// batchItemFailuresMember and itemIdentifierMember are the JSON member names,
// compared case-insensitively.
const (
	batchItemFailuresMember = "batchitemfailures"
	itemIdentifierMember    = "itemidentifier"
)

// Response is a parsed partial-batch failure response.
//
// The zero value is a complete success that reported nothing, which is what a
// function returns when it does not use the feature.
type Response struct {
	// Reported is true when the response carried a batchItemFailures member,
	// so the caller can tell "the function reported no failures" apart from
	// "the function said nothing about failures". Both acknowledge the batch;
	// only the first is worth logging as partial-batch reporting in use.
	Reported bool

	// Failed lists the item identifiers the function reported, in the order it
	// reported them. Empty when the batch fully succeeded.
	Failed []string

	// WholeBatchFailed is true when AWS's rules make this response a complete
	// batch failure: nothing may be acknowledged and the batch is retried.
	WholeBatchFailed bool

	// Reason explains WholeBatchFailed in one clause, for the log line that
	// accompanies it. Empty unless WholeBatchFailed is set.
	Reason string
}

// failed builds a whole-batch failure with the given reason.
func failed(format string, args ...any) Response {
	return Response{WholeBatchFailed: true, Reason: fmt.Sprintf(format, args...)}
}

// Reports reports whether payload mentions the batchItemFailures member at all.
//
// It exists for a caller with no opt-in flag to gate on — EventBridge Pipes has
// no FunctionResponseTypes, so every Lambda target's response reaches Parse.
// Without this check, AWS's "a response I cannot read is a complete failure"
// rule would turn a target that returns a non-JSON string, and today succeeds,
// into a batch that is retried forever. A response that never names the member
// is not a partial-batch response, so it is left alone; once it does name it,
// the malformed-response rules apply in full.
func Reports(payload []byte) bool {
	return strings.Contains(strings.ToLower(string(payload)), batchItemFailuresMember)
}

// Parse reads a function's response payload.
//
// It never returns an error: every way a response can be wrong is one of AWS's
// documented outcomes, carried on the returned Response.
func Parse(payload []byte) Response {
	trimmed := strings.TrimSpace(string(payload))
	if trimmed == "" || trimmed == "null" {
		// No response at all. AWS treats a null or empty response as a
		// complete success.
		return Response{}
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
		if json.Valid([]byte(trimmed)) {
			// Valid JSON that is not an object — an array, a string, a number.
			// AWS's documented lists do not name this shape either way: it is
			// not "an invalid JSON response", and it is not one of the four
			// complete-success forms. Taken as a success, on the same reading
			// as an empty response object: it carries no failure report, and a
			// handler that returns a bare value while the flag is on has said
			// nothing about its records. Failing the batch here would retry it
			// forever on a response the function meant nothing by.
			return Response{}
		}
		return failed("the response is not valid JSON")
	}

	raw, ok := lookup(envelope, batchItemFailuresMember)
	if !ok {
		return Response{}
	}
	if strings.TrimSpace(string(raw)) == "null" {
		return Response{}
	}

	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return failed("batchItemFailures is not a list")
	}
	out := Response{Reported: true, Failed: make([]string, 0, len(entries))}
	for i, entry := range entries {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(entry, &fields); err != nil {
			return failed("batchItemFailures entry %d is not an object", i+1)
		}
		value, ok := lookup(fields, itemIdentifierMember)
		if !ok {
			return failed("batchItemFailures entry %d has no itemIdentifier", i+1)
		}
		var id string
		if err := json.Unmarshal(value, &id); err != nil {
			// A null or non-string identifier. AWS names both as a complete
			// batch failure.
			return failed("batchItemFailures entry %d has a null or non-string itemIdentifier", i+1)
		}
		if id == "" {
			return failed("batchItemFailures entry %d has an empty itemIdentifier", i+1)
		}
		out.Failed = append(out.Failed, id)
	}
	return out
}

// lookup finds a member by its case-folded name.
func lookup(fields map[string]json.RawMessage, name string) (json.RawMessage, bool) {
	for key, value := range fields {
		if strings.EqualFold(key, name) {
			return value, true
		}
	}
	return nil, false
}

// Split partitions the identifiers of a delivered batch into the ones the
// response reported as failed and the ones that succeeded, preserving the
// order they were delivered in.
//
// unknown names the first reported identifier that is not in delivered. AWS
// treats an identifier that does not match a record of the batch as a complete
// batch failure, so a caller that gets a non-empty unknown must acknowledge
// nothing.
//
// Split must not be called on a Response whose WholeBatchFailed is set.
func (r Response) Split(delivered []string) (succeeded, failedIDs []string, unknown string) {
	reported := make(map[string]bool, len(r.Failed))
	for _, id := range r.Failed {
		reported[id] = true
	}
	known := make(map[string]bool, len(delivered))
	for _, id := range delivered {
		known[id] = true
	}
	for _, id := range r.Failed {
		if !known[id] {
			return nil, nil, id
		}
	}
	for _, id := range delivered {
		if reported[id] {
			failedIDs = append(failedIDs, id)
			continue
		}
		succeeded = append(succeeded, id)
	}
	return succeeded, failedIDs, ""
}

// LowestReportedIndex returns the index of the first record of delivered that
// the response reported as failed, which is where a stream source resumes:
// AWS checkpoints a stream batch at the lowest reported sequence number and
// retries everything from there, rather than skipping the records in between.
//
// ok is false when nothing was reported — the whole batch succeeded. unknown
// names the first reported identifier that is not in delivered, which is a
// complete batch failure exactly as it is for a queue source.
func (r Response) LowestReportedIndex(delivered []string) (index int, ok bool, unknown string) {
	position := make(map[string]int, len(delivered))
	for i, id := range delivered {
		if _, seen := position[id]; !seen {
			position[id] = i
		}
	}
	lowest := -1
	for _, id := range r.Failed {
		at, found := position[id]
		if !found {
			return 0, false, id
		}
		if lowest < 0 || at < lowest {
			lowest = at
		}
	}
	if lowest < 0 {
		return 0, false, ""
	}
	return lowest, true, ""
}
