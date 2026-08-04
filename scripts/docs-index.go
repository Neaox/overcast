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
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

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
)

var (
	headingRE       = regexp.MustCompile(`(?m)^(#{1,6})\s+(.+?)\s*$`)
	spaceRE         = regexp.MustCompile(`\s+`)
	md              = goldmark.New(goldmark.WithExtensions(extension.GFM))
	searchStopwords = map[string]bool{
		"a": true, "an": true, "and": true, "are": true, "as": true, "at": true,
		"be": true, "by": true, "for": true, "from": true, "in": true, "into": true,
		"is": true, "it": true, "of": true, "on": true, "or": true, "the": true,
		"this": true, "to": true, "with": true,
	}
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
// Terms are sorted, and a term can itself contain ':' (normalizeSearchText
// keeps it), so a reader splits each pair on its *last* colon.
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
	Entry    docEntry
	BodyText string
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
		case len(scoreDoc(doc)) == 0:
			problems = append(problems, e.Path+": no searchable terms")
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
		entries = append(entries, buildEntry(path, meta, bodyForIndex))
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

func buildEntry(path string, meta docMeta, body string) weightedDoc {
	entry := docEntry{
		Path:        filepath.ToSlash(path),
		Href:        strings.TrimPrefix(filepath.ToSlash(path), "docs/"),
		Title:       meta.Title,
		Description: meta.Description,
		Section:     meta.Section,
		Tags:        uniqueSortedTags(meta.Tags),
		Headings:    extractHeadings(body),
	}
	return weightedDoc{Entry: entry, BodyText: markdownText(body)}
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

func stripMarkdown(raw string) string {
	return markdownText(raw)
}

func markdownText(raw string) string {
	source := []byte(raw)
	doc := md.Parser().Parse(text.NewReader(source))
	var parts []string
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n.Kind() {
		case ast.KindHTMLBlock, ast.KindRawHTML:
			return ast.WalkSkipChildren, nil
		case ast.KindText, ast.KindCodeSpan, ast.KindString:
			text := strings.TrimSpace(string(n.Text(source)))
			if text != "" {
				parts = append(parts, text)
			}
		case ast.KindFencedCodeBlock, ast.KindCodeBlock:
			for i := 0; i < n.Lines().Len(); i++ {
				segment := n.Lines().At(i)
				text := strings.TrimSpace(string(segment.Value(source)))
				if text != "" {
					parts = append(parts, text)
				}
			}
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return strings.Join(parts, "\n")
}

func normalizeSearchText(s string) string {
	s = splitIdentifierWords(s)
	s = strings.ToLower(s)
	var b strings.Builder
	lastSpace := true
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == ':' || r == '/' {
			b.WriteRune(r)
			lastSpace = false
			continue
		}
		if !lastSpace {
			b.WriteByte(' ')
			lastSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

func splitIdentifierWords(s string) string {
	var b strings.Builder
	var prev rune
	for i, r := range s {
		if i > 0 && isIdentifierBoundary(prev, r) {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
		prev = r
	}
	return b.String()
}

func isIdentifierBoundary(prev, curr rune) bool {
	return (unicode.IsLower(prev) && unicode.IsUpper(curr)) || (unicode.IsLetter(prev) && unicode.IsDigit(curr)) || (unicode.IsDigit(prev) && unicode.IsLetter(curr))
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
// carrying that document's own term scores. The scores are the same weighted
// occurrence counts the inverted index held before (title 10, tags 8, heading
// 7/5, section 6, description 4, body 1), just grouped by document instead of
// by term, so ranking is unchanged and a docs edit touches one line.
func renderSearchIndex(docs []weightedDoc) ([]byte, error) {
	var b strings.Builder
	b.WriteString("# Generated by go run ./scripts/docs-index.go --write-search-index; DO NOT EDIT.\n")
	for _, doc := range docs {
		e := doc.Entry
		terms := scoreDoc(doc)
		names := make([]string, 0, len(terms))
		for term := range terms {
			names = append(names, term)
		}
		sort.Strings(names)
		var pairs strings.Builder
		for i, term := range names {
			if i > 0 {
				pairs.WriteByte(' ')
			}
			fmt.Fprintf(&pairs, "%s:%d", term, terms[term])
		}
		line, err := json.Marshal(searchEntry{
			Path:        e.Path,
			Href:        e.Href,
			Title:       e.Title,
			Description: e.Description,
			Section:     e.Section,
			Tags:        e.Tags,
			Terms:       pairs.String(),
		})
		if err != nil {
			return nil, fmt.Errorf("marshal search entry for %s: %w", e.Path, err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return []byte(b.String()), nil
}

// scoreDoc weights one document's terms the way search ranks them: a term in
// the title is worth ten of the same term in the body, and a term occurring
// twice counts twice.
func scoreDoc(doc weightedDoc) map[string]int {
	scores := map[string]int{}
	add := func(text string, weight int) {
		for _, token := range tokenize(text) {
			scores[token] += weight
		}
	}
	add(doc.Entry.Title, 10)
	add(strings.Join(doc.Entry.Tags, " "), 8)
	add(doc.Entry.Section, 6)
	for _, heading := range doc.Entry.Headings {
		weight := 5
		if heading.Depth == 1 {
			weight = 7
		}
		add(heading.Text, weight)
	}
	add(doc.Entry.Description, 4)
	add(doc.BodyText, 1)
	return scores
}

func tokenize(s string) []string {
	normalized := normalizeSearchText(s)
	fields := strings.Fields(normalized)
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.Trim(field, "-_:/. ")
		if len(field) < 2 || searchStopwords[field] {
			continue
		}
		out = append(out, field)
	}
	return out
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
