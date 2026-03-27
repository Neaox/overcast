/**
 * Vite dev-server middleware entry point.
 *
 * Mounts the Hono app as a Vite plugin so `/api/*` requests are handled
 * by the same process as the HMR dev server — no second terminal needed
 * during development.
 *
 * Request and response bodies are streamed — not buffered — so large S3
 * object downloads and uploads don't accumulate in memory.
 *
 * In production the standalone `node.ts` entry is used instead.
 */
import { Readable } from "node:stream"
import type { ReadableStream } from "node:stream/web"
import type { Plugin } from "vite"
import type { IncomingMessage, ServerResponse } from "node:http"
import { app } from "./app.js"

export function honoDevPlugin(): Plugin {
  return {
    name: "overcast-api",
    configureServer(server) {
      server.middlewares.use(async (req: IncomingMessage, res: ServerResponse, next) => {
        if (!req.url?.startsWith("/api")) return next()

        try {
          const protocol = "http"
          const host = req.headers.host ?? "localhost"
          const url = `${protocol}://${host}${req.url}`

          // Flatten Node headers (string | string[] | undefined) → Headers
          const headers = new Headers()
          for (const [key, val] of Object.entries(req.headers)) {
            if (val === undefined) continue
            if (Array.isArray(val)) {
              val.forEach((v) => headers.append(key, v))
            } else {
              headers.set(key, val)
            }
          }

          // Stream the request body through as a ReadableStream — no buffering.
          const hasBody = req.method !== "GET" && req.method !== "HEAD"
          const body = hasBody ? (Readable.toWeb(req) as ReadableStream) : undefined

          const webReq = new Request(url, {
            method: req.method ?? "GET",
            headers,
            body,
            // Required when body is a stream
            ...(body ? { duplex: "half" } : {}),
          } as RequestInit)

          const webRes = await app.fetch(webReq)

          res.statusCode = webRes.status
          webRes.headers.forEach((val, key) => res.setHeader(key, val))

          if (!webRes.body) {
            res.end()
            return
          }

          const isSSE = webRes.headers.get("content-type")?.includes("text/event-stream")

          if (isSSE) {
            // Flush HTTP headers to the browser immediately — SSE won't work
            // without this because there may be a long wait before the first
            // data frame arrives and Node buffers headers until first write.
            res.flushHeaders()
          }

          const readable = Readable.fromWeb(webRes.body as ReadableStream)

          readable.pipe(res, { end: true })

          // Propagate errors from the upstream stream to avoid unhandled
          // rejection crashing the Vite dev server.
          readable.on("error", () => res.end())

          // When the browser disconnects, destroy the readable so the upstream
          // fetch (and SSE subscription on the emulator) is cleaned up.
          res.on("close", () => readable.destroy())
        } catch (err) {
          next(err)
        }
      })
    },
  }
}
