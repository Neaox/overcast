export interface CloudFrontDistribution {
  id: string
  arn: string
  status: string
  domainName: string
  enabled: boolean
  comment: string
  lastModifiedTime: string
  origins: CloudFrontOrigin[]
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
