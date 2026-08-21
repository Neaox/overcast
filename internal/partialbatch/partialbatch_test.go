package partialbatch

import (
	"strings"
	"testing"
)

func TestParse_completeSuccess(t *testing.T) {
	// Given: the responses AWS documents as a complete batch success.
	cases := map[string]string{
		"no response":            "",
		"json null":              "null",
		"empty object":           "{}",
		"null member":            `{"batchItemFailures": null}`,
		"empty list":             `{"batchItemFailures": []}`,
		"unrelated members":      `{"statusCode": 200, "body": "ok"}`,
		"valid json, not object": `"done"`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			// When: it is parsed.
			got := Parse([]byte(payload))

			// Then: nothing failed and the batch may be acknowledged.
			if got.WholeBatchFailed {
				t.Fatalf("WholeBatchFailed = true (%s), want false", got.Reason)
			}
			if len(got.Failed) != 0 {
				t.Fatalf("Failed = %v, want none", got.Failed)
			}
		})
	}
}

func TestParse_completeFailure(t *testing.T) {
	// Given: the responses AWS documents as a complete batch failure.
	cases := map[string]string{
		"invalid json":      `{"batchItemFailures": [`,
		"member not a list": `{"batchItemFailures": {"itemIdentifier": "a"}}`,
		"entry not object":  `{"batchItemFailures": ["a"]}`,
		"bad key name":      `{"batchItemFailures": [{"id": "a"}]}`,
		"empty identifier":  `{"batchItemFailures": [{"itemIdentifier": ""}]}`,
		"null identifier":   `{"batchItemFailures": [{"itemIdentifier": null}]}`,
		"number identifier": `{"batchItemFailures": [{"itemIdentifier": 7}]}`,
		"second entry bad":  `{"batchItemFailures": [{"itemIdentifier": "a"}, {"itemIdentifier": ""}]}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			// When: it is parsed.
			got := Parse([]byte(payload))

			// Then: the caller is told to acknowledge nothing, with a reason it
			// can log.
			if !got.WholeBatchFailed {
				t.Fatal("WholeBatchFailed = false, want true")
			}
			if strings.TrimSpace(got.Reason) == "" {
				t.Fatal("WholeBatchFailed carries no reason")
			}
		})
	}
}

func TestParse_reportsTheIdentifiersInOrder(t *testing.T) {
	// Given: a response naming two records.
	got := Parse([]byte(`{"batchItemFailures": [{"itemIdentifier": "b"}, {"itemIdentifier": "a"}]}`))

	// Then: both are reported, in the order the function listed them.
	if !got.Reported {
		t.Fatal("Reported = false, want true")
	}
	if strings.Join(got.Failed, ",") != "b,a" {
		t.Fatalf("Failed = %v, want [b a]", got.Failed)
	}
}

func TestParse_acceptsEitherCasingOfTheMemberNames(t *testing.T) {
	// Given: the PascalCase spelling a strongly typed SDK serialises.
	got := Parse([]byte(`{"BatchItemFailures": [{"ItemIdentifier": "a"}]}`))

	// Then: it is honoured rather than read as a malformed response — the one
	// deliberate divergence, documented on the package.
	if got.WholeBatchFailed {
		t.Fatalf("WholeBatchFailed = true (%s), want the report to be honoured", got.Reason)
	}
	if strings.Join(got.Failed, ",") != "a" {
		t.Fatalf("Failed = %v, want [a]", got.Failed)
	}
}

func TestSplit_partitionsTheDeliveredBatch(t *testing.T) {
	// Given: a report naming the middle record of three.
	response := Parse([]byte(`{"batchItemFailures": [{"itemIdentifier": "m-2"}]}`))

	// When: it is applied to the delivered batch.
	succeeded, failed, unknown := response.Split([]string{"m-1", "m-2", "m-3"})

	// Then: the two others may be acknowledged, in delivery order.
	if unknown != "" {
		t.Fatalf("unknown = %q, want none", unknown)
	}
	if strings.Join(succeeded, ",") != "m-1,m-3" {
		t.Fatalf("succeeded = %v, want [m-1 m-3]", succeeded)
	}
	if strings.Join(failed, ",") != "m-2" {
		t.Fatalf("failed = %v, want [m-2]", failed)
	}
}

func TestSplit_reportsAnIdentifierThatWasNotDelivered(t *testing.T) {
	// Given: a report naming a record the batch never contained.
	response := Parse([]byte(`{"batchItemFailures": [{"itemIdentifier": "m-9"}]}`))

	// When: it is applied to the delivered batch.
	succeeded, _, unknown := response.Split([]string{"m-1", "m-2"})

	// Then: the caller is told which identifier, and nothing is acknowledged —
	// AWS makes an unknown identifier a complete batch failure.
	if unknown != "m-9" {
		t.Fatalf("unknown = %q, want m-9", unknown)
	}
	if len(succeeded) != 0 {
		t.Fatalf("succeeded = %v, want none", succeeded)
	}
}

func TestLowestReportedIndex_isTheEarliestReportedRecord(t *testing.T) {
	// Given: a report naming the third and second records, in that order.
	response := Parse([]byte(`{"batchItemFailures": [{"itemIdentifier": "c"}, {"itemIdentifier": "b"}]}`))

	// When: the resume point is resolved.
	index, reported, unknown := response.LowestReportedIndex([]string{"a", "b", "c", "d"})

	// Then: it is the earliest of them, because a stream checkpoints at the
	// lowest sequence number rather than skipping the records in between.
	if unknown != "" || !reported {
		t.Fatalf("reported = %v, unknown = %q, want true and none", reported, unknown)
	}
	if index != 1 {
		t.Fatalf("index = %d, want 1", index)
	}
}

func TestLowestReportedIndex_nothingReported(t *testing.T) {
	// Given: a response that named no record.
	response := Parse([]byte(`{"batchItemFailures": []}`))

	// Then: there is no resume point — the batch is done.
	if _, reported, _ := response.LowestReportedIndex([]string{"a"}); reported {
		t.Fatal("reported = true, want false")
	}
}

func TestReports_onlyWhenTheMemberIsNamed(t *testing.T) {
	// Given: responses a Lambda target might return without using the feature.
	for _, payload := range []string{"", "OK", `{"statusCode":200}`, "not json at all"} {
		if Reports([]byte(payload)) {
			t.Fatalf("Reports(%q) = true, want false", payload)
		}
	}
	// And: one that does name it, in either casing.
	for _, payload := range []string{`{"batchItemFailures":[]}`, `{"BatchItemFailures":[]}`} {
		if !Reports([]byte(payload)) {
			t.Fatalf("Reports(%q) = false, want true", payload)
		}
	}
}
