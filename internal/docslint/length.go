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

// The contributor budget, for docs/dev/. Bigger, because those pages are not
// the same kind of thing: a published page answers one question for somebody
// mid-task, and a contributor page explains a mechanism to somebody about to
// change it. Cutting docs/dev/networking.md to 6,000 characters would not make
// it a better page, it would make it a worse one somewhere else.
//
// The numbers come from measuring docs/dev/ the day the rule landed — twelve
// pages, prose median 9,894, p75 18,510, p90 19,743; page median 13,816,
// p75 23,926, p90 24,164. The budget sits above the largest page that is still
// about one thing (docs/dev/networking.md, 24,100 characters of prose after
// the merge with container-networking.md) and far below the one that is not:
// docs/dev/architecture.md, at 59,103, which is thirteen chapters under one H1
// and is on ContributorLengthBacklog until it is split.
//
// Same mechanism otherwise. LengthReviewMarker opts a page out with a stated
// reason, and the marker is self-deleting.
const (
	DevMaxProseChars = 26000
	DevMaxPageChars  = 30000
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
// The list is empty: every published page that predated the budget has been
// rewritten. Adding an entry back is not the way to land a long page — write
// the reason on the page in a LengthReviewMarker, where a reader can see it
// too. A budget arriving over a tree that never had one is the exception, and
// ContributorLengthBacklog below is that case.
var LengthBacklog = map[string]LengthBacklogEntry{}

// ContributorLengthBacklog is the same list for docs/dev/, kept separate
// because the two trees are linted by separate passes: a stale entry is one
// naming a page that pass no longer sees, and one list over two passes would
// call every entry stale in whichever pass did not own it.
var ContributorLengthBacklog = map[string]LengthBacklogEntry{
	"docs/dev/architecture.md": {
		Prose:    59500,
		Page:     69000,
		Measured: "59103/68945",
		Why:      "thirteen numbered chapters under one H1; split it by subsystem, one page each, with this page left as the map",
	},
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
//
// Fenced code is blanked first. A page that shows the marker as an example —
// docs/dev/content-charter.md and docs/dev/service-doc-template.md both do,
// which is how the rule is documented at all — is not a page claiming the
// exemption, and reading the sample as one told both of them to delete a
// marker they do not have.
func lengthReviewReason(body string) (string, bool) {
	m := lengthMarkerRE.FindStringSubmatch(strings.Join(eachOutsideFences(strings.Split(body, "\n")), "\n"))
	if m == nil {
		return "", false
	}
	return strings.Join(strings.Fields(m[1]), " "), true
}

// budget is one tree's pair of ceilings, with the backlog of pages that
// predate them and the name a failure tells the reader to edit.
type budget struct {
	prose, page int
	backlog     map[string]LengthBacklogEntry
	backlogName string
}

func publishedLengthBudget() budget {
	return budget{prose: MaxProseChars, page: MaxPageChars, backlog: LengthBacklog, backlogName: "LengthBacklog"}
}

func contributorLengthBudget() budget {
	return budget{prose: DevMaxProseChars, page: DevMaxPageChars, backlog: ContributorLengthBacklog, backlogName: "ContributorLengthBacklog"}
}

// CheckContributor applies the length budget, and only the length budget, to
// the contributor tree under docs/dev/.
//
// Only that rule. The structure rules describe one published page shape, the
// tells and the "## Related" footer are promises made to a reader who arrived
// from search, and a contributor page has neither shape nor that reader. What
// it does share with a published page is the failure mode the budget exists
// for: a page that grew until nobody could find anything in it.
//
// CheckContributor runs it with no whole-tree claim. See CheckContributorWith.
func CheckContributor(docs []Doc) []Problem {
	return CheckContributorWith(docs, Options{})
}

// CheckContributorWith is CheckContributor with Options. Only WholeCorpus is
// read, and it carries the same meaning it does for published pages: a stale
// ContributorLengthBacklog entry can only be retired by a run that saw the
// whole tree, because "this page is gone" is a true statement about the tree
// and a meaningless one about the single page a unit test lints.
func CheckContributorWith(docs []Doc, opts Options) []Problem {
	var problems []Problem
	seen := map[string]bool{}
	sorted := append([]Doc(nil), docs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	for _, doc := range sorted {
		seen[doc.Path] = true
		problems = append(problems, checkLength(doc, contributorLengthBudget(), opts.WholeCorpus)...)
	}
	if opts.WholeCorpus {
		problems = append(problems, checkBacklogPathsExist(seen, contributorLengthBudget())...)
	}
	return problems
}

// checkLength applies one budget to one page. retire says whether a stale
// backlog entry may be reported: only a whole-tree run can retire one, because
// a unit test lints a stub body under a real path and would otherwise be told
// to delete an entry that is still earning its place.
func checkLength(doc Doc, b budget, retire bool) []Problem {
	var problems []Problem
	report := func(format string, args ...any) {
		problems = append(problems, Problem{Path: doc.Path, Msg: fmt.Sprintf(format, args...)})
	}

	size := Measure(doc.Body)
	reason, opted := lengthReviewReason(doc.Body)
	backlog, backlogged := b.backlog[doc.Path]
	overProse := size.Prose > b.prose
	overPage := size.Page > b.page

	if opted {
		switch {
		case len(reason) < minLengthReason:
			report("<!-- %s … --> gives no reason worth the name (%q). Say why this page is legitimately long, in a sentence — the marker exists to make the decision visible, not to silence the check",
				LengthReviewMarker, reason)
		case !overProse && !overPage:
			report("carries <!-- %s … --> but is inside the budget (%d/%d prose, %d/%d page). Delete the marker",
				LengthReviewMarker, size.Prose, b.prose, size.Page, b.page)
		}
		return problems
	}

	if backlogged {
		switch {
		case !overProse && !overPage:
			if !retire {
				break
			}
			report("is inside the budget now (%d/%d prose, %d/%d page). Delete its %s entry in internal/docslint/length.go so the rule stays enforced",
				size.Prose, b.prose, size.Page, b.page, b.backlogName)
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
			size.Prose, b.prose, LengthReviewMarker)
	}
	if overPage {
		report("%d characters, over the %d page budget. Split it by concern, or move the reference tables to their own page. If it is legitimately long, say why in <!-- %s … --> (see docs/dev/content-charter.md)",
			size.Page, b.page, LengthReviewMarker)
	}
	return problems
}

// checkBacklogPathsExist rejects a backlog entry naming a page the run no
// longer sees — a rename or a deletion that left its ceiling behind, where it
// would sit forever waiting for a file that never comes back.
func checkBacklogPathsExist(seen map[string]bool, b budget) []Problem {
	var stale []string
	for path := range b.backlog {
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
		Msg: fmt.Sprintf("%s names %s that no longer exist; delete the entries: %s",
			b.backlogName, plural(len(stale), "page", "pages"), strings.Join(stale, ", ")),
	}}
}
