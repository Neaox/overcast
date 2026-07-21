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
const DOCS_INDEX_PATH = path.join(REPO_ROOT, "web", "src", "docs-index.gen.ts")

interface DocsIndexEntry {
  href: string
  title: string
  description: string
  section: string
  searchText: string
}

let docsIndexCache: DocsIndexEntry[] | null = null

async function loadDocsIndex(): Promise<DocsIndexEntry[]> {
  if (docsIndexCache) return docsIndexCache
  const raw = await fs.readFile(DOCS_INDEX_PATH, "utf-8")
  const start = raw.indexOf("[")
  const end = raw.lastIndexOf("]")
  docsIndexCache = JSON.parse(raw.slice(start, end + 1)) as DocsIndexEntry[]
  return docsIndexCache
}

function safeDocsPath(docPath: string): boolean {
  return (
    docPath.length > 0 &&
    docPath.endsWith(".md") &&
    docPath !== "plans" &&
    !docPath.startsWith("plans/") &&
    !docPath.includes("..") &&
    !docPath.startsWith("/") &&
    !docPath.startsWith("\\")
  )
}

function matchesDoc(entry: DocsIndexEntry, query: string): boolean {
  const tokens = query.toLowerCase().split(/\s+/).filter(Boolean)
  return tokens.every((token) => entry.searchText.includes(token))
}

docsRoutes.get("/search", async (c) => {
  const query = c.req.query("q")?.trim() ?? ""
  const limit = Math.min(Math.max(Number(c.req.query("limit") ?? 10), 1), 50)
  if (!query) return c.json({ query, results: [] })

  try {
    const index = await loadDocsIndex()
    const results = index
      .filter((entry) => matchesDoc(entry, query))
      .slice(0, limit)
      .map((entry, id) => ({
        ID: id,
        Href: entry.href,
        Title: entry.title,
        Description: entry.description,
        Section: entry.section,
        Score: 1,
      }))
    return c.json({ query, results })
  } catch {
    return c.json({ query, results: [] })
  }
})

docsRoutes.get("/page", async (c) => {
  const docPath = c.req.query("path")?.trim() ?? ""
  if (!safeDocsPath(docPath)) return c.json({ error: "NotFound" }, 404)

  const filePath = path.join(DOCS_ROOT, docPath)
  if (!filePath.startsWith(DOCS_ROOT + path.sep)) return c.json({ error: "NotFound" }, 404)

  try {
    const content = await fs.readFile(filePath, "utf-8")
    return c.text(content, 200, { "Content-Type": "text/markdown; charset=utf-8" })
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
    return c.text(content, 200, { "Content-Type": "text/markdown; charset=utf-8" })
  } catch {
    return c.json({ error: "NotFound" }, 404)
  }
})
