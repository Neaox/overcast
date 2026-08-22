import { awsClients } from "../aws-clients"
import { apiFetch } from "./base"
import type { MonitorResponse } from "@/types"
import type { ChartRangeToken } from "@/features/monitoring/types"
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

  /** Monitor section read-through — docs/plans/service-metrics-platform.md phase 3. */
  getMetrics: (name: string, range: ChartRangeToken) =>
    apiFetch<MonitorResponse>(`/sqs/queues/${encodeURIComponent(name)}/metrics?range=${range}`),

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

  /**
   * Moves a single message off a DLQ and back to the queue it failed on.
   *
   * SQS has no single-message redrive API — StartMessageMoveTask always drains
   * the whole DLQ. This does by hand what that task does per message, using the
   * same three calls AWS's own redrive needs permission for (SendMessage on the
   * destination, ReceiveMessage + DeleteMessage on the DLQ; see the redrive IAM
   * policy in the SQS Developer Guide).
   *
   * The destination comes from the message's DeadLetterSourceQueueArn system
   * attribute, which is how "Redrive to source queue(s)" routes each message.
   * Send happens before delete, so a failure mid-way leaves the message on the
   * DLQ rather than losing it.
   *
   * Like a real redrive, the message arrives as a *new* message: fresh
   * messageId and timestamps, receive count back to zero.
   */
  redriveMessage: async (dlqName: string, msg: SQSMessage) => {
    const sourceArn = msg.attributes.DeadLetterSourceQueueArn
    if (!sourceArn) {
      throw new Error(
        "Message has no DeadLetterSourceQueueArn attribute, so its source queue is unknown.",
      )
    }
    const destination = sourceArn.split(":").pop() ?? ""
    if (!destination) {
      throw new Error(`DeadLetterSourceQueueArn is not a valid SQS ARN: ${sourceArn}`)
    }

    const client = awsClients.sqs()
    const [dlqUrl, destUrl] = await Promise.all([
      client.send(new GetQueueUrlCommand({ QueueName: dlqName })),
      client.send(new GetQueueUrlCommand({ QueueName: destination })),
    ])

    await client.send(
      new SendMessageCommand({
        QueueUrl: destUrl.QueueUrl,
        MessageBody: msg.body,
        // FIFO destinations reject a send without a group ID. Reuse the group
        // the message was originally published under so ordering is preserved.
        ...(msg.attributes.MessageGroupId
          ? {
              MessageGroupId: msg.attributes.MessageGroupId,
              // AWS replaces a redriven message's dedup ID with the original
              // message ID; do the same so redriving twice isn't silently
              // deduplicated away inside the 5-minute window.
              MessageDeduplicationId: msg.messageId,
            }
          : {}),
        ...(Object.keys(msg.messageAttributes).length > 0
          ? {
              MessageAttributes: Object.fromEntries(
                Object.entries(msg.messageAttributes).map(([k, v]) => [
                  k,
                  { DataType: v.dataType, StringValue: v.stringValue },
                ]),
              ),
            }
          : {}),
      }),
    )

    await client.send(
      new DeleteMessageCommand({ QueueUrl: dlqUrl.QueueUrl, ReceiptHandle: msg.receiptHandle }),
    )

    return { destination }
  },
}
