package docssearch

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// indexSource is the committed search index: one JSON line per published doc,
// in path order, holding that doc's result metadata and its own term scores.
//
// It is document-major on disk and term-major in memory. On disk that means a
// docs edit rewrites one line, so two branches that both touch docs/ merge
// instead of conflicting. In memory an inverted index answers queries without
// walking every document, which is what parseIndex reconstructs below — a few
// milliseconds of work, done once, on the first search rather than at startup,
// so a run that never opens the console docs never pays it.
//
//go:embed index.gen.jsonl
var indexSource []byte

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

// indexEntry is one line of index.gen.jsonl, as scripts/docs-index.go writes it.
type indexEntry struct {
	Path        string   `json:"path"`
	Href        string   `json:"href"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Section     string   `json:"section"`
	Tags        []string `json:"tags"`
	Terms       string   `json:"terms"`
}

type searchIndex struct {
	docs     []Document
	postings map[string][]Posting
	err      error
}

// index is built once, on first use. A failure is kept rather than retried:
// the input is embedded at compile time, so nothing about a second attempt
// would differ, and callers report the index as unavailable instead of
// quietly searching a corpus that is missing pages.
var index = sync.OnceValue(func() *searchIndex { return parseIndex(indexSource) })

func parseIndex(source []byte) *searchIndex {
	idx := &searchIndex{postings: map[string][]Posting{}}
	for n, raw := range bytes.Split(source, []byte("\n")) {
		line := bytes.TrimSpace(raw)
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		var entry indexEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return &searchIndex{err: fmt.Errorf("docs index line %d: %w", n+1, err)}
		}
		id := len(idx.docs)
		idx.docs = append(idx.docs, Document{
			ID:          id,
			Path:        entry.Path,
			Href:        entry.Href,
			Title:       entry.Title,
			Description: entry.Description,
			Section:     entry.Section,
			Tags:        entry.Tags,
		})
		if err := idx.addTerms(id, entry.Terms); err != nil {
			return &searchIndex{err: fmt.Errorf("docs index line %d (%s): %w", n+1, entry.Path, err)}
		}
	}
	if len(idx.docs) == 0 {
		return &searchIndex{err: errors.New("docs index is empty; run: make docs-index")}
	}
	return idx
}

// addTerms reads one document's "term:score" pairs. A term may itself contain
// a colon — normalizeSearchText in the generator keeps ':' as a word
// character, so "logs:20140328" is one term — hence the split on the last
// colon rather than the first.
func (idx *searchIndex) addTerms(doc int, terms string) error {
	for _, pair := range strings.Fields(terms) {
		sep := strings.LastIndexByte(pair, ':')
		if sep <= 0 || sep == len(pair)-1 {
			return fmt.Errorf("malformed term %q", pair)
		}
		score, err := strconv.Atoi(pair[sep+1:])
		if err != nil {
			return fmt.Errorf("malformed score in term %q: %w", pair, err)
		}
		idx.postings[pair[:sep]] = append(idx.postings[pair[:sep]], Posting{Doc: doc, Score: score})
	}
	return nil
}

// Unavailable reports why the index cannot serve searches, or nil when it can.
// Callers serving the docs search endpoint must distinguish this from a query
// with no matches: an unreadable index would otherwise answer every query with
// an empty result set, which looks like a working search over an empty corpus.
// An index that parsed but holds no documents is one of the failures, not a
// third state — parseIndex reports it as an error.
func Unavailable() error { return index().err }
