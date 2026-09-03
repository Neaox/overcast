package docslint

import (
	"strings"
	"testing"
)

func TestCheck_rejectsAQuickStartThatSaysNothingAboutCredentials(t *testing.T) {
	// Given: the endpoint-only quick start, and nothing telling a reader with
	// an empty ~/.aws why their first command fails before it reaches Overcast.
	body := landing("## Quick start\n\n```bash\nexport AWS_ENDPOINT_URL=http://localhost:4566\n\naws s3 ls\n```")
	body = strings.Replace(body,
		"\n\nAny credentials work; see [Using AWS SDKs and CLI](../sdk-cli.md#credentials).", "", 1)

	// When / Then: the page's own first-hour reader is the one it loses.
	assertReports(t, Check([]Doc{doc(body)}),
		"does not say what to do about credentials")
}

func TestCheck_acceptsAnyWordingThatCarriesTheCredentialsLink(t *testing.T) {
	for name, line := range map[string]string{
		"the template's sentence": "Any credentials work; with none configured, run `eval \"$(overcast env)\"` first — see [Using AWS SDKs and CLI](../sdk-cli.md#credentials).",
		"a page's own words":      "No AWS profile on this machine? [Set any credentials](../sdk-cli.md#credentials) first.",
	} {
		t.Run(name, func(t *testing.T) {
			// Given: a quick start that answers the question in its own way.
			body := landing("## Quick start\n\n```bash\naws s3 ls\n```\n\n" + line)

			// When / Then: the rule is the link, not the wording.
			assertClean(t, Check([]Doc{doc(body)}))
		})
	}
}

func TestCheck_looksForTheCredentialsLinkOnlyInsideTheQuickStart(t *testing.T) {
	// Given: the link present, but down in the Related footer where a reader
	// running the first command will never meet it.
	body := landing("## Quick start\n\n```bash\naws s3 ls\n```")
	body = strings.Replace(body,
		"\n\nAny credentials work; see [Using AWS SDKs and CLI](../sdk-cli.md#credentials).", "", 1)
	body = strings.Replace(body,
		"- [All service pages](README.md)",
		"- [All service pages](README.md)\n- [Using AWS SDKs and CLI](../sdk-cli.md#credentials)", 1)

	// When / Then: the sentence has to be where the failure happens.
	assertReports(t, Check([]Doc{doc(body)}),
		"does not say what to do about credentials")
}

func TestCheck_doesNotAskSubPagesForTheCredentialsLine(t *testing.T) {
	// Given: a sub-page, which is reached from a landing page that already
	// carried the answer.
	body := withRelated("# S3 limitations\n\nThe full divergence list behind [S3](../s3.md).\n\n" +
		"## Quick start\n\n```bash\naws s3 ls\n```\n")

	// When / Then: the rule is a landing-page rule.
	assertClean(t, Check([]Doc{{Path: "docs/services/s3/limitations.md", Body: body}}))
}
