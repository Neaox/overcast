/**
 * /api/events — SSE proxy to the emulator's /_events endpoint.
 *
 * The browser cannot pass custom headers with EventSource, so the emulator
 * endpoint and region are accepted as query params ("ep" and "region") in
 * addition to the standard x-overcast-* request headers.
 *
 * Query params forwarded to upstream:
 *   source=s3          filter to one source (may be repeated)
 *   last_event_id=…    resume point, so a reconnect replays only what the
 *                      client is missing rather than the whole history
 *                      buffer (see internal/router/events.go)
 *
 * Must stay in step with the Go BFF's handleEvents (internal/bff/bff.go):
 * this route serves the dev server, that one serves the shipped binary, and
 * a resume point dropped by either is invisible until someone wonders why
 * their event list has three copies of everything.
 */
import { Hono } from "hono"
import { ENDPOINT_HEADER, REGION_HEADER, DEFAULT_ENDPOINT } from "../service-discovery.js"

export const eventsRoutes = new Hono()

eventsRoutes.get("/", async (c) => {
  // Support header OR query-param delivery (EventSource can't send custom headers).
  const endpointUrl = c.req.header(ENDPOINT_HEADER) ?? c.req.query("ep") ?? DEFAULT_ENDPOINT.baseUrl
  const region = c.req.header(REGION_HEADER) ?? c.req.query("region") ?? DEFAULT_ENDPOINT.region

  // Build upstream URL: preserve any source filters passed by the client.
  const upstream = new URL("/_events", endpointUrl)
  const sources = c.req.queries("source") ?? []
  for (const s of sources) upstream.searchParams.append("source", s)
  const resumeParam = c.req.query("last_event_id")
  if (resumeParam) upstream.searchParams.set("last_event_id", resumeParam)

  // Propagate client abort signal to the upstream fetch so the emulator
  // subscription is cancelled immediately when the browser tab closes.
  // Use a local AbortController so we can trigger it from either the
  // request signal (production node server) or the response close event
  // (Vite dev middleware, which doesn't wire signal to the raw Request).
  const ac = new AbortController()
  c.req.raw.signal.addEventListener("abort", () => ac.abort())

  let response: Response
  try {
    response = await fetch(upstream.toString(), {
      headers: {
        Accept: "text/event-stream",
        "x-overcast-endpoint": endpointUrl,
        "x-overcast-region": region,
        // Sent by the browser's EventSource on its own automatic reconnect.
        ...(c.req.header("Last-Event-ID")
          ? { "Last-Event-ID": c.req.header("Last-Event-ID")! }
          : {}),
      },
      signal: ac.signal,
    })
  } catch {
    // Emulator is not reachable (e.g. starting up or stopped).
    return c.json({ error: "event stream unavailable" }, 502)
  }

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
