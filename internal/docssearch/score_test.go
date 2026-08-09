package docssearch

import (
	"encoding/json"
	"strings"
	"testing"
)

// syntheticIndex builds a searchable index from documents described field by
// field, through the same scorer and the same "term:score" encoding the
// generator writes. Ranking questions are about the *shape* of a corpus — how
// often a word appears, and in which field — so they are asked here rather
// than against docs/, where the answer changes whenever someone edits a page.
func syntheticIndex(t *testing.T, docs ...DocumentFields) *searchIndex {
	t.Helper()
	var b strings.Builder
	for _, doc := range docs {
		href := strings.ToLower(strings.ReplaceAll(doc.Title, " ", "-")) + ".md"
		line, err := json.Marshal(indexEntry{
			Path:    "docs/" + href,
			Href:    href,
			Title:   doc.Title,
			Section: doc.Section,
			Tags:    doc.Tags,
			Terms:   FormatTerms(ScoreDocument(doc)),
		})
		if err != nil {
			t.Fatalf("marshal %s: %v", doc.Title, err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	idx := parseIndex([]byte(b.String()))
	if idx.err != nil {
		t.Fatalf("parse synthetic index: %v", idx.err)
	}
	return idx
}

func topHref(t *testing.T, idx *searchIndex, query string) string {
	t.Helper()
	results := idx.search(query, 5)
	if len(results) == 0 {
		t.Fatalf("%q: expected results", query)
	}
	return results[0].Href
}

// TestSearch_headingOutranksRepeatedBodyMentions pins the rule that field
// weights are worth nothing unless repetition is bounded: a page that happens
// to use two ordinary English words a dozen times in passing must not outrank
// the page that names the operation in a heading.
func TestSearch_headingOutranksRepeatedBodyMentions(t *testing.T) {
	// Given: a page that mentions the query's words repeatedly in body prose,
	// in an unrelated sense, and a page that carries them once in a heading.
	incidental := DocumentFields{
		Title:   "Lambda",
		Section: "Service Reference",
		Body: strings.Repeat("Containers run in live mode. ", 12) +
			strings.Repeat("The tail of the queue drains first. ", 5),
	}
	operation := DocumentFields{
		Title:    "CloudWatch Logs",
		Section:  "Service Reference",
		Headings: []Heading{{Depth: 2, Text: "StartLiveTail"}},
		Body:     "Streams events to a session over the event-stream protocol.",
	}
	idx := syntheticIndex(t, incidental, operation)

	// When: the operation is searched for by its words.
	// Then: the page whose heading names it ranks first.
	if got := topHref(t, idx, "live tail"); got != "cloudwatch-logs.md" {
		t.Fatalf("expected the heading match first, got %q", got)
	}
}

// TestSearch_operationNameInCodeOutranksIncidentalProse reproduces the shape
// that made "live tail" rank docs/services/lambda.md over
// docs/services/cloudwatch-logs.md: the Lambda page uses "live" six times in
// body prose about EFS mounts in live mode and once in a heading that happens
// to end in the word, while the CloudWatch Logs page holds StartLiveTail once,
// as an inline code span in its endpoints table. Summed occurrence counts made
// the volume of weak evidence win.
func TestSearch_operationNameInCodeOutranksIncidentalProse(t *testing.T) {
	// Given: the two documents in that shape.
	incidental := DocumentFields{
		Title:    "Lambda",
		Section:  "Service Reference",
		Headings: []Heading{{Depth: 2, Text: "Deleting a version, and where tags live"}},
		Body: "EFS mounts in live mode. Memory is budgeted across live containers. " +
			"These operations live under the versioned path. Reports live allocation. " +
			"Aggregate budget for live containers. Only live aliases resolve. " +
			"Retries tail off after the third attempt. The tail of the batch is " +
			"dropped. A tail latency spike is expected.",
	}
	operation := DocumentFields{
		Title:    "CloudWatch Logs",
		Section:  "Service Reference",
		Headings: []Heading{{Depth: 2, Text: "Endpoints"}},
		Code:     []string{"StartLiveTail"},
		Body:     "Supported. AWS event-stream response with a live session.",
	}
	idx := syntheticIndex(t, incidental, operation)

	// When: the operation is searched for by its words.
	// Then: the page that owns the operation ranks first.
	if got := topHref(t, idx, "live tail"); got != "cloudwatch-logs.md" {
		t.Fatalf("expected the operation's own page first, got %q", got)
	}
}

func TestScoreDocument_weightsFieldsAboveBody(t *testing.T) {
	// Given: the same word in one field of each of several documents.
	// When/Then: each field scores its declared weight for a single mention.
	for _, tc := range []struct {
		name  string
		doc   DocumentFields
		want  int
		token string
	}{
		{"title", DocumentFields{Title: "Firehose"}, weightTitle, "firehose"},
		{"tags", DocumentFields{Tags: []string{"firehose"}}, weightTags, "firehose"},
		{"h1", DocumentFields{Headings: []Heading{{Depth: 1, Text: "Firehose"}}}, weightHeading1, "firehose"},
		{"h2", DocumentFields{Headings: []Heading{{Depth: 2, Text: "Firehose"}}}, weightHeading, "firehose"},
		{"section", DocumentFields{Section: "Firehose"}, weightSection, "firehose"},
		{"code", DocumentFields{Code: []string{"Firehose"}}, weightCode, "firehose"},
		{"description", DocumentFields{Description: "Firehose"}, weightDescription, "firehose"},
		{"body", DocumentFields{Body: "Firehose"}, weightBody, "firehose"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ScoreDocument(tc.doc)[tc.token]; got != tc.want {
				t.Fatalf("scored %d, want %d", got, tc.want)
			}
		})
	}
}

// TestScoreDocument_saturatesRepeatedOccurrences is the property the ranking
// fix rests on. Body repetition still helps, but with diminishing returns, so
// no amount of it turns a passing mention into the strongest signal on a page.
// Fifteen passing mentions stay under one heading; it takes sixteen to draw
// level, and that is a page the query genuinely is about.
func TestScoreDocument_saturatesRepeatedOccurrences(t *testing.T) {
	// Given: the same word repeated in body prose a growing number of times.
	// When/Then: the score grows sub-linearly.
	for _, tc := range []struct{ repeats, want int }{
		{1, 1}, {2, 2}, {3, 2}, {4, 3}, {7, 3}, {8, 4}, {15, 4}, {16, 5},
	} {
		got := ScoreDocument(DocumentFields{Body: strings.Repeat("firehose ", tc.repeats)})["firehose"]
		if got != tc.want {
			t.Errorf("%d body mentions scored %d, want %d", tc.repeats, got, tc.want)
		}
		if tc.repeats < 16 && got >= weightHeading {
			t.Errorf("%d body mentions scored %d, which reaches heading weight %d", tc.repeats, got, weightHeading)
		}
	}
}

// TestFormatTerms_roundTripsThroughTheIndexReader keeps the writer and the
// reader of the "term:score" encoding honest about the awkward case: a term
// may itself contain a colon, so the pair splits on its last one.
func TestFormatTerms_roundTripsThroughTheIndexReader(t *testing.T) {
	// Given: scored terms, one of which contains a colon.
	scores := map[string]int{"logs:20140328": 9, "lambda": 3}

	// When: they are formatted and parsed back.
	idx := &searchIndex{postings: map[string][]Posting{}}
	if err := idx.addTerms(0, FormatTerms(scores)); err != nil {
		t.Fatalf("addTerms: %v", err)
	}

	// Then: every term survives with its score.
	for term, want := range scores {
		postings := idx.postings[term]
		if len(postings) != 1 || postings[0].Score != want {
			t.Errorf("%q: got %+v, want one posting scored %d", term, postings, want)
		}
	}
}
