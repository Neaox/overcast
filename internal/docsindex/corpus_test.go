package docsindex_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/overcast-sh/overcast/internal/docsindex"
	"github.com/overcast-sh/overcast/internal/docssearch"
)

// idx is the real corpus: the published docs on disk, collected and indexed the
// same way internal/bff does it from the docs the binary embeds. Built once per
// test binary, because every test below asks about the same corpus.
//
// These tests used to live in internal/docssearch, over a JSONL index generated
// from docs/ and committed. There is no artifact any more, so "is the committed
// index stale?" is not a question that can be asked — and does not need to be.
var idxOnce = sync.OnceValue(func() *docssearch.Index {
	docs, err := docsindex.Collect(os.DirFS(filepath.Join("..", "..", "docs")))
	if err != nil {
		return docssearch.Unavailable(err)
	}
	return docssearch.NewIndex(docsindex.SearchEntries(docs))
})

func idx(t *testing.T) *docssearch.Index {
	t.Helper()
	return idxOnce()
}

// publishedDocs walks the real docs/ tree the way Collect does.
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
	// Given: the published docs on disk, which are the same set embed.go
	// embeds into the binary.

	// When: they are collected and indexed.
	// Then: the index built cleanly and holds documents.
	if err := idx(t).Err(); err != nil {
		t.Fatalf("docs index failed to build: %v", err)
	}
	if len(idx(t).Documents()) == 0 {
		t.Fatal("docs index is empty")
	}
}

// TestIndex_coversEveryPublishedDoc catches an indexer that silently drops a
// page, and a new docs/ subdirectory this package picks up but embed.go's
// pattern in the repo root does not — which would leave the page searchable but
// unopenable.
func TestIndex_coversEveryPublishedDoc(t *testing.T) {
	// Given: the published docs on disk.
	want := publishedDocs(t)

	// When: we compare them with the indexed documents.
	indexed := map[string]bool{}
	for _, doc := range idx(t).Documents() {
		indexed[doc.Href] = true
	}

	// Then: every published doc is indexed, and nothing else is.
	for _, href := range want {
		if !indexed[href] {
			t.Errorf("docs/%s is published but missing from the index", href)
		}
	}
	if len(indexed) != len(want) {
		t.Errorf("index holds %d docs, docs/ publishes %d", len(indexed), len(want))
	}
}

func TestIndex_everyDocumentIsUsable(t *testing.T) {
	// Given: the loaded index.

	// When/Then: every entry can render a result and resolve to a real file.
	for _, doc := range idx(t).Documents() {
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

func TestSearch_cdkLocalVpc(t *testing.T) {
	// Given: the index built from the docs the binary embeds.

	// When: we search for the local VPC CDK workflow.
	results := idx(t).Search("cdk local vpc provider", 5)

	// Then: the focused local VPC guide is ranked first.
	if len(results) == 0 {
		t.Fatal("expected search results")
	}
	if results[0].Href != "cdk/local-vpc.md" {
		t.Fatalf("expected local VPC guide first, got %q", results[0].Href)
	}
}

func TestSearch_ignoresStopwords(t *testing.T) {
	// Given: a query containing only stopwords.

	// When: we search.
	results := idx(t).Search("the and of", 5)

	// Then: no documents are returned.
	if len(results) != 0 {
		t.Fatalf("expected no results, got %d", len(results))
	}
}

// TestSearch_servicePageOutranksAGeneratedListing guards a corpus-wide
// consequence of weighting by field: a generated listing beating the page that
// documents the thing it lists. A listing is a row per operation, so it mentions
// a service name as often as the service's own page does — and summed occurrence
// counts hand it the win on bulk, however little it explains.
//
// The live instance is each service's operations table, held above by
// docssearch's promoteServiceLandingPages rule. This is the corpus-wide check
// that the queries a reader actually types land on the page that answers them.
// It was written for docs/operation-manifest.md, a tree-wide listing of every
// typed operation, deleted once it turned out to have no reader; the queries
// outlived it because what they pin is the ranking, not that page. (This used
// to have a TypeScript mirror in the vite dev server's Hono BFF; that port was
// retired in #1104, so this is the only copy of the guard.)
func TestSearch_servicePageOutranksAGeneratedListing(t *testing.T) {
	// Given: queries naming a service that a generated listing also names.
	for query, want := range map[string]string{
		"msk cluster":         "services/msk.md",
		"opensearch domain":   "services/opensearch.md",
		"autoscaling group":   "services/autoscaling.md",
		"log group retention": "services/cloudwatch-logs.md",
	} {
		// When: each is searched for.
		results := idx(t).Search(query, 5)

		// Then: the service's own page ranks above the generated listing.
		if len(results) == 0 {
			t.Errorf("%q: expected search results", query)
			continue
		}
		if results[0].Href != want {
			t.Errorf("%q: ranked %q first, want %q", query, results[0].Href, want)
		}
	}
}

// TestSearch_camelCaseOperationWords checks that an operation is findable by
// the English words inside its CamelCase name, which is what tokenize's
// identifier splitting exists for.
//
// What is asserted is that the operations page is reachable by those words at
// all, not that it ranks first: whether the landing page also matches is a
// property of that page's prose, which is rewritten from time to time, and
// promoteServiceLandingPages puts the landing above its own sub-page whenever
// both are in the results. Pinning first place would make this test a fixture
// check on one paragraph of CloudWatch Logs documentation rather than a check
// on tokenize.
func TestSearch_camelCaseOperationWords(t *testing.T) {
	// Given: the index built from the docs the binary embeds.

	// When: we search for words from a CamelCase operation.
	results := idx(t).Search("live tail", 5)

	// Then: the CloudWatch Logs StartLiveTail documentation is discoverable.
	if len(results) == 0 {
		t.Fatal("expected search results")
	}
	found := false
	for _, r := range results {
		if r.Href == "services/cloudwatch-logs/operations.md" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected the CloudWatch Logs operations page in the results, got %v", hrefs(results))
	}
}

func hrefs(results []docssearch.Result) []string {
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.Href
	}
	return out
}

// TestSearch_serviceLandingPageOutranksItsOwnOperationsTable is the guard
// above, narrowed to the one generated listing the corpus still has: the
// operations table sitting directly beneath each service page.
func TestSearch_serviceLandingPageOutranksItsOwnOperationsTable(t *testing.T) {
	// Given: a query both the landing page and its operations table match.
	// When: it is searched for.
	results := idx(t).Search("log group retention", 5)

	// Then: the page that explains the behaviour comes first, and the table is
	// still in the results behind it.
	if len(results) == 0 {
		t.Fatal("expected search results")
	}
	if results[0].Href != "services/cloudwatch-logs.md" {
		t.Fatalf("ranked %q first, want services/cloudwatch-logs.md", results[0].Href)
	}
	found := false
	for _, r := range results {
		if r.Href == "services/cloudwatch-logs/operations.md" {
			found = true
		}
	}
	if !found {
		t.Error("the operations table should still be in the results, just not first")
	}
}

// BenchmarkBuildIndex measures what the first docs request of a process pays:
// parsing every published page and inverting it into a searchable index.
//
// It is off the startup path deliberately — a run that never opens the console
// docs never pays it — so this is first-request latency, and it is the number
// that decides whether deriving the corpus at runtime is cheaper than keeping a
// generated artifact in the repository. The artifact it replaced cost merge
// conflicts on every concurrent docs branch; see internal/docsindex's header.
func BenchmarkBuildIndex(b *testing.B) {
	b.ReportAllocs()
	root := os.DirFS(filepath.Join("..", "..", "docs"))
	for b.Loop() {
		docs, err := docsindex.Collect(root)
		if err != nil {
			b.Fatal(err)
		}
		if idx := docssearch.NewIndex(docsindex.SearchEntries(docs)); idx.Err() != nil {
			b.Fatal(idx.Err())
		}
	}
}
