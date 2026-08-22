package compat

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestMergeRegistryDocsLeavesHandWrittenBytesUntouchedWhenGeneratedIsEmpty(t *testing.T) {
	// Given: the shape phase G0 ships — an empty generated registry.
	hand := []byte("{\n  \"version\": 1,\n  \"groups\": [{\"name\":\"s3-crud\"}]\n}\n")
	generated := []byte(`{"version":1,"groups":[]}`)

	// When: the two are merged.
	got, err := mergeRegistryDocs(hand, generated)
	if err != nil {
		t.Fatalf("mergeRegistryDocs: %v", err)
	}

	// Then: GET /registry serves byte-for-byte what it served before the
	// generated file existed. Anything less makes "the file is present" a
	// change in itself, which is exactly what the G0 gate forbids.
	if !bytes.Equal(got, hand) {
		t.Errorf("merged bytes differ from the hand-written registry:\n got = %s\nwant = %s", got, hand)
	}
}

func TestMergeRegistryDocsConcatenatesAndKeepsTheGeneratedFacet(t *testing.T) {
	// Given: a generated group carrying the three fields the dashboard facets
	// on.
	hand := []byte(`{"version":1,"comment":"hand","groups":[{"name":"s3-crud","service":"s3"}]}`)
	generated := []byte(`{"version":1,"groups":[{"name":"sqs-generated","service":"sqs","generated":true,"state":"candidate","scenario":"compat/scenarios/sqs.json","suites":["python-sdk"]}]}`)

	// When: the two are merged.
	got, err := mergeRegistryDocs(hand, generated)
	if err != nil {
		t.Fatalf("mergeRegistryDocs: %v", err)
	}

	// Then: one document, both groups, and the generated group's own fields
	// survive verbatim — the server does not model them, so re-marshalling
	// through a typed struct would silently drop whatever it does not know.
	var doc struct {
		Comment string `json:"comment"`
		Groups  []struct {
			Name      string   `json:"name"`
			Generated bool     `json:"generated"`
			State     string   `json:"state"`
			Scenario  string   `json:"scenario"`
			Suites    []string `json:"suites"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("parse merged registry: %v", err)
	}
	if doc.Comment != "hand" {
		t.Errorf("comment = %q, want the hand-written document's own fields preserved", doc.Comment)
	}
	if len(doc.Groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(doc.Groups))
	}
	if doc.Groups[0].Name != "s3-crud" || doc.Groups[0].Generated {
		t.Errorf("group[0] = %+v, want the hand-written group first and unflagged", doc.Groups[0])
	}
	gen := doc.Groups[1]
	if gen.Name != "sqs-generated" || !gen.Generated || gen.State != "candidate" ||
		gen.Scenario != "compat/scenarios/sqs.json" || len(gen.Suites) != 1 {
		t.Errorf("group[1] = %+v, want the generated group with its facet fields intact", gen)
	}
}
