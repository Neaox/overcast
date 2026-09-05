// Package compatmodel holds the Go types for the compat model files in this
// directory that more than one command has to agree about.
//
// It lives under compat/ rather than in internal/ because the separation
// boundary in compat/AGENTS.md runs one way: nothing under compat/ may import
// a Go package from the emulator tree, and cmd/compat is held to the same rule.
// An internal/ home would have forced the soak's writer to break it. A
// build-time generator in the main module importing a leaf package under
// compat/ is the direction the boundary allows, and it is the direction
// cmd/compatgen already works in — it writes these files.
//
// The package is deliberately a dependency-free leaf: the compat package
// proper carries the runner, the dashboard server and the embedded UI, none of
// which a code generator has any business linking.
package compatmodel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// PromotionsVersion is the schema version of compat/model/promotions.json,
// pinned by promotions.schema.json as a `const`. A reader that accepted a
// version it does not know would decode a later schema lossily and — because
// the soak rewrites the whole file from what it decoded — erase whatever it
// failed to understand.
const PromotionsVersion = 1

// Promotions is the candidate → gated soak ledger, compat/model/promotions.json.
//
// `go run ./cmd/compat --promote-generated` is its only writer and cmd/compatgen
// its only reader: a generated group's state is an *input* to generation, so
// that no second tool ever writes compat/suites/registry.generated.json. See
// docs/plans/compat-coverage-modelgen.md § 3.6.
//
// Groups is a map so the encoder sorts it: a promotion is then a one-line diff
// wherever the group's name falls, rather than a reordering of an array.
type Promotions struct {
	Schema  string               `json:"$schema,omitempty"`
	Comment string               `json:"$comment,omitempty"`
	Version int                  `json:"version"`
	Groups  map[string]Promotion `json:"groups"`
}

// Promotion is one generated group's soak record.
type Promotion struct {
	// State is "candidate" or "gated" — the state cmd/compatgen emits into the
	// generated registry for this group.
	State string `json:"state"`
	// FirstSeen is the date the soak first observed the group, written once
	// and never rewritten. Without it a candidate cannot age, and a group that
	// will never agree would report forever while gating nothing — the inverse
	// of the flake-list problem the state machine was designed to avoid.
	FirstSeen string `json:"firstSeen"`
	// PromotedAt and Runs are the evidence: which day the group entered the
	// gate, and which runs agreed. A promotion nobody can audit back to its
	// artifacts is a hand edit with extra steps. Both are cleared when a group
	// is taken back out of the gate, so they never describe a promotion that
	// has been withdrawn.
	PromotedAt string   `json:"promotedAt,omitempty"`
	Runs       []string `json:"runs,omitempty"`
}

// EmptyPromotions is the ledger a checkout with no promotions.json has: no
// group observed, every generated group therefore still a candidate.
func EmptyPromotions() *Promotions {
	return &Promotions{Version: PromotionsVersion, Groups: map[string]Promotion{}}
}

// ReadPromotions reads and version-checks the ledger.
//
// A missing file is an empty ledger, for the same reason the generated
// registry reader tolerates a missing registry: a checkout that predates the
// soak must generate exactly what it generated before.
func ReadPromotions(path string) (*Promotions, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return EmptyPromotions(), nil
		}
		return nil, fmt.Errorf("read promotions %s: %w", path, err)
	}
	file, err := DecodePromotions(contents)
	if err != nil {
		return nil, fmt.Errorf("promotions %s: %w", path, err)
	}
	return file, nil
}

// DecodePromotions decodes the ledger strictly.
//
// Strictly matters more here than in most readers, because the soak is a
// read-modify-write over the whole file: a field a later schema version adds
// would decode into nothing and then be written back out as nothing, so a
// lenient reader silently deletes the part of the ledger it did not
// understand. Refusing an unknown field and an unknown version turns that
// silent erasure into a loud stop.
func DecodePromotions(contents []byte) (*Promotions, error) {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var file Promotions
	if err := decoder.Decode(&file); err != nil {
		return nil, err
	}
	if decoder.More() {
		return nil, fmt.Errorf("trailing data after the JSON document")
	}
	if file.Version != PromotionsVersion {
		return nil, fmt.Errorf("version %d is not the %d this build understands; upgrade the tool rather than the file",
			file.Version, PromotionsVersion)
	}
	if file.Groups == nil {
		file.Groups = map[string]Promotion{}
	}
	return &file, nil
}

// EncodePromotions renders the ledger exactly as cmd/compatgen renders its own
// output — two-space indent, no HTML escaping, one trailing newline — so that
// a file written by the soak and a file a human tidied by hand are the same
// bytes, and a no-op run produces no diff.
func EncodePromotions(file *Promotions) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(file); err != nil {
		return nil, fmt.Errorf("encode promotions: %w", err)
	}
	return buffer.Bytes(), nil
}
