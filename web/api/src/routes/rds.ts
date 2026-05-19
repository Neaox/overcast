import { Hono } from "hono"
import { resolveEndpoint, ENDPOINT_HEADER, REGION_HEADER } from "../service-discovery.js"

export const rdsRoutes = new Hono()

function emulatorUrl(c: { req: { header: (k: string) => string | undefined } }) {
  const endpoint = resolveEndpoint({
    [ENDPOINT_HEADER]: c.req.header(ENDPOINT_HEADER),
    [REGION_HEADER]: c.req.header(REGION_HEADER),
  })
  return endpoint.baseUrl
}

// ─── Get instance logs (emulator-only) ────────────────────────────────────

rdsRoutes.get("/instances/:id/logs", async (c) => {
  const baseUrl = emulatorUrl(c)
  const id = c.req.param("id")
  const res = await fetch(`${baseUrl}/_rds/instances/${encodeURIComponent(id)}/logs`)
  const data = await res.json()
  return c.json(data, res.status as 200)
})
