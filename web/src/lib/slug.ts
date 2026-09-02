import GithubSlugger, { slug as githubSlug } from "github-slugger"

/**
 * Heading text → anchor id, the docs viewer's slugger.
 *
 * It is GitHub's: the docs are read on github.com, and the public site
 * renders them through Astro, which assigns heading ids with github-slugger.
 * So this calls that library, internal/docsindex's Slug() is a Go port of it
 * (the ids the nav / "On this page" outline and the anchor checker use), and
 * internal/router/advisories.go hand-computes one fragment from a Go string
 * literal. routes/docs.slug.test.ts pins the three together.
 *
 * The shape, since it surprises: punctuation is DROPPED, not turned into a
 * hyphen, and only spaces become hyphens — nothing is collapsed or trimmed.
 * "Data-plane endpoints — RDS" is "data-plane-endpoints--rds".
 */
export function slug(s: string): string {
  return githubSlug(s)
}

/**
 * A per-document slugger: repeats are numbered the way GitHub numbers them
 * ("setup", "setup-1", "setup-2"). Use one instance per rendered document.
 */
export function createSlugger(): (s: string) => string {
  const slugger = new GithubSlugger()
  return (s) => slugger.slug(s)
}
