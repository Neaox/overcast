package docslint

import (
	"fmt"
	"regexp"
	"strings"
)

// Two callouts with nothing between them are one callout that failed.
//
// docs/dev/service-doc-template.md gives the alert vocabulary a budget — "one
// or two per page; a page of callouts is a page with no emphasis at all" — and
// the shape that breaks the budget first is the stacked pair:
//
//	> [!WARNING]
//	> The container keeps the volume.
//
//	> [!CAUTION]
//	> Deleting it loses the data.
//
// A reader skimming for the thing that will cost them an afternoon meets a wall
// of two boxes and reads neither. Whichever of the two is the real warning is
// the one that belongs in the callout; the other is a sentence.
//
// The rule is the adjacency only, not the count. A page may carry two callouts,
// and the template says so — they have to be separated by the prose that says
// why the second one is a different concern. Anything visible between them
// satisfies it: a sentence, a heading, a table, a command.
var calloutOpenRE = regexp.MustCompile(`^>\s*\[!(?:NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]`)

// calloutBlock is one alert, from its `> [!KIND]` line to the last line of the
// blockquote that carries it.
type calloutBlock struct {
	kind       string
	start, end int // end is exclusive
}

// findCallouts returns the page's alert blocks in order, ignoring fenced code —
// a callout inside a fence is a sample of Markdown, not emphasis on this page.
func findCallouts(visible []string) []calloutBlock {
	var out []calloutBlock
	for i := 0; i < len(visible); {
		line := strings.TrimSpace(visible[i])
		m := calloutOpenRE.FindStringSubmatch(line)
		if m == nil {
			i++
			continue
		}
		start := i
		for i < len(visible) && strings.HasPrefix(strings.TrimSpace(visible[i]), ">") {
			i++
		}
		out = append(out, calloutBlock{kind: calloutKind(line), start: start, end: i})
	}
	return out
}

// calloutKind pulls "WARNING" out of "> [!WARNING]", for a failure message that
// names the two blocks the writer has to look at.
func calloutKind(line string) string {
	start := strings.Index(line, "[!")
	end := strings.Index(line, "]")
	if start < 0 || end < start+2 {
		return "alert"
	}
	return line[start+2 : end]
}

// checkCallouts reports each pair of callouts with no visible line between them.
func checkCallouts(doc Doc) []Problem {
	lines := strings.Split(doc.Body, "\n")
	blocks := findCallouts(eachOutsideFences(lines))

	var problems []Problem
	for i := 1; i < len(blocks); i++ {
		prev, cur := blocks[i-1], blocks[i]
		if !nothingBetween(lines, prev.end, cur.start) {
			continue
		}
		problems = append(problems, Problem{
			Path: doc.Path,
			Line: doc.BodyLineOffset + cur.start + 1,
			Msg: fmt.Sprintf("[!%s] follows [!%s] with nothing between them. Two stacked callouts are read as one and skimmed past: keep the one that costs the reader an afternoon and write the other as a sentence, or put the prose that separates the two concerns between them (see docs/dev/service-doc-template.md)",
				cur.kind, prev.kind),
		})
	}
	return problems
}

// nothingBetween reports whether every line in [from, to) is blank.
//
// It reads the raw lines rather than the fence-blanked ones on purpose: a code
// block between two callouts is exactly the material that separates them, and
// eachOutsideFences blanks the fence markers along with their content, so
// asking the blanked slice would call a command "nothing".
func nothingBetween(lines []string, from, to int) bool {
	for i := from; i < to && i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" {
			return false
		}
	}
	return true
}
