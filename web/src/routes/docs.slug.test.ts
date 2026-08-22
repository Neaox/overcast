import { describe, expect, it } from "vitest"
import { slug } from "./docs"

/**
 * Pins the one cross-language pairing `scripts/docs-index.go --check` cannot
 * see (docs/plans/dev-bff-consolidation.md, B3): internal/router/advisories.go
 * builds an advisory deep-link by hand-computing this same slug from a Go
 * string literal, rather than from a Markdown link the docs-index checker
 * validates against real heading ids. If either the heading text or this
 * function's algorithm changes without the other, the advisory's link 404s
 * silently — this test is the pairing.
 *
 * The two fixture strings are the literal headings this slug feeds:
 *   - docs/performance.md:95  "### Data dir placement — avoid host bind
 *     mounts on Docker Desktop" → internal/router/advisories.go's
 *     dataDirDocsPath fragment.
 *   - docs/storage.md:92      "## Builds without SQLite" →
 *     noSQLiteDocsPath's fragment.
 */
describe("slug", () => {
  it("matches advisories.go's dataDirDocsPath fragment", () => {
    expect(slug("Data dir placement — avoid host bind mounts on Docker Desktop")).toBe(
      "data-dir-placement-avoid-host-bind-mounts-on-docker-desktop",
    )
  })

  it("matches advisories.go's noSQLiteDocsPath fragment", () => {
    expect(slug("Builds without SQLite")).toBe("builds-without-sqlite")
  })
})
