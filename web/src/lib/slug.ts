/**
 * Heading text → anchor id, the docs viewer's slugger.
 *
 * This must stay in lockstep with internal/docsindex's Slug(), which
 * computes the same ids for the generated nav/search index, and with
 * internal/router/advisories.go, which hand-computes fragments from Go string
 * literals (see slug.test.ts). Note it collapses every non-alphanumeric run to
 * a SINGLE hyphen — GitHub's slugger does not, so anchors into GitHub-read
 * files (docs/dev, docs/plans, AGENTS.md) use GitHub's form instead.
 */
export function slug(s: string): string {
  return s
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-|-$/g, "")
}
