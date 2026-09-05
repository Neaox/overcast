//go:build dev

package main

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// TestLoadModel_readsAWSQueryCompatible covers the derivation the scenario
// `client` header depends on: a service that carries
// aws.protocols#awsQueryCompatible also answers with the Query error code in
// the x-amzn-query-error header, and an interpreter is told which of the two
// spellings it may see. testdata/shapes/queryservice.json declares the trait;
// the widgets fixture does not.
func TestLoadModel_readsAWSQueryCompatible(t *testing.T) {
	for _, testCase := range []struct {
		service string
		want    bool
	}{
		{service: "queryservice", want: true},
		{service: "widgets", want: false},
	} {
		t.Run(testCase.service, func(t *testing.T) {
			// Given: a committed-shaped snapshot whose service shape does, or
			// does not, carry the trait.
			// When: the generator loads it.
			model, err := loadModel(filepath.Join("testdata", "shapes"), testCase.service)
			if err != nil {
				t.Fatalf("load %s: %v", testCase.service, err)
			}

			// Then: the derived fact matches what the snapshot declares.
			if model.QueryCompatible != testCase.want {
				t.Fatalf("QueryCompatible for %s = %v, want %v", testCase.service, model.QueryCompatible, testCase.want)
			}
		})
	}
}

// TestClientInfo_alwaysStatesAWSQueryCompatible holds the reason the field has
// no omitempty: a scenario that simply omitted it would be indistinguishable
// from one written before the field existed, and an interpreter would have to
// guess which error codes to accept.
func TestClientInfo_alwaysStatesAWSQueryCompatible(t *testing.T) {
	// Given: a client header for a service that is not Query-compatible.
	client := clientInfo{SDKID: "Widgets", EndpointPrefix: "widgets", Protocol: "awsJson1_1", APIVersion: "2026-01-01"}

	// When: it is encoded as the scenario file carries it.
	encoded, err := json.Marshal(client)
	if err != nil {
		t.Fatalf("marshal client: %v", err)
	}

	// Then: the field is stated as false rather than dropped.
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal client: %v", err)
	}
	value, stated := decoded["awsQueryCompatible"]
	if !stated {
		t.Fatalf("client header omits awsQueryCompatible: %s", encoded)
	}
	if value != false {
		t.Fatalf("awsQueryCompatible = %v, want false", value)
	}
}
