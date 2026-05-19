import { Hono } from "hono"
import { resolveEndpoint, ENDPOINT_HEADER, REGION_HEADER } from "../service-discovery.js"

export const metricsRoutes = new Hono()

/**
 * GET /api/metrics
 *
 * Proxies /_metrics from the configured emulator endpoint and returns a
 * snapshot of Go runtime statistics (memory, GC, goroutines, uptime).
 */
metricsRoutes.get("/", async (c) => {
  const endpoint = resolveEndpoint({
    [ENDPOINT_HEADER]: c.req.header(ENDPOINT_HEADER),
    [REGION_HEADER]: c.req.header(REGION_HEADER),
  })

  const res = await fetch(`${endpoint.baseUrl}/_metrics`)
  if (!res.ok) {
    return c.json({ error: "metrics fetch failed" }, 502)
  }

  const data = await res.json()
  return c.json(data)
})
