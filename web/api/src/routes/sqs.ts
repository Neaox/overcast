import { Hono } from "hono"
import { GetQueueUrlCommand } from "@aws-sdk/client-sqs"
import { resolveEndpoint, ENDPOINT_HEADER, REGION_HEADER } from "../service-discovery.js"
import { AWSClientFactory } from "../client/aws.js"

export const sqsRoutes = new Hono()

function sqs(c: { req: { header: (k: string) => string | undefined } }) {
  const endpoint = resolveEndpoint({
    [ENDPOINT_HEADER]: c.req.header(ENDPOINT_HEADER),
    [REGION_HEADER]: c.req.header(REGION_HEADER),
  })
  return { sqs: AWSClientFactory.makeSQS(endpoint), endpoint }
}

// ─── Peek messages (emulator-only, non-AWS endpoint) ───────────────────────

sqsRoutes.get("/queues/:name/messages", async (c) => {
  const { sqs: client, endpoint } = sqs(c)
  const name = c.req.param("name")

  // Use the non-AWS peek endpoint (GET /{accountID}/{queueName}) instead of
  // ReceiveMessage so that:
  //   • receive count is never incremented
  //   • visibility timeouts are never extended
  //   • in-flight messages ARE returned (ReceiveMessage skips them)
  const urlRes = await client.send(new GetQueueUrlCommand({ QueueName: name }))
  const queueUrl = urlRes.QueueUrl!

  // Build the peek URL: same path as the queue URL, but at the resolved endpoint.
  // queueUrl = http://localhost:PORT/ACCOUNT_ID/QUEUE_NAME
  const queuePath = new URL(queueUrl).pathname
  const peekUrl = `${endpoint.baseUrl}${queuePath}`

  const res = await fetch(peekUrl, { method: "GET" })
  if (!res.ok) {
    const body = await res.text()
    return c.text(body, res.status as Parameters<typeof c.text>[1])
  }

  const data = (await res.json()) as {
    Messages: Array<{
      MessageId: string
      ReceiptHandle: string
      Body: string
      MD5OfBody: string
      Attributes: Record<string, string>
      MessageAttributes: Record<string, { DataType: string; StringValue?: string }>
      Inflight: boolean
      Delayed: boolean
      VisibleAfter: number
      ApproximateReceiveCount: number
    }>
  }

  const messages = (data.Messages ?? []).map((m) => ({
    messageId: m.MessageId,
    receiptHandle: m.ReceiptHandle,
    body: m.Body,
    md5OfBody: m.MD5OfBody,
    attributes: m.Attributes ?? {},
    messageAttributes: Object.fromEntries(
      Object.entries(m.MessageAttributes ?? {}).map(([k, v]) => [
        k,
        { dataType: v.DataType ?? "String", stringValue: v.StringValue ?? "" },
      ]),
    ),
    inflight: m.Inflight,
    delayed: m.Delayed ?? false,
    visibleAfter: m.VisibleAfter,
    approximateReceiveCount: m.ApproximateReceiveCount ?? 0,
  }))

  return c.json({ messages })
})
