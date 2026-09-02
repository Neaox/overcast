// Package docsindex turns the published docs/ tree into the navigation and
// search data the console needs.
//
// It exists so that data has exactly one producer. It used to live in
// scripts/docs-index.go, which wrote two generated-and-committed artifacts —
// web/src/docs-nav.gen.ts and internal/docssearch/index.gen.jsonl — that every
// docs pull request rewrote. Both were sorted manifests, one entry per page, so
// a branch that *added* a sub-page and a branch that *edited* a neighbouring
// page produced overlapping hunks and git declared a conflict on a file nobody
// had written by hand. Resolving it was always the same non-decision: take
// main's side, regenerate. Three concurrent docs branches conflicted on the
// search index pairwise, including two whose markdown did not overlap at all.
//
// The artifacts are gone. Both are pure functions of docs/*.md, and the console
// binary already embeds exactly that file set (see embed.go), so internal/bff
// derives them at runtime instead: /api/docs/nav serves Entries, and
// internal/docssearch builds its inverted index from SearchEntries. Nothing to
// regenerate, nothing to keep in step, nothing to conflict over.
//
// scripts/docs-index.go still exists and still gates docs quality
// (frontmatter completeness, anchors, description budget) — it is now a linter
// over this package rather than a generator.
package docsindex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/overcast-sh/overcast/internal/docssearch"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// FrontmatterSep delimits a doc's YAML frontmatter block.
const FrontmatterSep = "---"

// MaxDescriptionLen caps a published doc's frontmatter description, as a proxy
// for the content charter's intro-budget rule (see docs/dev/content-charter.md):
// a long description is a reliable tell that the opening paragraph is doing too
// much before the reader hits a command, a table, or a linked next step.
const MaxDescriptionLen = 220

var (
	headingRE = regexp.MustCompile(`(?m)^(#{1,6})\s+(.+?)\s*$`)
	spaceRE   = regexp.MustCompile(`\s+`)
	md        = goldmark.New(goldmark.WithExtensions(extension.GFM))
)

// Meta is a document's frontmatter.
type Meta struct {
	Title       string
	Description string
	Section     string
	Tags        []string
}

// Heading is one Markdown heading, with the id the docs browser renders for it.
type Heading struct {
	Depth int    `json:"depth"`
	Text  string `json:"text"`
	ID    string `json:"id"`
}

// Entry is the navigation shape: what the console needs to render the docs
// sidebar and the "On this page" table of contents. It carries no search
// corpus — that comes from SearchEntries, which the SPA never downloads.
type Entry struct {
	Path        string    `json:"path"`
	Href        string    `json:"href"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Section     string    `json:"section"`
	Tags        []string  `json:"tags"`
	Headings    []Heading `json:"headings"`
}

// Link is one Markdown link destination and where it was written, so a broken
// anchor can be reported as file:line.
type Link struct {
	Dest string
	Line int
}

// Doc is one published page: its navigation entry plus the parsed content the
// scorer and the docs linter need.
type Doc struct {
	Entry Entry
	// BodyText is the document's prose; CodeSpans are its inline `code`
	// fragments, kept apart because they score differently — see
	// docssearch.ScoreDocument.
	BodyText  string
	CodeSpans []string
	// Links are the document's Markdown link destinations, for the anchor
	// check. They are not indexed and never reach the console.
	Links []Link
	// RawBody is the document's Markdown source after frontmatter is
	// stripped, with original line breaks intact — unlike BodyText, which is
	// the parsed prose joined by "\n" per AST node and cannot be scanned line
	// by line. BodyLineOffset is how many lines of the raw file sit above it,
	// so a match in RawBody maps back to a real file:line. Both exist only
	// for the docs linter; nothing else needs raw source.
	RawBody        string
	BodyLineOffset int
}

// Writer persists a rewritten doc. The generator script passes one so
// --write-frontmatter can fill in missing metadata in place; the runtime
// passes nil, which makes Collect read-only.
type Writer func(fsPath string, content []byte) error

// Collect walks fsys — rooted at the docs directory, so pages are addressed as
// "README.md", "services/s3.md" — and returns one Doc per published page, in
// path order. Entry.Href is that path and Entry.Path is "docs/" + Href, which
// is how both the console and the repo refer to a page.
//
// Read-only. Use CollectWriting to also repair missing frontmatter.
func Collect(fsys fs.FS) ([]Doc, error) {
	docs, _, err := CollectWriting(fsys, nil, false)
	return docs, err
}

// CollectWriting is Collect with the generator's frontmatter repair. When write
// is non-nil, a doc that has no frontmatter gets inferred metadata written back
// through it; when refresh is also true, every doc's frontmatter is replaced
// with freshly inferred metadata. The second return is how many docs were found
// without frontmatter, which --check reports rather than fixes.
func CollectWriting(fsys fs.FS, write Writer, refresh bool) ([]Doc, int, error) {
	paths, err := publishedPaths(fsys)
	if err != nil {
		return nil, 0, err
	}

	docs := make([]Doc, 0, len(paths))
	missingFrontmatter := 0
	for _, p := range paths {
		rawBytes, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil, 0, err
		}
		raw := string(rawBytes)

		meta, body, ok := SplitFrontmatter(raw)
		switch {
		case refresh && write != nil:
			if !ok {
				body = raw
			}
			meta = InferMeta(p, body)
			updated := RenderFrontmatter(meta) + strings.TrimLeft(body, "\n")
			if err := write(p, []byte(updated)); err != nil {
				return nil, 0, err
			}
			body = strings.TrimLeft(body, "\n")
		case !ok:
			missingFrontmatter++
			meta = InferMeta(p, raw)
			body = raw
			if write != nil {
				updated := RenderFrontmatter(meta) + strings.TrimLeft(body, "\n")
				if err := write(p, []byte(updated)); err != nil {
					return nil, 0, err
				}
				body = strings.TrimLeft(body, "\n")
			}
		default:
			if meta.Title == "" {
				meta.Title = InferTitle(body, p)
			}
			if meta.Section == "" {
				meta.Section = InferSection(p)
			}
			if len(meta.Tags) == 0 {
				meta.Tags = InferTags(p, meta.Title)
			}
		}

		bodyForIndex := body
		if write == nil && !ok {
			bodyForIndex = raw
		}
		docs = append(docs, BuildDoc(p, meta, bodyForIndex, frontmatterLines(raw, bodyForIndex)))
	}

	return docs, missingFrontmatter, nil
}

// publishedPaths lists the .md files under fsys that ship to readers, in path
// order. docs/plans and docs/dev are contributor-only and are excluded here for
// the same reason embed.go's pattern omits them.
func publishedPaths(fsys fs.FS) ([]string, error) {
	var out []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p == "plans" || p == "dev" {
				return fs.SkipDir
			}
			return nil
		}
		if path.Ext(p) == ".md" {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// IsPublishedPath reports whether a repo-relative docs path ships to readers.
func IsPublishedPath(repoPath string) bool {
	return !strings.HasPrefix(repoPath, "docs/plans/") && !strings.HasPrefix(repoPath, "docs/dev/")
}

// Entries projects docs down to the navigation shape served by /api/docs/nav.
func Entries(docs []Doc) []Entry {
	out := make([]Entry, 0, len(docs))
	for _, doc := range docs {
		out = append(out, doc.Entry)
	}
	return out
}

// SearchEntries projects docs down to the shape internal/docssearch indexes:
// each page's result metadata plus its own per-term scores.
func SearchEntries(docs []Doc) []docssearch.Entry {
	out := make([]docssearch.Entry, 0, len(docs))
	for i, doc := range docs {
		out = append(out, docssearch.Entry{
			Document: docssearch.Document{
				ID:          i,
				Path:        doc.Entry.Path,
				Href:        doc.Entry.Href,
				Title:       doc.Entry.Title,
				Description: doc.Entry.Description,
				Section:     doc.Entry.Section,
				Tags:        doc.Entry.Tags,
			},
			Terms: docssearch.ScoreDocument(SearchFields(doc)),
		})
	}
	return out
}

// SearchFields is what the scorer sees: the entry's metadata plus the prose and
// inline code the Markdown parse produced.
func SearchFields(doc Doc) docssearch.DocumentFields {
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

// Validate rejects a document set that would produce a broken index: a page
// that parses to nothing searchable, or that lost its title, is a bug in this
// package or in the doc, and either way it must not reach the console, where it
// would silently drop out of docs search.
func Validate(docs []Doc) error {
	if len(docs) == 0 {
		return fmt.Errorf("no published docs found")
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
		case len(docssearch.ScoreDocument(SearchFields(doc))) == 0:
			problems = append(problems, e.Path+": no searchable terms")
		case e.Description == "":
			problems = append(problems, e.Path+": no description")
		case len(e.Description) > MaxDescriptionLen:
			problems = append(problems, fmt.Sprintf("%s: description is %d chars (max %d) — a long one is a reliable tell the intro is doing too much before the first actionable thing; trim it", e.Path, len(e.Description), MaxDescriptionLen))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("docs cannot be indexed:\n\t%s", strings.Join(problems, "\n\t"))
	}
	return nil
}

// HasStrayFrontmatter reports whether body — a document's source *after* its
// real frontmatter has been stripped — begins with another `---` block.
//
// Only the first block is ever parsed. A second one renders as a literal
// horizontal rule followed by raw `title:` and `tags:` text at the top of the
// published page, and the page silently keeps the first block's metadata rather
// than the authored one. Both happen quietly: nothing else in this linter reads
// past the first block, which is how two pages shipped with stacked stubs after
// a merge.
func HasStrayFrontmatter(body string) bool {
	trimmed := strings.TrimLeft(body, "\n\r \t")
	if !strings.HasPrefix(trimmed, FrontmatterSep+"\n") {
		return false
	}
	rest := trimmed[len(FrontmatterSep)+1:]
	return strings.Contains(rest, "\n"+FrontmatterSep+"\n")
}

// SplitFrontmatter separates a doc's YAML frontmatter from its body. The third
// result reports whether frontmatter was present and well formed.
func SplitFrontmatter(raw string) (Meta, string, bool) {
	if !strings.HasPrefix(raw, FrontmatterSep+"\n") {
		return Meta{}, raw, false
	}
	rest := raw[len(FrontmatterSep)+1:]
	idx := strings.Index(rest, "\n"+FrontmatterSep+"\n")
	if idx < 0 {
		return Meta{}, raw, false
	}
	fm := rest[:idx]
	body := rest[idx+len(FrontmatterSep)+2:]
	return ParseFrontmatter(fm), body, true
}

// ParseFrontmatter reads the subset of YAML docs frontmatter uses.
func ParseFrontmatter(fm string) Meta {
	meta := Meta{}
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
			meta.Title = UnquoteYAML(value)
		case "description":
			meta.Description = UnquoteYAML(value)
		case "section":
			meta.Section = UnquoteYAML(value)
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
	meta.Tags = UniqueSortedTags(meta.Tags)
	return meta
}

// InferMeta derives frontmatter for a doc that has none, from its path and its
// opening prose.
func InferMeta(fsPath, raw string) Meta {
	title := InferTitle(raw, fsPath)
	return Meta{
		Title:       title,
		Description: inferDescription(raw, title),
		Section:     InferSection(fsPath),
		Tags:        InferTags(fsPath, title),
	}
}

// InferTitle is a doc's first h1, or a title cased from its filename.
func InferTitle(raw, fsPath string) string {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return CleanHeadingText(strings.TrimSpace(strings.TrimPrefix(line, "# ")))
		}
	}
	base := strings.TrimSuffix(path.Base(fsPath), path.Ext(fsPath))
	if strings.EqualFold(base, "README") {
		base = path.Base(path.Dir(fsPath))
	}
	return TitleFromSlug(base)
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

// InferSection maps a doc's directory to the sidebar group it belongs in.
func InferSection(fsPath string) string {
	parts := strings.Split(fsPath, "/")
	if len(parts) < 2 {
		return "Getting Started"
	}
	switch parts[0] {
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

// InferTags derives search tags from a doc's path segments and title words.
func InferTags(fsPath, title string) []string {
	stopwords := map[string]bool{
		"and": true, "are": true, "for": true, "from": true, "into": true,
		"the": true, "this": true, "that": true, "with": true, "using": true,
	}
	tags := []string{"docs"}
	for _, part := range strings.Split(fsPath, "/") {
		part = strings.TrimSuffix(part, path.Ext(part))
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
	return UniqueSortedTags(tags)
}

func trimSentence(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	cut := strings.LastIndexAny(s[:maxLen], ".;: ")
	if cut < 80 {
		cut = maxLen
	}
	return strings.TrimRight(strings.TrimSpace(s[:cut]), ".;:") + "..."
}

// RenderFrontmatter writes meta back as the YAML block --write-frontmatter adds.
func RenderFrontmatter(meta Meta) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %s\n", QuoteYAML(meta.Title))
	fmt.Fprintf(&b, "description: %s\n", QuoteYAML(meta.Description))
	fmt.Fprintf(&b, "section: %s\n", QuoteYAML(meta.Section))
	b.WriteString("tags:\n")
	for _, tag := range meta.Tags {
		fmt.Fprintf(&b, "  - %s\n", tag)
	}
	b.WriteString("---\n\n")
	return b.String()
}

// BuildDoc assembles one page's navigation entry and parsed content. fsPath is
// relative to the docs directory; lineOffset is how many lines of frontmatter
// sit above body.
func BuildDoc(fsPath string, meta Meta, body string, lineOffset int) Doc {
	entry := Entry{
		Path:        "docs/" + fsPath,
		Href:        fsPath,
		Title:       meta.Title,
		Description: meta.Description,
		Section:     meta.Section,
		Tags:        UniqueSortedTags(meta.Tags),
		Headings:    ExtractHeadings(body),
	}
	content := markdownFields(body)
	return Doc{
		Entry:          entry,
		BodyText:       content.Prose,
		CodeSpans:      content.Code,
		Links:          ExtractLinks(body, lineOffset),
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

// ExtractHeadings lists a body's Markdown headings with the ids GitHub, the
// public site and the docs browser all render (see Slug), numbering repeats
// the same way they do.
func ExtractHeadings(body string) []Heading {
	slugger := NewSlugger()
	matches := headingRE.FindAllStringSubmatch(withoutFencedCode(body), -1)
	out := make([]Heading, 0, len(matches))
	for _, m := range matches {
		text := CleanHeadingText(m[2])
		out = append(out, Heading{Depth: len(m[1]), Text: text, ID: slugger.Slug(text)})
	}
	return out
}

// withoutFencedCode blanks the lines inside ``` and ~~~ fences, so a shell
// comment in an example ("# Start the daemon") is not read as a heading: no
// renderer gives it an id, and counting it would shift the number a real
// heading gets when it repeats the comment's words.
func withoutFencedCode(body string) string {
	lines := strings.Split(body, "\n")
	fence := ""
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " ")
		if fence == "" {
			if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
				fence = trimmed[:3]
				lines[i] = ""
			}
			continue
		}
		lines[i] = ""
		if strings.HasPrefix(trimmed, fence) {
			fence = ""
		}
	}
	return strings.Join(lines, "\n")
}

// CleanHeadingText strips the decoration a heading may carry around its words.
func CleanHeadingText(s string) string {
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
			text := strings.TrimSpace(inlineText(n, source))
			if text != "" {
				all = append(all, text)
				code = append(code, text)
			}
			return ast.WalkSkipChildren, nil
		case ast.KindText, ast.KindString:
			text := strings.TrimSpace(leafText(n, source))
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

// leafText is the source text of a single inline leaf node.
//
// ast.Node.Text is deprecated, and its replacement is per-node — a Text node
// carries a source Segment, a String node carries its bytes outright — so the
// two cases are read directly rather than through the general walker.
func leafText(n ast.Node, source []byte) string {
	switch t := n.(type) {
	case *ast.Text:
		return string(t.Segment.Value(source))
	case *ast.String:
		return string(t.Value)
	}
	return ""
}

// inlineText concatenates the source text of an inline node's descendants,
// which is what a code span's content is: goldmark parses the bytes between the
// backticks into one or more Text children rather than storing them on the span.
func inlineText(n ast.Node, source []byte) string {
	var b strings.Builder
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		if leaf := leafText(child, source); leaf != "" {
			b.WriteString(leaf)
			continue
		}
		b.WriteString(inlineText(child, source))
	}
	return b.String()
}

// ExtractLinks collects every Markdown link destination in a document, with the
// line it was written on. The Markdown parse — rather than a regexp — is what
// keeps a link-shaped string inside a fenced example or a code span from being
// reported as a broken anchor.
func ExtractLinks(body string, lineOffset int) []Link {
	source := []byte(body)
	doc := md.Parser().Parse(text.NewReader(source))
	var out []Link
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		link, ok := n.(*ast.Link)
		if !ok {
			return ast.WalkContinue, nil
		}
		out = append(out, Link{Dest: string(link.Destination), Line: lineOffset + lineOf(source, firstTextOffset(n))})
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
		out = append(out, UnquoteYAML(strings.TrimSpace(part)))
	}
	return out
}

// UniqueSortedTags de-duplicates and orders a tag list, so two docs that infer
// the same tags in a different order produce the same entry.
func UniqueSortedTags(tags []string) []string {
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

// QuoteYAML renders a string as a YAML scalar.
func QuoteYAML(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

// UnquoteYAML reverses QuoteYAML for the quoting styles docs frontmatter uses.
func UnquoteYAML(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "\"") {
		var out string
		if err := json.Unmarshal([]byte(s), &out); err == nil {
			return out
		}
	}
	return strings.Trim(s, "'")
}

// Slug is the heading id every reader of the docs agrees on: GitHub's. The
// same files are read on github.com, and the public site renders them through
// Astro, which assigns ids with github-slugger — so that library's `slug` is
// the reference, this is its Go port, and web/src/lib/slug.ts (which the
// console's "On this page" anchors and code-tab ids come from) calls the
// library itself. web/src/routes/docs.slug.test.ts pins the two together.
//
// The rules, in github-slugger's terms: lower-case; drop every character its
// regex strips (everything but Unicode's Alphabetic set, marks, decimal
// digits, connector punctuation and the hyphen); turn each space into a
// hyphen. Runs are NOT collapsed and nothing is trimmed, so
// "Data-plane endpoints — RDS" is "data-plane-endpoints--rds": the em-dash
// goes and both spaces around it survive as hyphens.
func Slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case slugKeeps(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// slugKeeps is the complement of github-slugger's regex.js: the characters a
// slug keeps. Checked against the library over every code point; the only
// differences are characters Unicode assigned after the tables it was built
// from, which no heading here uses.
func slugKeeps(r rune) bool {
	return r == '-' || r == '_' ||
		unicode.IsLetter(r) || unicode.IsMark(r) ||
		unicode.Is(unicode.Nd, r) || unicode.Is(unicode.Nl, r) ||
		unicode.Is(unicode.Pc, r) || unicode.Is(unicode.Other_Alphabetic, r)
}

// Slugger hands out heading ids for one document, numbering repeats the way
// github-slugger's class does: the second "Setup" is "setup-1", the third
// "setup-2", and a later heading whose own slug is already taken moves along
// the same way.
type Slugger struct{ seen map[string]int }

// NewSlugger starts a fresh document.
func NewSlugger() *Slugger { return &Slugger{seen: map[string]int{}} }

// Slug returns the id for the next heading with this text.
func (s *Slugger) Slug(text string) string {
	base := Slug(text)
	id := base
	for {
		if _, taken := s.seen[id]; !taken {
			break
		}
		s.seen[base]++
		id = fmt.Sprintf("%s-%d", base, s.seen[base])
	}
	s.seen[id] = 0
	return id
}

// TitleFromSlug turns a filename stem into a display title.
func TitleFromSlug(s string) string {
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
