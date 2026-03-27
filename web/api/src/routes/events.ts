/**
 * /api/events — SSE proxy to the emulator's /_events endpoint.
 *
 * The browser cannot pass custom headers with EventSource, so the emulator
 * endpoint and region are accepted as query params ("ep" and "region") in
 * addition to the standard x-overcast-* request headers.
 *
 * Query params forwarded to upstream:
 *   source=s3          filter to one source (may be repeated)
 */
import { Hono } from "hono"
import { ENDPOINT_HEADER, REGION_HEADER } from "../service-discovery.js"

export const eventsRoutes = new Hono()

eventsRoutes.get("/", async (c) => {
  // Support header OR query-param delivery (EventSource can't send custom headers).
  const endpointUrl = c.req.header(ENDPOINT_HEADER) ?? c.req.query("ep") ?? "http://localhost:4566"
  const region = c.req.header(REGION_HEADER) ?? c.req.query("region") ?? "us-east-1"

  // Build upstream URL: preserve any source filters passed by the client.
  const upstream = new URL("/_events", endpointUrl)
  const sources = c.req.queries("source") ?? []
  for (const s of sources) upstream.searchParams.append("source", s)

  // Propagate client abort signal to the upstream fetch so the emulator
  // subscription is cancelled immediately when the browser tab closes.
  // Use a local AbortController so we can trigger it from either the
  // request signal (production node server) or the response close event
  // (Vite dev middleware, which doesn't wire signal to the raw Request).
  const ac = new AbortController()
  c.req.raw.signal.addEventListener("abort", () => ac.abort())

  const response = await fetch(upstream.toString(), {
    headers: {
      Accept: "text/event-stream",
      "x-overcast-endpoint": endpointUrl,
      "x-overcast-region": region,
    },
    signal: ac.signal,
  })

  if (!response.ok || !response.body) {
    return c.json({ error: "event stream unavailable" }, 502)
  }

  // Pipe the upstream SSE stream straight through — no buffering.
  return new Response(response.body, {
    headers: {
      "Content-Type": "text/event-stream",
      "Cache-Control": "no-cache",
      Connection: "keep-alive",
      "X-Accel-Buffering": "no",
    },
  })
})
