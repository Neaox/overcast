package docssearch

import (
	"fmt"
	"math/bits"
	"sort"
	"strings"
)

// Field weights. A term is worth more where the document declares what it is
// about than where it merely mentions it, so the same word scores differently
// depending on which field it came from.
//
// weightCode is the one that is not simply "prose, but more important". In
// this corpus inline code is the API surface: every service page lists its
// operations as `OperationName` in a table, and those names are what people
// search for. They are also the only place a name like StartLiveTail exists at
// all — tokenize splits it into "start live tail", which is what makes a
// CamelCase operation findable by its words. Scoring those words as ordinary
// prose meant the page that owns the operation lost to any page that happened
// to use the same English words a few times; see
// TestSearch_operationNameInCodeOutranksIncidentalProse.
const (
	weightTitle       = 10
	weightTags        = 8
	weightHeading1    = 7
	weightSection     = 6
	weightCode        = 6
	weightHeading     = 5
	weightDescription = 4
	weightBody        = 1
)

// Heading is one Markdown heading of a document, as the indexer found it.
type Heading struct {
	Depth int
	Text  string
}

// DocumentFields is one document's searchable content, split by the field it
// came from. The indexer (scripts/docs-index.go) fills this in; ScoreDocument
// turns it into the term scores that ship in index.gen.jsonl.
type DocumentFields struct {
	Title       string
	Tags        []string
	Section     string
	Headings    []Heading
	Description string
	// Code holds each inline code span's text. Fenced code blocks are Body:
	// a whole example is prose-like bulk, an inline span is a name.
	Code []string
	// Body is everything else — paragraphs, list items, table cells, and the
	// contents of fenced code blocks.
	Body string
}

// ScoreDocument computes the per-term scores for one document.
//
// Within a field, repeated occurrences count sub-linearly: the second mention
// of a word says much less than the first, and the tenth says almost nothing.
// Without that, field weights are advisory — a term mentioned eleven times in
// passing outscores the same term in a heading, which is how "live tail" came
// to rank a Lambda page (six body uses of "live mode", plus a heading ending
// in the word "live") above the CloudWatch Logs page that owns StartLiveTail.
// Saturating each field keeps quantity of weak evidence from beating quality
// of strong evidence.
func ScoreDocument(f DocumentFields) map[string]int {
	scores := map[string]int{}
	add := func(text string, weight int) {
		counts := map[string]int{}
		for _, token := range tokenize(text) {
			counts[token]++
		}
		for token, n := range counts {
			scores[token] += weight * saturate(n)
		}
	}
	add(f.Title, weightTitle)
	add(strings.Join(f.Tags, " "), weightTags)
	add(f.Section, weightSection)
	for _, heading := range f.Headings {
		weight := weightHeading
		if heading.Depth == 1 {
			weight = weightHeading1
		}
		add(heading.Text, weight)
	}
	add(f.Description, weightDescription)
	add(strings.Join(f.Code, "\n"), weightCode)
	add(f.Body, weightBody)
	return scores
}

// saturate maps an occurrence count to its diminishing-returns multiplier:
// 1 -> 1, 2-3 -> 2, 4-7 -> 3, 8-15 -> 4, and so on. It is 1+floor(log2(n)),
// the standard sub-linear term-frequency scale, computed in integers so the
// generated index stays exact and byte-reproducible across platforms.
func saturate(n int) int {
	if n <= 0 {
		return 0
	}
	return bits.Len(uint(n))
}

// FormatTerms renders term scores as the space-separated "term:score" pairs
// one index line carries, sorted by term so the artifact is reproducible. It
// is the inverse of searchIndex.addTerms, and lives beside it so the two
// cannot drift: a term may itself contain ':', which is why the reader splits
// on the last colon rather than the first.
func FormatTerms(scores map[string]int) string {
	names := make([]string, 0, len(scores))
	for term := range scores {
		names = append(names, term)
	}
	sort.Strings(names)
	var b strings.Builder
	for i, term := range names {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%s:%d", term, scores[term])
	}
	return b.String()
}
