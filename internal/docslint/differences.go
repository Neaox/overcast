package docslint

import (
	"fmt"
	"regexp"
	"strings"
)

// A service landing page states its divergences as `| Area | On AWS | Overcast |`.
//
// docs/dev/service-doc-template.md gives the section that shape, AWS before
// Overcast, because the reader it is written for — the platform engineer
// deciding what to trust — is comparing two behaviours and can only do that
// when both are on the row. A two-column `| Area | Overcast |` table does not
// remove the AWS half; it buries it, either inside the Overcast cell ("Real
// CloudWatch keeps 15 months at declining resolution") or in the Area cell as a
// negation ("No query engine"), leaving the reader to reconstruct what AWS does
// from a sentence about what Overcast does not.
//
// Half the corpus drifted this way one page at a time, which is why the shape
// is checked rather than reviewed: three separate audits found it and none of
// them fixed it, because it is one consistent change across every page or none.
//
// The rule is the header row only. What the rows say is editorial, and per this
// package's charter a rule that judges a cell produces its first annoying false
// positive on the day it lands.
const differencesHeading = "Differences from AWS"

// differencesColumns is the header the template gives the section, in order.
var differencesColumns = []string{"Area", "On AWS", "Overcast"}

// DifferencesTwoColumnMarker is the deliberate opt-out, for the page whose rows
// genuinely have no AWS half to state:
//
//	<!-- docs-differences-two-column: every row is a resource type AWS has and
//	     Overcast does not model at all, so "On AWS" would read "Full API" nine
//	     times -->
//
// An HTML comment naming the reason, for the same reasons LengthReviewMarker is
// one: it renders as nothing, it survives a frontmatter refresh, and it sits on
// the page it is excusing rather than in a list beside the linter. And like
// that marker it is self-deleting — a page carrying one whose table is already
// three columns fails, so the exception cannot outlive the table it was written
// for.
const DifferencesTwoColumnMarker = "docs-differences-two-column:"

// minDifferencesReason is how much reason the marker has to carry. Same number
// and same intent as minLengthReason: this rejects the empty gesture, it does
// not grade the writing.
const minDifferencesReason = 30

var differencesMarkerRE = regexp.MustCompile(`(?is)<!--\s*` + regexp.QuoteMeta(DifferencesTwoColumnMarker) + `\s*(.*?)-->`)

// differencesHeaderRow returns the header cells of the first table under
// "## Differences from AWS", and the line it sits on.
//
// The first table only. A section may carry a second one below its prose — the
// header that matters is the one the reader meets under the heading.
func differencesHeaderRow(lines []string) (cells []string, line int, found bool) {
	visible := eachOutsideFences(lines)
	start := -1
	for i, l := range visible {
		if strings.TrimSpace(l) == "## "+differencesHeading {
			start = i
			break
		}
	}
	if start < 0 {
		return nil, 0, false
	}
	for i := start + 1; i < len(visible); i++ {
		trimmed := strings.TrimSpace(visible[i])
		if strings.HasPrefix(trimmed, "## ") {
			return nil, 0, false
		}
		if !strings.HasPrefix(trimmed, "|") {
			continue
		}
		if i+1 >= len(visible) || !isTableDelimiter(visible[i+1]) {
			continue
		}
		return tableCells(trimmed), i, true
	}
	return nil, 0, false
}

// tableCells splits one GFM row into its trimmed cells.
func tableCells(row string) []string {
	parts := strings.Split(strings.Trim(strings.TrimSpace(row), "|"), "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(strings.Trim(strings.TrimSpace(p), "*`")))
	}
	return out
}

// differencesReason returns the reason given in the page's opt-out marker, and
// whether the page carries one at all.
func differencesReason(body string) (string, bool) {
	m := differencesMarkerRE.FindStringSubmatch(body)
	if m == nil {
		return "", false
	}
	return strings.Join(strings.Fields(m[1]), " "), true
}

// checkDifferencesTable applies the header rule to one landing page.
func checkDifferencesTable(doc Doc) []Problem {
	lines := strings.Split(doc.Body, "\n")
	cells, line, found := differencesHeaderRow(lines)
	reason, opted := differencesReason(doc.Body)
	report := func(at int, format string, args ...any) []Problem {
		return []Problem{{Path: doc.Path, Line: doc.BodyLineOffset + at + 1, Msg: fmt.Sprintf(format, args...)}}
	}

	if opted && len(reason) < minDifferencesReason {
		return report(line, "<!-- %s … --> gives no reason worth the name (%q). Say why these rows have no AWS half to state, in a sentence — the marker exists to make the decision visible, not to silence the check",
			DifferencesTwoColumnMarker, reason)
	}
	if !found {
		if opted {
			return report(0, "carries <!-- %s … --> but has no %q table to excuse. Delete the marker",
				DifferencesTwoColumnMarker, "## "+differencesHeading)
		}
		return nil
	}

	if headerMatches(cells) {
		if opted {
			return report(line, "carries <!-- %s … --> but the table is already `| %s |`. Delete the marker",
				DifferencesTwoColumnMarker, strings.Join(differencesColumns, " | "))
		}
		return nil
	}
	if opted {
		return nil
	}
	return report(line, "%q is `| %s |`; the template's shape is `| %s |`, AWS before Overcast, so a reader comparing the two behaviours has both on the row. Move the AWS half out of the Overcast cell. If these rows genuinely have no AWS half, say so in <!-- %s … --> (see docs/dev/service-doc-template.md)",
		"## "+differencesHeading, strings.Join(cells, " | "), strings.Join(differencesColumns, " | "), DifferencesTwoColumnMarker)
}

func headerMatches(cells []string) bool {
	if len(cells) != len(differencesColumns) {
		return false
	}
	for i, want := range differencesColumns {
		if !strings.EqualFold(cells[i], want) {
			return false
		}
	}
	return true
}
