package docsindex_test

import (
	"testing"

	"github.com/overcast-sh/overcast/internal/docsindex"
)

// slugCases is the Go ↔ TypeScript parity table. web/src/routes/docs.slug.test.ts
// holds the same rows against github-slugger itself, the library Slug ports; a
// case added here is added there, so a drift in either side fails a test rather
// than 404ing a link. Two rows are the literal headings internal/router/
// advisories.go hand-computes fragments from.
var slugCases = []struct{ text, id string }{
	{"Data-plane endpoints — RDS, and anything else that is a container", "data-plane-endpoints--rds-and-anything-else-that-is-a-container"},
	{"Data dir placement — avoid host bind mounts on Docker Desktop", "data-dir-placement--avoid-host-bind-mounts-on-docker-desktop"},
	{"Builds without SQLite", "builds-without-sqlite"},
	{"Lambda, ECS and VPCs", "lambda-ecs-and-vpcs"},
	{"`Fn::GetAtt` support", "fngetatt-support"},
	{"Stack stuck in `CREATE_IN_PROGRESS`", "stack-stuck-in-create_in_progress"},
	{"overcast stop [name]", "overcast-stop-name"},
	{"Why not create the VPC at stage scope?", "why-not-create-the-vpc-at-stage-scope"},
	{"\"This used to work with `LAMBDA_NETWORK` set\"", "this-used-to-work-with-lambda_network-set"},
	{"Node.js (AWS SDK v3)", "nodejs-aws-sdk-v3"},
	{"  spaced  out  ", "--spaced--out--"},
	{"Ünïcode café — naïve", "ünïcode-café--naïve"},
	{"Tabs\tand\u00a0nbsp", "tabsandnbsp"},
	{"", ""},
}

func TestSlug_isGitHubs(t *testing.T) {
	for _, c := range slugCases {
		if got := docsindex.Slug(c.text); got != c.id {
			t.Errorf("Slug(%q) = %q, want %q", c.text, got, c.id)
		}
	}
}

func TestExtractHeadings_numbersRepeatsLikeGitHub(t *testing.T) {
	// Given: repeats, and a heading whose own slug a repeat already took.
	body := "## Setup\n\n## Setup\n\n### Setup-1\n\n## Setup\n"

	// When
	got := docsindex.ExtractHeadings(body)

	// Then: github-slugger's numbering, -1 first, and Setup-1 moves along.
	want := []string{"setup", "setup-1", "setup-1-1", "setup-2"}
	if len(got) != len(want) {
		t.Fatalf("headings = %+v, want %d", got, len(want))
	}
	for i, h := range got {
		if h.ID != want[i] {
			t.Errorf("heading %d id = %q, want %q", i, h.ID, want[i])
		}
	}
}

func TestExtractHeadings_ignoresFencedCode(t *testing.T) {
	// Given: shell comments inside ``` and ~~~ fences, one indented.
	body := "## Before\n\n```sh\n# Before\n## not a heading\n```\n\n  ~~~\n  # also not\n  ~~~\n\n## Before\n"

	// When
	got := docsindex.ExtractHeadings(body)

	// Then: only the real headings, numbered without the comments.
	if len(got) != 2 || got[0].ID != "before" || got[1].ID != "before-1" {
		t.Fatalf("headings = %+v, want before, before-1", got)
	}
}
