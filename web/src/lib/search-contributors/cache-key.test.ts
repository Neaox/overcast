/**
 * Regression coverage for issue #716: the s3, sqs, sns, kinesis, lambda,
 * secretsmanager and logs contributors built their cacheKey as
 * [service, resource, baseUrl] (logs omitted the endpoint entirely), which
 * never matched the shape ([...endpointStore.getKeys(), service, resource])
 * the real feature queries use — so global search's `queryClient.getQueryData`
 * lookup always missed and every keystroke refetched over the network.
 *
 * The fix makes each contributor's cacheKey call the feature's own key
 * factory directly (e.g. `s3Keys.buckets()`), so the two can never drift
 * apart again. These tests seed the query cache under the *real* feature key
 * and assert the contributor reads it straight through — no network call —
 * and that unrelated queries still correctly miss.
 */
import { QueryClient } from "@tanstack/react-query"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { runSearch } from "@/lib/search"
import type { SearchContext } from "@/lib/search"
import { s3Keys } from "@/features/s3/data"
import { sqsKeys } from "@/features/sqs/data"
import { snsKeys } from "@/features/sns/data"
import { kinesisKeys } from "@/features/kinesis/data"
import { lambdaKeys } from "@/features/lambda/data"
import { smKeys } from "@/features/secretsmanager/data"
import { logsKeys } from "@/features/cloudwatch/logs/data"
import { s3, sqs, sns, kinesis, lambda, secretsmanager, logs } from "@/services/api"
import "./s3"
import "./sqs"
import "./sns"
import "./kinesis"
import "./lambda"
import "./secretsmanager"
import "./logs"

vi.mock("@/services/api", () => ({
  s3: { listBuckets: vi.fn(() => Promise.resolve([])) },
  sqs: { listQueues: vi.fn(() => Promise.resolve([])) },
  sns: { listTopics: vi.fn(() => Promise.resolve([])) },
  kinesis: { listStreams: vi.fn(() => Promise.resolve([])) },
  lambda: { listFunctions: vi.fn(() => Promise.resolve([])) },
  secretsmanager: { listSecrets: vi.fn(() => Promise.resolve([])) },
  logs: { listGroups: vi.fn(() => Promise.resolve([])) },
}))

const listBuckets = vi.mocked(s3.listBuckets)
const listQueues = vi.mocked(sqs.listQueues)
const listTopics = vi.mocked(sns.listTopics)
const listStreams = vi.mocked(kinesis.listStreams)
const listFunctions = vi.mocked(lambda.listFunctions)
const listSecrets = vi.mocked(secretsmanager.listSecrets)
const listGroups = vi.mocked(logs.listGroups)

function ctx(queryClient: QueryClient): SearchContext {
  return {
    queryClient,
    // Not read by any of the fixed contributors any more — they resolve the
    // live endpoint themselves via the feature key factory — but the
    // SearchContext contract still requires one.
    endpoint: { baseUrl: "http://localhost:4566", region: "us-east-1", label: "Local" },
  }
}

beforeEach(() => {
  listBuckets.mockClear()
  listQueues.mockClear()
  listTopics.mockClear()
  listStreams.mockClear()
  listFunctions.mockClear()
  listSecrets.mockClear()
  listGroups.mockClear()
})

describe("search-contributors cache key matching", () => {
  it("s3: hits the feature's own query cache and never calls the API", async () => {
    const queryClient = new QueryClient()
    queryClient.setQueryData(s3Keys.buckets(), [
      { name: "my-bucket", creationDate: "2024-01-01T00:00:00Z" },
    ])

    const grouped = await runSearch("my-bucket", ctx(queryClient))

    expect(grouped.get("/s3")).toEqual([expect.objectContaining({ id: "s3:my-bucket" })])
    expect(listBuckets).not.toHaveBeenCalled()
  })

  it("s3: a non-matching query still misses even with a warm cache", async () => {
    const queryClient = new QueryClient()
    queryClient.setQueryData(s3Keys.buckets(), [
      { name: "my-bucket", creationDate: "2024-01-01T00:00:00Z" },
    ])

    const grouped = await runSearch("no-such-thing", ctx(queryClient))

    expect(grouped.get("/s3")).toBeUndefined()
    expect(listBuckets).not.toHaveBeenCalled()
  })

  it("s3: falls back to the API on a genuine cache miss (cold cache)", async () => {
    listBuckets.mockResolvedValueOnce([
      { name: "fresh-bucket", creationDate: "2024-01-01T00:00:00Z" },
    ])
    const queryClient = new QueryClient()

    const grouped = await runSearch("fresh-bucket", ctx(queryClient))

    expect(grouped.get("/s3")).toEqual([expect.objectContaining({ id: "s3:fresh-bucket" })])
    expect(listBuckets).toHaveBeenCalledTimes(1)
  })

  it("sqs: hits the feature's own query cache and never calls the API", async () => {
    const queryClient = new QueryClient()
    queryClient.setQueryData(sqsKeys.queues(), [
      {
        name: "my-queue",
        url: "http://localhost:4566/000000000000/my-queue",
        arn: "arn:aws:sqs:us-east-1:000000000000:my-queue",
        visibilityTimeout: 30,
        approximateNumberOfMessages: 0,
        approximateNumberOfMessagesNotVisible: 0,
        createdTimestamp: "2024-01-01T00:00:00Z",
      },
    ])

    const grouped = await runSearch("my-queue", ctx(queryClient))

    expect(grouped.get("/sqs")).toEqual([expect.objectContaining({ id: "sqs:my-queue" })])
    expect(listQueues).not.toHaveBeenCalled()
  })

  it("sns: hits the feature's own query cache and never calls the API", async () => {
    const queryClient = new QueryClient()
    queryClient.setQueryData(snsKeys.topics(), [
      { TopicArn: "arn:aws:sns:us-east-1:000000000000:my-topic" },
    ])

    const grouped = await runSearch("my-topic", ctx(queryClient))

    expect(grouped.get("/sns")).toEqual([expect.objectContaining({ id: "sns:my-topic" })])
    expect(listTopics).not.toHaveBeenCalled()
  })

  it("kinesis: hits the feature's own query cache and never calls the API", async () => {
    const queryClient = new QueryClient()
    queryClient.setQueryData(kinesisKeys.streams(), [
      {
        name: "my-stream",
        arn: "arn:aws:kinesis:us-east-1:000000000000:stream/my-stream",
        status: "ACTIVE",
        shardCount: 1,
        retentionHours: 24,
      },
    ])

    const grouped = await runSearch("my-stream", ctx(queryClient))

    expect(grouped.get("/kinesis")).toEqual([expect.objectContaining({ id: "kinesis:my-stream" })])
    expect(listStreams).not.toHaveBeenCalled()
  })

  it("lambda: hits the feature's own query cache and never calls the API", async () => {
    const queryClient = new QueryClient()
    queryClient.setQueryData(lambdaKeys.functions(), [
      {
        FunctionName: "my-fn",
        FunctionArn: "arn:aws:lambda:us-east-1:000000000000:function:my-fn",
      },
    ])

    const grouped = await runSearch("my-fn", ctx(queryClient))

    expect(grouped.get("/lambda")).toEqual([expect.objectContaining({ id: "lambda:my-fn" })])
    expect(listFunctions).not.toHaveBeenCalled()
  })

  it("secretsmanager: hits the feature's own query cache and never calls the API", async () => {
    const queryClient = new QueryClient()
    queryClient.setQueryData(smKeys.secrets(), [
      { Name: "my-secret", ARN: "arn:aws:secretsmanager:us-east-1:000000000000:secret:my-secret" },
    ])

    const grouped = await runSearch("my-secret", ctx(queryClient))

    expect(grouped.get("/secretsmanager")).toEqual([
      expect.objectContaining({ id: "secretsmanager:my-secret" }),
    ])
    expect(listSecrets).not.toHaveBeenCalled()
  })

  it("logs: hits the feature's own query cache and never calls the API", async () => {
    const queryClient = new QueryClient()
    queryClient.setQueryData(logsKeys.groups(), [
      {
        logGroupName: "/aws/lambda/my-fn",
        arn: "arn:aws:logs:us-east-1:000000000000:log-group:/aws/lambda/my-fn",
      },
    ])

    const grouped = await runSearch("my-fn", ctx(queryClient))

    expect(grouped.get("/cloudwatch")).toEqual([
      expect.objectContaining({ id: "logs:/aws/lambda/my-fn" }),
    ])
    expect(listGroups).not.toHaveBeenCalled()
  })
})
