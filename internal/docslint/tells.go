package docslint

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// The house-style tells: phrasings that read as machine-written the moment a
// reader meets one, and that a human editor would have cut.
//
// This is the one place the charter's "no style bible as code" line is bent,
// and it is bent narrowly. Every pattern here is a fixed phrase or a fixed
// shape, not a judgment about whether a sentence is good. The published corpus
// had exactly one hit when the rule landed, so the cost of the rule is not the
// backlog — it is the false positive, and that is what the allowlist is for.
//
// It fails rather than warns. A warning inside a CI gate is read once, by the
// person who added it. The pattern set is small enough, and the allowlist cheap
// enough, that failing is the honest setting.
type tellRule struct {
	// ID is what an allowlist entry names.
	ID string
	// Why is what the failure tells the writer to do instead.
	Why string
	RE  *regexp.Regexp
	// NotFollowedBy vetoes a match whose following text matches it. RE2 has no
	// lookahead and one rule needs one: "rather" redefines the subject ("not a
	// cache — rather, a queue") but "rather than" is ordinary English.
	NotFollowedBy *regexp.Regexp
}

// brochureAdjectives are the words a marketing page reaches for. Three in a row
// is the "fast, simple, and powerful" triad.
const brochureAdjectives = `fast|simple|powerful|seamless|robust|elegant|intuitive|modern|flexible|scalable|lightweight|reliable|clean|efficient|effortless|blazing|comprehensive`

var tellRules = []tellRule{
	{
		ID: "contrast-definition",
		Why: "the \"not X — it's Y\" reveal. Cut the negated half and state what it is: " +
			"a reader who never held the wrong idea does not need it corrected",
		// Deliberately narrow. The follower has to be a pronoun copula or a
		// bare "rather" — the shape that redefines the subject. Plain "but" is
		// excluded: "requests are not blocked — but shutdown's final flush is"
		// is ordinary English and already appears in the corpus.
		RE:            regexp.MustCompile(`(?i)\b(?:is|are|was|were|do|does)?\s*n(?:'|’)?o?t\s+(?:a|an|the|just|merely|only|simply|about)\b[^.;!?|]{2,70}?\s*(?:—|--|–|,|;)\s*(?:it(?:'|’)?s|it is|they(?:'|’)?re|they are|that(?:'|’)?s|this is|rather)`),
		NotFollowedBy: regexp.MustCompile(`(?i)^\s+than\b`),
	},
	{
		ID:  "not-about",
		Why: "\"it's not about X\" frames the reader's question as wrong. Answer the question",
		RE:  regexp.MustCompile(`(?i)\b(?:it(?:'|’)?s|it is|this is|that(?:'|’)?s|the point is|the question is)\s+n(?:'|’)?o?t\s+about\b`),
	},
	{
		ID:  "brochure-word",
		Why: "marketing vocabulary. Say what it does",
		RE:  regexp.MustCompile(`(?i)\b(?:seamless(?:ly)?|effortless(?:ly)?|delv(?:e|es|ed|ing)|game[- ]?chang(?:er|ing)|cutting[- ]edge|best[- ]in[- ]class|deep dive|dive into|plethora|myriad|tapestry|a testament to|the realm of|unlock the (?:power|potential)|at its core|in today(?:'|’)?s\s+\w+\s+landscape)\b`),
	},
	{
		ID:  "adjective-triad",
		Why: "three adjectives in a row is a slogan. Keep the one that is load-bearing",
		RE:  regexp.MustCompile(`(?i)\b(?:` + brochureAdjectives + `),\s+\w+,\s+(?:and|or)\s+\w+`),
	},
	{
		ID:  "hedge-opener",
		Why: "throat-clearing in front of the sentence that matters. Delete the opener",
		RE:  regexp.MustCompile(`(?i)(?:^|[.;]\s+)\s*(?:it(?:'|’)?s worth noting that|it is worth noting that|it(?:'|’)?s important to note that|it is important to note that|as (?:we|you) (?:can see|mentioned earlier))`),
	},
}

// AllowlistPath is where the exceptions live. Under docs/dev/ because it is a
// contributor file, not a page.
const AllowlistPath = "docs/dev/llm-tells-allowlist.txt"

// AllowEntry is one line of the tells allowlist: a page, and the exact matched
// text that is allowed to stand on it.
type AllowEntry struct {
	Path  string
	Match string
}

// ParseAllowlist reads the tells allowlist file.
//
// One entry per line, `<docs path>: <the exact matched text>`. Blank lines and
// `#` comments are ignored. Pinning an allowance to the exact text rather than
// to the rule keeps it attached to the sentence it was argued for: rewrite the
// sentence and the allowance stops applying, which is what you want from an
// exception granted once.
func ParseAllowlist(contents string) []AllowEntry {
	var out []AllowEntry
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		path, match, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		match = strings.TrimSpace(match)
		if match == "" {
			continue
		}
		out = append(out, AllowEntry{Path: strings.TrimSpace(path), Match: match})
	}
	return out
}

type tellHit struct {
	rule tellRule
	text string
}

// lineHits returns every rule match on one line of visible Markdown, with the
// matched text normalised to single spaces so an allowlist entry is one line.
func lineHits(line string) []tellHit {
	if line == "" {
		return nil
	}
	var out []tellHit
	for _, rule := range tellRules {
		for _, span := range rule.RE.FindAllStringIndex(line, -1) {
			if rule.NotFollowedBy != nil && rule.NotFollowedBy.MatchString(line[span[1]:]) {
				continue
			}
			out = append(out, tellHit{rule: rule, text: strings.Join(strings.Fields(line[span[0]:span[1]]), " ")})
		}
	}
	return out
}

// checkTells scans one page for the tells, skipping fenced code — a sample log
// line or a config value is not prose and is not the writer's.
func checkTells(doc Doc, allow []AllowEntry) []Problem {
	allowed := map[string]bool{}
	for _, a := range allow {
		if a.Path == doc.Path || a.Path == "*" {
			allowed[strings.ToLower(a.Match)] = true
		}
	}

	var problems []Problem
	for i, line := range eachOutsideFences(strings.Split(doc.Body, "\n")) {
		for _, hit := range lineHits(line) {
			if allowed[strings.ToLower(hit.text)] {
				continue
			}
			problems = append(problems, Problem{
				Path: doc.Path,
				Line: doc.BodyLineOffset + i + 1,
				Msg: fmt.Sprintf("%s: %q — %s. If it is genuinely the right wording, add this line to %s:\n\t\t%s: %s",
					hit.rule.ID, hit.text, hit.rule.Why, AllowlistPath, doc.Path, hit.text),
			})
		}
	}
	return problems
}

// tellHits reports every allowlist key this page's text matches, so
// checkAllowlistIsStillNeeded can tell a live exception from a dead one.
func tellHits(doc Doc) map[string]bool {
	out := map[string]bool{}
	for _, line := range eachOutsideFences(strings.Split(doc.Body, "\n")) {
		for _, hit := range lineHits(line) {
			out[doc.Path+"\x00"+strings.ToLower(hit.text)] = true
		}
	}
	return out
}

// checkAllowlistIsStillNeeded fails on an allowlist line that no longer matches
// anything, so a rewritten sentence takes its exception with it.
func checkAllowlistIsStillNeeded(allow []AllowEntry, used map[string]bool) []Problem {
	var stale []string
	for _, a := range allow {
		if a.Path == "*" {
			continue
		}
		if !used[a.Path+"\x00"+strings.ToLower(a.Match)] {
			stale = append(stale, a.Path+": "+a.Match)
		}
	}
	if len(stale) == 0 {
		return nil
	}
	sort.Strings(stale)
	return []Problem{{
		Path: AllowlistPath,
		Msg: fmt.Sprintf("%s match nothing any more; delete them:\n\t\t%s",
			plural(len(stale), "allowlist entry", "allowlist entries"), strings.Join(stale, "\n\t\t")),
	}}
}
