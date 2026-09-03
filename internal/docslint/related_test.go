package docslint

import "testing"

func TestCheck_requiresARelatedSectionOnEveryPublishedPage(t *testing.T) {
	// Given: a guide with no link footer, outside docs/services/ where none of
	// the template's structure rules reach.
	d := Doc{Path: "docs/demo-guide.md", Body: "# Demo\n\nOne thing, explained.\n"}

	// When / Then: it is a dead end for anybody who arrived from search.
	assertReports(t, Check([]Doc{d}), `missing required section "## Related"`)
}

func TestCheck_exemptsADirectoryIndexFromTheRelatedRule(t *testing.T) {
	// Given: an index page, which is a list of links from top to bottom.
	for _, path := range []string{"docs/README.md", "docs/services/README.md"} {
		d := Doc{Path: path, Body: "# Index\n\n- [S3](./s3.md)\n"}

		// When / Then: one more list of links at the end would be a rule
		// satisfying itself.
		assertClean(t, Check([]Doc{d}))
	}
}

func TestCheck_requiresRelatedToBeTheLastSection(t *testing.T) {
	// Given: a section written below the link footer.
	body := "# Demo\n\nOne thing.\n\n## Related\n\n- [Index](./README.md)\n\n## Notes\n\nMore.\n"

	// When / Then: a footer with content under it is not a footer.
	assertReports(t, Check([]Doc{{Path: "docs/demo-guide.md", Body: body}}),
		`"## Related" must be the last section on the page`)
}

func TestCheck_requiresAPagesOwnSubPagesToComeFirstInRelated(t *testing.T) {
	// Given: a landing page listing a guide above its own sub-page.
	body := "# Networking\n\nOne thing.\n\n## Related\n\n" +
		"- [Using AWS SDKs and CLI](./sdk-cli.md)\n" +
		"- [Egress modes](./networking/egress.md)\n"

	// When / Then: the nearest link goes first.
	assertReports(t, Check([]Doc{{Path: "docs/networking.md", Body: body}}),
		`"./networking/egress.md" is one of this page's own sub-pages, listed after another page under docs/`)
}

func TestCheck_requiresLinksOffTheSiteToComeLastInRelated(t *testing.T) {
	// Given: the AWS API reference above a docs link.
	body := "# S3\n\nOne thing.\n\n## Related\n\n" +
		"- [AWS API reference](https://docs.aws.amazon.com/AmazonS3/latest/API/Welcome.html)\n" +
		"- [All service pages](./README.md)\n"

	// When / Then: a reader leaving the site is the last option offered.
	assertReports(t, Check([]Doc{{Path: "docs/demo-guide.md", Body: body}}),
		`"./README.md" is another page under docs/, listed after a link off the site`)
}

func TestCheck_acceptsTheTemplateOrderInRelated(t *testing.T) {
	// Given: own sub-pages, then the rest of docs/, then the AWS reference —
	// the shape docs/dev/service-doc-template.md describes.
	body := "# Networking\n\nOne thing.\n\n## Related\n\n" +
		"- [Egress modes](./networking/egress.md)\n" +
		"- [Hostnames](./networking/hostnames.md)\n" +
		"- [Using AWS SDKs and CLI](./sdk-cli.md)\n" +
		"- [An in-page anchor](#related)\n" +
		"- [AWS API reference](https://docs.aws.amazon.com/)\n"

	// When / Then: nothing to report.
	assertClean(t, Check([]Doc{{Path: "docs/networking.md", Body: body}}))
}

func TestCheck_doesNotOrderTheMiddleOfRelated(t *testing.T) {
	// Given: two ordinary docs links whose order is a judgment call.
	body := "# Demo\n\nOne thing.\n\n## Related\n\n" +
		"- [Storage](./storage.md)\n" +
		"- [A sub-page of another guide](./networking/egress.md)\n" +
		"- [Configuration](./configuration.md)\n"

	// When / Then: the linter has an opinion about the two ends of the list and
	// no opinion about the middle of it.
	assertClean(t, Check([]Doc{{Path: "docs/demo-guide.md", Body: body}}))
}
