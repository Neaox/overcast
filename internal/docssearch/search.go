package docssearch

import (
	"sort"
	"strings"
	"unicode"
)

// Document is the metadata returned for a searchable documentation page.
type Document struct {
	ID          int
	Path        string
	Href        string
	Title       string
	Description string
	Section     string
	Tags        []string
}

// Posting records a precomputed relevance score for a term in a document.
type Posting struct {
	Doc   int
	Score int
}

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

// Search returns ranked documentation matches from the generated inverted index.
func Search(query string, limit int) []Result {
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
		for _, posting := range postings[token] {
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
		if docID < 0 || docID >= len(docs) {
			continue
		}
		results = append(results, Result{Document: docs[docID], Score: score})
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
