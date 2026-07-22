/**
 * Default MSW request handlers for the BFF `/api/*` routes.
 *
 * Each handler mirrors a real BFF endpoint (web/api/src/routes/).
 * Tests override specific handlers with `server.use(...)` inside
 * a `describe` block or individual test — see render.tsx for the
 * `server` import.
 *
 * Keep handlers minimal: return the smallest shape that makes the
 * component under test render without errors. Detailed fixture
 * scenarios belong in the individual test files.
 */

import { http, HttpResponse } from "msw"

// ─── Health ───────────────────────────────────────────────────────────────

export const healthHandlers = [
  http.get("/api/health", () =>
    HttpResponse.json({
      status: "ok",
      services: ["s3", "sqs", "dynamodb", "lambda", "sns"],
    }),
  ),
]

// ─── S3 ───────────────────────────────────────────────────────────────────

export const s3Handlers = [http.get("/api/s3/buckets", () => HttpResponse.json({ buckets: [] }))]

// ─── SQS ──────────────────────────────────────────────────────────────────

export const sqsHandlers = [http.get("/api/sqs/queues", () => HttpResponse.json({ queues: [] }))]

// ─── ECR ──────────────────────────────────────────────────────────────────

export const ecrHandlers = [
  http.get("/api/ecr/repositories", () => HttpResponse.json({ repositories: [] })),
]

// ─── CloudWatch ───────────────────────────────────────────────────────────

export const cloudwatchHandlers = [
  http.get("/api/logs/log-groups", () => HttpResponse.json({ logGroups: [] })),
]

// ─── Inbox ────────────────────────────────────────────────────────────────

export const inboxHandlers = [http.get("/api/inbox/messages", () => HttpResponse.json([]))]

// ─── Debug ────────────────────────────────────────────────────────────────

export const debugHandlers = [http.get("/api/debug/state", () => HttpResponse.json({}))]

// ─── Default handler set (all services, empty state) ─────────────────────

export const handlers = [
  ...healthHandlers,
  ...s3Handlers,
  ...sqsHandlers,
  ...ecrHandlers,
  ...cloudwatchHandlers,
  ...inboxHandlers,
  ...debugHandlers,
]
