package docslint

import (
	"sort"
	"strings"
	"testing"
)

// guide is a published page outside docs/services/, where only the length and
// tells rules apply — the shape most of these tests want.
func guide(body string) Doc { return Doc{Path: "docs/demo-guide.md", Body: body} }

// prose returns a page whose measured prose is at least n characters, built out
// of paragraphs so nothing else in the linter has an opinion about it.
func prose(n int) string {
	var b strings.Builder
	b.WriteString("# Demo\n\n")
	for b.Len() < n+16 {
		b.WriteString("Overcast serves the request from the emulated backend and returns the response the SDK expects.\n\n")
	}
	return b.String()
}

// table returns a page that is almost entirely a GFM table: long as a page,
// nearly free of prose, which is the shape the page budget exists to catch.
func table(rows int) string {
	var b strings.Builder
	b.WriteString("# Demo\n\n| Variable | Default | Effect |\n| --- | --- | --- |\n")
	for i := 0; i < rows; i++ {
		b.WriteString("| `OVERCAST_SOME_SETTING_NAME` | `unset` | changes how the request is routed to the emulated backend |\n")
	}
	return b.String()
}

func TestMeasure_countsProseAndExcludesCodeAndTables(t *testing.T) {
	// Given: a page whose visible bulk is a code block and a table.
	body := "# Demo\n\nShort intro.\n\n```bash\n" + strings.Repeat("echo a very long command line here\n", 40) +
		"```\n\n| A | B |\n| - | - |\n" + strings.Repeat("| a long cell value | another long cell value |\n", 40)

	// When: it is measured.
	got := Measure(body)

	// Then: the prose is just the heading and the intro, and the page is not.
	if got.Prose > 40 {
		t.Errorf("prose = %d, want the heading and one short sentence only", got.Prose)
	}
	if got.Page < 3000 {
		t.Errorf("page = %d, want the code and table counted", got.Page)
	}
}

func TestMeasure_excludesTheGeneratedBlock(t *testing.T) {
	// Given: a page that is nothing but the block cmd/capgen writes — the shape
	// of every docs/services/<key>/operations.md.
	body := BeginMarker + "\n\n# S3 operations\n\n45 of 53 listed operations are implemented.\n\n" +
		strings.Repeat("| `GetObject` | GET | ✅ Supported | returns the stored object bytes |\n", 400) +
		"\n" + EndMarker + "\n"

	// When: it is measured.
	got := Measure(body)

	// Then: a generated page passes on its own measurements, not by being named
	// in an exemption list.
	if got.Prose > 40 || got.Page > 40 {
		t.Fatalf("generated page measured %d/%d, want ~0 — the block must not count", got.Prose, got.Page)
	}
	assertClean(t, Check([]Doc{{Path: "docs/services/demo/operations.md", Body: body}}))
}

func TestMeasure_excludesEveryGeneratedBlockNotOnlyCapabilities(t *testing.T) {
	// Given: a short page carrying a generated block written by something other
	// than the capabilities generator — the service-name table in
	// docs/configuration.md is the real case.
	body := "# Config\n\nShort intro.\n\n<!-- BEGIN overcast:service-names -->\n" +
		strings.Repeat("| `s3` | Amazon S3 | on |\n", 400) +
		"<!-- END overcast:service-names -->\n\n## After\n\nOne more line.\n"

	// When / Then: the author is not charged for content they cannot split.
	if got := Measure(body); got.Page > 100 {
		t.Fatalf("page = %d, want the generated block excluded", got.Page)
	}
	assertClean(t, Check([]Doc{guide(body)}))
}

func TestCheck_rejectsAWallOfProse(t *testing.T) {
	// Given: a page well over the prose budget.
	// When / Then: it is told to split, and told how to opt out.
	problems := Check([]Doc{guide(prose(MaxProseChars + 2000))})
	assertReports(t, problems, "characters of prose, over the 6000 budget")
	assertReports(t, problems, LengthReviewMarker)
}

func TestCheck_rejectsAMegaReferenceThatIsAllTable(t *testing.T) {
	// Given: a page with almost no prose and an enormous table — the shape that
	// would slip past a prose-only budget.
	body := table(200)

	// When / Then: the prose budget is silent and the page budget is not.
	if got := Measure(body); got.Prose > MaxProseChars {
		t.Fatalf("fixture has %d characters of prose; it is meant to pass the prose budget", got.Prose)
	}
	assertReports(t, Check([]Doc{guide(body)}), "over the 12000 page budget")
}

func TestCheck_acceptsAnOverLongPageThatSaysWhy(t *testing.T) {
	// Given: a long page carrying the deliberate opt-out with a real reason.
	body := prose(MaxProseChars+2000) +
		"\n<!-- " + LengthReviewMarker + " every environment variable in one table; splitting it by area would make readers guess which page a variable is on -->\n"

	// When / Then: a considered decision is allowed to stand.
	assertClean(t, Check([]Doc{guide(body)}))
}

func TestCheck_rejectsAnOptOutThatGivesNoReason(t *testing.T) {
	// Given: the marker used as a mute button.
	body := prose(MaxProseChars+2000) + "\n<!-- " + LengthReviewMarker + " it is long -->\n"

	// When / Then: the gesture is refused, and the budget failure is not
	// reported twice — the writer is told the one thing to fix.
	problems := Check([]Doc{guide(body)})
	assertReports(t, problems, "gives no reason worth the name")
	if strings.Contains(messages(problems), "over the 6000 budget") {
		t.Errorf("expected only the reason complaint, got:\n%s", messages(problems))
	}
}

func TestCheck_rejectsAnOptOutOnAPageThatIsNowShort(t *testing.T) {
	// Given: a page that was rewritten but kept its excuse.
	body := "# Demo\n\nShort now.\n\n<!-- " + LengthReviewMarker + " this reason was true before the page was split into four -->\n"

	// When / Then: the excuse cannot outlive its reason.
	assertReports(t, Check([]Doc{guide(body)}), "but is inside the budget")
}

// backlogged returns one path from LengthBacklog. The list only shrinks, so a
// hard-coded path would turn finishing that page into a failure in tests that
// have nothing to do with it.
// It picks a guide rather than a service page, so the service-template rules
// stay out of a test that is about the budget, and it picks deterministically
// so a failure is reproducible.
func backlogged(t *testing.T) (string, LengthBacklogEntry) {
	t.Helper()
	paths := make([]string, 0, len(LengthBacklog))
	for path := range LengthBacklog {
		if !strings.HasPrefix(path, "docs/services/") {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		t.Skip("no non-service pages left in LengthBacklog")
	}
	sort.Strings(paths)
	return paths[0], LengthBacklog[paths[0]]
}

func TestCheck_letsABackloggedPageShrinkButNotGrow(t *testing.T) {
	path, entry := backlogged(t)

	// Given: the page at its recorded ceiling, and the same page grown past it.
	at := Doc{Path: path, Body: prose(entry.Prose - 500)}
	grown := Doc{Path: path, Body: prose(entry.Prose + 500)}

	// When / Then: waiting for a rewrite is not permission to get worse.
	assertClean(t, Check([]Doc{at}))
	assertReports(t, Check([]Doc{grown}), "may only shrink")
}

func TestCheck_retiresABacklogEntryOnceThePageIsInsideTheBudget(t *testing.T) {
	path, _ := backlogged(t)

	// Given: a whole-corpus run over that page, now short.
	docs := []Doc{{Path: path, Body: "# Demo\n\nSplit at last.\n"}}

	// When / Then: the entry is deleted by failing the build, the same way
	// RestructurePending entries are.
	assertReports(t, CheckWith(docs, Options{WholeCorpus: true}), "Delete its LengthBacklog entry")
}

func TestCheck_rejectsABacklogEntryForAPageThatIsGone(t *testing.T) {
	// Given: a whole-corpus run that does not contain the backlogged pages.
	problems := CheckWith([]Doc{guide("# Demo\n\nShort.\n")}, Options{WholeCorpus: true})

	// When / Then: a ceiling left behind by a rename is reported.
	if len(LengthBacklog) > 0 {
		assertReports(t, problems, "no longer exist; delete the entries")
	}
}

func TestCheck_doesNotRetireEntriesOnAPartialRun(t *testing.T) {
	// Given: a single page linted on its own, as a unit test or an editor
	// integration would.
	// When / Then: it is not told that the other twenty pages have vanished.
	assertClean(t, Check([]Doc{guide("# Demo\n\nShort.\n")}))
}
