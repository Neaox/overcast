package docslint

import (
	"strings"
	"testing"
)

func TestCheck_rejectsTwoCalloutsWithNothingBetweenThem(t *testing.T) {
	for name, pair := range map[string]string{
		"two kinds":                "> [!WARNING]\n> The container keeps the volume.\n\n> [!CAUTION]\n> Deleting it loses the data.\n",
		"same kind":                "> [!NOTE]\n> One thing.\n\n> [!NOTE]\n> Another thing.\n",
		"a multi-line first block": "> [!WARNING]\n> The first line.\n>\n> And a second paragraph.\n\n> [!TIP]\n> Then this.\n",
	} {
		t.Run(name, func(t *testing.T) {
			// Given: a page stacking its emphasis into a wall of boxes.
			body := withRelated("# S3 — Simple Storage Service\n\nObject storage.\n\n" + pair)

			// When / Then: the second box has to earn its own line of prose.
			assertReports(t, Check([]Doc{{Path: "docs/storage.md", Body: body}}),
				"with nothing between them")
		})
	}
}

func TestCheck_acceptsCalloutsSeparatedBySomethingAReaderSees(t *testing.T) {
	for name, between := range map[string]string{
		"a sentence": "The second one is a different concern.\n",
		"a heading":  "## Deleting a bucket\n",
		"a command":  "```bash\naws s3 rb s3://demo\n```\n",
		"a table":    "| Area | Overcast |\n| --- | --- |\n| Versioning | Kept |\n",
	} {
		t.Run(name, func(t *testing.T) {
			// Given: two callouts with the material that says why they are two.
			body := withRelated("# S3 — Simple Storage Service\n\nObject storage.\n\n" +
				"> [!WARNING]\n> The container keeps the volume.\n\n" +
				between +
				"\n> [!CAUTION]\n> Deleting it loses the data.\n")

			// When / Then: the budget is on adjacency, not on the count.
			assertClean(t, Check([]Doc{{Path: "docs/storage.md", Body: body}}))
		})
	}
}

func TestCheck_ignoresStackedCalloutsInsideAFence(t *testing.T) {
	// Given: a page showing what the stacked shape looks like, in a code block.
	body := withRelated("# Writing docs\n\nThe alert vocabulary.\n\n" +
		"```md\n> [!WARNING]\n> One.\n\n> [!CAUTION]\n> Two.\n```\n")

	// When / Then: a sample of Markdown is not this page's emphasis.
	assertClean(t, Check([]Doc{{Path: "docs/storage.md", Body: body}}))
}

func TestCheck_reportsEveryStackedPairOnAPage(t *testing.T) {
	// Given: three callouts in a row, which is two adjacent pairs.
	body := withRelated("# S3 — Simple Storage Service\n\nObject storage.\n\n" +
		"> [!NOTE]\n> One.\n\n> [!TIP]\n> Two.\n\n> [!WARNING]\n> Three.\n")

	// When: the page is linted.
	problems := Check([]Doc{{Path: "docs/storage.md", Body: body}})

	// Then: the writer is shown both joins, not just the first.
	var stacked int
	for _, p := range problems {
		if strings.Contains(p.Msg, "with nothing between them") {
			stacked++
		}
	}
	if stacked != 2 {
		t.Fatalf("got %d stacked-callout problems, want 2:\n%v", stacked, problems)
	}
}
