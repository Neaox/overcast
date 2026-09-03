package docslint

import (
	"regexp"
	"strings"
)

// A page does not open with a bare back-link.
//
// "Back to [ECS](../ecs.md)." was the house habit on sub-pages, and it spends
// the one line a reader always reads on the one thing they already know — which
// service they clicked. The orientation form carries the same link inside the
// sentence that says what the page holds:
//
//	The full divergence list behind [RDS](../rds.md).
//
// Same link, same one line, and a reader arriving from search learns whether
// this is the page they wanted before they scroll.
//
// The rule is the shape only: a first line that is nothing but a link, with an
// optional "Back to" in front of it. Anything with a clause of its own passes,
// including one that keeps the back-link at the end.
var bareBackLinkRE = regexp.MustCompile(`^(?:Back to(?:\s+the)?\s+)?\[[^\]]+\]\([^)\s]+\)\s*\.?$`)

// checkOpeningLine reads the first body line under the H1 — skipping blank
// lines, HTML comments and blockquotes, which are signposts rather than the
// opening sentence.
func checkOpeningLine(doc Doc) []Problem {
	lines := strings.Split(doc.Body, "\n")
	visible := eachOutsideFences(lines)
	started := false
	for i, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if !started {
			if visible[i] != "" && strings.HasPrefix(trimmed, "# ") {
				started = true
			}
			continue
		}
		switch {
		case trimmed == "", trimmed == "---", strings.HasPrefix(trimmed, ">"), strings.HasPrefix(trimmed, "<!--"):
			continue
		case visible[i] == "":
			// A fenced block is the first thing on the page. Nothing to say.
			return nil
		case !bareBackLinkRE.MatchString(trimmed):
			return nil
		}
		return []Problem{{
			Path: doc.Path,
			Line: doc.BodyLineOffset + i + 1,
			Msg:  `opens with a bare back-link. Spend the first line saying what the page holds and carry the link inside it — "The full divergence list behind [RDS](../rds.md)."`,
		}}
	}
	return nil
}
