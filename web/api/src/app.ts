import { Hono } from "hono"
import { cors } from "hono/cors"
import { logger } from "hono/logger"
import { HTTPException } from "hono/http-exception"
import { s3Routes } from "./routes/s3.js"
import { sqsRoutes } from "./routes/sqs.js"
import { healthRoutes } from "./routes/health.js"
import { eventsRoutes } from "./routes/events.js"

const app = new Hono()

app.use(
  "*",
  cors({
    origin: ["http://localhost:3000", "http://localhost:5173"],
    allowHeaders: ["Content-Type", "x-overcast-endpoint", "x-overcast-region"],
  }),
)

if (process.env.NODE_ENV !== "production") {
  app.use("*", logger())
}

// ─── Global error handler — translates AWS SDK ServiceException to JSON ────
app.onError((err, c) => {
  if (err instanceof HTTPException) return err.getResponse()
  // AWS SDK errors carry a $metadata object and a name (the error code)
  const awsErr = err as unknown as Record<string, unknown>
  if (awsErr.$metadata) {
    const status = (awsErr.$metadata as { httpStatusCode?: number }).httpStatusCode ?? 500
    return c.json(
      { error: awsErr.name ?? "AWSError", message: (err as Error).message },
      status as never,
    )
  }
  console.error(err)
  return c.json({ error: "InternalError", message: (err as Error).message ?? "Unknown error" }, 500)
})

// ─── Health ────────────────────────────────────────────────────────────────
app.route("/api/health", healthRoutes)

// ─── Service routes ────────────────────────────────────────────────────────
app.route("/api/s3", s3Routes)
app.route("/api/sqs", sqsRoutes)
app.route("/api/events", eventsRoutes)
// future: app.route("/api/dynamodb", dynamodbRoutes)
// future: app.route("/api/sns",      snsRoutes)
// future: app.route("/api/lambda",   lambdaRoutes)

export { app }
