package docssearch

import (
	"errors"
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

// Entry is one document plus its own per-term scores, as ScoreDocument
// computed them. It is the unit NewIndex consumes; internal/docsindex builds
// one per published page from the docs the binary embeds.
type Entry struct {
	Document
	Terms map[string]int
}

// Posting records a precomputed relevance score for a term in a document.
type Posting struct {
	Doc   int
	Score int
}

// Index answers queries over a corpus of documentation pages.
//
// It is document-major going in — one Entry per page, each carrying its own
// term scores — and term-major in memory: NewIndex inverts it so a query walks
// only the documents that hold its terms rather than every page.
//
// That inversion used to run over a JSONL artifact generated from docs/ and
// committed to the repository. The artifact was a sorted manifest, one line per
// page, and every docs pull request rewrote part of it: a branch that added a
// sub-page and a branch that edited a neighbouring page produced overlapping
// hunks, so git reported a conflict in a file nobody had written by hand and
// the only resolution was to regenerate. It now builds from the docs the binary
// already embeds (internal/docsindex), so there is nothing to regenerate, keep
// in step, or merge.
type Index struct {
	docs     []Document
	postings map[string][]Posting
	err      error
}

// NewIndex inverts entries into a searchable index. An empty corpus produces an
// index that reports itself unavailable rather than one that answers every
// query with no matches — see Unavailable.
func NewIndex(entries []Entry) *Index {
	idx := &Index{postings: map[string][]Posting{}}
	for _, entry := range entries {
		id := len(idx.docs)
		doc := entry.Document
		doc.ID = id
		idx.docs = append(idx.docs, doc)
		for term, score := range entry.Terms {
			idx.postings[term] = append(idx.postings[term], Posting{Doc: id, Score: score})
		}
	}
	if len(idx.docs) == 0 {
		return Unavailable(errors.New("no documentation is embedded in this build"))
	}
	return idx
}

// Unavailable returns an index that serves no results and reports err as the
// reason. Callers build one when the corpus cannot be read at all, so the docs
// search endpoint can say so instead of answering every query with an empty
// result set — which looks like a working search over an empty corpus.
func Unavailable(err error) *Index { return &Index{err: err} }

// Err reports why the index cannot serve searches, or nil when it can. An index
// that built but holds no documents is one of the failures, not a third state.
func (idx *Index) Err() error { return idx.err }

// Documents lists the indexed pages, in the order they were supplied.
func (idx *Index) Documents() []Document { return idx.docs }
