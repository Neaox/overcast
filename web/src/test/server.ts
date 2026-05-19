/**
 * MSW Node server — used in Vitest (jsdom) tests.
 *
 * Import `server` wherever you need to override handlers for a
 * specific test or describe block:
 *
 *   import { server } from "@/test/server"
 *   import { http, HttpResponse } from "msw"
 *
 *   describe("when list is empty", () => {
 *     beforeEach(() => {
 *       server.use(http.get("/api/s3/buckets", () => HttpResponse.json({ buckets: [] })))
 *     })
 *   })
 *
 * The server is started/stopped globally in src/test/setup.ts.
 */

import { setupServer } from "msw/node"
import { handlers } from "./handlers"

export const server = setupServer(...handlers)
