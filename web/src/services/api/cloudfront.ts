import { awsClients } from "../aws-clients"
import {
  ListDistributionsCommand,
  GetDistributionCommand,
  CreateDistributionCommand,
  UpdateDistributionCommand,
  DeleteDistributionCommand,
  CreateInvalidationCommand,
  ListInvalidationsCommand,
  GetInvalidationCommand,
  ListOriginAccessControlsCommand,
  ListRealtimeLogConfigsCommand,
  ListKeyGroupsCommand,
  ListFieldLevelEncryptionConfigsCommand,
  ListFieldLevelEncryptionProfilesCommand,
  ListContinuousDeploymentPoliciesCommand,
  GetMonitoringSubscriptionCommand,
  type Origin,
  type Origins,
} from "@aws-sdk/client-cloudfront"
import type {
  CloudFrontDistribution,
  CloudFrontOrigin,
  CloudFrontInvalidation,
  CloudFrontOriginAccessControl,
  CloudFrontRealtimeLogConfig,
  CloudFrontKeyGroup,
  CloudFrontFLEConfig,
  CloudFrontFLEProfile,
  CloudFrontContinuousDeploymentPolicy,
  CloudFrontMonitoringSubscription,
} from "@/types"

function mapOrigins(origins?: Origins): CloudFrontOrigin[] {
  if (!origins?.Items) return []
  return origins.Items.map((o: Origin) => ({
    id: o.Id ?? "",
    domainName: o.DomainName ?? "",
    originPath: o.OriginPath ?? "",
    ...(o.S3OriginConfig
      ? {
          s3OriginConfig: {
            originAccessIdentity: o.S3OriginConfig.OriginAccessIdentity ?? "",
          },
        }
      : {}),
    ...(o.CustomOriginConfig
      ? {
          customOriginConfig: {
            httpPort: o.CustomOriginConfig.HTTPPort ?? 80,
            httpsPort: o.CustomOriginConfig.HTTPSPort ?? 443,
            originProtocolPolicy: o.CustomOriginConfig.OriginProtocolPolicy ?? "",
          },
        }
      : {}),
  }))
}

export const cloudfront = {
  listDistributions: async (): Promise<CloudFrontDistribution[]> => {
    const res = await awsClients.cloudfront().send(new ListDistributionsCommand({}))
    const items = res.DistributionList?.Items ?? []
    return items.map((d) => ({
      id: d.Id ?? "",
      arn: d.ARN ?? "",
      status: d.Status ?? "",
      domainName: d.DomainName ?? "",
      enabled: d.Enabled ?? false,
      comment: d.Comment ?? "",
      lastModifiedTime: d.LastModifiedTime?.toISOString() ?? "",
      origins: mapOrigins(d.Origins),
      defaultRootObject: "",
      priceClass: d.PriceClass ?? "",
      httpVersion: d.HttpVersion ?? "",
      aliases: d.Aliases?.Items ?? [],
    }))
  },

  getDistribution: async (
    id: string,
  ): Promise<{ distribution: CloudFrontDistribution; etag: string }> => {
    const res = await awsClients.cloudfront().send(new GetDistributionCommand({ Id: id }))
    const d = res.Distribution!
    const cfg = d.DistributionConfig!
    return {
      distribution: {
        id: d.Id ?? "",
        arn: d.ARN ?? "",
        status: d.Status ?? "",
        domainName: d.DomainName ?? "",
        enabled: cfg.Enabled ?? false,
        comment: cfg.Comment ?? "",
        lastModifiedTime: d.LastModifiedTime?.toISOString() ?? "",
        origins: mapOrigins(cfg.Origins),
        defaultRootObject: cfg.DefaultRootObject ?? "",
        priceClass: cfg.PriceClass ?? "",
        httpVersion: cfg.HttpVersion ?? "",
        aliases: cfg.Aliases?.Items ?? [],
      },
      etag: res.ETag ?? "",
    }
  },

  createDistribution: async (opts: {
    comment: string
    enabled: boolean
    originDomainName: string
    originId: string
    defaultRootObject?: string
  }) => {
    const res = await awsClients.cloudfront().send(
      new CreateDistributionCommand({
        DistributionConfig: {
          CallerReference: `ref-${Date.now()}`,
          Comment: opts.comment,
          Enabled: opts.enabled,
          DefaultRootObject: opts.defaultRootObject || "",
          Origins: {
            Quantity: 1,
            Items: [
              {
                Id: opts.originId,
                DomainName: opts.originDomainName,
                CustomOriginConfig: {
                  HTTPPort: 80,
                  HTTPSPort: 443,
                  OriginProtocolPolicy: "http-only",
                },
              },
            ],
          },
          DefaultCacheBehavior: {
            TargetOriginId: opts.originId,
            ViewerProtocolPolicy: "allow-all",
            ForwardedValues: {
              QueryString: false,
              Cookies: { Forward: "none" },
            },
            MinTTL: 0,
          },
        },
      }),
    )
    return { id: res.Distribution?.Id ?? "" }
  },

  updateDistribution: async (
    id: string,
    etag: string,
    updates: { comment?: string; enabled?: boolean; defaultRootObject?: string },
  ) => {
    const client = awsClients.cloudfront()
    // Fetch current config to merge updates
    const current = await client.send(new GetDistributionCommand({ Id: id }))
    const cfg = { ...current.Distribution!.DistributionConfig! }
    if (updates.comment !== undefined) cfg.Comment = updates.comment
    if (updates.enabled !== undefined) cfg.Enabled = updates.enabled
    if (updates.defaultRootObject !== undefined) cfg.DefaultRootObject = updates.defaultRootObject

    await client.send(
      new UpdateDistributionCommand({
        Id: id,
        IfMatch: etag,
        DistributionConfig: cfg,
      }),
    )
    return { ok: true }
  },

  deleteDistribution: async (id: string, etag: string) => {
    await awsClients.cloudfront().send(new DeleteDistributionCommand({ Id: id, IfMatch: etag }))
    return { ok: true }
  },

  createInvalidation: async (distributionId: string, paths: string[]) => {
    const res = await awsClients.cloudfront().send(
      new CreateInvalidationCommand({
        DistributionId: distributionId,
        InvalidationBatch: {
          CallerReference: `inv-${Date.now()}`,
          Paths: { Quantity: paths.length, Items: paths },
        },
      }),
    )
    return { id: res.Invalidation?.Id ?? "" }
  },

  listInvalidations: async (distributionId: string): Promise<CloudFrontInvalidation[]> => {
    const client = awsClients.cloudfront()
    const res = await client.send(new ListInvalidationsCommand({ DistributionId: distributionId }))
    const items = res.InvalidationList?.Items ?? []
    // ListInvalidations only returns summary — need GetInvalidation for paths.
    return await Promise.all(
      items.map(async (inv) => {
        try {
          const detail = await client.send(
            new GetInvalidationCommand({
              DistributionId: distributionId,
              Id: inv.Id,
            }),
          )
          return {
            id: inv.Id ?? "",
            status: inv.Status ?? "",
            createTime: inv.CreateTime?.toISOString() ?? "",
            paths: detail.Invalidation?.InvalidationBatch?.Paths?.Items ?? [],
          }
        } catch {
          return {
            id: inv.Id ?? "",
            status: inv.Status ?? "",
            createTime: inv.CreateTime?.toISOString() ?? "",
            paths: [],
          }
        }
      }),
    )
  },

  listOriginAccessControls: async (): Promise<CloudFrontOriginAccessControl[]> => {
    const res = await awsClients.cloudfront().send(new ListOriginAccessControlsCommand({}))
    const items = res.OriginAccessControlList?.Items ?? []
    return items.map((oac) => ({
      id: oac.Id ?? "",
      name: oac.Name ?? "",
      description: oac.Description ?? "",
      signingProtocol: oac.SigningProtocol ?? "",
      signingBehavior: oac.SigningBehavior ?? "",
      originAccessControlOriginType: oac.OriginAccessControlOriginType ?? "",
    }))
  },

  listRealtimeLogConfigs: async (): Promise<CloudFrontRealtimeLogConfig[]> => {
    const res = await awsClients.cloudfront().send(new ListRealtimeLogConfigsCommand({}))
    const items = res.RealtimeLogConfigs?.Items ?? []
    return items.map((c) => ({
      arn: c.ARN ?? "",
      name: c.Name ?? "",
      samplingRate: c.SamplingRate ?? 0,
    }))
  },

  listKeyGroups: async (): Promise<CloudFrontKeyGroup[]> => {
    const res = await awsClients.cloudfront().send(new ListKeyGroupsCommand({}))
    const items = res.KeyGroupList?.Items ?? []
    return items.map((kg) => ({
      id: kg.KeyGroup?.Id ?? "",
      name: kg.KeyGroup?.KeyGroupConfig?.Name ?? "",
      comment: kg.KeyGroup?.KeyGroupConfig?.Comment ?? "",
      lastModifiedTime: kg.KeyGroup?.LastModifiedTime?.toISOString() ?? "",
    }))
  },

  listFLEConfigs: async (): Promise<CloudFrontFLEConfig[]> => {
    const res = await awsClients.cloudfront().send(new ListFieldLevelEncryptionConfigsCommand({}))
    const items = res.FieldLevelEncryptionList?.Items ?? []
    return items.map((c) => ({
      id: c.Id ?? "",
      comment: c.Comment ?? "",
      contentTypeProfileConfig: c.ContentTypeProfileConfig?.ForwardWhenContentTypeIsUnknown
        ? "Forward unknown"
        : "Block unknown",
      lastModifiedTime: c.LastModifiedTime?.toISOString() ?? "",
    }))
  },

  listFLEProfiles: async (): Promise<CloudFrontFLEProfile[]> => {
    const res = await awsClients.cloudfront().send(new ListFieldLevelEncryptionProfilesCommand({}))
    const items = res.FieldLevelEncryptionProfileList?.Items ?? []
    return items.map((p) => ({
      id: p.Id ?? "",
      name: p.Name ?? "",
      comment: p.Comment ?? "",
      lastModifiedTime: p.LastModifiedTime?.toISOString() ?? "",
    }))
  },

  listContinuousDeploymentPolicies: async (): Promise<CloudFrontContinuousDeploymentPolicy[]> => {
    const res = await awsClients.cloudfront().send(new ListContinuousDeploymentPoliciesCommand({}))
    const items = res.ContinuousDeploymentPolicyList?.Items ?? []
    return items.map((p) => ({
      id: p.ContinuousDeploymentPolicy?.Id ?? "",
      enabled: p.ContinuousDeploymentPolicy?.ContinuousDeploymentPolicyConfig?.Enabled ?? false,
      lastModifiedTime: p.ContinuousDeploymentPolicy?.LastModifiedTime?.toISOString() ?? "",
    }))
  },

  getMonitoringSubscription: async (distId: string): Promise<CloudFrontMonitoringSubscription> => {
    const res = await awsClients
      .cloudfront()
      .send(new GetMonitoringSubscriptionCommand({ DistributionId: distId }))
    return {
      realtimeMetricsSubscriptionStatus:
        res.MonitoringSubscription?.RealtimeMetricsSubscriptionConfig
          ?.RealtimeMetricsSubscriptionStatus ?? "Disabled",
    }
  },
}
