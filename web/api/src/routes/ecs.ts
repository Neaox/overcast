import { Hono } from "hono"
import { resolveEndpoint, ENDPOINT_HEADER, REGION_HEADER } from "../service-discovery.js"

export const ecsRoutes = new Hono()

function emulatorUrl(c: { req: { header: (k: string) => string | undefined } }) {
  const endpoint = resolveEndpoint({
    [ENDPOINT_HEADER]: c.req.header(ENDPOINT_HEADER),
    [REGION_HEADER]: c.req.header(REGION_HEADER),
  })
  return endpoint.baseUrl
}

// ─── Get task logs (emulator-only) ────────────────────────────────────────

ecsRoutes.get("/tasks/:taskArn/logs/:container", async (c) => {
  const baseUrl = emulatorUrl(c)
  const taskArn = c.req.param("taskArn")
  const container = c.req.param("container")
  const res = await fetch(
    `${baseUrl}/_ecs/tasks/${encodeURIComponent(taskArn)}/logs/${encodeURIComponent(container)}`,
  )
  const data = await res.json()
  return c.json(data, res.status as 200)
})

// ─── List cluster tasks (emulator-only) ───────────────────────────────────

ecsRoutes.get("/clusters/:cluster/tasks", async (c) => {
  const baseUrl = emulatorUrl(c)
  const cluster = c.req.param("cluster")
  const res = await fetch(`${baseUrl}/_ecs/clusters/${encodeURIComponent(cluster)}/tasks`)
  const data = await res.json()
  return c.json(data, res.status as 200)
})
