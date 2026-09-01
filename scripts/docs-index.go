//go:build ignore

// Script: docs-index
// Adds missing docs frontmatter and generates a compact TypeScript docs index.
//
// Usage:
//
//	go run ./scripts/docs-index.go --write-frontmatter --write-nav --write-search-index
//	go run ./scripts/docs-index.go --check
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/overcast-sh/overcast/internal/docslint"
	"github.com/overcast-sh/overcast/internal/docssearch"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

const (
	docsRoot = "docs"
	// navOutput and searchOutput are generated but committed, like
	// internal/capabilities/all.gen.go: `git clone && go build ./...` has to
	// work with nothing but the Go toolchain, and importers of these files
	// cannot compile without them. --check keeps them honest. Naming/location
	// follows the TanStack Router convention used elsewhere in this repo
	// (web/src/routeTree.gen.ts, internal/capabilities/all.gen.go): a flat
	// "*.gen.*" file next to its consumers, no "generated/" subfolder.
	//
	// Both artifacts are document-major: one TypeScript object or one JSONL
	// line per doc, in path order. That is what keeps them out of the way in
	// git. The predecessor of searchOutput was a Go source file holding a
	// term-major inverted index — postings keyed by term, referring to
	// documents by position — so adding a doc renumbered every document after
	// it and a one-line docs edit rewrote 100 to 1,500 lines. Two PRs that
	// touched any docs at all therefore conflicted, and the conflict could
	// only be resolved by regenerating. Document-major output cannot do that:
	// editing a doc rewrites that doc's line and nothing else. The inverted
	// index still exists, but it is built at load time in internal/docssearch,
	// where it costs about a millisecond and no merge conflicts at all.
	navOutput      = "web/src/docs-nav.gen.ts"
	searchOutput   = "internal/docssearch/index.gen.jsonl"
	frontmatterSep = "---"

	// maxDescriptionLen caps a published doc's frontmatter description, as a
	// proxy for the content charter's intro-budget rule (see
	// docs/dev/content-charter.md): a long description is a reliable tell
	// that the opening paragraph is doing too much before the reader hits a
	// command, a table, or a linked next step.
	maxDescriptionLen = 220
)

var (
	headingRE = regexp.MustCompile(`(?m)^(#{1,6})\s+(.+?)\s*$`)
	spaceRE   = regexp.MustCompile(`\s+`)
	md        = goldmark.New(goldmark.WithExtensions(extension.GFM))
)

type docMeta struct {
	Title       string
	Description string
	Section     string
	Tags        []string
}

type heading struct {
	Depth int    `json:"depth"`
	Text  string `json:"text"`
	ID    string `json:"id"`
}

// docEntry is the navigation shape: what the console needs to render the docs
// sidebar and the "On this page" table of contents. It carries no search
// corpus — that lives in searchEntry, which the SPA never downloads.
type docEntry struct {
	Path        string    `json:"path"`
	Href        string    `json:"href"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Section     string    `json:"section"`
	Tags        []string  `json:"tags"`
	Headings    []heading `json:"headings"`
}

// searchEntry is one line of searchOutput: a document's result metadata plus
// its own term scores, as "term:score" pairs in one space-separated field.
// Terms are sorted by docssearch.FormatTerms, and a term can itself contain
// ':' (the tokenizer keeps it), so a reader splits each pair on its *last*
// colon.
type searchEntry struct {
	Path        string   `json:"path"`
	Href        string   `json:"href"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Section     string   `json:"section"`
	Tags        []string `json:"tags"`
	Terms       string   `json:"terms"`
}

type weightedDoc struct {
	Entry docEntry
	// BodyText is the document's prose; CodeSpans are its inline `code`
	// fragments, kept apart because they score differently — see
	// docssearch.ScoreDocument.
	BodyText  string
	CodeSpans []string
	// Links are the document's Markdown link destinations, for the anchor
	// check. They are not indexed and never reach an artifact.
	Links []docLink
	// RawBody is the document's Markdown source after frontmatter is
	// stripped, with original line breaks intact — unlike BodyText, which is
	// the parsed prose joined by "\n" per AST node and cannot be scanned line
	// by line. BodyLineOffset is how many lines of the raw file sit above it,
	// so a match in RawBody maps back to a real file:line. Both exist only
	// for checkExcludedRefs; nothing else needs raw source.
	RawBody        string
	BodyLineOffset int
}

// docLink is one Markdown link destination and where it was written, so a
// broken anchor can be reported as file:line.
type docLink struct {
	Dest string
	Line int
}

func main() {
	writeFrontmatter := flag.Bool("write-frontmatter", false, "add frontmatter to docs that are missing it")
	refreshFrontmatter := flag.Bool("refresh-frontmatter", false, "replace docs frontmatter with inferred metadata")
	writeNav := flag.Bool("write-nav", false, "write the generated TypeScript docs navigation index")
	writeSearchIndex := flag.Bool("write-search-index", false, "write the generated docs search index")
	check := flag.Bool("check", false, "verify frontmatter and generated indexes are up to date")
	flag.Parse()

	if !*writeFrontmatter && !*writeNav && !*writeSearchIndex && !*check {
		*writeNav = true
		*writeSearchIndex = true
	}

	docs, frontmatterChanges, err := collectDocs(*writeFrontmatter, *refreshFrontmatter)
	if err != nil {
		fatal(err)
	}
	// Fail rather than emit a degenerate index. A doc that parses to nothing
	// searchable, or that lost its title, is a bug in this generator or in the
	// doc — either way it must not reach an artifact, where it would silently
	// drop a page out of the console's docs search and still regenerate
	// byte-identically on the next --check.
	if err := validateDocs(docs); err != nil {
		fatal(err)
	}

	// --check validates doc content (frontmatter completeness) and that both
	// committed indexes match what docs/ would produce right now. It compares
	// bytes rather than shelling out to git, so it catches a hand-edited
	// index as well as a stale one, and reports the fix by name.
	if *check {
		if frontmatterChanges > 0 {
			fatal(fmt.Errorf("%d docs are missing frontmatter; run go run ./scripts/docs-index.go --write-frontmatter --write-nav", frontmatterChanges))
		}
		// Anchors and excluded-path citations are checked here rather than in
		// validateDocs because either is a doc bug, not an index bug: it must
		// not stop --write-nav from regenerating the index that proves the fix.
		if err := checkAnchors(docs); err != nil {
			fatal(err)
		}
		if err := checkExcludedRefs(docs); err != nil {
			fatal(err)
		}
		if err := checkServiceDocStructure(docs); err != nil {
			fatal(err)
		}
		if err := checkGeneratedDocsAreTracked(); err != nil {
			fatal(err)
		}
		entries := make([]docEntry, 0, len(docs))
		for _, doc := range docs {
			entries = append(entries, doc.Entry)
		}
		wantNav, err := renderNav(entries)
		if err != nil {
			fatal(err)
		}
		wantSearch, err := renderSearchIndex(docs)
		if err != nil {
			fatal(err)
		}
		stale := []string{}
		for _, f := range []struct {
			path string
			want []byte
		}{{navOutput, wantNav}, {searchOutput, wantSearch}} {
			got, err := os.ReadFile(f.path)
			if err != nil || !bytes.Equal(got, f.want) {
				stale = append(stale, f.path)
			}
		}
		if len(stale) > 0 {
			fatal(fmt.Errorf("%s out of date; run: make docs-index", strings.Join(stale, ", ")))
		}
		return
	}

	if *writeNav {
		entries := make([]docEntry, 0, len(docs))
		for _, doc := range docs {
			entries = append(entries, doc.Entry)
		}
		generated, err := renderNav(entries)
		if err != nil {
			fatal(err)
		}
		if err := writeGenerated(navOutput, generated); err != nil {
			fatal(err)
		}
	}
	if *writeSearchIndex {
		generated, err := renderSearchIndex(docs)
		if err != nil {
			fatal(err)
		}
		if err := writeGenerated(searchOutput, generated); err != nil {
			fatal(err)
		}
	}
}

func writeGenerated(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}

// validateDocs rejects a document set that would produce a broken index.
func validateDocs(docs []weightedDoc) error {
	if len(docs) == 0 {
		return fmt.Errorf("no published docs found under %s/", docsRoot)
	}
	problems := []string{}
	for _, doc := range docs {
		e := doc.Entry
		switch {
		case e.Title == "":
			problems = append(problems, e.Path+": no title")
		case e.Section == "":
			problems = append(problems, e.Path+": no section")
		case e.Href == "" || strings.HasPrefix(e.Href, "/"):
			problems = append(problems, e.Path+": bad href "+e.Href)
		case len(docssearch.ScoreDocument(searchFields(doc))) == 0:
			problems = append(problems, e.Path+": no searchable terms")
		case e.Description == "":
			problems = append(problems, e.Path+": no description")
		case len(e.Description) > maxDescriptionLen:
			problems = append(problems, fmt.Sprintf("%s: description is %d chars (max %d) — a long one is a reliable tell the intro is doing too much before the first actionable thing; trim it", e.Path, len(e.Description), maxDescriptionLen))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("docs cannot be indexed:\n\t%s", strings.Join(problems, "\n\t"))
	}
	return nil
}

func collectDocs(writeFrontmatter, refreshFrontmatter bool) ([]weightedDoc, int, error) {
	paths := []string{}
	if err := filepath.WalkDir(docsRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (filepath.ToSlash(path) == "docs/plans" || filepath.ToSlash(path) == "docs/dev") {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) == ".md" && isPublishedDocPath(filepath.ToSlash(path)) {
			paths = append(paths, filepath.ToSlash(path))
		}
		return nil
	}); err != nil {
		return nil, 0, err
	}
	sort.Strings(paths)

	entries := make([]weightedDoc, 0, len(paths))
	missingFrontmatter := 0
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, 0, err
		}

		meta, body, ok := splitFrontmatter(string(raw))
		if refreshFrontmatter {
			if !ok {
				body = string(raw)
			}
			meta = inferMeta(path, body)
			updated := renderFrontmatter(meta) + strings.TrimLeft(body, "\n")
			if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
				return nil, 0, err
			}
			body = strings.TrimLeft(body, "\n")
		} else if !ok {
			missingFrontmatter++
			meta = inferMeta(path, string(raw))
			body = string(raw)
			if writeFrontmatter {
				updated := renderFrontmatter(meta) + strings.TrimLeft(body, "\n")
				if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
					return nil, 0, err
				}
				body = strings.TrimLeft(body, "\n")
			}
		} else {
			if meta.Title == "" {
				meta.Title = inferTitle(body, path)
			}
			if meta.Section == "" {
				meta.Section = inferSection(path)
			}
			if len(meta.Tags) == 0 {
				meta.Tags = inferTags(path, meta.Title)
			}
		}

		bodyForIndex := body
		if !writeFrontmatter && !ok {
			bodyForIndex = string(raw)
		}
		entries = append(entries, buildEntry(path, meta, bodyForIndex, frontmatterLines(string(raw), bodyForIndex)))
	}

	return entries, missingFrontmatter, nil
}

func isPublishedDocPath(path string) bool {
	return !strings.HasPrefix(path, "docs/plans/") && !strings.HasPrefix(path, "docs/dev/")
}

func splitFrontmatter(raw string) (docMeta, string, bool) {
	if !strings.HasPrefix(raw, frontmatterSep+"\n") {
		return docMeta{}, raw, false
	}
	rest := raw[len(frontmatterSep)+1:]
	idx := strings.Index(rest, "\n"+frontmatterSep+"\n")
	if idx < 0 {
		return docMeta{}, raw, false
	}
	fm := rest[:idx]
	body := rest[idx+len(frontmatterSep)+2:]
	return parseFrontmatter(fm), body, true
}

func parseFrontmatter(fm string) docMeta {
	meta := docMeta{}
	lines := strings.Split(fm, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "title":
			meta.Title = unquoteYAML(value)
		case "description":
			meta.Description = unquoteYAML(value)
		case "section":
			meta.Section = unquoteYAML(value)
		case "tags":
			if value == "" {
				for i+1 < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i+1]), "-") {
					i++
					meta.Tags = append(meta.Tags, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i]), "-")))
				}
			} else {
				meta.Tags = parseInlineList(value)
			}
		}
	}
	meta.Tags = uniqueSortedTags(meta.Tags)
	return meta
}

func inferMeta(path, raw string) docMeta {
	title := inferTitle(raw, path)
	return docMeta{
		Title:       title,
		Description: inferDescription(raw, title),
		Section:     inferSection(path),
		Tags:        inferTags(path, title),
	}
}

func inferTitle(raw, path string) string {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return cleanHeadingText(strings.TrimSpace(strings.TrimPrefix(line, "# ")))
		}
	}
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if strings.EqualFold(base, "README") {
		base = filepath.Base(filepath.Dir(path))
	}
	return titleFromSlug(base)
}

func inferDescription(raw, title string) string {
	clean := stripMarkdown(raw)
	for _, para := range strings.Split(clean, "\n\n") {
		para = strings.TrimSpace(spaceRE.ReplaceAllString(para, " "))
		if para == "" || strings.EqualFold(para, title) || strings.HasPrefix(para, "AWS docs:") || looksGeneratedListing(para) {
			continue
		}
		if len(para) > 180 {
			para = trimSentence(para, 180)
		}
		return para
	}
	return title
}

func looksGeneratedListing(s string) bool {
	lower := strings.ToLower(s)
	return strings.Contains(lower, " ops, protocols:") || strings.Contains(s, "→")
}

func inferSection(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	if len(parts) < 2 {
		return "Documentation"
	}
	switch parts[1] {
	case "services":
		return "Service Reference"
	case "cdk":
		return "CDK"
	case "compatibility":
		return "Compatibility"
	case "plans":
		return "Plans"
	case "dev":
		return "Development"
	case "perf-baselines":
		return "Performance"
	default:
		return "Getting Started"
	}
}

func inferTags(path, title string) []string {
	stopwords := map[string]bool{
		"and": true, "are": true, "for": true, "from": true, "into": true,
		"the": true, "this": true, "that": true, "with": true, "using": true,
	}
	tags := []string{"docs"}
	path = filepath.ToSlash(path)
	parts := strings.Split(path, "/")
	for _, part := range parts[1:] {
		part = strings.TrimSuffix(part, filepath.Ext(part))
		if strings.EqualFold(part, "README") || part == "" {
			continue
		}
		tags = append(tags, strings.FieldsFunc(strings.ToLower(part), func(r rune) bool { return r == '-' || r == '_' })...)
	}
	for _, token := range strings.FieldsFunc(strings.ToLower(title), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len(token) >= 3 && !stopwords[token] {
			tags = append(tags, token)
		}
	}
	return uniqueSortedTags(tags)
}

func trimSentence(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := strings.LastIndexAny(s[:max], ".;: ")
	if cut < 80 {
		cut = max
	}
	return strings.TrimRight(strings.TrimSpace(s[:cut]), ".;:") + "..."
}

func renderFrontmatter(meta docMeta) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %s\n", quoteYAML(meta.Title))
	fmt.Fprintf(&b, "description: %s\n", quoteYAML(meta.Description))
	fmt.Fprintf(&b, "section: %s\n", quoteYAML(meta.Section))
	b.WriteString("tags:\n")
	for _, tag := range meta.Tags {
		fmt.Fprintf(&b, "  - %s\n", tag)
	}
	b.WriteString("---\n\n")
	return b.String()
}

func buildEntry(path string, meta docMeta, body string, lineOffset int) weightedDoc {
	entry := docEntry{
		Path:        filepath.ToSlash(path),
		Href:        strings.TrimPrefix(filepath.ToSlash(path), "docs/"),
		Title:       meta.Title,
		Description: meta.Description,
		Section:     meta.Section,
		Tags:        uniqueSortedTags(meta.Tags),
		Headings:    extractHeadings(body),
	}
	content := markdownFields(body)
	return weightedDoc{
		Entry:          entry,
		BodyText:       content.Prose,
		CodeSpans:      content.Code,
		Links:          extractLinks(body, lineOffset),
		RawBody:        body,
		BodyLineOffset: lineOffset,
	}
}

// frontmatterLines is how many lines body sits below the start of raw, so a
// link's line number matches the file rather than the post-frontmatter body.
func frontmatterLines(raw, body string) int {
	if !strings.HasSuffix(raw, body) {
		return 0
	}
	return strings.Count(raw[:len(raw)-len(body)], "\n")
}

// searchFields is what the scorer sees: the entry's metadata plus the prose
// and inline code the Markdown parse produced.
func searchFields(doc weightedDoc) docssearch.DocumentFields {
	headings := make([]docssearch.Heading, 0, len(doc.Entry.Headings))
	for _, h := range doc.Entry.Headings {
		headings = append(headings, docssearch.Heading{Depth: h.Depth, Text: h.Text})
	}
	return docssearch.DocumentFields{
		Title:       doc.Entry.Title,
		Tags:        doc.Entry.Tags,
		Section:     doc.Entry.Section,
		Headings:    headings,
		Description: doc.Entry.Description,
		Code:        doc.CodeSpans,
		Body:        doc.BodyText,
	}
}

func extractHeadings(body string) []heading {
	used := map[string]int{}
	matches := headingRE.FindAllStringSubmatch(body, -1)
	out := make([]heading, 0, len(matches))
	for _, m := range matches {
		text := cleanHeadingText(m[2])
		id := slug(text)
		used[id]++
		if used[id] > 1 {
			id = fmt.Sprintf("%s-%d", id, used[id])
		}
		out = append(out, heading{Depth: len(m[1]), Text: text, ID: id})
	}
	return out
}

func cleanHeadingText(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "#")
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "`*")
	return strings.TrimSpace(s)
}

func stripMarkdown(raw string) string { return markdownFields(raw).Text }

// markdownContent is one document's Markdown rendered for indexing. Text is
// everything in reading order, used to infer a description; Prose and Code are
// the same content split by how it should be weighted.
type markdownContent struct {
	Text  string
	Prose string
	Code  []string
}

// markdownFields walks the parsed Markdown once. A code span used to be
// emitted twice — once for the span and once for the Text node inside it —
// which made inline code accidentally worth double a plain word. It is now
// emitted once, and into its own field, where the scorer weights it
// deliberately instead of by accident.
//
// Fenced and indented code blocks stay in the prose: a whole worked example is
// bulk, not a name.
func markdownFields(raw string) markdownContent {
	source := []byte(raw)
	doc := md.Parser().Parse(text.NewReader(source))
	var all, prose []string
	var code []string
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n.Kind() {
		case ast.KindHTMLBlock, ast.KindRawHTML:
			return ast.WalkSkipChildren, nil
		case ast.KindCodeSpan:
			text := strings.TrimSpace(string(n.Text(source)))
			if text != "" {
				all = append(all, text)
				code = append(code, text)
			}
			return ast.WalkSkipChildren, nil
		case ast.KindText, ast.KindString:
			text := strings.TrimSpace(string(n.Text(source)))
			if text != "" {
				all = append(all, text)
				prose = append(prose, text)
			}
		case ast.KindFencedCodeBlock, ast.KindCodeBlock:
			for i := 0; i < n.Lines().Len(); i++ {
				segment := n.Lines().At(i)
				text := strings.TrimSpace(string(segment.Value(source)))
				if text != "" {
					all = append(all, text)
					prose = append(prose, text)
				}
			}
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return markdownContent{Text: strings.Join(all, "\n"), Prose: strings.Join(prose, "\n"), Code: code}
}

// extractLinks collects every Markdown link destination in a document, with
// the line it was written on. The Markdown parse — rather than a regexp — is
// what keeps a link-shaped string inside a fenced example or a code span from
// being reported as a broken anchor.
func extractLinks(body string, lineOffset int) []docLink {
	source := []byte(body)
	doc := md.Parser().Parse(text.NewReader(source))
	var out []docLink
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		link, ok := n.(*ast.Link)
		if !ok {
			return ast.WalkContinue, nil
		}
		out = append(out, docLink{Dest: string(link.Destination), Line: lineOffset + lineOf(source, firstTextOffset(n))})
		return ast.WalkContinue, nil
	})
	return out
}

// firstTextOffset is the byte offset of a node's first text descendant, which
// is the closest thing an inline node has to a source position.
func firstTextOffset(n ast.Node) int {
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		if t, ok := child.(*ast.Text); ok {
			return t.Segment.Start
		}
		if off := firstTextOffset(child); off >= 0 {
			return off
		}
	}
	return -1
}

func lineOf(source []byte, offset int) int {
	if offset < 0 {
		return 0
	}
	return 1 + bytes.Count(source[:offset], []byte("\n"))
}

// checkAnchors verifies that every in-page anchor a published doc writes
// resolves to a heading the docs browser actually renders an id for.
//
// Only links into published docs are checked. docs/plans/, docs/dev/, AGENTS.md
// and the root README are read on GitHub, whose slugger differs — it drops a
// character like an em dash and keeps the spaces on either side, so
// "Data dir placement — avoid …" becomes "#data-dir-placement--avoid-…" there
// and "#data-dir-placement-avoid-…" here. Published docs must use this file's
// slug, the one web/src/routes/docs.tsx computes; links out to GitHub-read
// files must use GitHub's, and are left alone.
//
// The docs browser only assigns ids to h2 and h3 (see the ReactMarkdown
// component overrides), and it strips a leading h1 that repeats the page
// title, so an anchor to any other depth is dead there even though the heading
// exists.
func checkAnchors(docs []weightedDoc) error {
	byPath := make(map[string][]heading, len(docs))
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
func checkExcludedRefs(docs []weightedDoc) error {
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
func checkServiceDocStructure(docs []weightedDoc) error {
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
	out, err := exec.Command("git", "ls-files", "--others", "--exclude-standard", "--", docsRoot+"/services").Output()
	if err != nil {
		return nil
	}
	untracked := strings.Fields(strings.TrimSpace(string(out)))
	if len(untracked) == 0 {
		return nil
	}
	return fmt.Errorf("untracked files under %s/services; run make docs, then commit them:\n\t%s", docsRoot, strings.Join(untracked, "\n\t"))
}

// suggestHeading names the heading a broken anchor most likely meant: one
// whose id differs only in its hyphens, which is what a GitHub-shaped anchor
// pasted into a published doc looks like.
func suggestHeading(headings []heading, fragment string) string {
	want := strings.ReplaceAll(fragment, "-", "")
	for _, h := range headings {
		if strings.ReplaceAll(h.ID, "-", "") == want {
			return "; did you mean #" + h.ID + " ?"
		}
	}
	return ""
}

func renderNav(entries []docEntry) ([]byte, error) {
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("// Code generated by go run ./scripts/docs-index.go --write-nav; DO NOT EDIT.\n\n")
	b.WriteString("export interface DocsHeading {\n")
	b.WriteString("  depth: number\n  text: string\n  id: string\n}\n\n")
	b.WriteString("export interface DocsNavEntry {\n")
	b.WriteString("  path: string\n  href: string\n  title: string\n  description: string\n  section: string\n  tags: readonly string[]\n  headings: readonly DocsHeading[]\n}\n\n")
	b.WriteString("export const DOCS_NAV = ")
	b.Write(raw)
	b.WriteString(" as const satisfies readonly DocsNavEntry[]\n")
	return []byte(b.String()), nil
}

// renderSearchIndex writes one JSONL line per document, in path order, each
// carrying that document's own term scores, so a docs edit touches one line.
//
// The scoring itself belongs to internal/docssearch, not here: the weights and
// the tokenizer are half of a contract whose other half is the query, and this
// file used to hold a second copy of both. docssearch.ScoreDocument is now the
// only implementation, and it is unit-testable — which a `//go:build ignore`
// script is not.
func renderSearchIndex(docs []weightedDoc) ([]byte, error) {
	var b strings.Builder
	b.WriteString("# Generated by go run ./scripts/docs-index.go --write-search-index; DO NOT EDIT.\n")
	for _, doc := range docs {
		e := doc.Entry
		line, err := json.Marshal(searchEntry{
			Path:        e.Path,
			Href:        e.Href,
			Title:       e.Title,
			Description: e.Description,
			Section:     e.Section,
			Tags:        e.Tags,
			Terms:       docssearch.FormatTerms(docssearch.ScoreDocument(searchFields(doc))),
		})
		if err != nil {
			return nil, fmt.Errorf("marshal search entry for %s: %w", e.Path, err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return []byte(b.String()), nil
}

func parseInlineList(value string) []string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "[")
	value = strings.TrimSuffix(value, "]")
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		out = append(out, unquoteYAML(strings.TrimSpace(part)))
	}
	return out
}

func uniqueSortedTags(tags []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		tag = strings.Trim(tag, "\"'")
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
	}
	sort.Strings(out)
	return out
}

func quoteYAML(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

func unquoteYAML(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "\"") {
		var out string
		if err := json.Unmarshal([]byte(s), &out); err == nil {
			return out
		}
	}
	return strings.Trim(s, "'")
}

func slug(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func titleFromSlug(s string) string {
	s = strings.ReplaceAll(s, "-", " ")
	s = strings.ReplaceAll(s, "_", " ")
	words := strings.Fields(s)
	for i, word := range words {
		if len(word) == 0 {
			continue
		}
		words[i] = strings.ToUpper(word[:1]) + word[1:]
	}
	return strings.Join(words, " ")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "docs-index:", err)
	os.Exit(1)
}
