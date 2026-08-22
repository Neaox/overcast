// Package docssearch serves search over the published docs/ tree.
//
// The corpus searched below is index.gen.jsonl, generated from docs/ and
// committed — this package embeds it, so a bare checkout has to build. After
// editing anything under docs/, regenerate with `make docs-index` (or `task
// docs-index`, or `go generate` here) and commit the result; CI fails on a
// stale index. See index.go for how it is loaded and why it is shaped that
// way.
//
//go:generate go run ../../scripts/docs-index.go --write-search-index
package docssearch

import (
	"sort"
	"strings"
	"unicode"
)

// Result is one ranked documentation search result.
type Result struct {
	Document
	Score int
}

var stopwords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true,
	"be": true, "by": true, "for": true, "from": true, "in": true, "into": true,
	"is": true, "it": true, "of": true, "on": true, "or": true, "the": true,
	"this": true, "to": true, "with": true,
}

// Search returns ranked documentation matches from the generated index. A
// document matches only when it holds every token in the query; its score is
// the sum of the per-term scores the generator weighted by field.
//
// The weighting happens once, at index time, in ScoreDocument — which field a
// term came from, and how often, with repetition counting sub-linearly. By the
// time a "term:score" pair is read here that judgement is already made, so
// this function only has to apply it. (A TypeScript port in the vite dev
// server's Hono BFF used to have to apply it identically; that mirror was
// retired in #1104 and the dev server now proxies /api/docs/search here.)
func Search(query string, limit int) []Result { return index().search(query, limit) }

// search is Search against a given index, so tests can rank a synthetic corpus
// rather than only the embedded one.
func (idx *searchIndex) search(query string, limit int) []Result {
	if limit <= 0 {
		limit = 10
	}
	tokens := uniqueTokens(tokenize(query))
	if len(tokens) == 0 {
		return []Result{}
	}
	scores := map[int]int{}
	hits := map[int]int{}
	for _, token := range tokens {
		for _, posting := range idx.postings[token] {
			scores[posting.Doc] += posting.Score
			hits[posting.Doc]++
		}
	}
	if len(scores) == 0 {
		return []Result{}
	}
	results := make([]Result, 0, len(scores))
	for docID, score := range scores {
		if hits[docID] != len(tokens) {
			continue
		}
		if docID < 0 || docID >= len(idx.docs) {
			continue
		}
		results = append(results, Result{Document: idx.docs[docID], Score: score})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].Title < results[j].Title
		}
		return results[i].Score > results[j].Score
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results
}

func tokenize(s string) []string {
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
	fields := strings.Fields(b.String())
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.Trim(field, "-_:/. ")
		if len(field) < 2 || stopwords[field] {
			continue
		}
		out = append(out, field)
	}
	return out
}

func uniqueTokens(tokens []string) []string {
	seen := make(map[string]bool, len(tokens))
	out := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if seen[token] {
			continue
		}
		seen[token] = true
		out = append(out, token)
	}
	return out
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
