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

/** A bucket's versioning state. "" is a bucket that has never been versioned. */
export type S3VersioningStatus = "" | "Enabled" | "Suspended"

/**
 * One entry from ListObjectVersions. A delete marker is the same shape minus
 * the body-derived fields, so `isDeleteMarker` is what tells the two apart —
 * without it a tombstone is indistinguishable from an object that is simply
 * gone, which is the whole reason the version listing exists.
 */
export interface S3ObjectVersion {
  key: string
  versionId: string
  isLatest: boolean
  isDeleteMarker: boolean
  lastModified: string
  size: number
  etag: string
  storageClass: string
}

export interface ListObjectVersionsResult {
  versions: S3ObjectVersion[]
  prefixes: S3Prefix[]
  isTruncated: boolean
  nextKeyMarker?: string
  nextVersionIdMarker?: string
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
  /**
   * The x-amz-transition-default-minimum-object-size behaviour in force. S3
   * reports one on every GetBucketLifecycleConfiguration, defaulting to
   * `all_storage_classes_128K`.
   */
  transitionDefaultMinimumObjectSize: string
}

export interface S3WebsiteRedirect {
  hostName?: string
  httpRedirectCode?: string
  protocol?: string
  replaceKeyPrefixWith?: string
  replaceKeyWith?: string
}

export interface S3WebsiteRoutingRule {
  condition?: {
    httpErrorCodeReturnedEquals?: string
    keyPrefixEquals?: string
  }
  redirect: S3WebsiteRedirect
}

/**
 * The bucket's website configuration. AWS's two forms are mutually exclusive:
 * `redirectAllRequestsTo` on its own, or an index document with an optional
 * error document and routing rules.
 */
export interface BucketWebsiteConfiguration {
  indexDocument?: string
  errorDocument?: string
  redirectAllRequestsTo?: { hostName: string; protocol?: string }
  routingRules: S3WebsiteRoutingRule[]
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
  /**
   * Whether the bucket sends every object event to the default EventBridge
   * bus. AWS models this as the presence of an empty `EventBridgeConfiguration`
   * element, so there is nothing else to carry.
   */
  eventBridgeEnabled: boolean
}
