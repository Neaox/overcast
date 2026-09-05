//go:build dev

package main

import (
	"fmt"
	"os"
)

// The soak ledger, compat/model/promotions.json.
//
// A generated group's `state` in compat/suites/registry.generated.json decides
// whether it gates anything, and something has to move it from "candidate" to
// "gated" once the nightly soak has watched it agree with itself. The obvious
// implementation — have the soak rewrite that one field in the registry — puts
// two tools in charge of one generated file, and the generator's whole contract
// is that it rewrites that file wholly and byte-identically from its inputs.
// The second writer would be indistinguishable from a hand edit and `-check`
// would be right to reject it.
//
// So the state is an *input*. `go run ./cmd/compat --promote-generated` writes
// this file and nothing else; the generator reads it and emits the state it
// names. Each tool owns exactly one file, `make generate-compat-model` is still
// the only thing that touches the registry, and a promotion is reviewable as a
// one-line diff next to the regenerated registry it explains.
//
// See docs/plans/compat-coverage-modelgen.md § 3.6 and cmd/compat/promote.go,
// which is the writer.

type promotionsFile struct {
	Schema  string                    `json:"$schema,omitempty"`
	Comment string                    `json:"$comment,omitempty"`
	Version int                       `json:"version"`
	Groups  map[string]promotionEntry `json:"groups"`
}

type promotionEntry struct {
	State      string   `json:"state"`
	FirstSeen  string   `json:"firstSeen"`
	PromotedAt string   `json:"promotedAt,omitempty"`
	Runs       []string `json:"runs,omitempty"`
}

// loadPromotions reads and schema-checks the ledger.
//
// A missing file is an empty ledger, for the same reason readGeneratedRegistry
// tolerates a missing registry: a checkout that predates the soak must generate
// exactly what it generated before, with every group a candidate.
func loadPromotions(path string, schema *schemaSet) (*promotionsFile, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &promotionsFile{Version: promotionsVersion, Groups: map[string]promotionEntry{}}, nil
		}
		return nil, fmt.Errorf("read promotions %s: %w", path, err)
	}
	if err := schema.validate(schemaPromotions, contents); err != nil {
		return nil, fmt.Errorf("promotions %s: %w", path, err)
	}
	var file promotionsFile
	if err := decodeStrict(contents, &file); err != nil {
		return nil, fmt.Errorf("promotions %s: %w", path, err)
	}
	if file.Groups == nil {
		file.Groups = map[string]promotionEntry{}
	}
	return &file, nil
}

// stateOf is the state the registry records for a group. Absent from the
// ledger means candidate: a group that nothing has observed yet has not soaked,
// and the safe default is the one that gates nothing.
func (p *promotionsFile) stateOf(group string) string {
	if p == nil {
		return generatedStateCandidate
	}
	if entry, ok := p.Groups[group]; ok && entry.State != "" {
		return entry.State
	}
	return generatedStateCandidate
}

// checkPromotionsAreKnownGroups refuses a ledger entry for a group no scenario
// produces.
//
// Without this a renamed or deleted group leaves its promotion behind, and the
// entry is then indistinguishable from one that is doing something: the next
// group to be given that name would inherit a gated state it never earned.
// Checking against the scenarios rather than the registry is deliberate — the
// registry is empty until a suite has a scenario backend, so an entry would
// look stale for a reason that has nothing to do with the group existing.
func checkPromotionsAreKnownGroups(promotions *promotionsFile, scenarios []*scenario) error {
	known := make(map[string]bool)
	for _, s := range scenarios {
		for _, g := range s.Groups {
			known[g.Name] = true
		}
	}
	var unknown []string
	for name := range promotions.Groups {
		if !known[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	return fmt.Errorf("%s names group(s) no scenario produces: %s; delete the entr(ies) — `go run ./cmd/compat --promote-generated` prunes them itself",
		promotionsPath, joinSorted(unknown, ", "))
}
