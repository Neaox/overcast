import { Hono, type Context } from "hono"
import { resolveEndpoint, ENDPOINT_HEADER, REGION_HEADER } from "../service-discovery.js"

export const debugRoutes = new Hono()

function endpointFromHeaders(c: Context) {
  return resolveEndpoint({
    [ENDPOINT_HEADER]: c.req.header(ENDPOINT_HEADER),
    [REGION_HEADER]: c.req.header(REGION_HEADER),
  })
}

debugRoutes.get("/state", async (c) => {
  const endpoint = endpointFromHeaders(c)
  const res = await fetch(`${endpoint.baseUrl}/_debug/state`)
  if (res.status === 404) {
    return c.json(
      { error: "DebugDisabled", message: "OVERCAST_DEBUG must be enabled to inspect raw state." },
      404,
    )
  }
  if (!res.ok) return c.json({ error: "debug state fetch failed" }, 502)
  return c.json(await res.json())
})

debugRoutes.get("/state/:namespace", async (c) => {
  const endpoint = endpointFromHeaders(c)
  const namespace = c.req.param("namespace")
  const key = c.req.query("key")
  const search = key ? `?key=${encodeURIComponent(key)}` : ""
  const res = await fetch(`${endpoint.baseUrl}/_debug/state/${encodeURIComponent(namespace)}${search}`)
  if (res.status === 404 && !key) {
    return c.json(
      { error: "DebugDisabled", message: "OVERCAST_DEBUG must be enabled to inspect raw state." },
      404,
    )
  }
  if (key) {
    return new Response(res.body, { status: res.status, headers: Object.fromEntries(res.headers) })
  }
  if (!res.ok) return c.json({ error: "debug namespace fetch failed" }, 502)
  return c.json(await res.json())
})
