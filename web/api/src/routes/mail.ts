import { Hono } from "hono"
import { resolveEndpoint, ENDPOINT_HEADER, REGION_HEADER } from "../service-discovery.js"

export const mailRoutes = new Hono()

function endpoint(c: { req: { header: (k: string) => string | undefined } }) {
  return resolveEndpoint({
    [ENDPOINT_HEADER]: c.req.header(ENDPOINT_HEADER),
    [REGION_HEADER]: c.req.header(REGION_HEADER),
  })
}

// ─── List messages ─────────────────────────────────────────────────────────

/**
 * GET /api/mail/messages[?limit=N]
 *
 * Returns all captured SMTP messages, newest first.
 */
mailRoutes.get("/messages", async (c) => {
  const ep = endpoint(c)
  const limit = c.req.query("limit")
  const url = `${ep.baseUrl}/_overcast/inbox/messages${limit ? `?limit=${limit}` : ""}`
  const res = await fetch(url)
  if (!res.ok) return c.json({ error: "mail fetch failed" }, 502)
  return c.json(await res.json())
})

// ─── Get single message ────────────────────────────────────────────────────

/**
 * GET /api/mail/messages/:id
 */
mailRoutes.get("/messages/:id", async (c) => {
  const ep = endpoint(c)
  const res = await fetch(`${ep.baseUrl}/_overcast/inbox/messages/${c.req.param("id")}`)
  if (res.status === 404) return c.json({ error: "not found" }, 404)
  if (!res.ok) return c.json({ error: "mail fetch failed" }, 502)
  return c.json(await res.json())
})

// ─── Clear all messages ────────────────────────────────────────────────────

/**
 * DELETE /api/mail/messages
 *
 * Clears the entire captured mail inbox.
 */
mailRoutes.delete("/messages", async (c) => {
  const ep = endpoint(c)
  const res = await fetch(`${ep.baseUrl}/_overcast/inbox/messages`, { method: "DELETE" })
  if (!res.ok) return c.json({ error: "mail clear failed" }, 502)
  return new Response(null, { status: 204 })
})

// ─── Delete single message ─────────────────────────────────────────────────

/**
 * DELETE /api/mail/messages/:id
 */
mailRoutes.delete("/messages/:id", async (c) => {
  const ep = endpoint(c)
  const res = await fetch(`${ep.baseUrl}/_overcast/inbox/messages/${c.req.param("id")}`, {
    method: "DELETE",
  })
  if (res.status === 404) return c.json({ error: "not found" }, 404)
  if (!res.ok) return c.json({ error: "mail delete failed" }, 502)
  return new Response(null, { status: 204 })
})
