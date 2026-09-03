package overcast_test

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast"
)

// TestDocsEmbed_coversEveryPublishedDoc is the guard on the //go:embed pattern
// in embed.go, and it has to live here because this is the only package that
// can see the embedded tree.
//
// The console serves docs out of DocsServicesFS while internal/docsindex builds
// the index — the nav, the search corpus — by walking docs/ on disk. The two
// disagree the moment a page lands in a directory the pattern does not name:
// the page is listed and searchable, and 404s when somebody opens it. Nothing
// inside internal/docsindex can catch that, because both halves of its own
// corpus test read from disk.
func TestDocsEmbed_coversEveryPublishedDoc(t *testing.T) {
	// Given: the docs the binary embeds.
	embedded := map[string]bool{}
	err := fs.WalkDir(overcast.DocsServicesFS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".md") {
			embedded[strings.TrimPrefix(p, "docs/")] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the embedded docs: %v", err)
	}
	// A slim build embeds nothing at all (embed_slim.go), which is a different
	// thing from embedding the wrong set.
	if len(embedded) == 0 {
		t.Skip("no docs embedded; this is a slim build")
	}

	// When: they are compared with the pages docs/ publishes.
	published := publishedDocs(t)

	// Then: the two sets are the same one.
	for _, rel := range published {
		if !embedded[rel] {
			t.Errorf("docs/%s is published but not embedded; add its directory to the //go:embed pattern in embed.go", rel)
		}
	}
	want := map[string]bool{}
	for _, rel := range published {
		want[rel] = true
	}
	var extra []string
	for rel := range embedded {
		if !want[rel] {
			extra = append(extra, rel)
		}
	}
	sort.Strings(extra)
	for _, rel := range extra {
		t.Errorf("docs/%s is embedded but not published; the //go:embed pattern is reaching an excluded tree", rel)
	}
}

// publishedDocs walks docs/ the way internal/docsindex does: every .md file
// outside the contributor-only trees.
func publishedDocs(t *testing.T) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir("docs", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel("docs", p)
		if relErr != nil {
			return relErr
		}
		slash := filepath.ToSlash(rel)
		if d.IsDir() && (slash == "plans" || slash == "dev") {
			return filepath.SkipDir
		}
		if !d.IsDir() && filepath.Ext(p) == ".md" {
			out = append(out, slash)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk docs/: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("no .md files found under docs/; this test is running from the wrong directory")
	}
	return out
}
