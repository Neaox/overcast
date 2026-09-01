// Package docslint holds the structural rules for the per-service
// documentation under docs/services/. It is the mechanical half of
// docs/dev/service-doc-template.md: the template says what a service page is
// for, and this says what shape it has to be in for `make docs-check` to pass.
//
// It lives in its own package rather than inside scripts/docs-index.go — the
// script that calls it — for the same reason internal/docssearch does: a
// `//go:build ignore` file cannot be unit-tested, and a lint whose rules are
// untested is a lint that quietly stops meaning anything.
//
// Scope is deliberately narrow. Everything here is a *structure* rule that can
// be decided by looking at the file: which sections exist, in what order, and
// what is generated. Nothing here judges prose. The content charter
// (docs/dev/content-charter.md) covers that, and it says why: a linter that
// grows into a style-bible-as-code stops getting maintained the moment it
// produces its first annoying false positive.
package docslint

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Doc is one published Markdown file as the linter sees it.
type Doc struct {
	// Path is the repo-relative slash path, e.g. "docs/services/s3.md".
	Path string
	// Body is the file's Markdown with any YAML frontmatter removed.
	Body string
	// BodyLineOffset is how many lines of the file sit above Body, so a
	// reported line number matches the file rather than the body.
	BodyLineOffset int
}

// Problem is one rule violation, reported as file:line.
type Problem struct {
	Path string
	Line int
	Msg  string
}

func (p Problem) String() string {
	if p.Line > 0 {
		return fmt.Sprintf("%s:%d: %s", p.Path, p.Line, p.Msg)
	}
	return fmt.Sprintf("%s: %s", p.Path, p.Msg)
}

const (
	// BeginMarker and EndMarker bracket what cmd/capgen writes. Both files
	// capgen owns carry them, so `make docs-check`'s regenerate-and-diff gate
	// sees drift in either one.
	BeginMarker = "<!-- BEGIN overcast:capabilities -->"
	EndMarker   = "<!-- END overcast:capabilities -->"

	// servicesRoot is the only tree this package lints.
	servicesRoot = "docs/services/"
)

// TemplateSections are the H2s docs/dev/service-doc-template.md defines, in the
// order a landing page must present them. A page may omit an optional one; it
// may not reorder the ones it has, and it may not invent a fifth spelling of
// "the bit about limitations".
//
// Headings outside this list are free-form and may appear anywhere before the
// Operations block — a service with something genuinely specific to say
// ("Queue URLs and endpoint resolution") should say it under its own heading
// rather than wedging it into one of these.
var TemplateSections = []string{
	"Quick start",
	"What works",
	"Differences from AWS",
	"Gotchas",
	"Operations",
	"Related",
}

// RequiredSections must be present on every service landing page.
//
// It is shorter than TemplateSections on purpose. "Operations" and "Related"
// are structural — capgen writes the first and the second is the page's link
// footer — so requiring them costs a writer nothing and keeps every page
// ending the same way. "Quick start" is the one required *content* section,
// and it is waived for the pages in RestructurePending below, which is what
// lets the split land before the prose is rewritten.
var RequiredSections = []string{"Operations", "Related"}

// SubPages are the only file names allowed under docs/services/<key>/.
//
// A fixed list is the point: the restructure exists because a single page per
// service was an undifferentiated dump, and "break it up" degenerates into the
// same thing one directory down if every writer invents their own filenames.
// operations.md is generated; the other three are hand-written and created
// only when there is enough content to warrant one.
var SubPages = []string{"operations.md", "limitations.md", "troubleshooting.md", "examples.md"}

// RestructurePending lists the service landing pages that have not yet been
// rewritten to the template. For these, the three rules that need real prose
// are waived:
//
//	Quick start present · intro of at most two sentences · no capability
//	table outside the generated block
//
// Everything else is enforced everywhere, immediately.
//
// The list only shrinks. checkPendingIsStillNeeded fails the build when a page
// on it satisfies all three waived rules, so finishing a service forces its
// entry to be deleted rather than leaving a permanent exemption behind — the
// same self-deleting shape cmd/capgen's capabilityManifestExemptions uses, and
// for the same reason: an exemption nobody is made to revisit is indefinite.
//
// Keys are doc file stems (docs/services/<stem>.md), not capability service
// keys — "elb", not "elbv2".
var RestructurePending = []string{
	"acm", "apigateway", "appconfig", "appconfigdata", "appregistry", "appsync",
	"autoscaling", "bedrock", "cloudformation", "cloudfront",
	"cloudtrail", "cloudwatch", "cloudwatch-logs", "cognito",
	"ec2", "ecr", "ecs", "eks", "elb",
	"eventbridge", "iam", "kms", "lambda",
	"organizations", "pipes", "route53", "scheduler",
	"secretsmanager", "ses", "shield", "sns", "sqs", "ssm", "stepfunctions",
	"sts", "waf",
}

// maxIntroSentences is the content charter's intro budget (rule 2), applied to
// the one page shape where it can be checked mechanically: a service landing
// page, where "the first actionable thing" is always a heading, a command, a
// table or a list.
const maxIntroSentences = 2

var (
	headingRE   = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*$`)
	listItemRE  = regexp.MustCompile(`^\s*([-*+]\s|\d+[.)]\s)`)
	sentenceEnd = regexp.MustCompile(`[.!?]["')\x60]*(\s|$)`)
)

// Check runs every rule over the documents it recognises and returns the
// problems in path order. Documents outside docs/services/ are ignored.
func Check(docs []Doc) []Problem {
	var problems []Problem
	pending := map[string]bool{}
	for _, key := range RestructurePending {
		pending[key] = true
	}
	waivedSatisfied := map[string]bool{}

	sorted := append([]Doc(nil), docs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })

	for _, doc := range sorted {
		switch stem, sub, kind := classify(doc.Path); kind {
		case docLanding:
			landing, waived := checkLanding(doc, pending[stem])
			problems = append(problems, landing...)
			if pending[stem] {
				waivedSatisfied[stem] = waived
			}
		case docSubPage:
			problems = append(problems, checkSubPage(doc, stem, sub)...)
		case docIgnored:
			// Anything outside docs/services/, and the services index itself.
		}
	}
	problems = append(problems, checkPendingIsStillNeeded(waivedSatisfied)...)
	return problems
}

type docKind int

const (
	docIgnored docKind = iota
	docLanding
	docSubPage
)

// classify decides what a path is: a service landing page, a sub-page of one,
// or something this package does not lint.
func classify(path string) (stem, sub string, kind docKind) {
	if !strings.HasPrefix(path, servicesRoot) {
		return "", "", docIgnored
	}
	rest := strings.TrimPrefix(path, servicesRoot)
	if rest == "README.md" {
		return "", "", docIgnored
	}
	if dir, file, nested := strings.Cut(rest, "/"); nested {
		return dir, file, docSubPage
	}
	return strings.TrimSuffix(rest, ".md"), "", docLanding
}

// checkLanding applies the landing-page rules. The second return value says
// whether the three waivable rules all passed, which is what forces a
// RestructurePending entry to be removed once it has stopped being true.
func checkLanding(doc Doc, waived bool) ([]Problem, bool) {
	var problems []Problem
	report := func(line int, format string, args ...any) {
		// line 0 means "the whole file"; only a real line gets the
		// frontmatter offset added so it points at the file, not the body.
		if line > 0 {
			line += doc.BodyLineOffset
		}
		problems = append(problems, Problem{Path: doc.Path, Line: line, Msg: fmt.Sprintf(format, args...)})
	}

	lines := strings.Split(doc.Body, "\n")
	begin, end := markerLines(lines)

	switch {
	case begin < 0:
		report(0, "no %s marker; run: make docs", BeginMarker)
	case end < 0:
		report(begin+1, "%s without a matching %s", BeginMarker, EndMarker)
	case end < begin:
		report(end+1, "%s comes before %s", EndMarker, BeginMarker)
	}

	headings := parseHeadings(lines)
	present := map[string]int{}
	for _, h := range headings {
		if h.depth == 2 {
			if _, seen := present[h.text]; !seen {
				present[h.text] = h.line
			}
		}
	}

	for _, want := range RequiredSections {
		if _, ok := present[want]; !ok {
			report(0, "missing required section %q; see docs/dev/service-doc-template.md", "## "+want)
		}
	}

	// Order among the template sections that are present. Free-form headings
	// are not ordered against anything — only the template vocabulary is.
	last := -1
	lastName := ""
	for i, name := range TemplateSections {
		line, ok := present[name]
		if !ok {
			continue
		}
		if last >= 0 && line < present[lastName] {
			report(line, "section %q must come after %q", "## "+name, "## "+lastName)
		}
		last = i
		lastName = name
	}

	if begin >= 0 && end > begin {
		if line, ok := present["Operations"]; !ok || line < begin || line > end {
			report(begin+1, "%q must be inside the generated block — capgen writes it; run: make docs", "## Operations")
		}
		problems = append(problems, checkTailAfterBlock(doc, lines, end)...)
	}

	introOK := true
	if line, count := introSentences(lines); count > maxIntroSentences {
		introOK = false
		if !waived {
			report(line, "intro is %d sentences before the first heading, command, table or list (max %d); see the content charter's intro budget", count, maxIntroSentences)
		}
	}

	tablesOK := true
	for _, line := range capabilityTableLines(lines, begin, end) {
		tablesOK = false
		if !waived {
			report(line, "capability table outside the generated block; the per-operation table belongs in the operations sub-page, which capgen writes")
		}
	}

	quickStartOK := true
	if _, ok := present["Quick start"]; !ok {
		quickStartOK = false
		if !waived {
			report(0, "missing required section %q; see docs/dev/service-doc-template.md", "## Quick start")
		}
	}

	return problems, introOK && tablesOK && quickStartOK
}

// checkTailAfterBlock enforces that the generated block is last, except for a
// trailing "## Related". Anything else after it is content a reader reaches
// only by scrolling past the generated section, and it is content capgen would
// shove further down on its next run.
func checkTailAfterBlock(doc Doc, lines []string, end int) []Problem {
	var problems []Problem
	fail := func(i int, msg string) []Problem {
		return append(problems, Problem{Path: doc.Path, Line: doc.BodyLineOffset + i + 1, Msg: msg})
	}
	visible := eachOutsideFences(lines)
	seenRelated := false
	for i := end + 1; i < len(lines); i++ {
		line := strings.TrimSpace(visible[i])
		if line == "" {
			continue
		}
		if !seenRelated {
			if line != "## Related" {
				return fail(i, fmt.Sprintf("content after %s; only a trailing %q section may follow the generated block", EndMarker, "## Related"))
			}
			seenRelated = true
			continue
		}
		if strings.HasPrefix(line, "## ") {
			return fail(i, fmt.Sprintf("%q must be the last section on the page", "## Related"))
		}
	}
	return problems
}

// checkSubPage applies the rules for docs/services/<key>/*.md.
func checkSubPage(doc Doc, stem, sub string) []Problem {
	var problems []Problem
	report := func(line int, format string, args ...any) {
		// line 0 means "the whole file"; only a real line gets the
		// frontmatter offset added so it points at the file, not the body.
		if line > 0 {
			line += doc.BodyLineOffset
		}
		problems = append(problems, Problem{Path: doc.Path, Line: line, Msg: fmt.Sprintf(format, args...)})
	}

	if !contains(SubPages, sub) {
		report(0, "unexpected service sub-page; docs/services/%s/ may hold only %s (see docs/dev/service-doc-template.md)", stem, strings.Join(SubPages, ", "))
		return problems
	}

	lines := strings.Split(doc.Body, "\n")
	begin, end := markerLines(lines)

	if sub == "operations.md" {
		if begin < 0 || end < begin {
			report(0, "generated operations page has no %s … %s block; run: make docs", BeginMarker, EndMarker)
			return problems
		}
		for i, line := range lines {
			if strings.TrimSpace(line) == "" || (i > begin && i < end) || i == begin || i == end {
				continue
			}
			report(i+1, "hand-written content in a generated file; edit cmd/capgen instead")
			return problems
		}
		return problems
	}

	// A hand-written sub-page has to be reachable from its landing page in
	// both directions: readers arrive at it from a link there, and land on it
	// from search with no idea which service they are looking at.
	if !strings.Contains(doc.Body, "../"+stem+".md") {
		report(0, "no link back to the landing page; link ../%s.md so a reader arriving from search knows where they are", stem)
	}
	return problems
}

// checkPendingIsStillNeeded deletes stale exemptions by failing the build,
// which is the only mechanism that reliably deletes anything.
func checkPendingIsStillNeeded(satisfied map[string]bool) []Problem {
	var stale []string
	for key, ok := range satisfied {
		if ok {
			stale = append(stale, key)
		}
	}
	if len(stale) == 0 {
		return nil
	}
	sort.Strings(stale)
	return []Problem{{
		Path: "internal/docslint/docslint.go",
		Msg: fmt.Sprintf("%s now follow the service page template; remove them from RestructurePending so the rules stay enforced: %s",
			plural(len(stale), "page", "pages"), strings.Join(stale, ", ")),
	}}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

type mdHeading struct {
	depth int
	text  string
	line  int // 0-based index into lines
}

// parseHeadings returns the ATX headings outside fenced code blocks.
func parseHeadings(lines []string) []mdHeading {
	var out []mdHeading
	for i, line := range eachOutsideFences(lines) {
		if line == "" {
			continue
		}
		m := headingRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		out = append(out, mdHeading{depth: len(m[1]), text: strings.TrimSpace(strings.Trim(m[2], "#")), line: i})
	}
	return out
}

// eachOutsideFences returns a slice parallel to lines with fenced code content
// blanked out, so a "## " inside a shell heredoc is not read as a heading.
func eachOutsideFences(lines []string) []string {
	out := make([]string, len(lines))
	fence := ""
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
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
		out[i] = line
	}
	return out
}

// markerLines returns the 0-based line indexes of the generated block's
// markers, or -1 for one that is absent.
func markerLines(lines []string) (begin, end int) {
	begin, end = -1, -1
	for i, line := range lines {
		switch strings.TrimSpace(line) {
		case BeginMarker:
			if begin < 0 {
				begin = i
			}
		case EndMarker:
			end = i
		}
	}
	return begin, end
}

// introSentences counts the sentences between the H1 and the first actionable
// thing — a heading, a fenced command, a table or a list — and returns the line
// the intro starts on. Blockquotes are skipped: a "> AWS docs: …" line or a
// GitHub alert is a signpost, not throat-clearing.
func introSentences(lines []string) (line, count int) {
	visible := eachOutsideFences(lines)
	started, startLine := false, 0
	var intro []string
	for i, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if !started {
			if visible[i] != "" && strings.HasPrefix(trimmed, "# ") {
				started = true
			}
			continue
		}
		if trimmed == "" || trimmed == "---" || strings.HasPrefix(trimmed, ">") ||
			strings.HasPrefix(trimmed, "<!--") {
			continue
		}
		if visible[i] == "" || strings.HasPrefix(trimmed, "#") ||
			strings.HasPrefix(trimmed, "|") || listItemRE.MatchString(raw) {
			break
		}
		if startLine == 0 {
			startLine = i + 1
		}
		intro = append(intro, trimmed)
	}
	return startLine, countSentences(strings.Join(intro, " "))
}

func countSentences(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	// Abbreviations that end in a period but not a sentence. Kept tiny — this
	// is a budget check, not a natural-language parser, and the failure mode
	// of a miscount is one extra sentence of slack.
	for _, abbr := range []string{"e.g.", "i.e.", "etc.", "vs.", "cf."} {
		s = strings.ReplaceAll(s, abbr, strings.ReplaceAll(abbr, ".", ""))
	}
	return len(sentenceEnd.FindAllString(s, -1))
}

// capabilityTableLines finds GFM tables outside the generated block whose first
// column is "Operation" — the shape of a hand-maintained copy of the table
// capgen generates. Any other table is fine; a service page is expected to use
// them.
func capabilityTableLines(lines []string, begin, end int) []int {
	var out []int
	visible := eachOutsideFences(lines)
	for i, line := range visible {
		if line == "" || (begin >= 0 && end > begin && i >= begin && i <= end) {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") {
			continue
		}
		if i+1 >= len(visible) || !isTableDelimiter(visible[i+1]) {
			continue
		}
		if strings.EqualFold(firstCell(trimmed), "Operation") {
			out = append(out, i+1)
		}
	}
	return out
}

func isTableDelimiter(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "|") {
		return false
	}
	return strings.Trim(trimmed, "|:- \t") == ""
}

func firstCell(row string) string {
	cells := strings.Split(strings.Trim(row, "|"), "|")
	if len(cells) == 0 {
		return ""
	}
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(cells[0]), "*`"))
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
