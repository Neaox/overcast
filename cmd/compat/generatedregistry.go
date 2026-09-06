// cmd/compat/generatedregistry.go — the generated half of the test registry.
//
// compat/suites/registry.json is hand-written and reviewable; its sibling
// compat/suites/registry.generated.json is machine output that cmd/compatgen
// rewrites wholly. Every loader concatenates the two. Splitting them keeps a
// five-thousand-entry diff out of the file humans edit, lets the generator
// rebuild its own file without merge conflicts, and makes "generated vs
// hand-written" an explicit fact rather than something inferred from a naming
// convention.
//
// Two invariants make the split safe, and both are enforced here:
//
//   - The join key is shared. baseline.json, flaky.json and parity-debt.json
//     all key on suite/group/test and are indifferent to which file a group
//     came from, so a generated name that collides with a hand-written one
//     silently merges two different tests into one gate entry.
//     lintGeneratedRegistry rejects that.
//
//   - Generated groups start out unable to break anything. A group in state
//     "candidate" runs everywhere and reports everywhere but gates nothing:
//     it is excluded from --compare-baseline and --max-failures in both
//     directions until a soak promotes it to "gated".
//
// See docs/plans/compat-coverage-modelgen.md § 3.6.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

const generatedRegistryVersion = 1

// Generated group lifecycle states.
//
// This is the inverse of compat/flaky.json, and the two must not be confused.
// A flaky test escaped a gate it was already under, with a reviewer's explicit
// approval and a tracking issue. A candidate has not entered the gate yet: it
// was produced mechanically from the AWS model, nobody has watched it run, and
// gating on it before the soak would mean a model-refresh PR could red every
// build until someone quarantined the new tests — which would make flaky.json
// the dumping ground the quarantine lint exists to prevent.
const (
	generatedStateCandidate = "candidate"
	generatedStateGated     = "gated"
)

// ---------------------------------------------------------------------------
// registry.generated.json
// ---------------------------------------------------------------------------

type generatedRegistry struct {
	Version int              `json:"version"`
	Comment string           `json:"comment,omitempty"`
	Groups  []generatedGroup `json:"groups"`
}

// generatedGroup models only what the gate path reads. The full group shape —
// `slow`, and the per-test `op`/`depends`/`requires`/`skip` — is the shared
// TestGroup's, defined once in registry.schema.json and consumed by the suite
// loaders; unknown fields simply pass through here.
type generatedGroup struct {
	Service string `json:"service"`
	Name    string `json:"name"`
	// Generated is required and always true. It is redundant with the file the
	// group was read from, and deliberately so: the dashboard, the report and
	// the lint all see groups after concatenation, by which point the file of
	// origin is gone.
	Generated bool `json:"generated"`
	// Scenario is the IR file this group was generated from, so a failing test
	// leads back to the recipe rather than to a dead end.
	Scenario string `json:"scenario,omitempty"`
	// State is generatedStateCandidate or generatedStateGated.
	State string `json:"state"`
	// ShadowOf names the hand-written group this one shadows, on a group
	// produced from an authored scenario that is being compared against the
	// native implementations it will replace (§3.11 step 2). It is what
	// --compare-shadow joins the two halves on, and why --promote-generated
	// leaves the group alone: a shadow is collecting evidence of agreement
	// with another group, not with itself, and it is deleted when that
	// evidence is in.
	ShadowOf string `json:"shadowOf,omitempty"`
	// Suites lists the backends that can execute the group. Always present and
	// mechanically derived: a suite absent from it is out of scope, not
	// indebted.
	Suites []string        `json:"suites"`
	Tests  []generatedTest `json:"tests"`
}

// generatedTest carries only the join key. The full test shape (op, depends,
// requires, skip) lives in the JSON and is read by the suite loaders; nothing
// on the gate path needs more than the name.
//
// An alias rather than a second struct: the two registries are concatenated and
// matched on exactly these names, so a copy that could drift from parityTest
// would be a copy of the one field that must never differ.
type generatedTest = parityTest

// readGeneratedRegistry loads the generated sibling, treating a missing file as
// an empty registry.
//
// Tolerating absence is not laziness: suite images, CI artifacts and checkouts
// that predate the file all have to keep working, and "the file is not there"
// must produce the same verdict as "the file is there and empty" — that
// equivalence is phase G0's whole acceptance gate.
func readGeneratedRegistry(path string) (*generatedRegistry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &generatedRegistry{Version: generatedRegistryVersion}, nil
		}
		return nil, fmt.Errorf("read generated registry %s: %w", path, err)
	}
	var reg generatedRegistry
	if err := json.Unmarshal(b, &reg); err != nil {
		return nil, fmt.Errorf("parse generated registry %s: %w", path, err)
	}
	return &reg, nil
}

// parityGroups projects generated groups onto the shape the parity checker
// already understands, so scoping, debt and the unregistered-result check all
// work with zero new concepts.
func (r *generatedRegistry) parityGroups() []parityGroup {
	out := make([]parityGroup, 0, len(r.Groups))
	for _, g := range r.Groups {
		out = append(out, parityGroup{
			Service:   g.Service,
			Name:      g.Name,
			Suites:    g.Suites,
			Generated: g.Generated,
			State:     g.State,
			Scenario:  g.Scenario,
			ShadowOf:  g.ShadowOf,
			Tests:     g.Tests,
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// The candidate exemption
// ---------------------------------------------------------------------------

// candidateSet is the set of group names in state "candidate".
//
// It mirrors flakySet, but keys on the group rather than on suite/group/test:
// candidacy is a property of the generated group as a whole, and applies to
// every suite that runs it. Keep the two sets distinct — merging them would
// make an untried generated test indistinguishable from a reviewer-approved
// quarantine, and the flaky lint's "this list only shrinks" promise depends on
// nothing else being hidden in it.
type candidateSet map[string]bool

// candidateGroups returns the groups that gate nothing yet.
func (r *generatedRegistry) candidateGroups() candidateSet {
	set := make(candidateSet)
	for _, g := range r.Groups {
		if g.State == generatedStateCandidate {
			set[g.Name] = true
		}
	}
	return set
}

// exempt reports whether a result belongs to a candidate group, and so is
// excluded from the baseline gate and the failure gate in both directions.
func (c candidateSet) exempt(group string) bool { return c[group] }

// loadCandidateGroups reads the candidate set from --generated-registry-file.
// Mirrors readFlakyFile(*flakyFilePath): the gate entry points each load their
// own exemptions rather than threading a pre-built set through.
//
// It lints before returning, and that is not belt-and-braces. The exemption
// this set grants is the ability to silence the baseline gate for a group, so
// the collision check has to hold on the path that grants it: a generated
// candidate group that reused a hand-written group's name would exempt the
// hand-written group from --compare-baseline and --max-failures, hiding a real
// regression. --check-parity runs the same lint, but compat.yml runs it after
// both baseline gates, so relying on it alone means the build reds for a
// confusing reason after the wrong verdict has already been reported.
func loadCandidateGroups() (candidateSet, error) {
	gen, err := readGeneratedRegistry(*generatedRegistryFile)
	if err != nil {
		return nil, err
	}
	// An empty generated registry needs no hand-written registry to check
	// against, and must not require one: --compare-baseline is run against
	// artifacts in contexts where compat/suites/registry.json may not be at
	// the default path.
	if len(gen.Groups) == 0 {
		return candidateSet{}, nil
	}
	hand, err := readParityRegistry(*registryFile)
	if err != nil {
		return nil, err
	}
	if issues := lintGeneratedRegistry(hand, gen); len(issues) > 0 {
		return nil, generatedRegistryIssueError(*generatedRegistryFile, issues)
	}
	return gen.candidateGroups(), nil
}

// ---------------------------------------------------------------------------
// Collision and shape lint
// ---------------------------------------------------------------------------

// lintGeneratedRegistry checks the invariants that make concatenation safe.
//
// The name checks are the load-bearing ones. Every gate file keys on
// suite/group/test with no notion of which registry a group came from, so a
// generated group that reuses a hand-written name does not conflict — it
// merges. The baseline would record one status for two different tests, the
// candidate exemption would silently disable the gate on a hand-written group,
// and parity would count the pair once. None of that surfaces as an error
// anywhere downstream, which is why it has to be caught at load time.
//
// The shape checks mirror registry.generated.schema.json so the invariants hold
// wherever the Go loader runs, including the suite images that never invoke the
// Python validator.
func lintGeneratedRegistry(hand *parityRegistry, gen *generatedRegistry) []string {
	var issues []string

	handGroups := make(map[string]bool)
	handKeys := make(map[string]bool)
	if hand != nil {
		for _, g := range hand.Groups {
			handGroups[g.Name] = true
			for _, t := range g.Tests {
				handKeys[g.Name+"/"+t.Name] = true
			}
			// The shared schema has to permit generated/state/scenario for
			// registry.generated.schema.json to extend its TestGroup by $ref,
			// so the ban on hand-written groups carrying them is enforced here
			// instead of by the schema.
			if g.Generated || g.State != "" || g.Scenario != "" || g.Parallel || g.ShadowOf != "" {
				issues = append(issues, fmt.Sprintf(
					"hand-written group %q carries a generated-only field (generated/state/scenario/shadowOf/parallel) — those belong in compat/suites/registry.generated.json, which cmd/compatgen owns",
					g.Name))
			}
		}
	}

	seenGroups := make(map[string]bool)
	seenKeys := make(map[string]bool)
	for _, g := range gen.Groups {
		switch {
		case handGroups[g.Name]:
			issues = append(issues, fmt.Sprintf(
				"generated group %q collides with a hand-written group of the same name — the two registries are concatenated and every gate file keys on suite/group/test, so the entries would merge rather than conflict",
				g.Name))
		case seenGroups[g.Name]:
			issues = append(issues, fmt.Sprintf(
				"generated group %q is declared twice in compat/suites/registry.generated.json",
				g.Name))
		}
		seenGroups[g.Name] = true

		if !g.Generated {
			issues = append(issues, fmt.Sprintf(
				"generated group %q does not set \"generated\": true — the dashboard facet, the report and this lint all read the flag, not the file it came from",
				g.Name))
		}
		switch g.State {
		case generatedStateCandidate, generatedStateGated:
		case "":
			issues = append(issues, fmt.Sprintf(
				"generated group %q has no \"state\" — it must be %q (gates nothing yet) or %q (soaked and enforced)",
				g.Name, generatedStateCandidate, generatedStateGated))
		default:
			issues = append(issues, fmt.Sprintf(
				"generated group %q has state %q, want %q or %q",
				g.Name, g.State, generatedStateCandidate, generatedStateGated))
		}
		if len(g.Suites) == 0 {
			issues = append(issues, fmt.Sprintf(
				"generated group %q has no \"suites\" — a generated group must name the backends that can execute it, or every suite without a backend inherits parity debt for tests it was never asked to run",
				g.Name))
		}
		if len(g.Tests) == 0 {
			issues = append(issues, fmt.Sprintf(
				"generated group %q has no tests", g.Name))
		}
		issues = append(issues, shadowIssues(g, hand)...)

		for _, t := range g.Tests {
			key := g.Name + "/" + t.Name
			if handKeys[key] {
				issues = append(issues, fmt.Sprintf(
					"generated test key %q duplicates a hand-written one — baseline.json, flaky.json and parity-debt.json cannot tell the two apart",
					key))
			}
			if seenKeys[key] {
				issues = append(issues, fmt.Sprintf(
					"generated test key %q is declared twice", key))
			}
			seenKeys[key] = true
		}
	}

	sort.Strings(issues)
	return issues
}

// generatedRegistryIssueError renders lint issues as a single error, one per
// line, so a --check-parity run names every collision rather than the first.
func generatedRegistryIssueError(path string, issues []string) error {
	return fmt.Errorf("%d problem(s) in %s:\n  %s", len(issues), path, strings.Join(issues, "\n  "))
}

// ---------------------------------------------------------------------------
// Shadow groups
// ---------------------------------------------------------------------------

// shadowIssues holds a shadow group to the two things --compare-shadow needs
// of it, and neither is decorative.
//
// The comparison joins shadow to native on the test name, per suite. A shadow
// naming a group that does not exist compares against nothing and reports a
// clean run; one whose test names have drifted compares eight of nine and says
// nothing about the ninth. Both read as "the port agrees with the natives",
// which is the one conclusion the soak exists to earn rather than assume — and
// the flip that follows deletes working code on the strength of it.
//
// A shadow must also stay a candidate. Gating a group that is scheduled for
// deletion would put the deletion behind a baseline update; more to the point,
// the promotion soak asks whether a group agrees with itself, which is not the
// question a shadow is being asked.
func shadowIssues(g generatedGroup, hand *parityRegistry) []string {
	if g.ShadowOf == "" {
		return nil
	}
	var issues []string
	if g.State != generatedStateCandidate {
		issues = append(issues, fmt.Sprintf(
			"generated group %q shadows %q but is in state %q — a shadow gates nothing and is deleted when the port lands, so it stays %q",
			g.Name, g.ShadowOf, g.State, generatedStateCandidate))
	}
	if hand == nil {
		return issues
	}
	var native *parityGroup
	for i := range hand.Groups {
		if hand.Groups[i].Name == g.ShadowOf {
			native = &hand.Groups[i]
			break
		}
	}
	if native == nil {
		return append(issues, fmt.Sprintf(
			"generated group %q shadows %q, which is not a group in the hand-written registry — --compare-shadow would join it against nothing and report agreement",
			g.Name, g.ShadowOf))
	}
	shadowTests := make(map[string]bool, len(g.Tests))
	for _, t := range g.Tests {
		shadowTests[t.Name] = true
	}
	nativeTests := make(map[string]bool, len(native.Tests))
	for _, t := range native.Tests {
		nativeTests[t.Name] = true
	}
	var missing, extra []string
	for name := range nativeTests {
		if !shadowTests[name] {
			missing = append(missing, name)
		}
	}
	for name := range shadowTests {
		if !nativeTests[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		issues = append(issues, fmt.Sprintf(
			"generated group %q shadows %q but does not declare %s — the comparison joins on the test name, so a test the shadow is missing is a test nobody proved the port reproduces",
			g.Name, g.ShadowOf, strings.Join(missing, ", ")))
	}
	if len(extra) > 0 {
		issues = append(issues, fmt.Sprintf(
			"generated group %q shadows %q and declares %s, which %q does not — a shadow reproduces the native group's tests, it does not add to them",
			g.Name, g.ShadowOf, strings.Join(extra, ", "), g.ShadowOf))
	}
	return issues
}
