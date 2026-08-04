import fs from "node:fs/promises"
import path from "node:path"
import { fileURLToPath } from "node:url"
import { Hono } from "hono"

export const docsRoutes = new Hono()

// Resolve the repo root relative to this file's location.
const __dirname = path.dirname(fileURLToPath(import.meta.url))
const REPO_ROOT = path.resolve(__dirname, "../../../../")
const DOCS_ROOT = path.join(REPO_ROOT, "docs")
const DOCS_SERVICES_DIR = path.join(DOCS_ROOT, "services")
const DOCS_INDEX_PATH = path.join(REPO_ROOT, "internal", "docssearch", "index.gen.jsonl")

interface DocsIndexLine {
  path: string
  href: string
  title: string
  description: string
  section: string
  tags: string[]
  terms: string
}

interface DocsIndexEntry extends Omit<DocsIndexLine, "terms"> {
  id: number
  terms: Map<string, number>
}

let docsIndexCache: DocsIndexEntry[] | null = null

/**
 * Read the same generated search index the Go BFF embeds
 * (internal/docssearch/index.gen.jsonl): one JSON line per doc, each carrying
 * its own "term:score" pairs. Sharing the artifact - and the tokenizer and
 * ranking below - is what keeps dev-mode search answering like production. A
 * term may itself contain ':', so each pair splits on its last colon.
 */
async function loadDocsIndex(): Promise<DocsIndexEntry[]> {
  if (docsIndexCache) return docsIndexCache
  const raw = await fs.readFile(DOCS_INDEX_PATH, "utf-8")
  const entries: DocsIndexEntry[] = []
  for (const line of raw.split("\n")) {
    const trimmed = line.trim()
    if (!trimmed || trimmed.startsWith("#")) continue
    const parsed = JSON.parse(trimmed) as DocsIndexLine
    const terms = new Map<string, number>()
    for (const pair of parsed.terms.split(" ").filter(Boolean)) {
      const sep = pair.lastIndexOf(":")
      if (sep <= 0 || sep === pair.length - 1) throw new Error(`malformed term "${pair}"`)
      const score = Number(pair.slice(sep + 1))
      if (!Number.isInteger(score)) throw new Error(`malformed score in term "${pair}"`)
      terms.set(pair.slice(0, sep), score)
    }
    entries.push({ ...parsed, id: entries.length, terms })
  }
  if (entries.length === 0) throw new Error("docs index is empty; run: make docs-index")
  docsIndexCache = entries
  return docsIndexCache
}

function safeDocsPath(docPath: string): boolean {
  return (
    docPath.length > 0 &&
    docPath.endsWith(".md") &&
    docPath !== "plans" &&
    !docPath.startsWith("plans/") &&
    docPath !== "dev" &&
    !docPath.startsWith("dev/") &&
    !docPath.includes("..") &&
    !docPath.startsWith("/") &&
    !docPath.startsWith("\\")
  )
}

/**
 * Strip a leading YAML frontmatter block. Mirrors the Go BFF's
 * serveDocMarkdown behavior (internal/bff/bff.go) - the two BFFs must stay
 * in lockstep on doc handling or dev mode renders frontmatter as content.
 */
function stripFrontmatter(content: string): string {
  if (!content.startsWith("---")) return content
  const end = content.indexOf("\n---", 3)
  if (end === -1) return content
  const after = content.indexOf("\n", end + 4)
  return after === -1 ? "" : content.slice(after + 1).replace(/^\s*\n/, "")
}

const STOPWORDS = new Set([
  "a", "an", "and", "are", "as", "at", "be", "by", "for", "from", "in", "into",
  "is", "it", "of", "on", "or", "the", "this", "to", "with",
])

const LOWER = /\p{Ll}/u
const UPPER = /\p{Lu}/u
const LETTER = /\p{L}/u
const DIGIT = /\p{Nd}/u

function isIdentifierBoundary(prev: string, curr: string): boolean {
  return (
    (LOWER.test(prev) && UPPER.test(curr)) ||
    (LETTER.test(prev) && DIGIT.test(curr)) ||
    (DIGIT.test(prev) && LETTER.test(curr))
  )
}

/**
 * Port of tokenize in internal/docssearch/search.go. The two must agree: the
 * index is keyed by whatever that tokenizer produced, so a query tokenized any
 * other way here would miss terms that exist. "StartLiveTail" splits into
 * "start live tail" so a CamelCase operation name is findable by its words.
 */
function tokenize(s: string): string[] {
  let split = ""
  let prev = ""
  for (const ch of s) {
    if (prev && isIdentifierBoundary(prev, ch)) split += " "
    split += ch
    prev = ch
  }
  let normalised = ""
  let lastSpace = true
  for (const ch of split.toLowerCase()) {
    if (LETTER.test(ch) || DIGIT.test(ch) || ch === "-" || ch === "_" || ch === ":" || ch === "/") {
      normalised += ch
      lastSpace = false
      continue
    }
    if (!lastSpace) {
      normalised += " "
      lastSpace = true
    }
  }
  return normalised
    .split(/\s+/)
    .filter(Boolean)
    .map((field) => field.replace(/^[-_:/. ]+/, "").replace(/[-_:/. ]+$/, ""))
    .filter((field) => field.length >= 2 && !STOPWORDS.has(field))
}

/**
 * Port of Search in internal/docssearch/search.go: a doc matches only when it
 * holds every query token, and ranks by the sum of its per-term scores, ties
 * broken by title.
 */
function searchDocs(index: DocsIndexEntry[], query: string, limit: number) {
  const tokens = [...new Set(tokenize(query))]
  if (tokens.length === 0) return []
  const results = []
  for (const entry of index) {
    let score = 0
    let hits = 0
    for (const token of tokens) {
      const termScore = entry.terms.get(token)
      if (termScore !== undefined) {
        score += termScore
        hits++
      }
    }
    if (hits !== tokens.length) continue
    results.push({
      ID: entry.id,
      Path: entry.path,
      Href: entry.href,
      Title: entry.title,
      Description: entry.description,
      Section: entry.section,
      Tags: entry.tags,
      Score: score,
    })
  }
  results.sort((a, b) => (b.Score !== a.Score ? b.Score - a.Score : a.Title < b.Title ? -1 : a.Title > b.Title ? 1 : 0))
  return results.slice(0, limit)
}

docsRoutes.get("/search", async (c) => {
  const query = c.req.query("q")?.trim() ?? ""
  const limit = Math.min(Math.max(Number(c.req.query("limit") ?? 10), 1), 50)
  if (!query) return c.json({ query, results: [] })

  let index: DocsIndexEntry[]
  try {
    index = await loadDocsIndex()
  } catch (err) {
    // Match the Go BFF: an index that cannot be read is reported, not
    // flattened into "no results" over a search box that looks like it works.
    return c.text(
      `docs search index unavailable (${(err as Error).message}). The docs pages themselves are unaffected.`,
      503,
    )
  }
  return c.json({ query, results: searchDocs(index, query, limit) })
})

docsRoutes.get("/page", async (c) => {
  const docPath = c.req.query("path")?.trim() ?? ""
  if (!safeDocsPath(docPath)) return c.json({ error: "NotFound" }, 404)

  const filePath = path.join(DOCS_ROOT, docPath)
  if (!filePath.startsWith(DOCS_ROOT + path.sep)) return c.json({ error: "NotFound" }, 404)

  try {
    const content = await fs.readFile(filePath, "utf-8")
    return c.text(stripFrontmatter(content), 200, { "Content-Type": "text/markdown; charset=utf-8" })
  } catch {
    return c.json({ error: "NotFound" }, 404)
  }
})

/**
 * GET /api/docs/:service
 *
 * Returns the raw Markdown content of docs/services/{service}.md.
 * Returns 404 if the file does not exist.
 */
docsRoutes.get("/:service", async (c) => {
  const service = c.req.param("service")

  // Sanitise: only allow alphanumeric, hyphens and underscores to prevent
  // path traversal attacks.
  if (!/^[a-z0-9_-]+$/i.test(service)) {
    return c.json({ error: "NotFound" }, 404)
  }

  const filePath = path.join(DOCS_SERVICES_DIR, `${service}.md`)

  // Ensure the resolved path stays within DOCS_SERVICES_DIR (defence-in-depth).
  if (!filePath.startsWith(DOCS_SERVICES_DIR + path.sep) && filePath !== DOCS_SERVICES_DIR) {
    return c.json({ error: "NotFound" }, 404)
  }

  try {
    const content = await fs.readFile(filePath, "utf-8")
    return c.text(stripFrontmatter(content), 200, { "Content-Type": "text/markdown; charset=utf-8" })
  } catch {
    return c.json({ error: "NotFound" }, 404)
  }
})
