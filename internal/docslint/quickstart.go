package docslint

import (
	"regexp"
	"strings"
)

// A service quick start says what to do about credentials.
//
// The template's quick-start block opens with AWS_ENDPOINT_URL and nothing
// else, so credentials, region and per-language client setup stay on
// docs/sdk-cli.md and are not taught fifty times. That is right for the app
// developer and wrong for the reader the first screen is written for: with an
// empty ~/.aws the AWS CLI and every SDK refuse to sign, so the first command
// in the quick start dies on "Unable to locate credentials" without ever
// reaching Overcast. The page's own first-hour reader is the one it loses.
//
// One sentence under the block fixes it, and the link in that sentence is what
// keeps the fix from becoming the duplication the endpoint-only rule exists to
// prevent. The rule here is the link, not the wording: a page may say it in its
// own words, and a page that drops it fails.
const credentialsAnchor = "sdk-cli.md#credentials"

var quickStartHeadingRE = regexp.MustCompile(`^##\s+Quick start\s*$`)

// checkQuickStartCredentials requires the credentials pointer inside a landing
// page's Quick start section.
//
// Landing pages only. A sub-page is reached from the landing, and examples.md
// is read by someone who is already past their first call.
func checkQuickStartCredentials(doc Doc, waived bool) []Problem {
	if waived {
		// Same waiver as the rest of the landing rules: a page still in
		// RestructurePending has not been given its quick start yet.
		return nil
	}

	lines := strings.Split(doc.Body, "\n")
	visible := eachOutsideFences(lines)

	start := -1
	for i, line := range visible {
		if quickStartHeadingRE.MatchString(strings.TrimSpace(line)) {
			start = i
			break
		}
	}
	if start < 0 {
		// Whether a landing page has a Quick start at all is checkLanding's.
		return nil
	}

	for i := start + 1; i < len(lines); i++ {
		if visible[i] != "" && strings.HasPrefix(strings.TrimSpace(visible[i]), "## ") {
			break
		}
		if strings.Contains(lines[i], credentialsAnchor) {
			return nil
		}
	}

	return []Problem{{
		Path: doc.Path,
		Line: doc.BodyLineOffset + start + 1,
		Msg:  `"## Quick start" does not say what to do about credentials. The block opens with AWS_ENDPOINT_URL and nothing else, so a reader with an empty ~/.aws meets "Unable to locate credentials" on the first command. Put one line under the code block linking ` + credentialsAnchor + ` — see docs/dev/service-doc-template.md`,
	}}
}
