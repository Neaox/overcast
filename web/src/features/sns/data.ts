/**
 * SNS query/mutation definitions.
 *
 * Key factory:
 *   snsKeys.all()                                     -> ["sns"]
 *   snsKeys.topics()                                -> ["sns", "topics"]
 *   snsKeys.topicList(baseUrl)                      -> ["sns", "topics", baseUrl]
 *   snsKeys.subscriptions()                         -> ["sns", "subscriptions"]
 *   snsKeys.subscriptionList(baseUrl, topicName)    -> ["sns", "subscriptions", baseUrl, topicName]
 */

import { queryOptions, mutationOptions } from "@tanstack/react-query"
import { sns } from "@/services/api"
import { endpointStore } from "@/services/endpoint-store"
import type { ChartRangeToken } from "@/features/monitoring/types"

// ─── Key factory ───────────────────────────────────────────────────────────

export const snsKeys = {
  all: () => [...endpointStore.getKeys(), "sns"] as const,
  topics: () => [...snsKeys.all(), "topics"] as const,
  subscriptions: () => [...snsKeys.all(), "subscriptions"] as const,
  subscriptionList: (topicName: string) => [...snsKeys.subscriptions(), topicName] as const,
  queueSubscriptions: (queueArn: string) =>
    [...snsKeys.all(), "queue-subscriptions", queueArn] as const,
  metrics: (topicName: string, range: ChartRangeToken) =>
    [...snsKeys.all(), "metrics", topicName, range] as const,
}

// ─── Query definitions ─────────────────────────────────────────────────────

export function snsTopicsQueryOptions() {
  return queryOptions({
    queryKey: snsKeys.topics(),
    queryFn: () => sns.listTopics(),
  })
}

export function snsSubscriptionsQueryOptions(topicName: string) {
  return queryOptions({
    queryKey: snsKeys.subscriptionList(topicName),
    queryFn: () => sns.listSubscriptions(topicName),
  })
}

/** All SNS subscriptions whose endpoint matches the given queue ARN. */
export function snsQueueSubscriptionsQueryOptions(queueArn: string) {
  return queryOptions({
    queryKey: snsKeys.queueSubscriptions(queueArn),
    queryFn: () => sns.listSubscriptionsByEndpoint(queueArn),
    enabled: !!queueArn,
  })
}

/**
 * Monitor tab read-through (docs/plans/service-metrics-platform.md phase 4).
 * Polls while the tab is visible, matching Lambda/SQS's own Monitor query
 * options.
 */
export function snsMetricsQueryOptions(topicName: string, range: ChartRangeToken) {
  return queryOptions({
    queryKey: snsKeys.metrics(topicName, range),
    queryFn: () => sns.getMetrics(topicName, range),
    refetchInterval: 30_000,
    enabled: Boolean(topicName),
  })
}

// ─── Mutation definitions ──────────────────────────────────────────────────

export function createTopicMutationOptions() {
  return mutationOptions({
    mutationKey: [...snsKeys.topics(), "create"] as const,
    mutationFn: (name: string) => sns.createTopic(name),
  })
}

export function deleteTopicMutationOptions() {
  return mutationOptions({
    mutationKey: [...snsKeys.topics(), "delete"] as const,
    mutationFn: (name: string) => sns.deleteTopic(name),
  })
}

export function subscribeMutationOptions() {
  return mutationOptions({
    mutationKey: [...snsKeys.all(), "subscribe"] as const,
    mutationFn: ({
      topicName,
      protocol,
      endpoint,
    }: {
      topicName: string
      protocol: string
      endpoint: string
    }) => sns.subscribe(topicName, protocol, endpoint),
  })
}

export function unsubscribeMutationOptions() {
  return mutationOptions({
    mutationKey: [...snsKeys.all(), "unsubscribe"] as const,
    mutationFn: (subscriptionArn: string) => sns.unsubscribe(subscriptionArn),
  })
}

export function publishMutationOptions(topicName: string) {
  return mutationOptions({
    mutationKey: [...snsKeys.all(), "publish", topicName] as const,
    mutationFn: (opts: {
      message: string
      subject?: string
      messageStructure?: "json"
      messageGroupId?: string
      messageDeduplicationId?: string
      messageAttributes?: Record<string, { dataType: string; stringValue: string }>
    }) => sns.publish(topicName, opts),
  })
}
