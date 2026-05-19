import { awsClients } from "../aws-clients"
import { apiFetch, endpointHeaders, API_BASE, endpointResolver } from "./base"
import {
  ListBucketsCommand,
  CreateBucketCommand,
  DeleteBucketCommand,
  HeadBucketCommand,
  ListObjectsV2Command,
  HeadObjectCommand,
  DeleteObjectCommand,
  DeleteObjectsCommand,
  GetBucketNotificationConfigurationCommand,
  PutBucketNotificationConfigurationCommand,
  type Event as S3Event,
  type BucketLocationConstraint,
  type FilterRuleName,
} from "@aws-sdk/client-s3"
import type {
  S3Bucket,
  S3ObjectMetadata,
  ListObjectsResult,
  NotificationFilterRule,
  BucketNotificationConfig,
} from "@/types"

export const s3 = {
  listBuckets: async () => {
    const { Buckets = [] } = await awsClients.s3().send(new ListBucketsCommand({}))
    return Buckets.map((b) => ({
      name: b.Name ?? "",
      creationDate: b.CreationDate?.toISOString() ?? "",
    })) as S3Bucket[]
  },

  createBucket: async (name: string, region?: string) => {
    const ep = endpointResolver.get()
    const resolvedRegion = region ?? ep.region
    await awsClients.s3().send(
      new CreateBucketCommand({
        Bucket: name,
        ...(resolvedRegion !== "us-east-1" && {
          CreateBucketConfiguration: {
            LocationConstraint: resolvedRegion as BucketLocationConstraint,
          },
        }),
      }),
    )
    return { ok: true }
  },

  deleteBucket: async (name: string) => {
    const client = awsClients.s3()
    // Drain all objects first so the backend's "bucket must be empty" check passes.
    let token: string | undefined
    do {
      const list = await client.send(
        new ListObjectsV2Command({ Bucket: name, MaxKeys: 1000, ContinuationToken: token }),
      )
      const keys = (list.Contents ?? []).map((o) => o.Key).filter(Boolean) as string[]
      if (keys.length > 0) {
        await client.send(
          new DeleteObjectsCommand({
            Bucket: name,
            Delete: { Objects: keys.map((Key) => ({ Key })), Quiet: true },
          }),
        )
      }
      token = list.IsTruncated ? list.NextContinuationToken : undefined
    } while (token)

    await client.send(new DeleteBucketCommand({ Bucket: name }))
    return { ok: true }
  },

  headBucket: async (name: string) => {
    await awsClients.s3().send(new HeadBucketCommand({ Bucket: name }))
    return { ok: true }
  },

  listObjects: async (
    bucket: string,
    opts: {
      prefix?: string
      delimiter?: string
      maxKeys?: number
      token?: string
    } = {},
  ) => {
    const res = await awsClients.s3().send(
      new ListObjectsV2Command({
        Bucket: bucket,
        Prefix: opts.prefix || undefined,
        Delimiter: opts.delimiter ?? "/",
        MaxKeys: opts.maxKeys ?? 200,
        ContinuationToken: opts.token || undefined,
      }),
    )
    return {
      objects: (res.Contents ?? []).map((o) => ({
        key: o.Key ?? "",
        size: o.Size ?? 0,
        lastModified: o.LastModified?.toISOString() ?? "",
        etag: (o.ETag ?? "").replace(/"/g, ""),
        storageClass: o.StorageClass ?? "STANDARD",
      })),
      prefixes: (res.CommonPrefixes ?? []).map((p) => ({ prefix: p.Prefix ?? "" })),
      isTruncated: res.IsTruncated ?? false,
      nextContinuationToken: res.NextContinuationToken,
    } as ListObjectsResult
  },

  getObjectMetadata: async (bucket: string, key: string) => {
    const res = await awsClients.s3().send(new HeadObjectCommand({ Bucket: bucket, Key: key }))
    return {
      contentType: res.ContentType ?? "application/octet-stream",
      contentLength: res.ContentLength ?? 0,
      lastModified: res.LastModified?.toISOString() ?? "",
      etag: (res.ETag ?? "").replace(/"/g, ""),
      metadata: res.Metadata ?? {},
    } as S3ObjectMetadata
  },

  /** Returns a URL to stream the object via the BFF — for <a> download links. */
  getObjectDownloadUrl: (bucket: string, key: string): string => {
    const endpoint = endpointResolver.get()
    const params = new URLSearchParams(endpointHeaders(endpoint))
    return `${API_BASE}/s3/buckets/${encodeURIComponent(bucket)}/objects/${encodeURIComponent(key)}/download?${params}`
  },

  deleteObject: async (bucket: string, key: string) => {
    await awsClients.s3().send(new DeleteObjectCommand({ Bucket: bucket, Key: key }))
    return { ok: true }
  },

  deleteObjectsByPrefix: async (bucket: string, prefix: string) => {
    const client = awsClients.s3()
    let deleted = 0
    let token: string | undefined
    do {
      const list = await client.send(
        new ListObjectsV2Command({
          Bucket: bucket,
          Prefix: prefix,
          MaxKeys: 1000,
          ContinuationToken: token,
        }),
      )
      const keys = (list.Contents ?? []).map((o) => o.Key).filter(Boolean) as string[]
      if (keys.length > 0) {
        await client.send(
          new DeleteObjectsCommand({
            Bucket: bucket,
            Delete: { Objects: keys.map((Key) => ({ Key })), Quiet: true },
          }),
        )
      }
      deleted += keys.length
      token = list.IsTruncated ? list.NextContinuationToken : undefined
    } while (token)
    return { ok: true, deleted }
  },

  getBucketNotification: async (bucket: string) => {
    const res = await awsClients
      .s3()
      .send(new GetBucketNotificationConfigurationCommand({ Bucket: bucket }))
    return {
      queueConfigurations: (res.QueueConfigurations ?? []).map((q) => ({
        id: q.Id ?? "",
        queueArn: q.QueueArn ?? "",
        events: q.Events ?? [],
        filterRules:
          q.Filter?.Key?.FilterRules?.map((r) => ({
            name: r.Name ?? "",
            value: r.Value ?? "",
          })) ?? [],
      })),
      topicConfigurations: (res.TopicConfigurations ?? []).map((t) => ({
        id: t.Id ?? "",
        topicArn: t.TopicArn ?? "",
        events: t.Events ?? [],
        filterRules:
          t.Filter?.Key?.FilterRules?.map((r) => ({
            name: r.Name ?? "",
            value: r.Value ?? "",
          })) ?? [],
      })),
      lambdaConfigurations: (res.LambdaFunctionConfigurations ?? []).map((l) => ({
        id: l.Id ?? "",
        functionArn: l.LambdaFunctionArn ?? "",
        events: l.Events ?? [],
        filterRules:
          l.Filter?.Key?.FilterRules?.map((r) => ({
            name: r.Name ?? "",
            value: r.Value ?? "",
          })) ?? [],
      })),
    } as BucketNotificationConfig
  },

  putBucketNotification: async (bucket: string, config: BucketNotificationConfig) => {
    const toFilterRules = (rules?: NotificationFilterRule[]) =>
      rules && rules.length > 0
        ? {
            Key: {
              FilterRules: rules.map((r) => ({
                Name: r.name as FilterRuleName,
                Value: r.value,
              })),
            },
          }
        : undefined

    await awsClients.s3().send(
      new PutBucketNotificationConfigurationCommand({
        Bucket: bucket,
        NotificationConfiguration: {
          QueueConfigurations: config.queueConfigurations.map((q) => ({
            Id: q.id || undefined,
            QueueArn: q.queueArn,
            Events: q.events as S3Event[],
            Filter: toFilterRules(q.filterRules),
          })),
          TopicConfigurations: config.topicConfigurations.map((t) => ({
            Id: t.id || undefined,
            TopicArn: t.topicArn,
            Events: t.events as S3Event[],
            Filter: toFilterRules(t.filterRules),
          })),
          LambdaFunctionConfigurations: config.lambdaConfigurations.map((l) => ({
            Id: l.id || undefined,
            LambdaFunctionArn: l.functionArn,
            Events: l.events as S3Event[],
            Filter: toFilterRules(l.filterRules),
          })),
        },
      }),
    )
    return { ok: true }
  },

  getBucketStreams: (bucket: string) =>
    apiFetch<{ enabled: boolean }>(`/s3/buckets/${encodeURIComponent(bucket)}/streams`),

  setBucketStreams: (bucket: string, enabled: boolean) =>
    apiFetch<{ enabled: boolean }>(`/s3/buckets/${encodeURIComponent(bucket)}/streams`, {
      method: "PUT",
      body: JSON.stringify({ enabled }),
    }),
}
