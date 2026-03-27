import { Hono } from "hono"
import {
  CreateQueueCommand,
  DeleteQueueCommand,
  GetQueueAttributesCommand,
  GetQueueUrlCommand,
  ListQueuesCommand,
  PurgeQueueCommand,
  ReceiveMessageCommand,
  SendMessageCommand,
  DeleteMessageCommand,
  SetQueueAttributesCommand,
  type MessageAttributeValue,
} from "@aws-sdk/client-sqs"
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

// ─── Queue list ────────────────────────────────────────────────────────────

sqsRoutes.get("/queues", async (c) => {
  const { sqs: client } = sqs(c)
  const prefix = c.req.query("prefix") || undefined
  const res = await client.send(new ListQueuesCommand({ QueueNamePrefix: prefix }))
  const urls = res.QueueUrls ?? []

  // Fetch attributes for each queue in parallel (name, ARN, message counts, VT).
  const queues = await Promise.all(
    urls.map(async (queueUrl) => {
      const name = queueUrl.split("/").pop() ?? queueUrl
      try {
        const attrs = await client.send(
          new GetQueueAttributesCommand({
            QueueUrl: queueUrl,
            AttributeNames: ["All"],
          }),
        )
        const a = attrs.Attributes ?? {}
        return {
          name,
          url: queueUrl,
          arn: a.QueueArn ?? "",
          visibilityTimeout: parseInt(a.VisibilityTimeout ?? "30", 10),
          approximateNumberOfMessages: parseInt(a.ApproximateNumberOfMessages ?? "0", 10),
          approximateNumberOfMessagesNotVisible: parseInt(
            a.ApproximateNumberOfMessagesNotVisible ?? "0",
            10,
          ),
          createdTimestamp: a.CreatedTimestamp ?? "",
        }
      } catch {
        return {
          name,
          url: queueUrl,
          arn: "",
          visibilityTimeout: 30,
          approximateNumberOfMessages: 0,
          approximateNumberOfMessagesNotVisible: 0,
          createdTimestamp: "",
        }
      }
    }),
  )

  return c.json({ queues })
})

sqsRoutes.post("/queues", async (c) => {
  const { sqs: client } = sqs(c)
  const { name, visibilityTimeout, messageRetentionPeriod, receiveMessageWaitTimeSeconds } =
    await c.req.json<{
      name: string
      visibilityTimeout?: number
      messageRetentionPeriod?: number
      receiveMessageWaitTimeSeconds?: number
    }>()

  const Attributes: Record<string, string> = {}
  if (visibilityTimeout != null) Attributes.VisibilityTimeout = String(visibilityTimeout)
  if (messageRetentionPeriod != null)
    Attributes.MessageRetentionPeriod = String(messageRetentionPeriod)
  if (receiveMessageWaitTimeSeconds != null)
    Attributes.ReceiveMessageWaitTimeSeconds = String(receiveMessageWaitTimeSeconds)

  const res = await client.send(new CreateQueueCommand({ QueueName: name, Attributes }))
  return c.json({ queueUrl: res.QueueUrl }, 201)
})

sqsRoutes.delete("/queues/:name", async (c) => {
  const { sqs: client } = sqs(c)
  const name = c.req.param("name")
  const urlRes = await client.send(new GetQueueUrlCommand({ QueueName: name }))
  await client.send(new DeleteQueueCommand({ QueueUrl: urlRes.QueueUrl }))
  return c.json({ ok: true })
})

// ─── Queue detail ──────────────────────────────────────────────────────────

sqsRoutes.get("/queues/:name", async (c) => {
  const { sqs: client } = sqs(c)
  const name = c.req.param("name")
  const urlRes = await client.send(new GetQueueUrlCommand({ QueueName: name }))
  const queueUrl = urlRes.QueueUrl!
  const attrs = await client.send(
    new GetQueueAttributesCommand({ QueueUrl: queueUrl, AttributeNames: ["All"] }),
  )
  const a = attrs.Attributes ?? {}
  return c.json({
    name,
    url: queueUrl,
    arn: a.QueueArn ?? "",
    visibilityTimeout: parseInt(a.VisibilityTimeout ?? "30", 10),
    messageRetentionPeriod: parseInt(a.MessageRetentionPeriod ?? "345600", 10),
    receiveMessageWaitTimeSeconds: parseInt(a.ReceiveMessageWaitTimeSeconds ?? "0", 10),
    delaySeconds: parseInt(a.DelaySeconds ?? "0", 10),
    maximumMessageSize: parseInt(a.MaximumMessageSize ?? "262144", 10),
    approximateNumberOfMessages: parseInt(a.ApproximateNumberOfMessages ?? "0", 10),
    approximateNumberOfMessagesNotVisible: parseInt(
      a.ApproximateNumberOfMessagesNotVisible ?? "0",
      10,
    ),
    createdTimestamp: a.CreatedTimestamp ?? "",
  })
})

sqsRoutes.patch("/queues/:name", async (c) => {
  const { sqs: client } = sqs(c)
  const name = c.req.param("name")
  const urlRes = await client.send(new GetQueueUrlCommand({ QueueName: name }))
  const { attributes } = await c.req.json<{ attributes: Record<string, string> }>()
  await client.send(
    new SetQueueAttributesCommand({ QueueUrl: urlRes.QueueUrl, Attributes: attributes }),
  )
  return c.json({ ok: true })
})

sqsRoutes.post("/queues/:name/purge", async (c) => {
  const { sqs: client } = sqs(c)
  const name = c.req.param("name")
  const urlRes = await client.send(new GetQueueUrlCommand({ QueueName: name }))
  await client.send(new PurgeQueueCommand({ QueueUrl: urlRes.QueueUrl }))
  return c.json({ ok: true })
})

// ─── Messages ──────────────────────────────────────────────────────────────

sqsRoutes.get("/queues/:name/messages", async (c) => {
  const { sqs: client } = sqs(c)
  const name = c.req.param("name")
  const max = Math.min(parseInt(c.req.query("max") ?? "10", 10), 10)
  const visibilityTimeout = parseInt(c.req.query("visibilityTimeout") ?? "0", 10)

  const urlRes = await client.send(new GetQueueUrlCommand({ QueueName: name }))
  const res = await client.send(
    new ReceiveMessageCommand({
      QueueUrl: urlRes.QueueUrl,
      MaxNumberOfMessages: max,
      VisibilityTimeout: visibilityTimeout,
      MessageAttributeNames: ["All"],
      AttributeNames: ["All"],
    }),
  )

  const messages = (res.Messages ?? []).map((m) => ({
    messageId: m.MessageId ?? "",
    receiptHandle: m.ReceiptHandle ?? "",
    body: m.Body ?? "",
    md5OfBody: m.MD5OfBody ?? "",
    attributes: m.Attributes ?? {},
    messageAttributes: Object.fromEntries(
      Object.entries(m.MessageAttributes ?? {}).map(([k, v]: [string, MessageAttributeValue]) => [
        k,
        { dataType: v.DataType ?? "String", stringValue: v.StringValue ?? "" },
      ]),
    ),
  }))

  return c.json({ messages })
})

sqsRoutes.post("/queues/:name/messages", async (c) => {
  const { sqs: client } = sqs(c)
  const name = c.req.param("name")
  const { body, delaySeconds, messageAttributes } = await c.req.json<{
    body: string
    delaySeconds?: number
    messageAttributes?: Record<string, { dataType: string; stringValue: string }>
  }>()

  const urlRes = await client.send(new GetQueueUrlCommand({ QueueName: name }))

  const sdkAttrs: Record<string, MessageAttributeValue> = {}
  for (const [k, v] of Object.entries(messageAttributes ?? {})) {
    sdkAttrs[k] = { DataType: v.dataType, StringValue: v.stringValue }
  }

  const res = await client.send(
    new SendMessageCommand({
      QueueUrl: urlRes.QueueUrl,
      MessageBody: body,
      DelaySeconds: delaySeconds,
      MessageAttributes: Object.keys(sdkAttrs).length > 0 ? sdkAttrs : undefined,
    }),
  )

  return c.json({ messageId: res.MessageId, md5OfMessageBody: res.MD5OfMessageBody }, 201)
})

sqsRoutes.delete("/queues/:name/messages/:receiptHandle", async (c) => {
  const { sqs: client } = sqs(c)
  const name = c.req.param("name")
  const receiptHandle = decodeURIComponent(c.req.param("receiptHandle"))

  const urlRes = await client.send(new GetQueueUrlCommand({ QueueName: name }))
  await client.send(
    new DeleteMessageCommand({ QueueUrl: urlRes.QueueUrl, ReceiptHandle: receiptHandle }),
  )
  return c.json({ ok: true })
})
