import { describe, expect, it } from "vitest"
// The implementation lives in @/lib/slug; importing through the route keeps
// this test pinned to the function the docs viewer actually renders ids with.
import { slug } from "./docs"

/**
 * Go ↔ TypeScript parity. This side calls github-slugger, the reference —
 * GitHub's own ids, and the public site's, which Astro assigns with the same
 * library. internal/docsindex.Slug is a Go port of it, and its slug_test.go
 * holds this same table: a case added here is added there, so a drift in the
 * port fails a test rather than 404ing a link.
 *
 * Two rows are the literal headings internal/router/advisories.go
 * hand-computes fragments from (dataDirDocsPath, noSQLiteDocsPath) — the one
 * pairing scripts/docs-index.go --check cannot see, because the fragment is a
 * Go string literal rather than a Markdown link it can validate.
 */
const CASES: Array<[text: string, id: string]> = [
  [
    "Data-plane endpoints — RDS, and anything else that is a container",
    "data-plane-endpoints--rds-and-anything-else-that-is-a-container",
  ],
  [
    "Data dir placement — avoid host bind mounts on Docker Desktop",
    "data-dir-placement--avoid-host-bind-mounts-on-docker-desktop",
  ],
  ["Builds without SQLite", "builds-without-sqlite"],
  ["Lambda, ECS and VPCs", "lambda-ecs-and-vpcs"],
  ["`Fn::GetAtt` support", "fngetatt-support"],
  ["Stack stuck in `CREATE_IN_PROGRESS`", "stack-stuck-in-create_in_progress"],
  ["overcast stop [name]", "overcast-stop-name"],
  ["Why not create the VPC at stage scope?", "why-not-create-the-vpc-at-stage-scope"],
  ['"This used to work with `LAMBDA_NETWORK` set"', "this-used-to-work-with-lambda_network-set"],
  ["Node.js (AWS SDK v3)", "nodejs-aws-sdk-v3"],
  ["  spaced  out  ", "--spaced--out--"],
  ["Ünïcode café — naïve", "ünïcode-café--naïve"],
  ["Tabs\tand\u00a0nbsp", "tabsandnbsp"],
  ["", ""],
]

describe("slug", () => {
  it.each(CASES)("slug(%j) is GitHub's %j", (text, id) => {
    expect(slug(text)).toBe(id)
  })

  it("keeps punctuation runs as double hyphens rather than collapsing them", () => {
    // The shape the four dead anchors got wrong: an em-dash between spaces.
    expect(slug("a — b")).toBe("a--b")
    expect(slug("a - b")).toBe("a---b")
  })
})
