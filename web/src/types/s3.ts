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
