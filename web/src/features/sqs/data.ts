/**
 * SQS query/mutation definitions.
 *
 * Key factory:
 *   sqsKeys.all                               -> ["sqs"]
 *   sqsKeys.queues()                          -> ["sqs", "queues"]
 *   sqsKeys.queueList(baseUrl, region)             -> ["sqs", "queues", baseUrl, region]
 *   sqsKeys.queue()                                -> ["sqs", "queue"]
 *   sqsKeys.queueDetail(baseUrl, name, region)     -> ["sqs", "queue", baseUrl, name, region]
 *   sqsKeys.messages()                             -> ["sqs", "messages"]
 *   sqsKeys.messageList(baseUrl, name, region)     -> ["sqs", "messages", baseUrl, name, region]
 *   sqsKeys.mapPeek()                         -> ["sqs", "map-peek"]
 */

import { queryOptions, mutationOptions } from "@tanstack/react-query"
import { sqs } from "@/services/api"
import { endpointStore } from "@/services/endpoint-store"

// ─── Key factory ───────────────────────────────────────────────────────────

export const sqsKeys = {
  all: () => [...endpointStore.getKeys(), "sqs"] as const,
  queues: () => [...sqsKeys.all(), "queues"] as const,
  queue: () => [...sqsKeys.all(), "queue"] as const,
  queueDetail: (name: string) => [...sqsKeys.queue(), name] as const,
  messages: () => [...sqsKeys.all(), "messages"] as const,
  messageList: (name: string) => [...sqsKeys.messages(), name] as const,
  mapPeek: () => [...sqsKeys.all(), "map-peek"] as const,
}

// ─── Query definitions ─────────────────────────────────────────────────────

export function sqsQueuesQueryOptions() {
  return queryOptions({
    queryKey: sqsKeys.queues(),
    queryFn: () => sqs.listQueues(),
  })
}

export function sqsQueueQueryOptions(name: string) {
  return queryOptions({
    queryKey: sqsKeys.queueDetail(name),
    queryFn: () => sqs.getQueue(name),
  })
}

export function sqsMessagesQueryOptions(name: string) {
  return queryOptions({
    queryKey: sqsKeys.messageList(name),
    queryFn: () => sqs.receiveMessages(name),
    // Don't cache peeked messages — every refetch does a fresh peek
    staleTime: 0,
    gcTime: 0,
  })
}

// ─── Mutation definitions ──────────────────────────────────────────────────

export function createQueueMutationOptions() {
  return mutationOptions({
    mutationKey: [...sqsKeys.queues(), "create"] as const,
    mutationFn: (opts: {
      name: string
      fifo?: boolean
      contentBasedDeduplication?: boolean
      visibilityTimeout?: number
      messageRetentionPeriod?: number
      receiveMessageWaitTimeSeconds?: number
    }) => sqs.createQueue(opts),
  })
}

export function deleteQueueMutationOptions() {
  return mutationOptions({
    mutationKey: [...sqsKeys.queues(), "delete"] as const,
    mutationFn: (name: string) => sqs.deleteQueue(name),
  })
}

export function purgeQueueMutationOptions(name: string) {
  return mutationOptions({
    mutationKey: [...sqsKeys.queueDetail(name), "purge"] as const,
    mutationFn: () => sqs.purgeQueue(name),
  })
}

export function sendMessageMutationOptions(name: string) {
  return mutationOptions({
    mutationKey: [...sqsKeys.messageList(name), "send"] as const,
    mutationFn: (opts: {
      body: string
      delaySeconds?: number
      messageGroupId?: string
      messageDeduplicationId?: string
      messageAttributes?: Record<string, { dataType: string; stringValue: string }>
    }) => sqs.sendMessage(name, opts.body, opts),
  })
}

export function deleteMessageMutationOptions(name: string) {
  return mutationOptions({
    mutationKey: [...sqsKeys.messageList(name), "delete"] as const,
    mutationFn: (receiptHandle: string) => sqs.deleteMessage(name, receiptHandle),
  })
}

export function receiveMessagesMutationOptions(name: string) {
  return mutationOptions({
    mutationKey: [...sqsKeys.messageList(name), "receive"] as const,
    mutationFn: (maxMessages?: number) => sqs.receiveMessagesImmediate(name, maxMessages),
  })
}

export function updateQueueAttributesMutationOptions(name: string) {
  return mutationOptions({
    mutationKey: [...sqsKeys.queueDetail(name), "update"] as const,
    mutationFn: (attrs: {
      visibilityTimeout?: number
      messageRetentionPeriod?: number
      receiveMessageWaitTimeSeconds?: number
      delaySeconds?: number
      redrivePolicy?: { deadLetterTargetArn: string; maxReceiveCount: number } | null
    }) => sqs.updateQueueAttributes(name, attrs),
  })
}

export function deadLetterSourceQueuesQueryOptions(name: string) {
  return queryOptions({
    queryKey: [...sqsKeys.queueDetail(name), "dlq-sources"] as const,
    queryFn: () => sqs.listDeadLetterSourceQueues(name),
  })
}

export function redriveMutationOptions(name: string) {
  return mutationOptions({
    mutationKey: [...sqsKeys.queueDetail(name), "redrive"] as const,
    mutationFn: (sourceArn: string) => sqs.startMessageMoveTask(sourceArn),
  })
}
