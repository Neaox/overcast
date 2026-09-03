package docslint

import "testing"

func TestCheck_rejectsABareBackLinkOpener(t *testing.T) {
	for name, opener := range map[string]string{
		"the house habit": "Back to [ECS](../ecs.md).",
		"no full stop":    "Back to [ECS](../ecs.md)",
		"an article":      "Back to the [ECS page](../ecs.md).",
		"the link alone":  "[ECS](../ecs.md)",
	} {
		t.Run(name, func(t *testing.T) {
			// Given: a sub-page spending its first line on the one thing the
			// reader already knows.
			body := withRelated("# ECS limitations\n\n" + opener + "\n")

			// When / Then: the first line has a job.
			assertReports(t, Check([]Doc{{Path: "docs/services/ecs/limitations.md", Body: body}}),
				"opens with a bare back-link")
		})
	}
}

func TestCheck_acceptsAnOpenerThatCarriesTheLinkInASentence(t *testing.T) {
	for name, opener := range map[string]string{
		"the orientation form": "The full divergence list behind [ECS](../ecs.md).",
		"the link at the end":  "Symptom, cause, fix. Back to [ECS](../ecs.md).",
	} {
		t.Run(name, func(t *testing.T) {
			// Given: a first line that says what the page holds and carries the
			// link inside it.
			body := withRelated("# ECS limitations\n\n" + opener + "\n")

			// When / Then: same link, same line, and the reader learns something.
			assertClean(t, Check([]Doc{{Path: "docs/services/ecs/limitations.md", Body: body}}))
		})
	}
}

func TestCheck_readsPastASignpostToTheOpeningLine(t *testing.T) {
	// Given: an alert and an HTML comment above the opening sentence.
	body := withRelated("# ECS limitations\n\n> [!NOTE]\n> A signpost.\n\n<!-- a comment -->\n\nBack to [ECS](../ecs.md).\n")

	// When / Then: neither is the opening line.
	assertReports(t, Check([]Doc{{Path: "docs/services/ecs/limitations.md", Body: body}}),
		"opens with a bare back-link")
}

func TestCheck_ignoresAPageThatOpensOnACommand(t *testing.T) {
	// Given: a page whose first thing is the command the reader came for.
	body := withRelated("# Network state verification\n\n```sh\novercast network status\n```\n")

	// When / Then: leading with the command is the house style, not a defect.
	assertClean(t, Check([]Doc{{Path: "docs/networking/network-state.md", Body: body}}))
}
