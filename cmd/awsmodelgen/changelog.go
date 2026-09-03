package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"
)

// Deriving the refresh PR's changelog fragment from the inventory diff.
//
// A model refresh is not a no-op at runtime, which is why the refresh PR
// cannot simply waive the changelog gate. internal/router's restFallback
// switches on whether the pinned corpus claims a request:
//
//	restClaimAnswers      -> writeNotImplemented (protocol-correct 501)
//	restClaimScopeMismatch-> writeScopeMismatch  (403 InvalidSignatureException)
//	restClaimNone         -> the S3 fallback
//
// Every category this file reports moves requests across that switch:
//
//   - An added operation takes its path off the S3 fallback, so a signed
//     caller that was getting a bucket-or-object answer starts getting the
//     protocol-correct 501 — a fixed template marked x-emulator-unsupported;
//     it does not name the operation (established by alpha.38's RC
//     verification), and unsigned traffic without a bearer token stays S3's
//     by restFallback's design.
//   - A removed one hands the path back.
//   - A protocol trait change moves the operation's ErrorProfile, which is what
//     both writeNotImplemented and writeScopeMismatch pick the error envelope
//     from — the same request, a different response shape.
//   - A collision change moves a binding between the ambiguous and unambiguous
//     tiers. claimAnswersCaller returns true unconditionally for an ambiguous
//     claim but compares signing names for an unambiguous one, so a binding
//     becoming shared turns a 403 scope mismatch into a 501.
//
// What this deliberately does not attempt is *significance*. It reports which
// classes moved and by how much, at the same altitude as the PR body, and
// leaves the judgement of whether one matters to the reviewer the workflow
// never merges around. The value is that the class and the count are exact and
// come from the same diff the gates read.
//
// When no category moved, no fragment is written and the caller waives the gate
// instead — a waiver backed by the generator having looked, which is a
// different thing from a bot waiving because it is a bot.

// changelogFragment renders the fragment for a refresh, or "" when nothing a
// caller can observe moved.
//
// The prose avoids the words the fragment linter reads as an unmarked breaking
// change ("refuses", "rejects", "now requires"); scripts/changelog.py check is
// the authority and TestChangelogFragment_passesTheFragmentLinter runs it.
func changelogFragment(d inventoryDiff, modelDate string) string {
	if d.empty() {
		return ""
	}

	var out bytes.Buffer
	if len(d.AddedOperations) > 0 {
		fmt.Fprintf(&out, "+ [router] %s newly modeled by AWS are recognised.\n",
			countLabel(len(d.AddedOperations), "operation", "operations"))
		// A named-services clause is unbounded in length (up to nameLimit real
		// AWS service slugs), so it goes on its own detail line rather than the
		// capped summary — see .changelog/README.md "Leading with a summary".
		if clause := addedServicesClause(d.AddedServices); clause != "" {
			fmt.Fprintf(&out, "  %s\n", clause)
		}
		out.WriteString("  a signed request to one reaches a protocol-correct `501` in that service's own error envelope, instead of the S3 fallback's bucket-or-object answer\n")
	}
	if counts, effects := d.changedClauses(); len(counts) > 0 {
		fmt.Fprintf(&out, "~ [router] the pinned AWS API models moved to %s.\n", modelDate)
		for _, line := range packDetail(joinClauses(counts), joinEffects(effects)) {
			fmt.Fprintf(&out, "  %s\n", line)
		}
	}
	return out.String()
}

// changedClauses lists the non-additive categories that moved, in the order the
// PR body reports them, with the runtime effect of each. Only the effects of
// categories that actually moved are returned: a sentence explaining collisions
// on a refresh with no collision change describes something that did not
// happen, which is worse than saying less.
//
// Removals are folded in here rather than written as a Removed entry. The
// Removed category records something Overcast withdrew, and an operation AWS
// retired from its own models is a change in what Overcast recognises, not a
// feature taken away. Writing it as `-` would also mark every such refresh
// breaking by default and trip the release hold on a routine corpus bump.
func (d inventoryDiff) changedClauses() (counts, effects []string) {
	if n := len(d.RemovedOperations); n > 0 {
		counts = append(counts, countLabel(n, "operation AWS retired from its models is no longer claimed", "operations AWS retired from its models are no longer claimed"))
		effects = append(effects, "an unclaimed operation goes back to the S3 fallback")
	}
	if n := len(d.TraitChanges); n > 0 {
		counts = append(counts, countLabel(n, "operation changed protocol traits", "operations changed protocol traits"))
		effects = append(effects, "a trait change moves the error envelope")
	}
	if n := len(d.BindingChanges); n > 0 {
		counts = append(counts, countLabel(n, "changed an HTTP or target binding", "changed an HTTP or target binding"))
		effects = append(effects, "a binding change moves which request shape reaches it")
	}
	if n := len(d.AddedCollisions) + len(d.RemovedCollisions); n > 0 {
		counts = append(counts, countLabel(n, "path binding changed which services share it", "path bindings changed which services share them"))
		effects = append(effects, "a shared binding skips the credential-scope check, having no single owner")
	}
	return counts, effects
}

// joinEffects renders the effects as one sentence, capitalised as prose.
func joinEffects(effects []string) string {
	joined := strings.Join(effects, "; ")
	return strings.ToUpper(joined[:1]) + joined[1:]
}

// detailLineMax mirrors DETAIL_LINE_MAX in scripts/changelog.py: a continuation
// line is a note, not a paragraph. Kept here rather than read from there
// because this generator has to produce a fragment the gate accepts, and
// TestChangelogFragment_passesTheFragmentLinter runs the real linter over the
// fragment to prove the two have not drifted.
const detailLineMax = 200

// packDetail folds sentences into detail lines within the linter's per-line
// cap, splitting on the "; " the effects sentence is built from rather than
// mid-clause. A clause that will not fit even alone is emitted whole: dropping
// it would lose a count the entry exists to report, and the linter failing
// loudly is the better outcome.
func packDetail(sentences ...string) []string {
	var out []string
	for _, sentence := range sentences {
		if len(sentence) <= detailLineMax {
			out = append(out, sentence)
			continue
		}
		line := ""
		for _, clause := range strings.Split(sentence, "; ") {
			switch {
			case line == "":
				line = clause
			case len(line)+2+len(clause) <= detailLineMax:
				line += "; " + clause
			default:
				out = append(out, line+";")
				line = clause
			}
		}
		out = append(out, line)
	}
	return out
}

// addedServicesClause names the new services when there are few enough to read,
// and counts them otherwise. The cutoff keeps one entry line readable; the PR
// body carries the full list either way. Rendered as a standalone sentence
// (its own detail line), not a continuation of the summary above it — see
// changelogFragment.
func addedServicesClause(services []string) string {
	const nameLimit = 6
	if len(services) == 0 {
		return ""
	}
	clause := fmt.Sprintf("spanning %s new to the corpus", countLabel(len(services), "service", "services"))
	if len(services) <= nameLimit {
		clause += fmt.Sprintf(" (`%s`)", strings.Join(services, "`, `"))
	}
	return clause + "."
}

func countLabel(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, plural)
}

func joinClauses(clauses []string) string {
	switch len(clauses) {
	case 1:
		return clauses[0]
	case 2:
		return clauses[0] + " and " + clauses[1]
	default:
		return strings.Join(clauses[:len(clauses)-1], ", ") + " and " + clauses[len(clauses)-1]
	}
}

// writeChangelogFragment writes the fragment for this refresh and reports
// whether one was warranted. A false return is the signal to waive the gate:
// the diff is real (the revision moved) but no category a caller can observe
// went with it.
func writeChangelogFragment(path string, d inventoryDiff, modelDate string) (bool, error) {
	fragment := changelogFragment(d, modelDate)
	if fragment == "" {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(fragment), 0o644); err != nil {
		return false, fmt.Errorf("write changelog fragment %s: %w", path, err)
	}
	return true, nil
}
