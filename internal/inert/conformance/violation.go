package conformance

import "fmt"

// Violation is one broken clause of docs/plans/inert-tier-rollout.md §3.
type Violation struct {
	// Clause is a stable id of the form "<section>/<name>", e.g.
	// "3.2/roundtrip-fidelity", tied to the plan section that states the
	// rule. Never invent a clause id outside the set the check functions in
	// check.go emit — that stability is what lets a failure message name
	// the exact contract it broke.
	Clause string
	// Message explains what was observed, in enough detail to fix without
	// re-reading the check's source.
	Message string
}

func (v Violation) String() string {
	return fmt.Sprintf("%s: %s", v.Clause, v.Message)
}

// violation is a small constructor to keep check functions to one line per
// failure.
func violation(clause, format string, args ...any) Violation {
	return Violation{Clause: clause, Message: fmt.Sprintf(format, args...)}
}
