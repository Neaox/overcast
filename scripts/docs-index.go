//go:build ignore

// Script: docs-index
// Adds missing docs frontmatter and generates a compact TypeScript docs index.
//
// Usage:
//
//	go run ./scripts/docs-index.go --write-frontmatter --write-index --write-go-index
//	go run ./scripts/docs-index.go --check
package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/format"
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
	docsRoot       = "docs"
	indexOutput    = "web/src/generated/docs-index.ts"
	goIndexOutput  = "internal/docssearch/index_gen.go"
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

type docEntry struct {
	Path        string    `json:"path"`
	Href        string    `json:"href"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Section     string    `json:"section"`
	Tags        []string  `json:"tags"`
	Headings    []heading `json:"headings"`
	SearchText  string    `json:"searchText"`
	Checksum    string    `json:"checksum"`
}

type weightedDoc struct {
	Entry    docEntry
	BodyText string
}

type goPosting struct {
	Doc   int
	Score int
}

func main() {
	writeFrontmatter := flag.Bool("write-frontmatter", false, "add frontmatter to docs that are missing it")
	refreshFrontmatter := flag.Bool("refresh-frontmatter", false, "replace docs frontmatter with inferred metadata")
	writeIndex := flag.Bool("write-index", false, "write the generated TypeScript docs index")
	writeGoIndex := flag.Bool("write-go-index", false, "write the generated Go docs search index")
	check := flag.Bool("check", false, "verify frontmatter and generated index are up to date")
	flag.Parse()

	if !*writeFrontmatter && !*writeIndex && !*writeGoIndex && !*check {
		*writeIndex = true
		*writeGoIndex = true
	}

	docs, frontmatterChanges, err := collectDocs(*writeFrontmatter, *refreshFrontmatter)
	if err != nil {
		fatal(err)
	}
	entries := make([]docEntry, 0, len(docs))
	for _, doc := range docs {
		entries = append(entries, doc.Entry)
	}

	generated, err := renderIndex(entries)
	if err != nil {
		fatal(err)
	}
	goGenerated, err := renderGoIndex(docs)
	if err != nil {
		fatal(err)
	}

	if *check {
		if frontmatterChanges > 0 {
			fatal(fmt.Errorf("%d docs are missing frontmatter; run go run ./scripts/docs-index.go --write-frontmatter --write-index", frontmatterChanges))
		}
		existing, err := os.ReadFile(indexOutput)
		if err != nil {
			fatal(err)
		}
		if !bytes.Equal(existing, generated) {
			fatal(errors.New("docs index is stale; run go run ./scripts/docs-index.go --write-index"))
		}
		goExisting, err := os.ReadFile(goIndexOutput)
		if err != nil {
			fatal(err)
		}
		if !bytes.Equal(goExisting, goGenerated) {
			fatal(errors.New("Go docs search index is stale; run go run ./scripts/docs-index.go --write-go-index"))
		}
		return
	}

	if *writeIndex {
		if err := os.MkdirAll(filepath.Dir(indexOutput), 0o755); err != nil {
			fatal(err)
		}
		if err := os.WriteFile(indexOutput, generated, 0o644); err != nil {
			fatal(err)
		}
	}
	if *writeGoIndex {
		if err := os.MkdirAll(filepath.Dir(goIndexOutput), 0o755); err != nil {
			fatal(err)
		}
		if err := os.WriteFile(goIndexOutput, goGenerated, 0o644); err != nil {
			fatal(err)
		}
	}
}

func collectDocs(writeFrontmatter, refreshFrontmatter bool) ([]weightedDoc, int, error) {
	paths := []string{}
	if err := filepath.WalkDir(docsRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && filepath.ToSlash(path) == "docs/plans" {
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
	return !strings.HasPrefix(path, "docs/plans/")
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
	headings := extractHeadings(body)
	bodyText := markdownText(body)
	searchText := normalizeSearchText(strings.Join([]string{
		meta.Title,
		meta.Description,
		meta.Section,
		strings.Join(meta.Tags, " "),
		bodyText,
	}, "\n"))
	sum := sha1.Sum([]byte(body))
	entry := docEntry{
		Path:        filepath.ToSlash(path),
		Href:        strings.TrimPrefix(filepath.ToSlash(path), "docs/"),
		Title:       meta.Title,
		Description: meta.Description,
		Section:     meta.Section,
		Tags:        uniqueSortedTags(meta.Tags),
		Headings:    headings,
		SearchText:  searchText,
		Checksum:    hex.EncodeToString(sum[:8]),
	}
	return weightedDoc{Entry: entry, BodyText: bodyText}
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

func renderIndex(entries []docEntry) ([]byte, error) {
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("// Code generated by go run ./scripts/docs-index.go --write-index; DO NOT EDIT.\n\n")
	b.WriteString("export interface DocsHeading {\n")
	b.WriteString("  depth: number\n  text: string\n  id: string\n}\n\n")
	b.WriteString("export interface DocsIndexEntry {\n")
	b.WriteString("  path: string\n  href: string\n  title: string\n  description: string\n  section: string\n  tags: readonly string[]\n  headings: readonly DocsHeading[]\n  searchText: string\n  checksum: string\n}\n\n")
	b.WriteString("export const DOCS_INDEX = ")
	b.Write(raw)
	b.WriteString(" as const satisfies readonly DocsIndexEntry[]\n")
	return []byte(b.String()), nil
}

func renderGoIndex(docs []weightedDoc) ([]byte, error) {
	postings := buildPostings(docs)
	terms := make([]string, 0, len(postings))
	for term := range postings {
		terms = append(terms, term)
	}
	sort.Strings(terms)

	var b strings.Builder
	b.WriteString("// Code generated by go run ./scripts/docs-index.go --write-go-index; DO NOT EDIT.\n")
	b.WriteString("//go:build !slim\n\n")
	b.WriteString("package docssearch\n\n")
	b.WriteString("var docs = []Document{\n")
	for id, doc := range docs {
		e := doc.Entry
		fmt.Fprintf(&b, "\t{ID: %d, Path: %q, Href: %q, Title: %q, Description: %q, Section: %q, Tags: %#v},\n",
			id, e.Path, e.Href, e.Title, e.Description, e.Section, e.Tags)
	}
	b.WriteString("}\n\n")
	b.WriteString("var postings = map[string][]Posting{\n")
	for _, term := range terms {
		b.WriteString("\t")
		b.WriteString(fmt.Sprintf("%q: {", term))
		for _, posting := range postings[term] {
			b.WriteString(fmt.Sprintf("{Doc: %d, Score: %d},", posting.Doc, posting.Score))
		}
		b.WriteString("},\n")
	}
	b.WriteString("}\n")
	formatted, err := format.Source([]byte(b.String()))
	if err != nil {
		return nil, fmt.Errorf("format Go docs index: %w", err)
	}
	return formatted, nil
}

func buildPostings(docs []weightedDoc) map[string][]goPosting {
	perTerm := map[string]map[int]int{}
	for id, doc := range docs {
		addWeightedTokens(perTerm, id, doc.Entry.Title, 10)
		addWeightedTokens(perTerm, id, strings.Join(doc.Entry.Tags, " "), 8)
		addWeightedTokens(perTerm, id, doc.Entry.Section, 6)
		for _, heading := range doc.Entry.Headings {
			weight := 5
			if heading.Depth == 1 {
				weight = 7
			}
			addWeightedTokens(perTerm, id, heading.Text, weight)
		}
		addWeightedTokens(perTerm, id, doc.Entry.Description, 4)
		addWeightedTokens(perTerm, id, doc.BodyText, 1)
	}
	out := make(map[string][]goPosting, len(perTerm))
	for term, docScores := range perTerm {
		postings := make([]goPosting, 0, len(docScores))
		for docID, score := range docScores {
			postings = append(postings, goPosting{Doc: docID, Score: score})
		}
		sort.Slice(postings, func(i, j int) bool {
			if postings[i].Score == postings[j].Score {
				return postings[i].Doc < postings[j].Doc
			}
			return postings[i].Score > postings[j].Score
		})
		out[term] = postings
	}
	return out
}

func addWeightedTokens(index map[string]map[int]int, docID int, text string, weight int) {
	for _, token := range tokenize(text) {
		if index[token] == nil {
			index[token] = map[int]int{}
		}
		index[token][docID] += weight
	}
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
