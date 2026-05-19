/**
 * Topology route — thin proxy to the emulator's internal /_topology endpoint.
 *
 * GET /api/topology[?region=us-east-1]
 *
 * The emulator builds the topology graph directly from the state store,
 * returning nodes (resources) and edges (connections) across all regions
 * in a single fast response. This BFF route simply forwards the request.
 */

import { Hono } from "hono"
import { resolveEndpoint, ENDPOINT_HEADER, REGION_HEADER } from "../service-discovery.js"

export const topologyRoutes = new Hono()

topologyRoutes.get("/", async (c) => {
  const endpoint = resolveEndpoint({
    [ENDPOINT_HEADER]: c.req.header(ENDPOINT_HEADER),
    [REGION_HEADER]: c.req.header(REGION_HEADER),
  })

  // Forward optional region filter to the emulator's internal topology endpoint.
  const region = c.req.query("region")
  const qs = region ? `?region=${encodeURIComponent(region)}` : ""

  const res = await fetch(`${endpoint.baseUrl}/_topology${qs}`)
  if (!res.ok) {
    return c.json({ error: "topology fetch failed" }, 502)
  }
  return c.json(await res.json())
})
