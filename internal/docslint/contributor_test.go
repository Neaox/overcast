package docslint

import (
	"strings"
	"testing"
)

// dev is a contributor page under docs/dev/, where only the length budget
// applies.
func dev(body string) Doc { return Doc{Path: "docs/dev/demo.md", Body: body} }

func TestCheckContributor_rejectsAPageOverTheContributorBudget(t *testing.T) {
	// Given: a contributor page past the bigger ceiling docs/dev/ gets.
	d := dev(prose(DevMaxProseChars + 500))

	// When / Then: a page nobody can find anything in costs a contributor the
	// same afternoon it costs a user.
	assertReports(t, CheckContributor([]Doc{d}),
		"characters of prose, over the 26000 budget")
}

func TestCheckContributor_acceptsAPageThePublishedBudgetWouldReject(t *testing.T) {
	// Given: a page four times the published prose budget, which is ordinary
	// for a page explaining a mechanism to somebody about to change it.
	d := dev(prose(MaxProseChars * 4))

	// When / Then: the two trees are not the same kind of writing.
	assertClean(t, CheckContributor([]Doc{d}))
}

func TestCheckContributor_appliesOnlyTheLengthBudget(t *testing.T) {
	// Given: a contributor page with no "## Related" footer, no opening line
	// worth the name, and a house-style tell in it.
	d := dev("# Demo\n\n[Back](./other.md)\n\nIt's not about the protocol.\n")

	// When / Then: the footer is a promise to a reader who arrived from search,
	// and docs/dev/ has no such reader.
	assertClean(t, CheckContributor([]Doc{d}))
}

func TestCheckContributor_honoursTheReviewMarker(t *testing.T) {
	// Given: an over-budget contributor page saying why it is legitimately long.
	d := dev("<!-- " + LengthReviewMarker +
		" one numbered walkthrough of the request path; splitting it would break the numbering readers cite -->\n" +
		prose(DevMaxProseChars+500))

	// When / Then: the same opt-out, on the same terms, as a published page.
	assertClean(t, CheckContributor([]Doc{d}))
}

func TestCheckContributor_doesNotReadAFencedSampleAsAMarker(t *testing.T) {
	// Given: a page documenting the marker rather than claiming it — which is
	// what docs/dev/content-charter.md and the service page template both do.
	d := dev("# Demo\n\nThe opt-out looks like this:\n\n```md\n<!-- " + LengthReviewMarker +
		" why this page is legitimately long -->\n```\n")

	// When / Then: showing the escape hatch is not taking it, and reading it as
	// one told both of those pages to delete a marker they do not have.
	assertClean(t, CheckContributor([]Doc{d}))
}

func TestCheckContributor_letsABackloggedPageShrinkAndNotGrow(t *testing.T) {
	path := backloggedContributorPath(t)
	entry := ContributorLengthBacklog[path]

	t.Run("growing past the recorded ceiling fails", func(t *testing.T) {
		// Given: the page a little larger than it was when the budget landed.
		d := Doc{Path: path, Body: prose(entry.Prose + 2000)}

		// When / Then: a page waiting to be split may only shrink.
		assertReports(t, CheckContributor([]Doc{d}), "may only shrink")
	})

	t.Run("coming inside the budget retires the entry", func(t *testing.T) {
		// Given: the page finally split down under the budget, on a run that
		// saw the whole tree.
		d := Doc{Path: path, Body: prose(100)}

		// When / Then: an exemption nobody is made to revisit is indefinite.
		assertReports(t, CheckContributorWith([]Doc{d}, Options{WholeCorpus: true}),
			"Delete its ContributorLengthBacklog entry")
	})
}

func TestCheckContributor_retiresABacklogEntryForAPageThatIsGone(t *testing.T) {
	path := backloggedContributorPath(t)

	// Given: a whole-tree run that no longer holds the backlogged page.
	problems := CheckContributorWith([]Doc{dev(prose(100))}, Options{WholeCorpus: true})

	// When / Then: a ceiling left behind by a rename would wait forever.
	assertReports(t, problems, "ContributorLengthBacklog names 1 page that no longer exist")
	if !strings.Contains(messages(problems), path) {
		t.Fatalf("expected the stale entry to name %q, got:\n%s", path, messages(problems))
	}
}

// backloggedContributorPath reads a path from ContributorLengthBacklog rather
// than naming one, for the reason pendingStem does: the list only shrinks, so a
// hard-coded path turns splitting that page into a failure in tests that have
// nothing to do with it.
func backloggedContributorPath(t *testing.T) string {
	t.Helper()
	if len(ContributorLengthBacklog) == 0 {
		t.Skip("no pages left on ContributorLengthBacklog")
	}
	for path := range ContributorLengthBacklog {
		return path
	}
	return ""
}
