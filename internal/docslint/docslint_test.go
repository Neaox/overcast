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
		b.WriteString(withCredentialsLine(s))
		b.WriteString("\n\n")
	}
	b.WriteString(BeginMarker + "\n\n## Operations\n\nAll 3 listed operations are implemented.\n\n" + EndMarker + "\n\n")
	b.WriteString("## Related\n\n- [All service pages](README.md)\n")
	return b.String()
}

// withCredentialsLine gives a fixture's Quick start the credentials pointer the
// template requires, so a test about some other rule is not also a test about
// that one. A section that is not a Quick start passes through untouched.
func withCredentialsLine(section string) string {
	if !strings.HasPrefix(section, "## Quick start") || strings.Contains(section, credentialsAnchor) {
		return section
	}
	return section + "\n\nAny credentials work; see [Using AWS SDKs and CLI](../sdk-cli.md#credentials)."
}

// withRelated gives a fixture the link footer every published page needs, so a
// test about one rule is not also a test about that one.
func withRelated(body string) string {
	if strings.Contains(body, "\n## Related") {
		return body
	}
	return strings.TrimRight(body, "\n") + "\n\n## Related\n\n- [Reference index](./README.md)\n"
}

// doc is a landing page for a service that is NOT in RestructurePending, so
// every rule applies to it. pending is one that still is, so the three
// prose-dependent rules are waived.
func doc(body string) Doc { return Doc{Path: "docs/services/demo.md", Body: body} }

// pendingStem is read from RestructurePending rather than named here. The list
// only shrinks, so a hard-coded stem turns finishing that one page into a
// failure in tests that have nothing to do with it.
func pendingStem(t *testing.T) string {
	t.Helper()
	if len(RestructurePending) == 0 {
		t.Skip("no pages left in RestructurePending")
	}
	return RestructurePending[0]
}

func pending(t *testing.T, body string) Doc {
	t.Helper()
	return Doc{Path: "docs/services/" + pendingStem(t) + ".md", Body: body}
}

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
	assertClean(t, Check([]Doc{pending(t, body)}))

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
	assertReports(t, Check([]Doc{pending(t, body)}), "remove them from RestructurePending so the rules stay enforced: "+pendingStem(t))
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
		withCredentialsLine("## Quick start\n\nx.") + "\n\n" +
		BeginMarker + "\n\n## Operations\n\nAll 3 listed operations are implemented.\n\n" +
		EndMarker + "\n\n## Related\n\n- [x](README.md)\n"

	// When / Then: only the prose is budgeted
	assertClean(t, Check([]Doc{doc(body)}))
}

// concernPage builds an additional sub-page that satisfies every rule but the
// one a test is about, and the landing page that links it.
func concernPage(sub, body string) []Doc {
	return []Doc{
		{Path: "docs/services/s3.md", Body: landing("## Quick start\n\n```sh\naws s3 ls\n```\n\n- [Concern](./s3/" + sub + ")")},
		{Path: "docs/services/s3/" + sub, Body: body},
	}
}

func TestCheck_acceptsAConcernNamedServiceSubPage(t *testing.T) {
	// Given: a page named after the one concern it covers, linked from the
	// landing page and leading back to it.
	docs := concernPage("multipart-uploads.md",
		"# S3 multipart uploads\n\nWhat a multipart upload does differently behind [S3](../s3.md).\n\n"+
			"## Related\n\n- [S3](../s3.md)\n- [All service pages](../README.md)\n")

	// When / Then: the four fixed names are not the whole directory any more.
	assertClean(t, Check(docs))
}

func TestCheck_rejectsAConcernSubPageNameThatIsNotASlug(t *testing.T) {
	for name, file := range map[string]string{
		"capitalised":     "Multipart.md",
		"underscored":     "multipart_uploads.md",
		"double-hyphened": "multipart--uploads.md",
	} {
		t.Run(name, func(t *testing.T) {
			// Given: a file name the console and the website would route badly
			docs := concernPage(file, "# S3\n\nSomething behind [S3](../s3.md).\n\n## Related\n\n- [S3](../s3.md)\n")

			// When / Then: the name is the route
			assertReports(t, Check(docs), "is not a concern slug")
		})
	}
}

func TestCheck_rejectsAConcernSubPageThatRespellsACanonicalName(t *testing.T) {
	for name, file := range map[string]string{
		"a singular":   "limitation.md",
		"a truncation": "troubleshoot.md",
		"a qualifier":  "examples-advanced.md",
	} {
		t.Run(name, func(t *testing.T) {
			// Given: a second plausible home for material one of the four
			// fixed names already owns
			docs := concernPage(file, "# S3\n\nSomething behind [S3](../s3.md).\n\n## Related\n\n- [S3](../s3.md)\n")

			// When / Then: the fixed names keep their meaning
			assertReports(t, Check(docs), "which has a fixed meaning")
		})
	}
}

func TestCheck_requiresAConcernSubPageToBeLinkedFromItsLandingPage(t *testing.T) {
	// Given: a concern page the landing page never mentions
	docs := []Doc{
		{Path: "docs/services/s3.md", Body: landing("## Quick start\n\n```sh\naws s3 ls\n```")},
		{Path: "docs/services/s3/multipart-uploads.md", Body: "# S3 multipart uploads\n\nBehind [S3](../s3.md).\n\n## Related\n\n- [S3](../s3.md)\n"},
	}

	// When / Then: reachable only from search is not reachable
	assertReports(t, Check(docs), "is not linked from docs/services/s3.md")
}

func TestCheck_requiresAConcernSubPagesRelatedToOpenWithItsLandingPage(t *testing.T) {
	// Given: a footer that sends the reader to a sibling first
	docs := concernPage("multipart-uploads.md",
		"# S3 multipart uploads\n\nBehind [S3](../s3.md).\n\n"+
			"## Related\n\n- [Limitations](./limitations.md)\n- [S3](../s3.md)\n")

	// When / Then: orientation comes before anywhere else
	assertReports(t, Check(docs), `"## Related" opens with "docs/services/s3/limitations.md"`)
}

func TestCheck_leavesTheCanonicalSubPagesRelatedOrderEditorial(t *testing.T) {
	// Given: a canonical sub-page whose footer opens with a sibling. The
	// landing-page-first rule is for the pages the reader has no map of; the
	// four fixed names are the map.
	sub := Doc{
		Path: "docs/services/s3/troubleshooting.md",
		Body: "# S3 troubleshooting\n\nSymptom, cause and fix behind [S3](../s3.md).\n\n" +
			"## Related\n\n- [Limitations](./limitations.md)\n- [S3](../s3.md)\n",
	}

	// When / Then: nothing to report
	assertClean(t, Check([]Doc{sub}))
}

func TestCheck_requiresAHandWrittenSubPageToLinkBackToItsLandingPage(t *testing.T) {
	// Given: a limitations page with no way back
	sub := Doc{Path: "docs/services/s3/limitations.md", Body: "# S3 limitations\n\nSomething.\n"}

	// When / Then: a reader arriving from search has to be able to get oriented
	assertReports(t, Check([]Doc{sub}), "no link back to the landing page")

	// And: one that links back is fine
	linked := Doc{Path: "docs/services/s3/limitations.md", Body: withRelated("# S3 limitations\n\nThe full divergence list behind [S3](../s3.md).\n")}
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
	assertClean(t, Check([]Doc{{Path: "docs/cdk.md", Body: withRelated("# CDK\n\nAnything at all.\n")}}))
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

func TestCheck_rejectsALinkOutsideDocs(t *testing.T) {
	for name, doc := range map[string]Doc{
		"the hub's root README link": {Path: "docs/README.md", Body: "# Docs\n\nStart with the [root README](../README.md).\n"},
		"a contributor file":         {Path: "docs/services/s3.md", Body: "# S3\n\nSee [contributing](../../CONTRIBUTING.md#how-to-add-a-service).\n"},
		"an escape through docs":     {Path: "docs/networking/egress.md", Body: "# Egress\n\nSee [the file](../../embed.go).\n"},
	} {
		t.Run(name, func(t *testing.T) {
			// Given: a link that works on GitHub and 404s in the console,
			// which embeds docs/ and nothing else.
			// When / Then: it is reported at its line.
			assertReports(t, Check([]Doc{doc}), "which is outside docs/")
		})
	}
}

func TestCheck_acceptsLinksTheConsoleCanOpen(t *testing.T) {
	body := "# Docs\n\n" +
		"- [a sibling](./sdk-cli.md)\n" +
		"- [a sub-page](./networking/egress.md#what-none-covers)\n" +
		"- [an anchor](#related)\n" +
		"- [the repository](https://github.com/overcast-sh/overcast/blob/main/CONTRIBUTING.md)\n" +
		"- [the site](/console/)\n" +
		"- [mail](mailto:hi@overcast.sh)\n"
	assertClean(t, Check([]Doc{{Path: "docs/README.md", Body: body}}))
}

func TestCheck_readsAnUpwardLinkFromASubPage(t *testing.T) {
	// Given: a service sub-page linking back up two levels, which lands on
	// docs/networking.md and is therefore fine.
	doc := Doc{Path: "docs/services/ec2/limitations.md", Body: withRelated("# EC2\n\nBack to [../ec2.md](../ec2.md) and [networking](../../networking.md).\n")}

	// When / Then: only a target outside docs/ is a problem.
	assertClean(t, Check([]Doc{doc}))
}

func TestCheck_ignoresLinksInsideFencedCode(t *testing.T) {
	// Given: a sample the writer is quoting, not linking.
	body := "# Docs\n\n```md\n[root README](../README.md)\n```\n"

	assertClean(t, Check([]Doc{{Path: "docs/README.md", Body: body}}))
}
