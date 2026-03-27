/**
 * Standalone Node.js entry point.
 * Used in production (Docker) and for `npm run dev` in the api package.
 *
 * `app` is pure Hono — no Vite involved.
 */
import { serve } from "@hono/node-server"
import { app } from "./app.js"

const port = parseInt(process.env.PORT ?? "3001", 10)

serve({ fetch: app.fetch, port }, (info) => {
  console.log(`[overcast-api] Listening on http://localhost:${info.port}`)
})
