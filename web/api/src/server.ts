/**
 * Production BFF server for the Docker console image.
 *
 * Serves:
 *   - /api/*  → Hono BFF routes (proxies to Go backend)
 *   - /*      → SPA static files with index.html fallback
 *
 * Port defaults to 8080 (non-root safe), configurable via WEB_PORT env var.
 *
 * The emulator endpoint is read from EMULATOR_ENDPOINT (or constructed from
 * OVERCAST_PORT for backward compatibility). Falls back to localhost:4566.
 *
 * Binds to 0.0.0.0 so Docker Desktop port-publish works on all host OSes.
 */
import { readFile } from "node:fs/promises"
import { serve } from "@hono/node-server"
import { serveStatic } from "@hono/node-server/serve-static"
import { Hono } from "hono"
import { app as api } from "./app.js"

const apiPort = process.env.OVERCAST_PORT || "4566"
const endpointUrl =
  process.env.EMULATOR_ENDPOINT || `http://localhost:${apiPort}`
const region = process.env.OVERCAST_DEFAULT_REGION || "us-east-1"

const bootstrapScript = `<script>window.__OVERCAST__=${JSON.stringify({ apiBaseUrl: endpointUrl, region })};</script>`
const headClose = /<\/head>/i

let cachedIndexHtml: string | null = null

async function getInjectedIndexHtml(): Promise<string> {
  if (cachedIndexHtml) return cachedIndexHtml
  const raw = await readFile("/app/web/index.html", "utf-8")
  cachedIndexHtml = raw.replace(headClose, bootstrapScript + "</head>")
  return cachedIndexHtml
}

const server = new Hono()

// Mount API routes first — they take precedence over static files.
server.route("/", api)

// Inject window.__OVERCAST__ into index.html (exact path match).
server.get("/index.html", async (c) => {
  return c.html(await getInjectedIndexHtml())
})

// Serve SPA static assets from /app/web (copied in Dockerfile).
server.use(
  "*",
  serveStatic({
    root: "/app/web",
    rewriteRequestPath: (path) => path,
  }),
)

// SPA fallback — serve index.html with window.__OVERCAST__ injected for
// client-side routing. Calls to / are injected too so the SPA knows the
// emulator endpoint without client-side guessing.
server.use("*", async (c) => {
  return c.html(await getInjectedIndexHtml())
})

const port = parseInt(process.env.WEB_PORT ?? "8080", 10)
serve({ fetch: server.fetch, hostname: "0.0.0.0", port }, () => {
  console.log(`[overcast-web] Console listening on http://0.0.0.0:${port}`)
})
