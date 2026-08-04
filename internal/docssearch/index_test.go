package docssearch

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// publishedDocs walks the real docs/ tree the way scripts/docs-index.go does.
func publishedDocs(t *testing.T) []string {
	t.Helper()
	root := filepath.Join("..", "..", "docs")
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		slash := filepath.ToSlash(rel)
		if d.IsDir() && (slash == "plans" || slash == "dev") {
			return filepath.SkipDir
		}
		if !d.IsDir() && filepath.Ext(path) == ".md" {
			out = append(out, slash)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk docs/: %v", err)
	}
	return out
}

func TestIndex_loads(t *testing.T) {
	// Given: the committed index is embedded in this package.

	// When: it is parsed.
	// Then: it parsed cleanly and holds documents.
	if err := Unavailable(); err != nil {
		t.Fatalf("docs index failed to load: %v", err)
	}
	if len(index().docs) == 0 {
		t.Fatal("docs index is empty")
	}
}

// TestIndex_coversEveryPublishedDoc is the check a byte-for-byte staleness
// comparison cannot make: a generator that silently drops a doc drops it from
// the committed artifact too, so regeneration still matches. It also catches a
// new docs/ subdirectory that the generator picks up but embed.go's pattern in
// the repo root does not, which would leave the page searchable but unopenable.
func TestIndex_coversEveryPublishedDoc(t *testing.T) {
	// Given: the published docs on disk.
	want := publishedDocs(t)

	// When: we compare them with the indexed documents.
	indexed := map[string]bool{}
	for _, doc := range index().docs {
		indexed[doc.Href] = true
	}

	// Then: every published doc is indexed, and nothing else is.
	for _, href := range want {
		if !indexed[href] {
			t.Errorf("docs/%s is published but missing from the index; run: make docs-index", href)
		}
	}
	if len(indexed) != len(want) {
		t.Errorf("index holds %d docs, docs/ publishes %d; run: make docs-index", len(indexed), len(want))
	}
}

func TestIndex_everyDocumentIsUsable(t *testing.T) {
	// Given: the loaded index.

	// When/Then: every entry can render a result and resolve to a real file.
	for _, doc := range index().docs {
		switch {
		case doc.Title == "":
			t.Errorf("%s: no title", doc.Path)
		case doc.Section == "":
			t.Errorf("%s: no section", doc.Path)
		case doc.Href == "" || strings.HasPrefix(doc.Href, "/"):
			t.Errorf("%s: unusable href %q", doc.Path, doc.Href)
		case doc.Path != "docs/"+doc.Href:
			t.Errorf("%s: href %q does not match path", doc.Path, doc.Href)
		}
		if _, err := os.Stat(filepath.Join("..", "..", filepath.FromSlash(doc.Path))); err != nil {
			t.Errorf("%s: indexed but not on disk: %v", doc.Path, err)
		}
	}
}

func TestParseIndex_rejectsMalformedInput(t *testing.T) {
	// Given: index lines that a generator bug could produce.
	cases := map[string]string{
		"empty":              "# header only\n",
		"bad json":           `{"path":"docs/x.md",`,
		"term without score": `{"path":"docs/x.md","href":"x.md","terms":"lambda"}`,
		"non-numeric score":  `{"path":"docs/x.md","href":"x.md","terms":"lambda:high"}`,
	}

	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			// When: the index is parsed.
			idx := parseIndex([]byte(source))

			// Then: it reports unavailable rather than searching a partial corpus.
			if idx.err == nil {
				t.Fatalf("expected an error, got %d documents", len(idx.docs))
			}
		})
	}
}

// BenchmarkParseIndex measures what the first search of a process pays to
// build the inverted index. It is off the startup path deliberately — a run
// that never opens the console docs never pays it — so this is first-query
// latency, and it is the reason the artifact stores precomputed term scores
// rather than making the binary re-read and re-tokenize the markdown.
func BenchmarkParseIndex(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if idx := parseIndex(indexSource); idx.err != nil {
			b.Fatal(idx.err)
		}
	}
}

func TestParseIndex_keepsColonsInsideTerms(t *testing.T) {
	// Given: a term containing a colon, which the tokenizer preserves.
	source := `{"path":"docs/x.md","href":"x.md","title":"X","section":"S","terms":"logs:20140328:9 lambda:3"}`

	// When: the index is parsed.
	idx := parseIndex([]byte(source))

	// Then: the term keeps its colon and only the trailing number is the score.
	if idx.err != nil {
		t.Fatalf("parse: %v", idx.err)
	}
	postings := idx.postings["logs:20140328"]
	if len(postings) != 1 || postings[0].Score != 9 {
		t.Fatalf("expected one posting scored 9, got %+v", postings)
	}
}
