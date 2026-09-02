//go:build ignore

// Script: docs-index
// Lints the published docs/ tree and, on request, fills in missing frontmatter.
//
// Usage:
//
//	go run ./scripts/docs-index.go --check               # the CI gate (make docs-check)
//	go run ./scripts/docs-index.go --write-frontmatter   # add frontmatter where it is missing
//	go run ./scripts/docs-index.go --refresh-frontmatter # re-infer every doc's frontmatter
//
// This script used to generate two artifacts and commit them —
// web/src/docs-nav.gen.ts and internal/docssearch/index.gen.jsonl. Both were
// sorted manifests with one entry per page, so every docs pull request rewrote
// part of them and concurrent docs branches conflicted on files nobody had
// written by hand. They are gone: internal/docsindex derives the same data from
// the docs the binary already embeds, at runtime, and internal/bff serves it.
// See that package for the full reasoning.
//
// What is left here is the part that cannot be derived — the checks. Building
// the index is still how they are made: a doc that cannot be indexed, cites an
// excluded tree, or links to a heading that does not exist fails --check.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/overcast-sh/overcast/internal/docsindex"
	"github.com/overcast-sh/overcast/internal/docslint"
)

const docsRoot = "docs"

func main() {
	writeFrontmatter := flag.Bool("write-frontmatter", false, "add frontmatter to docs that are missing it")
	refreshFrontmatter := flag.Bool("refresh-frontmatter", false, "replace docs frontmatter with inferred metadata")
	check := flag.Bool("check", false, "verify docs frontmatter, anchors and structure")
	flag.Parse()

	if !*writeFrontmatter && !*refreshFrontmatter {
		*check = true
	}

	var write docsindex.Writer
	if *writeFrontmatter || *refreshFrontmatter {
		write = func(fsPath string, content []byte) error {
			return os.WriteFile(filepath.Join(docsRoot, filepath.FromSlash(fsPath)), content, 0o644)
		}
	}

	docs, frontmatterChanges, err := docsindex.CollectWriting(os.DirFS(docsRoot), write, *refreshFrontmatter)
	if err != nil {
		fatal(err)
	}
	// Fail rather than serve a degenerate index. A doc that parses to nothing
	// searchable, or that lost its title, is a bug in the indexer or in the
	// doc — either way it must not reach the console, where it would silently
	// drop a page out of docs search.
	if err := docsindex.Validate(docs); err != nil {
		fatal(err)
	}

	if !*check {
		return
	}
	if frontmatterChanges > 0 {
		fatal(fmt.Errorf("%d docs are missing frontmatter; run go run ./scripts/docs-index.go --write-frontmatter", frontmatterChanges))
	}
	for _, checkFn := range []func([]docsindex.Doc) error{checkAnchors, checkHeadingIDs, checkExcludedRefs, checkServiceDocStructure} {
		if err := checkFn(docs); err != nil {
			fatal(err)
		}
	}
	if err := checkGeneratedDocsAreTracked(); err != nil {
		fatal(err)
	}
}

func checkAnchors(docs []docsindex.Doc) error {
	byPath := make(map[string][]docsindex.Heading, len(docs))
	for _, doc := range docs {
		byPath[doc.Entry.Path] = doc.Entry.Headings
	}

	problems := []string{}
	for _, doc := range docs {
		for _, link := range doc.Links {
			target, fragment, hasFragment := strings.Cut(link.Dest, "#")
			if !hasFragment || fragment == "" || strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			targetPath := doc.Entry.Path
			if target != "" {
				targetPath = path.Join(path.Dir(doc.Entry.Path), target)
			}
			headings, published := byPath[targetPath]
			if !published {
				continue
			}
			where := fmt.Sprintf("%s:%d: [#%s]", doc.Entry.Path, link.Line, fragment)
			if target != "" {
				where += " -> " + targetPath
			}
			found := false
			for _, h := range headings {
				if h.ID != fragment {
					continue
				}
				found = true
				if h.Depth < 2 || h.Depth > 3 {
					problems = append(problems, fmt.Sprintf("%s: heading %q is h%d; the docs browser only gives h2 and h3 an id", where, h.Text, h.Depth))
				}
				break
			}
			if !found {
				problems = append(problems, fmt.Sprintf("%s: no such heading%s", where, suggestHeading(headings, fragment)))
			}
		}
	}
	// A second frontmatter block is invisible to every other check here: only
	// the first is parsed, so a stacked stub renders as literal text and silently
	// replaces the authored metadata. Two pages shipped that way after a merge.
	for _, doc := range docs {
		if docsindex.HasStrayFrontmatter(doc.RawBody) {
			problems = append(problems, fmt.Sprintf("%s: a second `---` frontmatter block sits above the "+
				"first heading. Only the first is parsed, so the rest render as a literal rule and raw "+
				"`title:`/`tags:` text and the page keeps the wrong metadata. Delete the duplicates — "+
				"they are usually a merge artefact", doc.Entry.Path))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("broken in-page anchor links:\n\t%s", strings.Join(problems, "\n\t"))
	}
	return nil
}

// excludedPathFragments are literal path fragments that can never
// legitimately appear in a published doc's body: docs/dev/** and
// docs/plans/** are excluded from the publish step (see
// isPublishedDocPath), so a reference to either — however it is spelled —
// is a citation a reader can never resolve. See the content charter,
// docs/dev/content-charter.md § rule 3.
var excludedPathFragments = []string{"docs/dev/", "docs/plans/"}

// checkExcludedRefs rejects a published doc that cites the excluded
// docs/dev/** or docs/plans/** trees, in either of the two ways that shows
// up: a literal path fragment written into the prose (e.g. a bare
// "(docs/plans/foo.md)" aside, never a link at all), or a Markdown link
// whose destination resolves into either tree. The second case reuses the
// same relative-path resolution checkAnchors does, so a link that never
// spells out "docs/" at all — "./dev/foo.md", written from inside docs/ — is
// still caught.
func checkExcludedRefs(docs []docsindex.Doc) error {
	problems := []string{}
	for _, doc := range docs {
		for i, line := range strings.Split(doc.RawBody, "\n") {
			for _, frag := range excludedPathFragments {
				if strings.Contains(line, frag) {
					problems = append(problems, fmt.Sprintf("%s:%d: cites excluded path %q, which a reader can never resolve", doc.Entry.Path, doc.BodyLineOffset+i+1, frag))
				}
			}
		}
		for _, link := range doc.Links {
			target, _, _ := strings.Cut(link.Dest, "#")
			if target == "" || strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			resolved := path.Join(path.Dir(doc.Entry.Path), target)
			if strings.HasPrefix(resolved, "docs/dev/") || strings.HasPrefix(resolved, "docs/plans/") {
				problems = append(problems, fmt.Sprintf("%s:%d: links to %s, which scripts/docs-index.go excludes from the published site", doc.Entry.Path, link.Line, resolved))
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("published docs cite excluded docs/dev/ or docs/plans/ content:\n\t%s", strings.Join(problems, "\n\t"))
	}
	return nil
}

// checkServiceDocStructure holds every page under docs/services/ to the shape
// docs/dev/service-doc-template.md describes: required sections, template
// section order, the generated block last, and the fixed set of sub-page names.
//
// The rules themselves live in internal/docslint, which has tests. This file
// cannot: it is `//go:build ignore`, which is also why the search scoring moved
// out to internal/docssearch.
func checkServiceDocStructure(docs []docsindex.Doc) error {
	entries := make([]docslint.Doc, 0, len(docs))
	for _, doc := range docs {
		entries = append(entries, docslint.Doc{
			Path:           doc.Entry.Path,
			Body:           doc.RawBody,
			BodyLineOffset: doc.BodyLineOffset,
		})
	}
	problems := docslint.Check(entries)
	if len(problems) == 0 {
		return nil
	}
	lines := make([]string, 0, len(problems))
	for _, p := range problems {
		lines = append(lines, p.String())
	}
	return fmt.Errorf("service docs do not follow the service page template:\n\t%s", strings.Join(lines, "\n\t"))
}

// checkGeneratedDocsAreTracked rejects an untracked file under docs/services/.
//
// `make docs-check` proves the committed docs match what the generators would
// write by regenerating and diffing — and a diff cannot see a file git has
// never been told about. cmd/capgen writes one operations sub-page per
// service, so adding a service and forgetting to `git add` its sub-page
// produces a landing page whose Operations link 404s, with every check green.
// The same gap covers a hand-written sub-page nobody staged.
//
// Absent git — a source tarball, a vendored copy — this is not a check that
// can be run, and not running it is the right answer rather than an error
// about the environment.
func checkGeneratedDocsAreTracked() error {
	out, err := exec.Command("git", "ls-files", "--others", "--exclude-standard", "--", "docs/services").Output()
	if err != nil {
		return nil
	}
	untracked := strings.Fields(strings.TrimSpace(string(out)))
	if len(untracked) == 0 {
		return nil
	}
	return fmt.Errorf("untracked files under %s/services; run make docs, then commit them:\n\t%s", docsRoot, strings.Join(untracked, "\n\t"))
}

// checkHeadingIDs rejects a heading whose id ends in a hyphen — one that ends
// in a space and a symbol, "## Ports —". GitHub keeps that hyphen and the
// public site's renderer (Astro) trims it, so no one link could satisfy both;
// the fix is to reword the heading.
func checkHeadingIDs(docs []docsindex.Doc) error {
	problems := []string{}
	for _, doc := range docs {
		for _, h := range doc.Entry.Headings {
			if strings.HasSuffix(h.ID, "-") {
				problems = append(problems, fmt.Sprintf("%s: heading %q gets id #%s; GitHub keeps the trailing hyphen and the public site drops it, so reword the heading", doc.Entry.Path, h.Text, h.ID))
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("heading ids GitHub and the public site disagree on:\n\t%s", strings.Join(problems, "\n\t"))
	}
	return nil
}

// suggestHeading names the heading a broken anchor most likely meant: one
// whose id differs only in its hyphens — the shape of a link that folded a
// punctuation run into one hyphen by hand, or that predates the ids matching
// GitHub's.
func suggestHeading(headings []docsindex.Heading, fragment string) string {
	want := strings.ReplaceAll(fragment, "-", "")
	for _, h := range headings {
		if strings.ReplaceAll(h.ID, "-", "") == want {
			return "; did you mean #" + h.ID + " ?"
		}
	}
	return ""
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "docs-index:", err)
	os.Exit(1)
}
