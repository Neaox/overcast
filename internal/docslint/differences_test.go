package docslint

import "testing"

// quickStart is the section every landing page must have, so a test about the
// divergence table is not also a test about that rule.
const quickStart = "## Quick start\n\n```bash\naws s3 ls\n```"

// differences builds a "## Differences from AWS" section with the given header
// row, so each test varies exactly the thing the rule looks at.
func differences(header string) string {
	return "## Differences from AWS\n\n" + header + "\n| --- | --- | --- |\n| Health checks | Probed | Always healthy |"
}

func TestCheck_rejectsATwoColumnDifferencesTable(t *testing.T) {
	// Given: the shape half the corpus drifted into, where what AWS does is
	// left inside the Overcast cell for the reader to dig out.
	body := landing(quickStart, "## Differences from AWS\n\n| Area | Overcast |\n| --- | --- |\n"+
		"| Metric retention | About one hour. Real CloudWatch keeps 15 months |")

	// When / Then: the platform engineer is comparing two behaviours and needs
	// both of them on the row.
	assertReports(t, Check([]Doc{doc(body)}),
		"is `| Area | Overcast |`; the template's shape is `| Area | On AWS | Overcast |`")
}

func TestCheck_acceptsTheTemplatesDifferencesHeader(t *testing.T) {
	// Given: AWS before Overcast, as the template gives it.
	assertClean(t, Check([]Doc{doc(landing(quickStart, differences("| Area | On AWS | Overcast |")))}))
}

func TestCheck_rejectsAThreeColumnDifferencesTableInTheWrongOrder(t *testing.T) {
	// Given: three columns, but Overcast first.
	body := landing(quickStart, differences("| Area | Overcast | On AWS |"))

	// When / Then: the order is the point — a reader scans the AWS column to
	// find the row that matters to them.
	assertReports(t, Check([]Doc{doc(body)}),
		"is `| Area | Overcast | On AWS |`")
}

func TestCheck_acceptsATwoColumnDifferencesTableWithAStatedReason(t *testing.T) {
	// Given: a page whose rows genuinely have no AWS half, saying so.
	body := landing(quickStart, "<!-- "+DifferencesTwoColumnMarker+
		" every row names a resource type AWS has and Overcast does not model at all -->\n\n"+
		"## Differences from AWS\n\n| Area | Overcast |\n| --- | --- |\n| Access points | Not modelled |")

	// When / Then: the opt-out is what makes going two-column a decision rather
	// than a drift.
	assertClean(t, Check([]Doc{doc(body)}))
}

func TestCheck_rejectsAnEmptyReasonOnTheTwoColumnMarker(t *testing.T) {
	// Given: the marker used to silence the check rather than to explain.
	body := landing(quickStart, "<!-- "+DifferencesTwoColumnMarker+" no AWS half -->\n\n"+
		"## Differences from AWS\n\n| Area | Overcast |\n| --- | --- |\n| Access points | Not modelled |")

	// When / Then: a considered decision is a sentence.
	assertReports(t, Check([]Doc{doc(body)}),
		"gives no reason worth the name")
}

func TestCheck_retiresTheTwoColumnMarkerOnceTheTableIsThreeColumn(t *testing.T) {
	// Given: a page that carries the opt-out and has since been converted.
	body := landing(quickStart, "<!-- "+DifferencesTwoColumnMarker+
		" every row names a resource type AWS has and Overcast does not model at all -->\n\n"+
		differences("| Area | On AWS | Overcast |"))

	// When / Then: an exception nobody is made to revisit is indefinite.
	assertReports(t, Check([]Doc{doc(body)}),
		"but the table is already `| Area | On AWS | Overcast |`. Delete the marker")
}

func TestCheck_retiresTheTwoColumnMarkerOnAPageWithNoSuchTable(t *testing.T) {
	// Given: the marker left behind after the section itself went away.
	body := landing(quickStart, "<!-- "+DifferencesTwoColumnMarker+
		" every row names a resource type AWS has and Overcast does not model at all -->\n\n"+
		"## What works\n\n| Area | Behaviour |\n| --- | --- |\n| Buckets | Created |")

	// When / Then: it has nothing left to excuse.
	assertReports(t, Check([]Doc{doc(body)}),
		"has no \"## Differences from AWS\" table to excuse. Delete the marker")
}

func TestCheck_ignoresADifferencesSectionWithNoTable(t *testing.T) {
	// Given: a section that links out instead of tabulating.
	body := landing(quickStart, "## Differences from AWS\n\nThe full list is in [Limitations](./demo/limitations.md).")

	// When / Then: the rule is about a table's header, and there is no table.
	assertClean(t, Check([]Doc{doc(body)}))
}

func TestCheck_readsTheDifferencesHeaderOutsideFencedCode(t *testing.T) {
	// Given: the template's own shape quoted in a fenced block above the real
	// table, which is what docs/dev/service-doc-template.md itself looks like.
	body := landing(quickStart, "## Differences from AWS\n\n```md\n| Area | Overcast |\n| --- | --- |\n```\n\n"+
		"| Area | On AWS | Overcast |\n| --- | --- | --- |\n| Health checks | Probed | Always healthy |")

	// When / Then: a sample of Markdown is not this page's table.
	assertClean(t, Check([]Doc{doc(body)}))
}

func TestCheck_doesNotAskSubPagesForTheDifferencesHeader(t *testing.T) {
	// Given: a limitations sub-page with a two-column divergence table.
	body := withRelated("# S3 limitations\n\nThe full divergence list behind [S3](../s3.md).\n\n" +
		"## Differences from AWS\n\n| Area | Overcast |\n| --- | --- |\n| Access points | Not modelled |\n")

	// When / Then: the rule is a landing-page rule; a sub-page is free to shape
	// its own reference tables.
	assertClean(t, Check([]Doc{{Path: "docs/services/s3/limitations.md", Body: body}}))
}

func TestCheck_doesNotReadAFencedTwoColumnMarkerAsAnOptOut(t *testing.T) {
	// Given: a page showing the opt-out as an example, above a two-column
	// table it has not excused — the shape docs/dev/service-doc-template.md
	// has, applied to a landing page.
	body := landing(quickStart, "## Differences from AWS\n\n```md\n<!-- "+DifferencesTwoColumnMarker+
		" why these rows have no AWS half -->\n```\n\n| Area | Overcast |\n| --- | --- |\n| Access points | Not modelled |")

	// When / Then: documenting the escape hatch is not taking it.
	assertReports(t, Check([]Doc{doc(body)}),
		"is `| Area | Overcast |`")
}
