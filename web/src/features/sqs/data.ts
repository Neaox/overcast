/**
 * SQS query/mutation definitions.
 *
 * Key factory:
 *   sqsKeys.all                               -> ["sqs"]
 *   sqsKeys.queues()                          -> ["sqs", "queues"]
 *   sqsKeys.queueList(baseUrl)                -> ["sqs", "queues", baseUrl]
 *   sqsKeys.queue(baseUrl, name)              -> ["sqs", "queue", baseUrl, name]
 *   sqsKeys.messages()                        -> ["sqs", "messages"]
 *   sqsKeys.messageList(baseUrl, name)        -> ["sqs", "messages", baseUrl, name]
 */

import { queryOptions, mutationOptions } from "@tanstack/react-query"
import { api } from "@/services/api"

// ─── Key factory ───────────────────────────────────────────────────────────

export const sqsKeys = {
  all: ["sqs"] as const,
  queues: () => [...sqsKeys.all, "queues"] as const,
  queueList: (baseUrl: string) => [...sqsKeys.queues(), baseUrl] as const,
  queue: (baseUrl: string, name: string) => [...sqsKeys.all, "queue", baseUrl, name] as const,
  messages: () => [...sqsKeys.all, "messages"] as const,
  messageList: (baseUrl: string, name: string) => [...sqsKeys.messages(), baseUrl, name] as const,
}

// ─── Query definitions ─────────────────────────────────────────────────────

export const sqsQueries = {
  queues(baseUrl: string) {
    return queryOptions({
      queryKey: sqsKeys.queueList(baseUrl),
      queryFn: () => api.sqs.listQueues(),
    })
  },

  queue(baseUrl: string, name: string) {
    return queryOptions({
      queryKey: sqsKeys.queue(baseUrl, name),
      queryFn: () => api.sqs.getQueue(name),
    })
  },

  messages(baseUrl: string, name: string, visibilityTimeout = 0) {
    return queryOptions({
      queryKey: sqsKeys.messageList(baseUrl, name),
      queryFn: () => api.sqs.receiveMessages(name, 10, visibilityTimeout),
      // Don't cache peeked messages — every refetch does a fresh receive
      staleTime: 0,
      gcTime: 0,
    })
  },
}

// ─── Mutation definitions ──────────────────────────────────────────────────

export function createQueueMutationOptions() {
  return mutationOptions({
    mutationKey: [...sqsKeys.queues(), "create"] as const,
    mutationFn: (opts: {
      name: string
      visibilityTimeout?: number
      messageRetentionPeriod?: number
      receiveMessageWaitTimeSeconds?: number
    }) => api.sqs.createQueue(opts),
  })
}

export function deleteQueueMutationOptions() {
  return mutationOptions({
    mutationKey: [...sqsKeys.queues(), "delete"] as const,
    mutationFn: (name: string) => api.sqs.deleteQueue(name),
  })
}

export function purgeQueueMutationOptions(name: string) {
  return mutationOptions({
    mutationKey: [...sqsKeys.queue("", name), "purge"] as const,
    mutationFn: () => api.sqs.purgeQueue(name),
  })
}

export function sendMessageMutationOptions(name: string) {
  return mutationOptions({
    mutationKey: [...sqsKeys.messageList("", name), "send"] as const,
    mutationFn: (opts: {
      body: string
      delaySeconds?: number
      messageAttributes?: Record<string, { dataType: string; stringValue: string }>
    }) => api.sqs.sendMessage(name, opts.body, opts),
  })
}

export function deleteMessageMutationOptions(name: string) {
  return mutationOptions({
    mutationKey: [...sqsKeys.messageList("", name), "delete"] as const,
    mutationFn: (receiptHandle: string) => api.sqs.deleteMessage(name, receiptHandle),
  })
}
