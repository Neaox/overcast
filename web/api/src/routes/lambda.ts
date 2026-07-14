import { Hono } from "hono"
import { resolveEndpoint, ENDPOINT_HEADER, REGION_HEADER } from "../service-discovery.js"

export const lambdaRoutes = new Hono()

function emulatorUrl(c: { req: { header: (k: string) => string | undefined } }) {
  const endpoint = resolveEndpoint({
    [ENDPOINT_HEADER]: c.req.header(ENDPOINT_HEADER),
    [REGION_HEADER]: c.req.header(REGION_HEADER),
  })
  return endpoint.baseUrl
}

// ─── List runtimes (emulator-only) ───────────────────────────────────────────

lambdaRoutes.get("/runtimes", async (c) => {
  const baseUrl = emulatorUrl(c)
  const res = await fetch(`${baseUrl}/_lambda/runtimes`)
  const data = await res.json()
  return c.json(data, res.status as 200)
})

// ─── Layer metadata (emulator-only) ──────────────────────────────────────────

lambdaRoutes.get("/layers/:layerName/versions/:version/metadata", async (c) => {
  const baseUrl = emulatorUrl(c)
  const layerName = c.req.param("layerName")
  const version = c.req.param("version")
  const res = await fetch(
    `${baseUrl}/_lambda/layers/${encodeURIComponent(layerName)}/versions/${encodeURIComponent(version)}/metadata`,
  )
  const text = await res.text()
  if (!text) {
    return c.body(null, res.status as 200 | 400 | 404 | 500)
  }
  try {
    return c.json(JSON.parse(text) as unknown, res.status as 200 | 400 | 404 | 500)
  } catch {
    return c.text(text, res.status as 200 | 400 | 404 | 500)
  }
})

// ─── Get function source (emulator-only) ─────────────────────────────────────

lambdaRoutes.get("/functions/:name/source", async (c) => {
  const baseUrl = emulatorUrl(c)
  const name = c.req.param("name")
  const fileParam = c.req.query("file")
  const qs = fileParam ? `?file=${encodeURIComponent(fileParam)}` : ""
  const res = await fetch(
    `${baseUrl}/2015-03-31/functions/${encodeURIComponent(name)}/source${qs}`,
  )
  const data = await res.json()
  return c.json(data, res.status as 200 | 404)
})

// ─── Put function source (emulator-only) ─────────────────────────────────────

lambdaRoutes.put("/functions/:name/source", async (c) => {
  const baseUrl = emulatorUrl(c)
  const name = c.req.param("name")
  const body = await c.req.json()
  const res = await fetch(
    `${baseUrl}/2015-03-31/functions/${encodeURIComponent(name)}/source`,
    {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    },
  )
  const data = await res.json()
  return c.json(data, res.status as 200 | 404)
})

// ─── Invoke with progress (emulator-only SSE stream) ──────────────────────────

lambdaRoutes.post("/functions/:name/invoke-with-progress", async (c) => {
  const baseUrl = emulatorUrl(c)
  const name = c.req.param("name")
  const body = await c.req.text()

  const upstream = await fetch(
    `${baseUrl}/2015-03-31/functions/${encodeURIComponent(name)}/invoke-with-progress`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body,
    },
  )

  if (!upstream.ok || !upstream.body) {
    return c.text(await upstream.text(), upstream.status as 200)
  }

  // Stream the SSE response from the backend through to the client.
  return new Response(upstream.body, {
    status: 200,
    headers: {
      "Content-Type": "text/event-stream",
      "Cache-Control": "no-cache",
      Connection: "keep-alive",
      "X-Accel-Buffering": "no",
    },
  })
})

// ─── Saved test events (emulator-only) ────────────────────────────────────────

lambdaRoutes.get("/functions/:name/test-events", async (c) => {
  const baseUrl = emulatorUrl(c)
  const name = c.req.param("name")
  const res = await fetch(
    `${baseUrl}/2015-03-31/functions/${encodeURIComponent(name)}/test-events`,
  )
  const data = await res.json()
  return c.json(data, res.status as 200)
})

lambdaRoutes.put("/functions/:name/test-events/:eventName", async (c) => {
  const baseUrl = emulatorUrl(c)
  const name = c.req.param("name")
  const eventName = c.req.param("eventName")
  const body = await c.req.json()
  const res = await fetch(
    `${baseUrl}/2015-03-31/functions/${encodeURIComponent(name)}/test-events/${encodeURIComponent(eventName)}`,
    {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    },
  )
  const data = await res.json()
  return c.json(data, res.status as 200)
})

lambdaRoutes.delete("/functions/:name/test-events/:eventName", async (c) => {
  const baseUrl = emulatorUrl(c)
  const name = c.req.param("name")
  const eventName = c.req.param("eventName")
  await fetch(
    `${baseUrl}/2015-03-31/functions/${encodeURIComponent(name)}/test-events/${encodeURIComponent(eventName)}`,
    { method: "DELETE" },
  )
  return c.body(null, 204)
})
