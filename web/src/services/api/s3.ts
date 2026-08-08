import { awsClients } from "../aws-clients"
import { apiFetch, endpointHeaders, API_BASE, endpointResolver } from "./base"
import {
  ListBucketsCommand,
  CreateBucketCommand,
  DeleteBucketCommand,
  HeadBucketCommand,
  ListObjectsV2Command,
  ListObjectVersionsCommand,
  GetBucketVersioningCommand,
  HeadObjectCommand,
  DeleteObjectCommand,
  DeleteObjectsCommand,
  GetBucketNotificationConfigurationCommand,
  PutBucketNotificationConfigurationCommand,
  GetBucketLifecycleConfigurationCommand,
  GetBucketWebsiteCommand,
  type Event as S3Event,
  type BucketLocationConstraint,
  type FilterRuleName,
  type LifecycleRule,
  type LifecycleRuleFilter,
} from "@aws-sdk/client-s3"
import type {
  S3Bucket,
  S3ObjectMetadata,
  ListObjectsResult,
  ListObjectVersionsResult,
  S3VersioningStatus,
  NotificationFilterRule,
  BucketNotificationConfig,
  BucketLifecycleConfiguration,
  BucketWebsiteConfiguration,
  S3LifecycleFilter,
  S3LifecycleRule,
} from "@/types"

/**
 * Parses S3's x-amz-expiration header, which the SDK surfaces as `Expiration`:
 *   expiry-date="Fri, 21 Dec 2012 00:00:00 GMT", rule-id="rule-1"
 * Returns undefined when the header is absent or in an unexpected shape —
 * a hint that cannot be read is simply not shown.
 */
function parseExpirationHeader(raw?: string): S3ObjectMetadata["expiration"] {
  if (!raw) return undefined
  const expiryDate = /expiry-date="([^"]+)"/.exec(raw)?.[1]
  const ruleId = /rule-id="([^"]*)"/.exec(raw)?.[1]
  if (!expiryDate) return undefined
  return { expiryDate, ruleId: ruleId ?? "" }
}

function toLifecycleFilter(filter?: LifecycleRuleFilter): S3LifecycleFilter | undefined {
  if (!filter) return undefined
  return {
    prefix: filter.Prefix,
    tag: filter.Tag ? { key: filter.Tag.Key ?? "", value: filter.Tag.Value ?? "" } : undefined,
    objectSizeGreaterThan: filter.ObjectSizeGreaterThan,
    objectSizeLessThan: filter.ObjectSizeLessThan,
    and: filter.And
      ? {
          prefix: filter.And.Prefix,
          tags: (filter.And.Tags ?? []).map((t) => ({ key: t.Key ?? "", value: t.Value ?? "" })),
          objectSizeGreaterThan: filter.And.ObjectSizeGreaterThan,
          objectSizeLessThan: filter.And.ObjectSizeLessThan,
        }
      : undefined,
  }
}

function toLifecycleRule(rule: LifecycleRule, index: number): S3LifecycleRule {
  return {
    id: rule.ID ?? `rule-${index + 1}`,
    status: rule.Status === "Disabled" ? "Disabled" : "Enabled",
    prefix: rule.Prefix,
    filter: toLifecycleFilter(rule.Filter),
    expirationDays: rule.Expiration?.Days,
    expirationDate: rule.Expiration?.Date?.toISOString(),
    transitions: (rule.Transitions ?? []).map((t) => ({
      days: t.Days,
      date: t.Date?.toISOString(),
      storageClass: t.StorageClass ?? "",
    })),
    abortIncompleteMultipartUploadDays: rule.AbortIncompleteMultipartUpload?.DaysAfterInitiation,
  }
}

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
    // Drain the bucket first so the backend's "bucket must be empty" check
    // passes. This walks ListObjectVersions rather than ListObjectsV2 and
    // deletes each entry by version id: in a versioned bucket a plain delete
    // adds a delete marker instead of removing anything, so a ListObjectsV2
    // drain loops forever without ever emptying the bucket. An unversioned
    // bucket reports one "null" version per key and behaves identically.
    let keyMarker: string | undefined
    let versionIdMarker: string | undefined
    do {
      const list = await client.send(
        new ListObjectVersionsCommand({
          Bucket: name,
          MaxKeys: 1000,
          KeyMarker: keyMarker,
          VersionIdMarker: versionIdMarker,
        }),
      )
      const targets = [...(list.Versions ?? []), ...(list.DeleteMarkers ?? [])]
        .filter((v) => v.Key)
        .map((v) => ({ Key: v.Key as string, VersionId: v.VersionId }))
      if (targets.length > 0) {
        await client.send(
          new DeleteObjectsCommand({ Bucket: name, Delete: { Objects: targets, Quiet: true } }),
        )
      }
      keyMarker = list.IsTruncated ? list.NextKeyMarker : undefined
      versionIdMarker = list.IsTruncated ? list.NextVersionIdMarker : undefined
    } while (keyMarker)

    await client.send(new DeleteBucketCommand({ Bucket: name }))
    return { ok: true }
  },

  getBucketVersioning: async (bucket: string): Promise<S3VersioningStatus> => {
    const res = await awsClients.s3().send(new GetBucketVersioningCommand({ Bucket: bucket }))
    return res.Status ?? ""
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

  /**
   * Lists every version and delete marker under a prefix, in AWS's order:
   * keys ascending, then most recently stored first.
   */
  listObjectVersions: async (
    bucket: string,
    opts: {
      prefix?: string
      delimiter?: string
      maxKeys?: number
      keyMarker?: string
      versionIdMarker?: string
    } = {},
  ): Promise<ListObjectVersionsResult> => {
    const res = await awsClients.s3().send(
      new ListObjectVersionsCommand({
        Bucket: bucket,
        Prefix: opts.prefix || undefined,
        Delimiter: opts.delimiter ?? "/",
        MaxKeys: opts.maxKeys ?? 200,
        KeyMarker: opts.keyMarker || undefined,
        VersionIdMarker: opts.versionIdMarker || undefined,
      }),
    )
    const versions = (res.Versions ?? []).map((v) => ({
      key: v.Key ?? "",
      versionId: v.VersionId ?? "null",
      isLatest: v.IsLatest ?? false,
      isDeleteMarker: false,
      lastModified: v.LastModified?.toISOString() ?? "",
      size: v.Size ?? 0,
      etag: (v.ETag ?? "").replace(/"/g, ""),
      storageClass: v.StorageClass ?? "STANDARD",
    }))
    const markers = (res.DeleteMarkers ?? []).map((m) => ({
      key: m.Key ?? "",
      versionId: m.VersionId ?? "null",
      isLatest: m.IsLatest ?? false,
      isDeleteMarker: true,
      lastModified: m.LastModified?.toISOString() ?? "",
      size: 0,
      etag: "",
      storageClass: "",
    }))
    // The SDK splits the interleaved response into two arrays, so the wire
    // order has to be rebuilt: key ascending, then newest first. Version ids
    // sort that way already for versions Overcast minted, but "null" does not,
    // so the timestamp is the tiebreaker that works for both.
    const all = [...versions, ...markers].sort(
      (a, b) => a.key.localeCompare(b.key) || b.lastModified.localeCompare(a.lastModified),
    )
    return {
      versions: all,
      prefixes: (res.CommonPrefixes ?? []).map((p) => ({ prefix: p.Prefix ?? "" })),
      isTruncated: res.IsTruncated ?? false,
      nextKeyMarker: res.NextKeyMarker,
      nextVersionIdMarker: res.NextVersionIdMarker,
    }
  },

  deleteObjectVersion: async (bucket: string, key: string, versionId: string) => {
    await awsClients
      .s3()
      .send(new DeleteObjectCommand({ Bucket: bucket, Key: key, VersionId: versionId }))
    return { ok: true }
  },

  getObjectMetadata: async (bucket: string, key: string): Promise<S3ObjectMetadata> => {
    const res = await awsClients.s3().send(new HeadObjectCommand({ Bucket: bucket, Key: key }))
    return {
      contentType: res.ContentType ?? "application/octet-stream",
      contentLength: res.ContentLength ?? 0,
      lastModified: res.LastModified?.toISOString() ?? "",
      etag: (res.ETag ?? "").replace(/"/g, ""),
      metadata: res.Metadata ?? {},
      storageClass: res.StorageClass ?? "STANDARD",
      expiration: parseExpirationHeader(res.Expiration),
    }
  },

  /** Returns a URL to stream the object via the BFF — for <a> download links. */
  getObjectDownloadUrl: (bucket: string, key: string): string => {
    const endpoint = endpointResolver.get()
    const params = new URLSearchParams(endpointHeaders(endpoint))
    return `${API_BASE}/s3/buckets/${encodeURIComponent(bucket)}/objects/${encodeURIComponent(key)}/download?${params}`
  },

  getObjectText: async (
    bucket: string,
    key: string,
    limitBytes = 1024 * 1024,
  ): Promise<{ text: string; truncated: boolean }> => {
    const res = await fetch(s3.getObjectDownloadUrl(bucket, key), {
      headers: { Range: `bytes=0-${limitBytes - 1}` },
    })
    if (!res.ok) throw new Error(`Preview failed: HTTP ${res.status}`)
    return { text: await res.text(), truncated: res.status === 206 }
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
      eventBridgeEnabled: res.EventBridgeConfiguration !== undefined,
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
          // Put replaces the whole configuration, so an omitted element turns
          // EventBridge delivery off. Echo it back rather than clearing it
          // behind the user's back when they edit an SQS destination.
          EventBridgeConfiguration: config.eventBridgeEnabled ? {} : undefined,
        },
      }),
    )
    return { ok: true }
  },

  /**
   * Returns the bucket's lifecycle rules, or null when it has none.
   * S3 answers NoSuchLifecycleConfiguration rather than an empty document, and
   * "no rules" is a normal state rather than an error worth surfacing.
   */
  getBucketLifecycle: async (bucket: string): Promise<BucketLifecycleConfiguration | null> => {
    try {
      const res = await awsClients
        .s3()
        .send(new GetBucketLifecycleConfigurationCommand({ Bucket: bucket }))
      return {
        rules: (res.Rules ?? []).map(toLifecycleRule),
        transitionDefaultMinimumObjectSize:
          res.TransitionDefaultMinimumObjectSize ?? "all_storage_classes_128K",
      }
    } catch (err) {
      if ((err as { name?: string }).name === "NoSuchLifecycleConfiguration") return null
      throw err
    }
  },

  /**
   * Returns the bucket's website configuration, or null when it has none.
   * S3 answers NoSuchWebsiteConfiguration rather than an empty document, and
   * "not a website bucket" is a normal state rather than an error.
   */
  getBucketWebsite: async (bucket: string): Promise<BucketWebsiteConfiguration | null> => {
    try {
      const res = await awsClients.s3().send(new GetBucketWebsiteCommand({ Bucket: bucket }))
      return {
        indexDocument: res.IndexDocument?.Suffix,
        errorDocument: res.ErrorDocument?.Key,
        redirectAllRequestsTo: res.RedirectAllRequestsTo?.HostName
          ? {
              hostName: res.RedirectAllRequestsTo.HostName,
              protocol: res.RedirectAllRequestsTo.Protocol,
            }
          : undefined,
        routingRules: (res.RoutingRules ?? []).map((rule) => ({
          condition: rule.Condition
            ? {
                httpErrorCodeReturnedEquals: rule.Condition.HttpErrorCodeReturnedEquals,
                keyPrefixEquals: rule.Condition.KeyPrefixEquals,
              }
            : undefined,
          redirect: {
            hostName: rule.Redirect?.HostName,
            httpRedirectCode: rule.Redirect?.HttpRedirectCode,
            protocol: rule.Redirect?.Protocol,
            replaceKeyPrefixWith: rule.Redirect?.ReplaceKeyPrefixWith,
            replaceKeyWith: rule.Redirect?.ReplaceKeyWith,
          },
        })),
      }
    } catch (err) {
      if ((err as { name?: string }).name === "NoSuchWebsiteConfiguration") return null
      throw err
    }
  },

  getBucketStreams: (bucket: string) =>
    apiFetch<{ enabled: boolean }>(`/s3/buckets/${encodeURIComponent(bucket)}/streams`),

  setBucketStreams: (bucket: string, enabled: boolean) =>
    apiFetch<{ enabled: boolean }>(`/s3/buckets/${encodeURIComponent(bucket)}/streams`, {
      method: "PUT",
      body: JSON.stringify({ enabled }),
    }),
}
