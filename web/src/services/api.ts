/**
 * Typed API client for the Hono BFF.
 *
 * Injects the current emulator endpoint as headers on every request
 * so the API server knows where to point the AWS SDK clients.
 *
 * Usage:
 *   const { data } = useQuery({ queryKey: ['buckets'], queryFn: () => api.s3.listBuckets() })
 */

import { endpointResolver } from "./discovery"
import type { EmulatorEndpoint } from "./discovery"

const API_BASE = "/api"

// ─── HTTP helpers ──────────────────────────────────────────────────────────

function endpointHeaders(endpoint: EmulatorEndpoint): Record<string, string> {
  return {
    "x-overcast-endpoint": endpoint.baseUrl,
    "x-overcast-region": endpoint.region,
  }
}

async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const endpoint = endpointResolver.get()
  const res = await fetch(`${API_BASE}${path}`, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      ...endpointHeaders(endpoint),
      ...(init?.headers as Record<string, string> | undefined),
    },
  })

  if (!res.ok) {
    const body = (await res.json().catch(() => ({ error: res.statusText }))) as {
      error?: string
      message?: string
    }
    throw new Error(body.message ?? body.error ?? `HTTP ${res.status}`)
  }

  // 204 / empty body
  const text = await res.text()
  return text ? (JSON.parse(text) as T) : ({} as T)
}

// ─── Response types ────────────────────────────────────────────────────────

export interface S3Bucket {
  name: string
  creationDate: string
}

export interface S3Object {
  key: string
  size: number
  lastModified: string
  etag: string
  storageClass: string
}

export interface S3Prefix {
  prefix: string
}

export interface ListObjectsResult {
  objects: S3Object[]
  prefixes: S3Prefix[]
  isTruncated: boolean
  nextContinuationToken?: string
}

export interface S3ObjectMetadata {
  contentType: string
  contentLength: number
  lastModified: string
  etag: string
  metadata: Record<string, string>
}

export interface NotificationFilterRule {
  name: string
  value: string
}

export interface QueueNotificationConfig {
  id: string
  queueArn: string
  events: string[]
  filterRules: NotificationFilterRule[]
}

export interface TopicNotificationConfig {
  id: string
  topicArn: string
  events: string[]
  filterRules: NotificationFilterRule[]
}

export interface LambdaNotificationConfig {
  id: string
  functionArn: string
  events: string[]
  filterRules: NotificationFilterRule[]
}

export interface BucketNotificationConfig {
  queueConfigurations: QueueNotificationConfig[]
  topicConfigurations: TopicNotificationConfig[]
  lambdaConfigurations: LambdaNotificationConfig[]
}

// ─── Event stream ─────────────────────────────────────────────────────────

/** Shape of a single event sent over the /_events SSE stream. */
export interface StreamEvent {
  type: string // e.g. "s3:ObjectCreated:*"
  time: string // ISO-8601
  source: string // "s3", "sqs", "dynamodb", …
  payload: unknown
}

// ─── S3 ────────────────────────────────────────────────────────────────────

const s3 = {
  listBuckets: () => apiFetch<{ buckets: S3Bucket[] }>("/s3/buckets").then((r) => r.buckets),

  createBucket: (name: string, region?: string) =>
    apiFetch<{ ok: boolean }>("/s3/buckets", {
      method: "POST",
      body: JSON.stringify({ name, region }),
    }),

  deleteBucket: (name: string) =>
    apiFetch<{ ok: boolean }>(`/s3/buckets/${encodeURIComponent(name)}`, { method: "DELETE" }),

  headBucket: (name: string) =>
    apiFetch<{ ok: boolean }>(`/s3/buckets/${encodeURIComponent(name)}`),

  listObjects: (
    bucket: string,
    opts: {
      prefix?: string
      delimiter?: string
      maxKeys?: number
      token?: string
    } = {},
  ) => {
    const params = new URLSearchParams()
    if (opts.prefix) params.set("prefix", opts.prefix)
    if (opts.delimiter) params.set("delimiter", opts.delimiter)
    if (opts.maxKeys) params.set("maxKeys", String(opts.maxKeys))
    if (opts.token) params.set("token", opts.token)
    return apiFetch<ListObjectsResult>(
      `/s3/buckets/${encodeURIComponent(bucket)}/objects?${params}`,
    )
  },

  getObjectMetadata: (bucket: string, key: string) =>
    apiFetch<S3ObjectMetadata>(
      `/s3/buckets/${encodeURIComponent(bucket)}/objects/${encodeURIComponent(key)}/metadata`,
    ),

  /** Returns a URL to stream the object directly — caller opens/downloads it. */
  getObjectDownloadUrl: (bucket: string, key: string): string => {
    const endpoint = endpointResolver.get()
    const params = new URLSearchParams(endpointHeaders(endpoint))
    return `${API_BASE}/s3/buckets/${encodeURIComponent(bucket)}/objects/${encodeURIComponent(key)}/download?${params}`
  },

  deleteObject: (bucket: string, key: string) =>
    apiFetch<{ ok: boolean }>(
      `/s3/buckets/${encodeURIComponent(bucket)}/objects/${encodeURIComponent(key)}`,
      { method: "DELETE" },
    ),

  deleteObjectsByPrefix: (bucket: string, prefix: string) =>
    apiFetch<{ ok: boolean; deleted: number }>(
      `/s3/buckets/${encodeURIComponent(bucket)}/objects-by-prefix?prefix=${encodeURIComponent(prefix)}`,
      { method: "DELETE" },
    ),

  getBucketNotification: (bucket: string) =>
    apiFetch<BucketNotificationConfig>(`/s3/buckets/${encodeURIComponent(bucket)}/notification`),
}

// ─── SQS ───────────────────────────────────────────────────────────────────

export interface SQSQueue {
  name: string
  url: string
  arn: string
  visibilityTimeout: number
  approximateNumberOfMessages: number
  approximateNumberOfMessagesNotVisible: number
  createdTimestamp: string
}

export interface SQSQueueDetail extends SQSQueue {
  messageRetentionPeriod: number
  receiveMessageWaitTimeSeconds: number
  delaySeconds: number
  maximumMessageSize: number
}

export interface SQSMessageAttribute {
  dataType: string
  stringValue: string
}

export interface SQSMessage {
  messageId: string
  receiptHandle: string
  body: string
  md5OfBody: string
  attributes: Record<string, string>
  messageAttributes: Record<string, SQSMessageAttribute>
}

const sqs = {
  listQueues: (prefix?: string) => {
    const params = new URLSearchParams()
    if (prefix) params.set("prefix", prefix)
    return apiFetch<{ queues: SQSQueue[] }>(`/sqs/queues?${params}`).then((r) => r.queues)
  },

  createQueue: (opts: {
    name: string
    visibilityTimeout?: number
    messageRetentionPeriod?: number
    receiveMessageWaitTimeSeconds?: number
  }) =>
    apiFetch<{ queueUrl: string }>("/sqs/queues", {
      method: "POST",
      body: JSON.stringify(opts),
    }),

  deleteQueue: (name: string) =>
    apiFetch<{ ok: boolean }>(`/sqs/queues/${encodeURIComponent(name)}`, { method: "DELETE" }),

  getQueue: (name: string) => apiFetch<SQSQueueDetail>(`/sqs/queues/${encodeURIComponent(name)}`),

  purgeQueue: (name: string) =>
    apiFetch<{ ok: boolean }>(`/sqs/queues/${encodeURIComponent(name)}/purge`, {
      method: "POST",
    }),

  receiveMessages: (name: string, max = 10, visibilityTimeout = 0) => {
    const params = new URLSearchParams({
      max: String(max),
      visibilityTimeout: String(visibilityTimeout),
    })
    return apiFetch<{ messages: SQSMessage[] }>(
      `/sqs/queues/${encodeURIComponent(name)}/messages?${params}`,
    ).then((r) => r.messages)
  },

  sendMessage: (
    name: string,
    body: string,
    opts: {
      delaySeconds?: number
      messageAttributes?: Record<string, { dataType: string; stringValue: string }>
    } = {},
  ) =>
    apiFetch<{ messageId: string; md5OfMessageBody: string }>(
      `/sqs/queues/${encodeURIComponent(name)}/messages`,
      { method: "POST", body: JSON.stringify({ body, ...opts }) },
    ),

  deleteMessage: (name: string, receiptHandle: string) =>
    apiFetch<{ ok: boolean }>(
      `/sqs/queues/${encodeURIComponent(name)}/messages/${encodeURIComponent(receiptHandle)}`,
      { method: "DELETE" },
    ),
}

// ─── Health ────────────────────────────────────────────────────────────────

export interface HealthResponse {
  status: string
  timestamp: string
  version: string
  services: string[]
}

const health = {
  check: () => apiFetch<HealthResponse>("/health"),
}

export const api = { s3, sqs, health }
