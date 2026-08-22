import { Hono, type Context } from "hono"
import { cors } from "hono/cors"
import { logger } from "hono/logger"
import { HTTPException } from "hono/http-exception"
import { GO_BFF_ENDPOINT } from "./service-discovery.js"

const app = new Hono()

// ─── Trailing-slash normalisation ─────────────────────────────────────────
// The built-in trimTrailingSlash middleware only handles GET/HEAD (via 301).
// For API methods (POST/PUT/PATCH/DELETE) a redirect would lose the request
// body, so we rewrite the URL internally before routing instead.
app.use("*", async (c, next) => {
  const path = c.req.path
  if (path !== "/" && path.endsWith("/")) {
    const url = new URL(c.req.url)
    url.pathname = url.pathname.slice(0, -1)
    if (c.req.method === "GET" || c.req.method === "HEAD") {
      return c.redirect(url.toString(), 301)
    }
    // Mutation methods: internal re-dispatch — body is forwarded unchanged.
    const raw = c.req.raw
    return app.fetch(
      new Request(url.toString(), {
        method: raw.method,
        headers: raw.headers,
        body: raw.body,
        ...(raw.body ? ({ duplex: "half" } as object) : {}),
      }),
    )
  }
  return next()
})

app.use(
  "*",
  cors({
    origin: [
      "http://localhost:3000",
      "https://localhost:3000",
      "http://localhost:5173",
      "https://localhost:5173",
    ],
    allowMethods: ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"],
    allowHeaders: ["Content-Type", "x-overcast-endpoint", "x-overcast-region"],
    credentials: true,
  }),
)

if (process.env.NODE_ENV !== "production") {
  app.use("*", logger())
}

// ─── Global error handler ──────────────────────────────────────────────────
// A thrown network error (the proxy fetch below failing to reach the Go BFF)
// is the one error this app still has to translate itself — everything else
// about an /api/* response, including AWS SDK errors, is now decided by the
// Go BFF and passed through verbatim by the proxy handler.
export function apiErrorHandler(err: Error, c: Context) {
  if (err instanceof HTTPException) return err.getResponse()
  const cause = (err as NodeJS.ErrnoException).cause as NodeJS.ErrnoException | undefined
  const code = (err as NodeJS.ErrnoException).code ?? cause?.code
  if (
    code === "ECONNREFUSED" ||
    code === "ECONNRESET" ||
    code === "ENOTFOUND" ||
    code === "ETIMEDOUT"
  ) {
    return c.json(
      {
        error: "OvercastUnavailable",
        message:
          `Could not reach the Go BFF at ${GO_BFF_ENDPOINT} — run a built overcast ` +
          "(`overcast serve` / `make run`) alongside `pnpm dev`. See " +
          "docs/dev/development-setup.md.",
      },
      502,
    )
  }
  console.error(err)
  return c.json({ error: "InternalError", message: err.message }, 500)
}

app.onError(apiErrorHandler)

// ─── /api/* → proxy to the Go BFF ──────────────────────────────────────────
// Every /api/* route the console calls is implemented exactly once, in the Go
// BFF (internal/bff/bff.go) embedded in the same overcast binary this dev
// server already depends on for AWS calls (the emulator). Rather than
// hand-mirroring each route in TypeScript — the drift class
// docs/plans/dev-bff-consolidation.md (B1) tracks, where a Go-only route
// silently 404s here until someone remembers to port it (#1104; the
// CloudFormation stack-diagnostics tab was the concrete instance) — forward
// the request byte-for-byte to the Go BFF and let it answer, streaming or not.
//
// Method, path, query string, headers (including x-overcast-endpoint and
// x-overcast-region, however the browser delivered them — as headers or, for
// EventSource/download-link requests that cannot set custom headers, as query
// params the Go BFF's own resolveEndpointQP already understands) and body all
// pass through unchanged; only the small hop-by-hop / framing headers below
// are stripped so Node's HTTP stack (not a stale upstream value) decides them.
const HOP_BY_HOP_REQUEST_HEADERS = ["host", "connection", "keep-alive", "content-length"]
const HOP_BY_HOP_RESPONSE_HEADERS = [
  "connection",
  "keep-alive",
  "transfer-encoding",
  "content-encoding",
  "content-length",
]

async function proxyToGoBFF(c: Context): Promise<Response> {
  const url = new URL(c.req.url)
  const target = new URL(url.pathname + url.search, GO_BFF_ENDPOINT)

  const headers = new Headers(c.req.raw.headers)
  for (const h of HOP_BY_HOP_REQUEST_HEADERS) headers.delete(h)

  const hasBody = c.req.method !== "GET" && c.req.method !== "HEAD"

  const upstream = await fetch(target, {
    method: c.req.method,
    headers,
    body: hasBody ? c.req.raw.body : undefined,
    signal: c.req.raw.signal,
    ...(hasBody ? { duplex: "half" } : {}),
  })

  const resHeaders = new Headers(upstream.headers)
  for (const h of HOP_BY_HOP_RESPONSE_HEADERS) resHeaders.delete(h)

  return new Response(upstream.body, { status: upstream.status, headers: resHeaders })
}

app.all("/api", proxyToGoBFF)
app.all("/api/*", proxyToGoBFF)

export { app }
