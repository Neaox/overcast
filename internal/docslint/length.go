package docslint

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// The length budget. A page over either number is doing more than one job, and
// the fix is almost always to split it rather than to tighten the wording.
//
// The numbers come from measuring the corpus at the time the rule landed
// (143 published pages). Prose median 1,005 characters, p75 2,518, p90 6,421;
// authored-page median 3,496, p75 5,568, p90 12,190. Both budgets are set at
// roughly the corpus p90, which is the point where the distribution stops being
// "a page about one thing" and starts being a page that was never split:
//
//   - MaxProseChars catches the wall of text. 6,000 characters is about 900
//     words, four minutes of reading, and four fifths of the corpus already
//     sits under half of it.
//   - MaxPageChars catches the mega-reference that evades the prose budget by
//     being made of tables — the shape docs/configuration.md had, with 5,618
//     characters of prose inside a 28,265-character page.
//
// Together they flag 20 of the 143 pages: 16 over on prose, 15 over on page.
//
// Neither is a hard ceiling on what a page may say. A page that has a real
// reason to run long carries the LengthReviewMarker, which is what turns going
// over from an accident into a decision.
const (
	MaxProseChars = 6000
	MaxPageChars  = 12000
)

// LengthReviewMarker is the deliberate opt-out: an HTML comment naming why this
// particular page is legitimately long.
//
//	<!-- docs-length-review: the env var reference is one table; splitting it
//	     by area would make readers guess which page a variable is on -->
//
// An HTML comment rather than a frontmatter key on purpose. It renders as
// nothing on GitHub and on the site, it survives
// `docs-index.go --refresh-frontmatter` (which rewrites frontmatter from
// inferred metadata and would drop an unknown key), and it sits next to the
// content it is excusing rather than in a header nobody reads.
const LengthReviewMarker = "docs-length-review:"

// minLengthReason is how much reason the marker has to carry. "too long" is not
// a considered decision; a sentence is. The number is deliberately low — this
// rejects the empty gesture, it does not grade the writing.
const minLengthReason = 30

// LengthBacklogEntry is one page that was already over budget when the budget
// landed, with the size it was at the time.
//
// The recorded numbers are ceilings, not exemptions: the page fails if it grows
// past them, and it fails again — as a stale entry — once it comes under both
// budgets, which is what deletes the entry. So a backlogged page can only move
// one way. Ceilings are rounded up to the next 500 characters so an ordinary
// typo fix does not trip the ratchet.
type LengthBacklogEntry struct {
	Prose int
	Page  int
	// Measured is what the page actually was when the entry was written,
	// "<prose>/<page>", so the slack in the ceilings is visible.
	Measured string
	// Why is the one-line note on what the page needs, shown in the failure so
	// whoever trips it knows what was already known about it.
	Why string
}

// LengthBacklog is the shrinking list of pages that predate the budget.
//
// Same self-deleting shape as RestructurePending and cmd/capgen's
// capabilityManifestExemptions, and for the same reason: an exemption nobody is
// made to revisit is indefinite. This one is stricter than either — it also
// fails on growth, so a page waiting for its rewrite cannot quietly get worse
// while it waits.
//
// Every entry was measured on the day the budget landed. `Why` names the split
// the page is waiting for, so the entry is a work item rather than a note that
// the page is big.
var LengthBacklog = map[string]LengthBacklogEntry{
	"docs/services/ecs/limitations.md": {Prose: 10000, Page: 12000, Measured: "9666/10641", Why: "table the divergences, move the explanations out"},
}

// LengthReviewed records a page's authored size as the linter measures it.
type LengthReviewed struct {
	Prose int
	Page  int
}

var (
	lengthMarkerRE = regexp.MustCompile(`(?is)<!--\s*` + regexp.QuoteMeta(LengthReviewMarker) + `\s*(.*?)-->`)
	tableRowRE     = regexp.MustCompile(`^\s*\|`)
	// Every generated block, not only cmd/capgen's capabilities table: the
	// service-name table in docs/configuration.md, the service index in
	// docs/README.md, the status blocks, the code tabs. All of them are
	// written by a generator, so none of them is a page the author can split.
	generatedBeginRE = regexp.MustCompile(`^<!--\s*BEGIN overcast:[a-z0-9-]+\s*-->$`)
	generatedEndRE   = regexp.MustCompile(`^<!--\s*END overcast:[a-z0-9-]+\s*-->$`)
)

// Measure returns a page's prose length and its authored-page length, both in
// characters.
//
// Prose is what a reader reads top to bottom: headings, paragraphs, list items,
// blockquotes. Fenced code, GFM table rows and HTML comments are excluded — a
// page is not a wall of text because it holds a big table, and the page budget
// is what covers that case.
//
// Both measures exclude every `<!-- BEGIN overcast:… -->` block. That is what
// makes the rule apply to generated content by kind rather than by name:
// docs/services/<key>/operations.md is nothing but a generated table, so it
// measures zero and passes on its own merits, and docs/configuration.md is not
// charged for a service-name table cmd/capgen writes and the author cannot
// split. A hand-written paragraph smuggled into a generated file is measured
// like any other prose (and rejected outright by checkSubPage anyway).
func Measure(body string) LengthReviewed {
	lines := strings.Split(body, "\n")
	fence := ""
	generated := false
	var prose, page []string
	for _, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		switch {
		case generatedBeginRE.MatchString(trimmed):
			generated = true
			continue
		case generatedEndRE.MatchString(trimmed):
			generated = false
			continue
		case generated:
			continue
		}
		if trimmed != "" {
			page = append(page, trimmed)
		}
		if fence != "" {
			if strings.HasPrefix(trimmed, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fence = trimmed[:3]
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "<!--") || tableRowRE.MatchString(raw) {
			continue
		}
		prose = append(prose, trimmed)
	}
	return LengthReviewed{
		Prose: utf8.RuneCountInString(strings.Join(prose, " ")),
		Page:  utf8.RuneCountInString(strings.Join(page, "\n")),
	}
}

// lengthReviewReason returns the reason given in the page's opt-out marker, and
// whether the page carries one at all.
func lengthReviewReason(body string) (string, bool) {
	m := lengthMarkerRE.FindStringSubmatch(body)
	if m == nil {
		return "", false
	}
	return strings.Join(strings.Fields(m[1]), " "), true
}

// checkLength applies the budget to one page. retire says whether a stale
// LengthBacklog entry may be reported: only a whole-corpus run can retire one,
// because a unit test lints a stub body under a real path and would otherwise
// be told to delete an entry that is still earning its place.
func checkLength(doc Doc, retire bool) []Problem {
	var problems []Problem
	report := func(format string, args ...any) {
		problems = append(problems, Problem{Path: doc.Path, Msg: fmt.Sprintf(format, args...)})
	}

	size := Measure(doc.Body)
	reason, opted := lengthReviewReason(doc.Body)
	backlog, backlogged := LengthBacklog[doc.Path]
	overProse := size.Prose > MaxProseChars
	overPage := size.Page > MaxPageChars

	if opted {
		switch {
		case len(reason) < minLengthReason:
			report("<!-- %s … --> gives no reason worth the name (%q). Say why this page is legitimately long, in a sentence — the marker exists to make the decision visible, not to silence the check",
				LengthReviewMarker, reason)
		case !overProse && !overPage:
			report("carries <!-- %s … --> but is inside the budget (%d/%d prose, %d/%d page). Delete the marker",
				LengthReviewMarker, size.Prose, MaxProseChars, size.Page, MaxPageChars)
		}
		return problems
	}

	if backlogged {
		switch {
		case !overProse && !overPage:
			if !retire {
				break
			}
			report("is inside the budget now (%d/%d prose, %d/%d page). Delete its LengthBacklog entry in internal/docslint/length.go so the rule stays enforced",
				size.Prose, MaxProseChars, size.Page, MaxPageChars)
		case size.Prose > backlog.Prose:
			report("is on the length backlog at %d characters of prose and this grows it to %d. A page waiting to be split may only shrink — %s",
				backlog.Prose, size.Prose, backlog.Why)
		case size.Page > backlog.Page:
			report("is on the length backlog at %d characters of page and this grows it to %d. A page waiting to be split may only shrink — %s",
				backlog.Page, size.Page, backlog.Why)
		}
		return problems
	}

	if overProse {
		report("%d characters of prose, over the %d budget. Split it: one concern per page, lead with the command or the decision the reader came for, push the exhaustive detail into a table. If it is legitimately long, say why in <!-- %s … --> (see docs/dev/content-charter.md)",
			size.Prose, MaxProseChars, LengthReviewMarker)
	}
	if overPage {
		report("%d characters, over the %d page budget. Split it by concern, or move the reference tables to their own page. If it is legitimately long, say why in <!-- %s … --> (see docs/dev/content-charter.md)",
			size.Page, MaxPageChars, LengthReviewMarker)
	}
	return problems
}

// checkBacklogPathsExist rejects a LengthBacklog entry naming a page that is no
// longer published — a rename or a deletion that left its ceiling behind, where
// it would sit forever waiting for a file that never comes back.
func checkBacklogPathsExist(seen map[string]bool) []Problem {
	var stale []string
	for path := range LengthBacklog {
		if !seen[path] {
			stale = append(stale, path)
		}
	}
	if len(stale) == 0 {
		return nil
	}
	sort.Strings(stale)
	return []Problem{{
		Path: "internal/docslint/length.go",
		Msg: fmt.Sprintf("LengthBacklog names %s that no longer exist; delete the entries: %s",
			plural(len(stale), "page", "pages"), strings.Join(stale, ", ")),
	}}
}
