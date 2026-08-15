import { Hono } from "hono"
import { resolveEndpoint, ENDPOINT_HEADER } from "../service-discovery.js"

export const settingsRoutes = new Hono()

/**
 * GET /api/ca.pem (mounted at that path in app.ts)
 *
 * The daemon's CA certificate (public half only), proxied from
 * /_overcast/ca.pem — the dev twin of the Go BFF's /api/ca.pem — so the
 * settings page's "download certificate" affordance works identically under
 * the Vite dev server. 404 until a CA has been minted.
 */
export const caPemRoutes = new Hono().get("/", async (c) => {
  const endpoint = resolveEndpoint({ [ENDPOINT_HEADER]: c.req.header(ENDPOINT_HEADER) })
  const res = await fetch(`${endpoint.baseUrl}/_overcast/ca.pem`)
  return c.newResponse(res.body, res.status as 200, {
    "Content-Type": res.headers.get("Content-Type") ?? "application/x-pem-file",
  })
})

/**
 * GET /api/settings/https
 *
 * Proxies GET /_overcast/tls/status — the daemon-side view of the HTTPS
 * setup: serving mode, whether the local CA exists, trust-store state, and
 * the host-side command when the daemon cannot install trust itself.
 */
settingsRoutes.get("/https", async (c) => {
  const endpoint = resolveEndpoint({ [ENDPOINT_HEADER]: c.req.header(ENDPOINT_HEADER) })
  const res = await fetch(`${endpoint.baseUrl}/_overcast/tls/status`)
  return c.newResponse(res.body, res.status as 200, {
    "Content-Type": res.headers.get("Content-Type") ?? "application/json",
  })
})

/**
 * POST /api/settings/https/enable
 *
 * Proxies POST /_overcast/tls/setup — the in-daemon `overcast https enable`.
 * The browser's Origin header is forwarded so the daemon's cross-origin
 * guard judges the real page origin rather than this proxy's header-less
 * dial (see internal/router/tls_settings.go). No fetch timeout is set: a
 * native trust install blocks on the OS's approval prompt for as long as the
 * user takes to read it.
 */
settingsRoutes.post("/https/enable", async (c) => {
  const endpoint = resolveEndpoint({ [ENDPOINT_HEADER]: c.req.header(ENDPOINT_HEADER) })
  const origin = c.req.header("Origin")
  const res = await fetch(`${endpoint.baseUrl}/_overcast/tls/setup`, {
    method: "POST",
    headers: origin ? { Origin: origin } : undefined,
  })
  return c.newResponse(res.body, res.status as 200, {
    "Content-Type": res.headers.get("Content-Type") ?? "application/json",
  })
})
