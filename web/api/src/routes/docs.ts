import fs from "node:fs/promises"
import path from "node:path"
import { fileURLToPath } from "node:url"
import { Hono } from "hono"

export const docsRoutes = new Hono()

// Resolve the repo root relative to this file's location.
const __dirname = path.dirname(fileURLToPath(import.meta.url))
const REPO_ROOT = path.resolve(__dirname, "../../../../")
const DOCS_DIR = path.join(REPO_ROOT, "docs", "services")

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

  const filePath = path.join(DOCS_DIR, `${service}.md`)

  // Ensure the resolved path stays within DOCS_DIR (defence-in-depth).
  if (!filePath.startsWith(DOCS_DIR + path.sep) && filePath !== DOCS_DIR) {
    return c.json({ error: "NotFound" }, 404)
  }

  try {
    const content = await fs.readFile(filePath, "utf-8")
    return c.text(content, 200, { "Content-Type": "text/markdown; charset=utf-8" })
  } catch {
    return c.json({ error: "NotFound" }, 404)
  }
})
