import type { CopyUrlFormat } from "@/components/ui/copy-url-button"

export function s3CopyFormats(baseUrl: string, bucket: string, key?: string): CopyUrlFormat[] {
  const path = key ? `${bucket}/${key}` : bucket
  return [
    { label: "S3 URI", value: `s3://${path}`, description: "aws cli" },
    { label: "Path-style", value: `${baseUrl}/${path}`, description: "http" },
  ]
}
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
  storageClass: string
  /**
   * The x-amz-expiration hint, present only when a lifecycle rule will expire
   * the object. Server-computed, so this is the authoritative answer where the
   * object list's per-row estimate is not.
   */
  expiration?: { expiryDate: string; ruleId: string }
}

export interface S3LifecycleTag {
  key: string
  value: string
}

/**
 * A lifecycle rule's filter. At most one of the fields is set: AWS models the
 * filter as a union, with `and` carrying a conjunction of predicates.
 * `undefined` means the rule used the deprecated rule-level Prefix form.
 */
export interface S3LifecycleFilter {
  prefix?: string
  tag?: S3LifecycleTag
  objectSizeGreaterThan?: number
  objectSizeLessThan?: number
  and?: {
    prefix?: string
    tags: S3LifecycleTag[]
    objectSizeGreaterThan?: number
    objectSizeLessThan?: number
  }
}

export interface S3LifecycleTransition {
  days?: number
  date?: string
  storageClass: string
}

export interface S3LifecycleRule {
  id: string
  status: "Enabled" | "Disabled"
  /** The deprecated rule-level Prefix form, when that is what was stored. */
  prefix?: string
  filter?: S3LifecycleFilter
  expirationDays?: number
  expirationDate?: string
  transitions: S3LifecycleTransition[]
  abortIncompleteMultipartUploadDays?: number
}

export interface BucketLifecycleConfiguration {
  rules: S3LifecycleRule[]
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
