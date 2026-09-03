package docslint

import (
	"fmt"
	"path"
	"strings"
)

// Every published page ends with "## Related", and the links in it run from
// nearest to furthest.
//
// The requirement is navigational rather than decorative. The docs are read
// inside the console and on the website, where a reader arrives from search on
// whichever page matched: there is no "up", and the link footer is the only
// route from the page they landed on to the page they wanted. A page without
// one is a dead end for everybody who did not come from the hub.
//
// Two of the template's rules for that footer can be decided by looking at the
// file, and only those two are enforced:
//
//   - a page's own sub-pages come before anything else, and
//   - links off the site come last.
//
// The rest of the order docs/dev/service-doc-template.md gives — siblings, then
// the service index, then the guides — stays editorial. Which of two guides is
// "nearer" is a judgment, and per this package's charter a rule that guesses at
// one produces its first annoying false positive on the day it lands.
//
// A directory index is exempt: docs/README.md and docs/services/README.md are
// link footers from top to bottom, and a second list of links at the end of one
// would be a rule satisfying itself.
const relatedHeading = "Related"

// relatedTier ranks a link by how far from the page it goes. The zero value is
// the nearest tier, so a Related section with no links at all is trivially in
// order.
type relatedTier int

const (
	tierOwnSubPage relatedTier = iota
	tierElsewhereInDocs
	tierOffSite
)

func (t relatedTier) String() string {
	switch t {
	case tierOwnSubPage:
		return "one of this page's own sub-pages"
	case tierOffSite:
		return "a link off the site"
	default:
		return "another page under docs/"
	}
}

// linkTier resolves one Markdown link target against the page that carries it.
func linkTier(docPath, target string) relatedTier {
	switch {
	case strings.Contains(target, "://"), strings.HasPrefix(target, "mailto:"):
		return tierOffSite
	case strings.HasPrefix(target, "#"), strings.HasPrefix(target, "/"):
		// An in-page anchor, or a site-absolute path the website's router
		// resolves. Neither is a sub-page of this one.
		return tierElsewhereInDocs
	}
	file, _, _ := strings.Cut(target, "#")
	if file == "" {
		return tierElsewhereInDocs
	}
	resolved := path.Clean(path.Join(path.Dir(docPath), file))
	if own := strings.TrimSuffix(docPath, ".md") + "/"; strings.HasPrefix(resolved, own) {
		return tierOwnSubPage
	}
	return tierElsewhereInDocs
}

// checkRelated applies the link-footer rules to one published page.
func checkRelated(doc Doc) []Problem {
	if path.Base(doc.Path) == "README.md" {
		return nil
	}

	lines := strings.Split(doc.Body, "\n")
	related, lastH2 := -1, ""
	for _, h := range parseHeadings(lines) {
		if h.depth != 2 {
			continue
		}
		if h.text == relatedHeading && related < 0 {
			related = h.line
		}
		lastH2 = h.text
	}

	if related < 0 {
		return []Problem{{
			Path: doc.Path,
			Msg: fmt.Sprintf("missing required section %q. Every published page ends with one — a reader who arrived here from search needs somewhere to go next; see docs/dev/service-doc-template.md",
				"## "+relatedHeading),
		}}
	}
	if lastH2 != relatedHeading {
		return []Problem{{
			Path: doc.Path,
			Line: doc.BodyLineOffset + related + 1,
			Msg:  fmt.Sprintf("%q must be the last section on the page", "## "+relatedHeading),
		}}
	}

	var problems []Problem
	visible := eachOutsideFences(lines)
	furthest := tierOwnSubPage
	for i := related + 1; i < len(lines); i++ {
		if visible[i] == "" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(visible[i]), "## ") {
			break
		}
		for _, m := range markdownLinkRE.FindAllStringSubmatch(visible[i], -1) {
			tier := linkTier(doc.Path, m[1])
			if tier < furthest {
				problems = append(problems, Problem{
					Path: doc.Path,
					Line: doc.BodyLineOffset + i + 1,
					Msg: fmt.Sprintf("%q is %s, listed after %s. In %q a page's own sub-pages come first and links off the site come last; see docs/dev/service-doc-template.md",
						m[1], tier, furthest, "## "+relatedHeading),
				})
				continue
			}
			furthest = tier
		}
	}
	return problems
}
