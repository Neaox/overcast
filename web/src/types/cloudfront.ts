export interface CloudFrontDistribution {
  id: string
  arn: string
  status: string
  domainName: string
  enabled: boolean
  comment: string
  lastModifiedTime: string
  origins: CloudFrontOrigin[]
  originGroups: CloudFrontOriginGroup[]
  defaultRootObject: string
  priceClass: string
  httpVersion: string
  aliases: string[]
}

export interface CloudFrontOrigin {
  id: string
  domainName: string
  originPath: string
  s3OriginConfig?: { originAccessIdentity: string }
  customOriginConfig?: {
    httpPort: number
    httpsPort: number
    originProtocolPolicy: string
  }
}

/**
 * An origin group gives a cache behavior a primary origin and a failover one.
 * A behavior's TargetOriginId may name a group instead of an origin, so a
 * distribution's origin list alone does not explain where its traffic goes.
 */
export interface CloudFrontOriginGroup {
  id: string
  /** Origin IDs in failover order: the first is primary. */
  members: string[]
  /** Origin response codes that trigger failover to the next member. */
  failoverStatusCodes: number[]
}

export interface CloudFrontInvalidation {
  id: string
  status: string
  createTime: string
  paths: string[]
}

export interface CloudFrontOriginAccessControl {
  id: string
  name: string
  description: string
  signingProtocol: string
  signingBehavior: string
  originAccessControlOriginType: string
}

export interface CloudFrontCachePolicy {
  id: string
  name: string
  comment: string
  defaultTTL: number
  maxTTL: number
  minTTL: number
  lastModifiedTime: string
}

export interface CloudFrontRealtimeLogConfig {
  arn: string
  name: string
  samplingRate: number
}

export interface CloudFrontKeyGroup {
  id: string
  name: string
  comment: string
  lastModifiedTime: string
}

export interface CloudFrontFLEConfig {
  id: string
  comment: string
  contentTypeProfileConfig: string
  lastModifiedTime: string
}

export interface CloudFrontFLEProfile {
  id: string
  name: string
  comment: string
  lastModifiedTime: string
}

export interface CloudFrontContinuousDeploymentPolicy {
  id: string
  enabled: boolean
  lastModifiedTime: string
}

export interface CloudFrontMonitoringSubscription {
  realtimeMetricsSubscriptionStatus: string
}
