import { awsClients } from "../aws-clients"
import { apiFetch } from "./base"
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
  ListDeadLetterSourceQueuesCommand,
  StartMessageMoveTaskCommand,
} from "@aws-sdk/client-sqs"
import type { SQSQueue, SQSQueueDetail, SQSMessage } from "@/types"

export const sqs = {
  listQueues: async (prefix?: string) => {
    const client = awsClients.sqs()
    const res = await client.send(new ListQueuesCommand({ QueueNamePrefix: prefix || undefined }))
    const urls = res.QueueUrls ?? []
    // Fetch attributes for each queue in parallel.
    const queues = await Promise.all(
      urls.map(async (queueUrl) => {
        const name = queueUrl.split("/").pop() ?? queueUrl
        try {
          const attrs = await client.send(
            new GetQueueAttributesCommand({ QueueUrl: queueUrl, AttributeNames: ["All"] }),
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
    return queues as SQSQueue[]
  },

  createQueue: async (opts: {
    name: string
    fifo?: boolean
    contentBasedDeduplication?: boolean
    visibilityTimeout?: number
    messageRetentionPeriod?: number
    receiveMessageWaitTimeSeconds?: number
  }) => {
    const Attributes: Record<string, string> = {}
    if (opts.visibilityTimeout != null)
      Attributes.VisibilityTimeout = String(opts.visibilityTimeout)
    if (opts.messageRetentionPeriod != null)
      Attributes.MessageRetentionPeriod = String(opts.messageRetentionPeriod)
    if (opts.receiveMessageWaitTimeSeconds != null)
      Attributes.ReceiveMessageWaitTimeSeconds = String(opts.receiveMessageWaitTimeSeconds)
    if (opts.fifo) {
      Attributes.FifoQueue = "true"
      if (opts.contentBasedDeduplication) Attributes.ContentBasedDeduplication = "true"
    }
    const res = await awsClients
      .sqs()
      .send(new CreateQueueCommand({ QueueName: opts.name, Attributes }))
    return { queueUrl: res.QueueUrl }
  },

  deleteQueue: async (name: string) => {
    const client = awsClients.sqs()
    const urlRes = await client.send(new GetQueueUrlCommand({ QueueName: name }))
    await client.send(new DeleteQueueCommand({ QueueUrl: urlRes.QueueUrl }))
    return { ok: true }
  },

  getQueue: async (name: string) => {
    const client = awsClients.sqs()
    const urlRes = await client.send(new GetQueueUrlCommand({ QueueName: name }))
    const queueUrl = urlRes.QueueUrl!
    const attrs = await client.send(
      new GetQueueAttributesCommand({ QueueUrl: queueUrl, AttributeNames: ["All"] }),
    )
    const a = attrs.Attributes ?? {}
    let redrivePolicy: { deadLetterTargetArn: string; maxReceiveCount: number } | null = null
    if (a.RedrivePolicy) {
      try {
        redrivePolicy = JSON.parse(a.RedrivePolicy)
      } catch {
        // ignore
      }
    }
    return {
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
      redrivePolicy,
    } as SQSQueueDetail
  },

  purgeQueue: async (name: string) => {
    const client = awsClients.sqs()
    const urlRes = await client.send(new GetQueueUrlCommand({ QueueName: name }))
    await client.send(new PurgeQueueCommand({ QueueUrl: urlRes.QueueUrl }))
    return { ok: true }
  },

  /** Custom peek endpoint — reads messages without incrementing receive count. */
  receiveMessages: (name: string) => {
    return apiFetch<{ messages: SQSMessage[] }>(
      `/sqs/queues/${encodeURIComponent(name)}/messages`,
    ).then((r) => r.messages)
  },

  sendMessage: async (
    name: string,
    body: string,
    opts: {
      delaySeconds?: number
      messageGroupId?: string
      messageDeduplicationId?: string
      messageAttributes?: Record<string, { dataType: string; stringValue: string }>
    } = {},
  ) => {
    const client = awsClients.sqs()
    const urlRes = await client.send(new GetQueueUrlCommand({ QueueName: name }))
    const res = await client.send(
      new SendMessageCommand({
        QueueUrl: urlRes.QueueUrl,
        MessageBody: body,
        ...(opts.delaySeconds != null ? { DelaySeconds: opts.delaySeconds } : {}),
        ...(opts.messageGroupId ? { MessageGroupId: opts.messageGroupId } : {}),
        ...(opts.messageDeduplicationId
          ? { MessageDeduplicationId: opts.messageDeduplicationId }
          : {}),
        ...(opts.messageAttributes
          ? {
              MessageAttributes: Object.fromEntries(
                Object.entries(opts.messageAttributes).map(([k, v]) => [
                  k,
                  { DataType: v.dataType, StringValue: v.stringValue },
                ]),
              ),
            }
          : {}),
      }),
    )
    return { messageId: res.MessageId ?? "", md5OfMessageBody: res.MD5OfMessageBody ?? "" }
  },

  deleteMessage: async (name: string, receiptHandle: string) => {
    const client = awsClients.sqs()
    const urlRes = await client.send(new GetQueueUrlCommand({ QueueName: name }))
    await client.send(
      new DeleteMessageCommand({ QueueUrl: urlRes.QueueUrl, ReceiptHandle: receiptHandle }),
    )
    return { ok: true }
  },

  /** Calls ReceiveMessage with the queue's default VT — increments receive count. */
  receiveMessagesImmediate: async (name: string, maxMessages = 10) => {
    const client = awsClients.sqs()
    const urlRes = await client.send(new GetQueueUrlCommand({ QueueName: name }))
    const res = await client.send(
      new ReceiveMessageCommand({
        QueueUrl: urlRes.QueueUrl,
        MaxNumberOfMessages: Math.min(maxMessages, 10),
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
        Object.entries(m.MessageAttributes ?? {}).map(([k, v]) => [
          k,
          { dataType: v.DataType ?? "String", stringValue: v.StringValue ?? "" },
        ]),
      ),
    }))
    return { messages, count: messages.length }
  },

  updateQueueAttributes: async (
    name: string,
    attrs: {
      visibilityTimeout?: number
      messageRetentionPeriod?: number
      receiveMessageWaitTimeSeconds?: number
      delaySeconds?: number
      redrivePolicy?: { deadLetterTargetArn: string; maxReceiveCount: number } | null
    },
  ) => {
    const client = awsClients.sqs()
    const urlRes = await client.send(new GetQueueUrlCommand({ QueueName: name }))
    const attributes: Record<string, string> = {}
    if (attrs.visibilityTimeout != null)
      attributes.VisibilityTimeout = String(attrs.visibilityTimeout)
    if (attrs.messageRetentionPeriod != null)
      attributes.MessageRetentionPeriod = String(attrs.messageRetentionPeriod)
    if (attrs.receiveMessageWaitTimeSeconds != null)
      attributes.ReceiveMessageWaitTimeSeconds = String(attrs.receiveMessageWaitTimeSeconds)
    if (attrs.delaySeconds != null) attributes.DelaySeconds = String(attrs.delaySeconds)
    if (attrs.redrivePolicy !== undefined) {
      attributes.RedrivePolicy =
        attrs.redrivePolicy == null
          ? ""
          : JSON.stringify({
              deadLetterTargetArn: attrs.redrivePolicy.deadLetterTargetArn,
              maxReceiveCount: attrs.redrivePolicy.maxReceiveCount,
            })
    }
    await client.send(
      new SetQueueAttributesCommand({ QueueUrl: urlRes.QueueUrl, Attributes: attributes }),
    )
    return { ok: true }
  },

  listDeadLetterSourceQueues: async (name: string) => {
    const client = awsClients.sqs()
    const urlRes = await client.send(new GetQueueUrlCommand({ QueueName: name }))
    const res = await client.send(
      new ListDeadLetterSourceQueuesCommand({ QueueUrl: urlRes.QueueUrl }),
    )
    return res.queueUrls ?? []
  },

  startMessageMoveTask: async (sourceArn: string, destinationArn?: string) => {
    const client = awsClients.sqs()
    const res = await client.send(
      new StartMessageMoveTaskCommand({
        SourceArn: sourceArn,
        ...(destinationArn ? { DestinationArn: destinationArn } : {}),
      }),
    )
    return { taskHandle: res.TaskHandle ?? "" }
  },
}
