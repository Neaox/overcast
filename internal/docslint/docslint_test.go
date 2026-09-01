package docslint

import (
	"strings"
	"testing"
)

// landing builds a service landing page that passes every rule, so each test
// can break exactly one thing and see exactly one problem.
func landing(sections ...string) string {
	var b strings.Builder
	b.WriteString("# S3\n\nOne sentence. Two sentences.\n\n")
	for _, s := range sections {
		b.WriteString(s)
		b.WriteString("\n\n")
	}
	b.WriteString(BeginMarker + "\n\n## Operations\n\nAll 3 listed operations are implemented.\n\n" + EndMarker + "\n\n")
	b.WriteString("## Related\n\n- [All service pages](README.md)\n")
	return b.String()
}

// doc is a landing page for a service that is NOT in RestructurePending, so
// every rule applies to it. pending is one that still is, so the three
// prose-dependent rules are waived.
func doc(body string) Doc     { return Doc{Path: "docs/services/demo.md", Body: body} }
func pending(body string) Doc { return Doc{Path: "docs/services/s3.md", Body: body} }

func messages(problems []Problem) string {
	out := make([]string, 0, len(problems))
	for _, p := range problems {
		out = append(out, p.String())
	}
	return strings.Join(out, "\n")
}

func assertClean(t *testing.T, problems []Problem) {
	t.Helper()
	if len(problems) > 0 {
		t.Fatalf("expected no problems, got:\n%s", messages(problems))
	}
}

func assertReports(t *testing.T, problems []Problem, want string) {
	t.Helper()
	if !strings.Contains(messages(problems), want) {
		t.Fatalf("expected a problem containing %q, got:\n%s", want, messages(problems))
	}
}

func TestCheck_acceptsATemplateShapedLandingPage(t *testing.T) {
	// Given: a page with every template section, in order
	body := landing("## Quick start\n\n```sh\naws s3 ls\n```", "## What works\n\n- Buckets", "## Differences from AWS\n\n| A | B |\n| - | - |\n| c | d |")

	// When / Then: nothing to report
	assertClean(t, Check([]Doc{doc(body)}))
}

func TestCheck_requiresTheOperationsAndRelatedSections(t *testing.T) {
	// Given: a page with neither
	body := "# S3\n\nOne sentence.\n\n## Quick start\n\n```sh\naws s3 ls\n```\n"

	// When: it is checked
	problems := Check([]Doc{doc(body)})

	// Then: both are named, and so is the missing generated block
	assertReports(t, problems, `missing required section "## Operations"`)
	assertReports(t, problems, `missing required section "## Related"`)
	assertReports(t, problems, "no "+BeginMarker+" marker")
}

func TestCheck_rejectsTemplateSectionsOutOfOrder(t *testing.T) {
	// Given: "Differences from AWS" written before "What works"
	body := landing("## Quick start\n\nx.", "## Differences from AWS\n\nx.", "## What works\n\nx.")

	// When / Then: the out-of-place section is named against the one it follows
	assertReports(t, Check([]Doc{doc(body)}), `section "## Differences from AWS" must come after "## What works"`)
}

func TestCheck_freeFormHeadingsAreNotOrderedAgainstAnything(t *testing.T) {
	// Given: a service-specific heading between two template sections
	body := landing("## Quick start\n\nx.", "## Queue URLs and endpoint resolution\n\nx.", "## What works\n\nx.")

	// When / Then: only the template vocabulary is ordered
	assertClean(t, Check([]Doc{doc(body)}))
}

func TestCheck_rejectsContentAfterTheGeneratedBlock(t *testing.T) {
	// Given: a section wedged between the generated block and "## Related"
	body := strings.Replace(landing("## Quick start\n\nx."), EndMarker+"\n\n## Related",
		EndMarker+"\n\n## Notes\n\nSomething.\n\n## Related", 1)

	// When / Then: the stray section is reported
	assertReports(t, Check([]Doc{doc(body)}), "content after "+EndMarker)
}

func TestCheck_rejectsASectionAfterRelated(t *testing.T) {
	// Given: a section written below "## Related"
	body := landing("## Quick start\n\nx.") + "\n## Notes\n\nSomething.\n"

	// When / Then: the link footer has to stay the footer
	assertReports(t, Check([]Doc{doc(body)}), `"## Related" must be the last section on the page`)
}

func TestCheck_relatedMayFollowTheGeneratedBlock(t *testing.T) {
	// Given: the canonical shape — block, then Related, then nothing
	// When / Then: accepted (this is what cmd/capgen writes)
	assertClean(t, Check([]Doc{doc(landing("## Quick start\n\nx."))}))
}

func TestCheck_requiresOperationsToLiveInsideTheGeneratedBlock(t *testing.T) {
	// Given: a hand-written Operations heading above the block
	body := "# S3\n\nx.\n\n## Operations\n\nBy hand.\n\n" + BeginMarker + "\n\n" + EndMarker + "\n\n## Related\n\n- [x](README.md)\n"

	// When / Then: it is refused — capgen owns that heading
	assertReports(t, Check([]Doc{doc(body)}), `"## Operations" must be inside the generated block`)
}

func TestCheck_waivesQuickStartIntroAndTablesForPendingPages(t *testing.T) {
	// Given: a page with no Quick start, a four-sentence intro and a
	// hand-maintained capability table
	body := "# S3\n\nOne. Two. Three. Four.\n\n" +
		"## What works\n\n| Operation | Status |\n| --- | --- |\n| `Get` | ok |\n\n" +
		BeginMarker + "\n\n## Operations\n\nAll 3 listed operations are implemented.\n\n" + EndMarker +
		"\n\n## Related\n\n- [x](README.md)\n"

	// When: the service is still in RestructurePending
	// Then: none of the three fire
	assertClean(t, Check([]Doc{pending(body)}))

	// And: the same page fails once the service is no longer pending
	problems := Check([]Doc{doc(body)})
	assertReports(t, problems, `missing required section "## Quick start"`)
	assertReports(t, problems, "intro is 4 sentences")
	assertReports(t, problems, "capability table outside the generated block")
}

func TestCheck_demandsAPendingEntryBeRemovedOnceItIsNoLongerTrue(t *testing.T) {
	// Given: a page still listed in RestructurePending that now satisfies all
	// three waived rules
	body := landing("## Quick start\n\n```sh\naws s3 ls\n```")

	// When / Then: the build fails until the entry is deleted, so an exemption
	// cannot outlive its reason
	assertReports(t, Check([]Doc{pending(body)}), "remove them from RestructurePending so the rules stay enforced: s3")
}

func TestCheck_ignoresTablesThatAreNotCapabilityTables(t *testing.T) {
	// Given: an ordinary table on a finished page
	body := landing("## Quick start\n\nx.", "## What works\n\n| Setting | Default |\n| --- | --- |\n| a | b |")

	// When / Then: only a table whose first column is "Operation" is a
	// hand-maintained copy of the generated one
	assertClean(t, Check([]Doc{doc(body)}))
}

func TestCheck_doesNotReadHeadingsOrTablesInsideFencedCode(t *testing.T) {
	// Given: a fenced example containing a heading and a capability table
	body := landing("## Quick start\n\n```md\n## Operations\n\n| Operation | Status |\n| --- | --- |\n```")

	// When / Then: a worked example is not structure
	assertClean(t, Check([]Doc{doc(body)}))
}

func TestCheck_blockquotesAndAlertsDoNotCountTowardTheIntroBudget(t *testing.T) {
	// Given: a signpost blockquote and a GitHub alert above the prose
	body := "# S3\n\n> [!NOTE]\n> Something worth knowing. And more of it.\n\nOne sentence. Two sentences.\n\n" +
		"## Quick start\n\nx.\n\n" + BeginMarker + "\n\n## Operations\n\nAll 3 listed operations are implemented.\n\n" +
		EndMarker + "\n\n## Related\n\n- [x](README.md)\n"

	// When / Then: only the prose is budgeted
	assertClean(t, Check([]Doc{doc(body)}))
}

func TestCheck_rejectsAnUnknownServiceSubPage(t *testing.T) {
	// Given: a sub-page with a name nobody agreed on
	sub := Doc{Path: "docs/services/s3/faq.md", Body: "# FAQ\n\nSee [S3](../s3.md).\n"}

	// When / Then: the fixed set is the point of the directory
	assertReports(t, Check([]Doc{sub}), "unexpected service sub-page")
}

func TestCheck_requiresAHandWrittenSubPageToLinkBackToItsLandingPage(t *testing.T) {
	// Given: a limitations page with no way back
	sub := Doc{Path: "docs/services/s3/limitations.md", Body: "# S3 limitations\n\nSomething.\n"}

	// When / Then: a reader arriving from search has to be able to get oriented
	assertReports(t, Check([]Doc{sub}), "no link back to the landing page")

	// And: one that links back is fine
	linked := Doc{Path: "docs/services/s3/limitations.md", Body: "# S3 limitations\n\nBack to [S3](../s3.md).\n"}
	assertClean(t, Check([]Doc{linked}))
}

func TestCheck_rejectsHandWrittenContentInTheGeneratedOperationsPage(t *testing.T) {
	// Given: a paragraph added below the generated block
	sub := Doc{
		Path: "docs/services/s3/operations.md",
		Body: BeginMarker + "\n\n# S3 operations\n\n" + EndMarker + "\n\nOne more thought.\n",
	}

	// When / Then: the file belongs to capgen
	assertReports(t, Check([]Doc{sub}), "hand-written content in a generated file")
}

func TestCheck_ignoresDocsOutsideTheServicesTree(t *testing.T) {
	// Given: a page elsewhere under docs/ with none of the required sections
	// When / Then: this package lints service docs only
	assertClean(t, Check([]Doc{{Path: "docs/cdk.md", Body: "# CDK\n\nAnything at all.\n"}}))
	assertClean(t, Check([]Doc{{Path: "docs/services/README.md", Body: "# Service Reference\n\nAnything.\n"}}))
}

func TestCheck_reportsALineNumberThatMatchesTheFileNotTheBody(t *testing.T) {
	// Given: a doc whose body starts nine lines into the file (frontmatter)
	d := Doc{
		Path:           "docs/services/demo.md",
		BodyLineOffset: 9,
		Body:           "# Demo\n\nOne. Two. Three.\n\n## Quick start\n\nx.\n\n" + BeginMarker + "\n\n## Operations\n\nAll 1 listed operation is implemented.\n\n" + EndMarker + "\n\n## Related\n\n- [x](README.md)\n",
	}

	// When: the intro budget is exceeded on body line 3
	problems := Check([]Doc{d})

	// Then: it is reported at file line 12
	assertReports(t, problems, "docs/services/demo.md:12: intro is 3 sentences")
}
