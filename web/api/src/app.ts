import { Hono, type Context } from "hono"
import { cors } from "hono/cors"
import { logger } from "hono/logger"
import { HTTPException } from "hono/http-exception"

// ─── Lazy route loading ───────────────────────────────────────────────────
// Route modules that import AWS SDK clients are loaded on first request to
// their prefix instead of at import time. This avoids ~7s of synchronous
// SDK initialisation blocking Vite startup.

type LazyRoute = {
  app?: Hono
  loading?: Promise<Hono>
}

function lazyRoute(loader: () => Promise<Hono>): Hono {
  const state: LazyRoute = {}
  const proxy = new Hono()
  // Attach the shared error handler so that errors from merged sub-app routes
  // (after the first request) are handled correctly instead of defaulting to 500.
  proxy.onError(apiErrorHandler)
  proxy.all("*", async (c) => {
    if (!state.app) {
      state.loading ??= loader().then((a) => {
        // Also attach to the sub-app itself so that state.app.fetch() calls
        // (used on the first request) translate AWS errors to the right status.
        a.onError(apiErrorHandler)
        state.app = a
        state.loading = undefined
        return a
      })
      state.app = await state.loading
      // Mount the real app into the proxy so future requests bypass the
      // catch-all and benefit from Hono's normal prefix stripping.
      proxy.route("/", state.app)
    }
    // For the first request(s): strip our mount prefix from the URL.
    // c.req.routePath is e.g. "/api/health/*" — extract the mount prefix.
    const routePrefix = c.req.routePath.replace(/\/?\*$/, "")
    const url = new URL(c.req.url)
    url.pathname = url.pathname.startsWith(routePrefix)
      ? url.pathname.slice(routePrefix.length) || "/"
      : url.pathname
    const req = new Request(url.toString(), c.req.raw)
    return state.app.fetch(req)
  })
  return proxy
}

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
      } as RequestInit),
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

// ─── Global error handler — translates AWS SDK ServiceException to JSON ────
// Exported so lazyRoute can attach it to each lazily-loaded sub-app and its
// proxy Hono instance (both lack onError by default, causing all SDK errors
// to fall through to Hono's built-in 500 fallback).
export function apiErrorHandler(err: Error, c: Context) {
  if (err instanceof HTTPException) return err.getResponse()
  // Network errors reaching the emulator — return 502 instead of 500 so the
  // frontend (and the EventSource retry logic) can distinguish "emulator not
  // ready" from a real server bug.
  const cause = (err as NodeJS.ErrnoException).cause as NodeJS.ErrnoException | undefined
  const code = (err as NodeJS.ErrnoException).code ?? cause?.code
  if (
    code === "ECONNREFUSED" ||
    code === "ECONNRESET" ||
    code === "ENOTFOUND" ||
    code === "ETIMEDOUT"
  ) {
    return c.json(
      { error: "EmulatorUnavailable", message: "Overcast emulator is not reachable" },
      502,
    )
  }
  // AWS SDK errors carry a $metadata object and a name (the error code)
  const awsErr = err as unknown as Record<string, unknown>
  if (awsErr.$metadata) {
    const status = (awsErr.$metadata as { httpStatusCode?: number }).httpStatusCode ?? 500
    return c.json({ error: awsErr.name ?? "AWSError", message: err.message }, status as never)
  }
  console.error(err)
  return c.json({ error: "InternalError", message: err.message ?? "Unknown error" }, 500)
}

app.onError(apiErrorHandler)

// ─── Health (eager — needed for readiness probes) ──────────────────────────
app.route(
  "/api/health",
  lazyRoute(async () => (await import("./routes/health.js")).healthRoutes),
)

// ─── Service routes (lazy — AWS SDK clients loaded on first request) ───────
app.route(
  "/api/s3",
  lazyRoute(async () => (await import("./routes/s3.js")).s3Routes),
)
app.route(
  "/api/sqs",
  lazyRoute(async () => (await import("./routes/sqs.js")).sqsRoutes),
)
app.route(
  "/api/events",
  lazyRoute(async () => (await import("./routes/events.js")).eventsRoutes),
)
app.route(
  "/api/metrics",
  lazyRoute(async () => (await import("./routes/metrics.js")).metricsRoutes),
)
app.route(
  "/api/topology",
  lazyRoute(async () => (await import("./routes/topology.js")).topologyRoutes),
)
app.route(
  "/api/inbox",
  lazyRoute(async () => (await import("./routes/mail.js")).mailRoutes),
)
app.route(
  "/api/lambda",
  lazyRoute(async () => {
    const [{ lambdaRoutes }, { lambdaInstancesRoutes }] = await Promise.all([
      import("./routes/lambda.js"),
      import("./routes/lambda-instances.js"),
    ])
    const merged = new Hono()
    merged.route("/", lambdaRoutes)
    merged.route("/", lambdaInstancesRoutes)
    return merged
  }),
)
app.route(
  "/api/ecs",
  lazyRoute(async () => (await import("./routes/ecs.js")).ecsRoutes),
)
app.route(
  "/api/rds",
  lazyRoute(async () => (await import("./routes/rds.js")).rdsRoutes),
)
app.route(
  "/api/docs",
  lazyRoute(async () => (await import("./routes/docs.js")).docsRoutes),
)
// future: additional services

export { app }
