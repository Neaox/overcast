/**
 * lambda-instances route — emulator-specific endpoint for Lambda instance tracking.
 *
 * GET /api/lambda/instances
 *
 * Returns the current snapshot of warm/running Lambda execution instances from
 * the /_lambda/instances emulator endpoint. Used by the topology map to render
 * per-function instance sub-nodes.
 */
import { Hono } from "hono"
import { resolveEndpoint, ENDPOINT_HEADER, REGION_HEADER } from "../service-discovery.js"

export const lambdaInstancesRoutes = new Hono()

export interface LambdaInstance {
  /** Stable UUID assigned at cold start, preserved across warm reuses of the same container. */
  instanceId: string
  functionName: string
  /** "running" while an invocation is in progress; "idle" between invocations. */
  status: "running" | "idle"
  /** Unix milliseconds — when the instance was first created (cold start). */
  startedAt: number
  /** Unix milliseconds — when the last invocation completed. */
  lastUsed: number
  /** Unix milliseconds — lastUsed + 15 min idle TTL. */
  expiresAt: number
  logGroup?: string
  logStream?: string
  /** Full JSON payload of the event that last triggered this instance. */
  triggerEvent?: unknown
  /** Reserved for future real metrics collection. Currently always 0. */
  memoryUsedMB: number
  /** Reserved for future real metrics collection. Currently always 0. */
  cpuPercent: number
}

export interface LambdaInstancesResponse {
  instances: LambdaInstance[]
}

// GET /api/lambda/instances
lambdaInstancesRoutes.get("/instances", async (c) => {
  const endpoint = resolveEndpoint({
    [ENDPOINT_HEADER]: c.req.header(ENDPOINT_HEADER),
    [REGION_HEADER]: c.req.header(REGION_HEADER),
  })

  try {
    const res = await fetch(`${endpoint.baseUrl}/_lambda/instances`, {
      headers: { Accept: "application/json" },
    })
    if (!res.ok) {
      // Emulator may not have any functions yet; return empty list gracefully.
      return c.json<LambdaInstancesResponse>({ instances: [] })
    }
    const data = (await res.json()) as { instances?: Array<LambdaInstance & { id?: string }> }
    // The emulator uses "id"; the frontend contract uses "instanceId". Normalise here.
    const instances = (data.instances ?? []).map((inst) => ({
      ...inst,
      instanceId: inst.instanceId || inst.id || "",
    }))
    return c.json<LambdaInstancesResponse>({ instances })
  } catch {
    // Emulator unreachable — return empty list rather than failing the whole page.
    return c.json<LambdaInstancesResponse>({ instances: [] })
  }
})
