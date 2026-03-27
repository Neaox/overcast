import { Hono } from "hono"
import { resolveEndpoint, ENDPOINT_HEADER, REGION_HEADER } from "../service-discovery.js"

export const healthRoutes = new Hono()

/**
 * GET /api/health
 *
 * Proxies /_health from the configured emulator endpoint and returns the
 * response including the list of enabled services.
 */
healthRoutes.get("/", async (c) => {
  const endpoint = resolveEndpoint({
    [ENDPOINT_HEADER]: c.req.header(ENDPOINT_HEADER),
    [REGION_HEADER]: c.req.header(REGION_HEADER),
  })

  const res = await fetch(`${endpoint.baseUrl}/_health`)
  if (!res.ok) {
    return c.json({ error: "health check failed" }, 502)
  }

  const data = await res.json()
  return c.json(data)
})
