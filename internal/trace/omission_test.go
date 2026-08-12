package trace

import (
	"encoding/json"
	"testing"
)

// The producers of each OmitReason are covered where that behaviour lives:
// SetRequestBody / SetResponse and the two hop-body caps in trace_test.go, and
// the streaming case in buffer_test.go. What is only observable here is the
// wire shape — the `*Truncated` booleans are the shipped contract and are now
// derived from the reason rather than stored beside it.

// decodeJSON marshals v and decodes it back into a generic map, so a test can
// assert on the JSON a client receives rather than on the Go struct behind it.
func decodeJSON(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func TestEntryMarshalJSON_derivesTruncatedFlagsFromReason(t *testing.T) {
	// Given: an entry that lost each body for a different reason
	entry := Entry{
		RequestBodyOmitted:  OmitSize,
		ResponseBodyOmitted: OmitStreaming,
	}

	// When: it is serialised
	got := decodeJSON(t, entry)

	// Then: the legacy flags are still true for both, and the reasons say what
	// the flags cannot — that one body was cut short and the other never held
	if got["requestBodyTruncated"] != true || got["responseBodyTruncated"] != true {
		t.Errorf("legacy flags = (%v, %v), want both true",
			got["requestBodyTruncated"], got["responseBodyTruncated"])
	}
	if got["requestBodyOmitted"] != string(OmitSize) {
		t.Errorf("requestBodyOmitted = %v, want %q", got["requestBodyOmitted"], OmitSize)
	}
	if got["responseBodyOmitted"] != string(OmitStreaming) {
		t.Errorf("responseBodyOmitted = %v, want %q", got["responseBodyOmitted"], OmitStreaming)
	}
}

func TestHopMarshalJSON_derivesTruncatedFlagsFromReason(t *testing.T) {
	// Given: a hop whose bodies the per-trace budget dropped
	hop := Hop{
		RequestBodyOmitted:  OmitTraceBudget,
		ResponseBodyOmitted: OmitTraceBudget,
	}

	// When: it is serialised
	got := decodeJSON(t, hop)

	// Then: the legacy flags still report the loss, and the reason explains it
	if got["requestBodyTruncated"] != true || got["responseBodyTruncated"] != true {
		t.Errorf("legacy flags = (%v, %v), want both true",
			got["requestBodyTruncated"], got["responseBodyTruncated"])
	}
	if got["requestBodyOmitted"] != string(OmitTraceBudget) {
		t.Errorf("requestBodyOmitted = %v, want %q", got["requestBodyOmitted"], OmitTraceBudget)
	}
}

// An intact trace must not grow two empty fields per body: every trace in the
// ring is serialised on every detail read, and the list page holds fifty.
func TestMarshalJSON_omitsBothFlagAndReasonWhenNothingWasLost(t *testing.T) {
	keys := []string{"requestBodyTruncated", "responseBodyTruncated", "requestBodyOmitted", "responseBodyOmitted"}

	for name, got := range map[string]map[string]any{
		"entry": decodeJSON(t, Entry{}),
		"hop":   decodeJSON(t, Hop{}),
	} {
		for _, key := range keys {
			if _, ok := got[key]; ok {
				t.Errorf("%s: expected %q to be absent, got %v", name, key, got[key])
			}
		}
	}
}
