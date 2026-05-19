/**
 * groups/cloudfront.ts — CloudFront compatibility test groups for the Node.js suite.
 *
 * Groups:
 *   cloudfront-distributions            — CloudFront distribution lifecycle
 *   cloudfront-oac                      — Origin Access Controls
 *   cloudfront-cache-policy             — Cache Policies
 *   cloudfront-key-group                — Key Groups
 *   cloudfront-realtime-log             — Realtime Log Configs
 *   cloudfront-monitoring               — Monitoring Subscriptions
 *   cloudfront-fle-config               — Field-Level Encryption Configs
 *   cloudfront-fle-profile              — Field-Level Encryption Profiles
 *   cloudfront-continuous-deployment    — Continuous Deployment Policies
 */

import {
  CreateDistributionCommand,
  DeleteDistributionCommand,
  GetDistributionCommand,
  UpdateDistributionCommand,
  ListDistributionsCommand,
  CreateOriginAccessControlCommand,
  GetOriginAccessControlCommand,
  UpdateOriginAccessControlCommand,
  DeleteOriginAccessControlCommand,
  ListOriginAccessControlsCommand,
  CreateCachePolicyCommand,
  GetCachePolicyCommand,
  GetCachePolicyConfigCommand,
  UpdateCachePolicyCommand,
  DeleteCachePolicyCommand,
  ListCachePoliciesCommand,
  CreateKeyGroupCommand,
  GetKeyGroupCommand,
  GetKeyGroupConfigCommand,
  UpdateKeyGroupCommand,
  DeleteKeyGroupCommand,
  ListKeyGroupsCommand,
  CreateRealtimeLogConfigCommand,
  GetRealtimeLogConfigCommand,
  UpdateRealtimeLogConfigCommand,
  DeleteRealtimeLogConfigCommand,
  ListRealtimeLogConfigsCommand,
  CreateMonitoringSubscriptionCommand,
  GetMonitoringSubscriptionCommand,
  DeleteMonitoringSubscriptionCommand,
  CreateFieldLevelEncryptionConfigCommand,
  GetFieldLevelEncryptionCommand,
  GetFieldLevelEncryptionConfigCommand,
  UpdateFieldLevelEncryptionConfigCommand,
  DeleteFieldLevelEncryptionConfigCommand,
  ListFieldLevelEncryptionConfigsCommand,
  CreateFieldLevelEncryptionProfileCommand,
  GetFieldLevelEncryptionProfileCommand,
  GetFieldLevelEncryptionProfileConfigCommand,
  UpdateFieldLevelEncryptionProfileCommand,
  DeleteFieldLevelEncryptionProfileCommand,
  ListFieldLevelEncryptionProfilesCommand,
  CreateContinuousDeploymentPolicyCommand,
  GetContinuousDeploymentPolicyCommand,
  GetContinuousDeploymentPolicyConfigCommand,
  UpdateContinuousDeploymentPolicyCommand,
  DeleteContinuousDeploymentPolicyCommand,
  ListContinuousDeploymentPoliciesCommand,
} from "@aws-sdk/client-cloudfront";
import { makeClients } from "../lib/clients.js";
import type { TestGroup } from "../lib/harness.js";
import * as assert from "node:assert/strict";

export function makeCloudFrontGroups(suite: string): TestGroup[] {
  return [
    // ── cloudfront-distributions ───────────────────────────────────────────
    {
      suite,
      service: "cloudfront",
      name: "cloudfront-distributions",
      tests: [
        {
          name: "CreateDistribution",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const resp = await cloudfront.send(
              new CreateDistributionCommand({
                DistributionConfig: {
                  CallerReference: `compat-${ctx.runId}`,
                  Comment: "compat test distribution",
                  Enabled: true,
                  Origins: {
                    Quantity: 1,
                    Items: [
                      {
                        Id: "origin-1",
                        DomainName: "example.com",
                        S3OriginConfig: { OriginAccessIdentity: "" },
                      },
                    ],
                  },
                  DefaultCacheBehavior: {
                    TargetOriginId: "origin-1",
                    ViewerProtocolPolicy: "redirect-to-https",
                    ForwardedValues: {
                      QueryString: false,
                      Cookies: { Forward: "none" },
                    },
                    MinTTL: 0,
                    TrustedSigners: { Enabled: false, Quantity: 0 },
                  },
                },
              }),
            );
            assert.ok(resp.Distribution?.Id, "CreateDistribution: missing Id");
            (ctx as Record<string, unknown>)["_distroId"] =
              resp.Distribution.Id;
            (ctx as Record<string, unknown>)["_distroEtag"] = resp.ETag;
          },
        },
        {
          name: "GetDistribution",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const id = (ctx as Record<string, unknown>)["_distroId"] as string;
            assert.ok(
              id,
              "GetDistribution: no distribution from CreateDistribution",
            );
            const resp = await cloudfront.send(
              new GetDistributionCommand({ Id: id }),
            );
            assert.ok(resp.Distribution?.Id, "GetDistribution: missing Id");
          },
        },
        {
          name: "ListDistributions",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            await cloudfront.send(new ListDistributionsCommand({}));
          },
        },
        {
          name: "DeleteDistribution",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const id = (ctx as Record<string, unknown>)["_distroId"] as string;
            if (!id) return;
            // Must disable before deleting — fetch current config, disable, then delete.
            const get = await cloudfront.send(
              new GetDistributionCommand({ Id: id }),
            );
            const config = get.Distribution?.DistributionConfig;
            const etag = get.ETag;
            assert.ok(
              config && etag,
              "DeleteDistribution: missing config/etag",
            );
            config!.Enabled = false;
            const update = await cloudfront.send(
              new UpdateDistributionCommand({
                Id: id,
                IfMatch: etag,
                DistributionConfig: config,
              }),
            );
            await cloudfront.send(
              new DeleteDistributionCommand({ Id: id, IfMatch: update.ETag }),
            );
          },
        },
      ],
      teardown: async (ctx) => {
        const { cloudfront } = makeClients(ctx);
        const id = (ctx as Record<string, unknown>)["_distroId"] as string;
        if (!id) return;
        try {
          // CloudFront requires a distribution to be disabled before deletion.
          // Fetch the current config, disable it, then delete using the new ETag.
          const get = await cloudfront.send(
            new GetDistributionCommand({ Id: id }),
          );
          const config = get.Distribution?.DistributionConfig;
          const etag = get.ETag;
          if (config && etag) {
            config.Enabled = false;
            const update = await cloudfront.send(
              new UpdateDistributionCommand({
                Id: id,
                IfMatch: etag,
                DistributionConfig: config,
              }),
            );
            const newEtag = update.ETag;
            if (newEtag) {
              await cloudfront.send(
                new DeleteDistributionCommand({ Id: id, IfMatch: newEtag }),
              );
            }
          }
        } catch {}
      },
    },

    // ── cloudfront-oac ─────────────────────────────────────────────────────
    {
      suite,
      service: "cloudfront",
      name: "cloudfront-oac",
      tests: [
        {
          name: "CreateOriginAccessControl",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const resp = await cloudfront.send(
              new CreateOriginAccessControlCommand({
                OriginAccessControlConfig: {
                  Name: `oc-oac-${ctx.runId}`,
                  OriginAccessControlOriginType: "s3",
                  SigningBehavior: "always",
                  SigningProtocol: "sigv4",
                },
              }),
            );
            assert.ok(
              resp.OriginAccessControl?.Id,
              "CreateOriginAccessControl: missing Id",
            );
            (ctx as Record<string, unknown>)["_oacId"] =
              resp.OriginAccessControl.Id;
            (ctx as Record<string, unknown>)["_oacEtag"] = resp.ETag;
          },
        },
        {
          name: "GetOriginAccessControl",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const id = (ctx as Record<string, unknown>)["_oacId"] as string;
            assert.ok(id, "GetOriginAccessControl: no OAC from Create");
            const resp = await cloudfront.send(
              new GetOriginAccessControlCommand({ Id: id }),
            );
            assert.ok(
              resp.OriginAccessControl?.Id,
              "GetOriginAccessControl: missing Id",
            );
          },
        },
        {
          name: "UpdateOriginAccessControl",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const id = (ctx as Record<string, unknown>)["_oacId"] as string;
            const etag = (ctx as Record<string, unknown>)["_oacEtag"] as string;
            assert.ok(id, "UpdateOriginAccessControl: no OAC from Create");
            const resp = await cloudfront.send(
              new UpdateOriginAccessControlCommand({
                Id: id,
                IfMatch: etag,
                OriginAccessControlConfig: {
                  Name: `oc-oac-${ctx.runId}`,
                  OriginAccessControlOriginType: "s3",
                  SigningBehavior: "never",
                  SigningProtocol: "sigv4",
                },
              }),
            );
            assert.ok(
              resp.OriginAccessControl?.Id,
              "UpdateOriginAccessControl: missing Id",
            );
            (ctx as Record<string, unknown>)["_oacEtag"] = resp.ETag;
          },
        },
        {
          name: "ListOriginAccessControls",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const resp = await cloudfront.send(
              new ListOriginAccessControlsCommand({}),
            );
            assert.ok(
              resp.OriginAccessControlList,
              "ListOriginAccessControls: missing list",
            );
          },
        },
        {
          name: "DeleteOriginAccessControl",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const id = (ctx as Record<string, unknown>)["_oacId"] as string;
            const etag = (ctx as Record<string, unknown>)["_oacEtag"] as string;
            if (!id || !etag) return;
            await cloudfront.send(
              new DeleteOriginAccessControlCommand({ Id: id, IfMatch: etag }),
            );
            (ctx as Record<string, unknown>)["_oacId"] = undefined;
          },
        },
      ],
      teardown: async (ctx) => {
        const { cloudfront } = makeClients(ctx);
        const id = (ctx as Record<string, unknown>)["_oacId"] as string;
        if (!id) return;
        try {
          const get = await cloudfront.send(
            new GetOriginAccessControlCommand({ Id: id }),
          );
          if (get.ETag) {
            await cloudfront.send(
              new DeleteOriginAccessControlCommand({
                Id: id,
                IfMatch: get.ETag,
              }),
            );
          }
        } catch {}
      },
    },

    // ── cloudfront-cache-policy ────────────────────────────────────────────
    {
      suite,
      service: "cloudfront",
      name: "cloudfront-cache-policy",
      tests: [
        {
          name: "CreateCachePolicy",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const resp = await cloudfront.send(
              new CreateCachePolicyCommand({
                CachePolicyConfig: {
                  Name: `oc-cp-${ctx.runId}`,
                  MinTTL: 0,
                  DefaultTTL: 86400,
                  MaxTTL: 31536000,
                  ParametersInCacheKeyAndForwardedToOrigin: {
                    CookiesConfig: { CookieBehavior: "none" },
                    EnableAcceptEncodingGzip: false,
                    HeadersConfig: { HeaderBehavior: "none" },
                    QueryStringsConfig: { QueryStringBehavior: "none" },
                  },
                },
              }),
            );
            assert.ok(resp.CachePolicy?.Id, "CreateCachePolicy: missing Id");
            (ctx as Record<string, unknown>)["_cpId"] = resp.CachePolicy.Id;
            (ctx as Record<string, unknown>)["_cpEtag"] = resp.ETag;
          },
        },
        {
          name: "GetCachePolicy",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const id = (ctx as Record<string, unknown>)["_cpId"] as string;
            assert.ok(id, "GetCachePolicy: no policy from Create");
            const resp = await cloudfront.send(
              new GetCachePolicyCommand({ Id: id }),
            );
            assert.ok(resp.CachePolicy?.Id, "GetCachePolicy: missing Id");
          },
        },
        {
          name: "GetCachePolicyConfig",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const id = (ctx as Record<string, unknown>)["_cpId"] as string;
            assert.ok(id, "GetCachePolicyConfig: no policy from Create");
            const resp = await cloudfront.send(
              new GetCachePolicyConfigCommand({ Id: id }),
            );
            assert.ok(
              resp.CachePolicyConfig,
              "GetCachePolicyConfig: missing config",
            );
          },
        },
        {
          name: "UpdateCachePolicy",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const id = (ctx as Record<string, unknown>)["_cpId"] as string;
            const etag = (ctx as Record<string, unknown>)["_cpEtag"] as string;
            assert.ok(id, "UpdateCachePolicy: no policy from Create");
            const resp = await cloudfront.send(
              new UpdateCachePolicyCommand({
                Id: id,
                IfMatch: etag,
                CachePolicyConfig: {
                  Name: `oc-cp-${ctx.runId}`,
                  MinTTL: 0,
                  DefaultTTL: 3600,
                  MaxTTL: 86400,
                  ParametersInCacheKeyAndForwardedToOrigin: {
                    CookiesConfig: { CookieBehavior: "none" },
                    EnableAcceptEncodingGzip: true,
                    HeadersConfig: { HeaderBehavior: "none" },
                    QueryStringsConfig: { QueryStringBehavior: "none" },
                  },
                },
              }),
            );
            assert.ok(resp.CachePolicy?.Id, "UpdateCachePolicy: missing Id");
            (ctx as Record<string, unknown>)["_cpEtag"] = resp.ETag;
          },
        },
        {
          name: "ListCachePolicies",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const resp = await cloudfront.send(
              new ListCachePoliciesCommand({}),
            );
            assert.ok(resp.CachePolicyList, "ListCachePolicies: missing list");
          },
        },
        {
          name: "DeleteCachePolicy",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const id = (ctx as Record<string, unknown>)["_cpId"] as string;
            const etag = (ctx as Record<string, unknown>)["_cpEtag"] as string;
            if (!id || !etag) return;
            await cloudfront.send(
              new DeleteCachePolicyCommand({ Id: id, IfMatch: etag }),
            );
            (ctx as Record<string, unknown>)["_cpId"] = undefined;
          },
        },
      ],
      teardown: async (ctx) => {
        const { cloudfront } = makeClients(ctx);
        const id = (ctx as Record<string, unknown>)["_cpId"] as string;
        if (!id) return;
        try {
          const get = await cloudfront.send(
            new GetCachePolicyCommand({ Id: id }),
          );
          if (get.ETag) {
            await cloudfront.send(
              new DeleteCachePolicyCommand({ Id: id, IfMatch: get.ETag }),
            );
          }
        } catch {}
      },
    },

    // ── cloudfront-key-group ───────────────────────────────────────────────
    {
      suite,
      service: "cloudfront",
      name: "cloudfront-key-group",
      tests: [
        {
          name: "CreateKeyGroup",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const resp = await cloudfront.send(
              new CreateKeyGroupCommand({
                KeyGroupConfig: {
                  Name: `oc-kg-${ctx.runId}`,
                  Items: ["K1234567890ABCDE"],
                },
              }),
            );
            assert.ok(resp.KeyGroup?.Id, "CreateKeyGroup: missing Id");
            (ctx as Record<string, unknown>)["_kgId"] = resp.KeyGroup.Id;
            (ctx as Record<string, unknown>)["_kgEtag"] = resp.ETag;
          },
        },
        {
          name: "GetKeyGroup",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const id = (ctx as Record<string, unknown>)["_kgId"] as string;
            assert.ok(id, "GetKeyGroup: no key group from Create");
            const resp = await cloudfront.send(
              new GetKeyGroupCommand({ Id: id }),
            );
            assert.ok(resp.KeyGroup?.Id, "GetKeyGroup: missing Id");
          },
        },
        {
          name: "GetKeyGroupConfig",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const id = (ctx as Record<string, unknown>)["_kgId"] as string;
            assert.ok(id, "GetKeyGroupConfig: no key group from Create");
            const resp = await cloudfront.send(
              new GetKeyGroupConfigCommand({ Id: id }),
            );
            assert.ok(resp.KeyGroupConfig, "GetKeyGroupConfig: missing config");
          },
        },
        {
          name: "UpdateKeyGroup",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const id = (ctx as Record<string, unknown>)["_kgId"] as string;
            const etag = (ctx as Record<string, unknown>)["_kgEtag"] as string;
            assert.ok(id, "UpdateKeyGroup: no key group from Create");
            const resp = await cloudfront.send(
              new UpdateKeyGroupCommand({
                Id: id,
                IfMatch: etag,
                KeyGroupConfig: {
                  Name: `oc-kg-${ctx.runId}`,
                  Comment: "updated",
                  Items: ["K1234567890ABCDE"],
                },
              }),
            );
            assert.ok(resp.KeyGroup?.Id, "UpdateKeyGroup: missing Id");
            (ctx as Record<string, unknown>)["_kgEtag"] = resp.ETag;
          },
        },
        {
          name: "ListKeyGroups",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const resp = await cloudfront.send(new ListKeyGroupsCommand({}));
            assert.ok(resp.KeyGroupList, "ListKeyGroups: missing list");
          },
        },
        {
          name: "DeleteKeyGroup",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const id = (ctx as Record<string, unknown>)["_kgId"] as string;
            const etag = (ctx as Record<string, unknown>)["_kgEtag"] as string;
            if (!id || !etag) return;
            await cloudfront.send(
              new DeleteKeyGroupCommand({ Id: id, IfMatch: etag }),
            );
            (ctx as Record<string, unknown>)["_kgId"] = undefined;
          },
        },
      ],
      teardown: async (ctx) => {
        const { cloudfront } = makeClients(ctx);
        const id = (ctx as Record<string, unknown>)["_kgId"] as string;
        if (!id) return;
        try {
          const get = await cloudfront.send(new GetKeyGroupCommand({ Id: id }));
          if (get.ETag) {
            await cloudfront.send(
              new DeleteKeyGroupCommand({ Id: id, IfMatch: get.ETag }),
            );
          }
        } catch {}
      },
    },

    // ── cloudfront-realtime-log ────────────────────────────────────────────
    {
      suite,
      service: "cloudfront",
      name: "cloudfront-realtime-log",
      tests: [
        {
          name: "CreateRealtimeLogConfig",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const name = `oc-rlc-${ctx.runId}`;
            const resp = await cloudfront.send(
              new CreateRealtimeLogConfigCommand({
                Name: name,
                SamplingRate: 100,
                EndPoints: [
                  {
                    StreamType: "Kinesis",
                    KinesisStreamConfig: {
                      RoleARN: "arn:aws:iam::000000000000:role/test",
                      StreamARN:
                        "arn:aws:kinesis:us-east-1:000000000000:stream/test",
                    },
                  },
                ],
                Fields: ["timestamp", "c-ip"],
              }),
            );
            assert.ok(
              resp.RealtimeLogConfig?.ARN,
              "CreateRealtimeLogConfig: missing ARN",
            );
            (ctx as Record<string, unknown>)["_rlcName"] = name;
            (ctx as Record<string, unknown>)["_rlcArn"] =
              resp.RealtimeLogConfig.ARN;
          },
        },
        {
          name: "GetRealtimeLogConfig",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const name = (ctx as Record<string, unknown>)["_rlcName"] as string;
            assert.ok(name, "GetRealtimeLogConfig: no config from Create");
            const resp = await cloudfront.send(
              new GetRealtimeLogConfigCommand({ Name: name }),
            );
            assert.ok(
              resp.RealtimeLogConfig?.ARN,
              "GetRealtimeLogConfig: missing ARN",
            );
          },
        },
        {
          name: "UpdateRealtimeLogConfig",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const name = (ctx as Record<string, unknown>)["_rlcName"] as string;
            const arn = (ctx as Record<string, unknown>)["_rlcArn"] as string;
            assert.ok(name, "UpdateRealtimeLogConfig: no config from Create");
            const resp = await cloudfront.send(
              new UpdateRealtimeLogConfigCommand({
                Name: name,
                ARN: arn,
                SamplingRate: 50,
                EndPoints: [
                  {
                    StreamType: "Kinesis",
                    KinesisStreamConfig: {
                      RoleARN: "arn:aws:iam::000000000000:role/test",
                      StreamARN:
                        "arn:aws:kinesis:us-east-1:000000000000:stream/test",
                    },
                  },
                ],
                Fields: ["timestamp", "c-ip", "sc-status"],
              }),
            );
            assert.ok(
              resp.RealtimeLogConfig?.ARN,
              "UpdateRealtimeLogConfig: missing ARN",
            );
          },
        },
        {
          name: "ListRealtimeLogConfigs",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const resp = await cloudfront.send(
              new ListRealtimeLogConfigsCommand({}),
            );
            assert.ok(
              resp.RealtimeLogConfigs,
              "ListRealtimeLogConfigs: missing list",
            );
          },
        },
        {
          name: "DeleteRealtimeLogConfig",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const name = (ctx as Record<string, unknown>)["_rlcName"] as string;
            if (!name) return;
            await cloudfront.send(
              new DeleteRealtimeLogConfigCommand({ Name: name }),
            );
            (ctx as Record<string, unknown>)["_rlcName"] = undefined;
          },
        },
      ],
      teardown: async (ctx) => {
        const { cloudfront } = makeClients(ctx);
        const name = (ctx as Record<string, unknown>)["_rlcName"] as string;
        if (!name) return;
        try {
          await cloudfront.send(
            new DeleteRealtimeLogConfigCommand({ Name: name }),
          );
        } catch {}
      },
    },

    // ── cloudfront-monitoring ──────────────────────────────────────────────
    {
      suite,
      service: "cloudfront",
      name: "cloudfront-monitoring",
      setup: async (ctx) => {
        const { cloudfront } = makeClients(ctx);
        const resp = await cloudfront.send(
          new CreateDistributionCommand({
            DistributionConfig: {
              CallerReference: `compat-mon-${ctx.runId}`,
              Comment: "compat monitoring test distribution",
              Enabled: true,
              Origins: {
                Quantity: 1,
                Items: [
                  {
                    Id: "origin-1",
                    DomainName: "example.com",
                    S3OriginConfig: { OriginAccessIdentity: "" },
                  },
                ],
              },
              DefaultCacheBehavior: {
                TargetOriginId: "origin-1",
                ViewerProtocolPolicy: "redirect-to-https",
                ForwardedValues: {
                  QueryString: false,
                  Cookies: { Forward: "none" },
                },
                MinTTL: 0,
                TrustedSigners: { Enabled: false, Quantity: 0 },
              },
            },
          }),
        );
        (ctx as Record<string, unknown>)["_monDistId"] = resp.Distribution?.Id;
      },
      tests: [
        {
          name: "CreateMonitoringSubscription",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const distId = (ctx as Record<string, unknown>)[
              "_monDistId"
            ] as string;
            assert.ok(
              distId,
              "CreateMonitoringSubscription: no distribution from setup",
            );
            await cloudfront.send(
              new CreateMonitoringSubscriptionCommand({
                DistributionId: distId,
                MonitoringSubscription: {
                  RealtimeMetricsSubscriptionConfig: {
                    RealtimeMetricsSubscriptionStatus: "Enabled",
                  },
                },
              }),
            );
          },
        },
        {
          name: "GetMonitoringSubscription",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const distId = (ctx as Record<string, unknown>)[
              "_monDistId"
            ] as string;
            assert.ok(
              distId,
              "GetMonitoringSubscription: no distribution from setup",
            );
            const resp = await cloudfront.send(
              new GetMonitoringSubscriptionCommand({
                DistributionId: distId,
              }),
            );
            assert.ok(
              resp.MonitoringSubscription,
              "GetMonitoringSubscription: missing subscription",
            );
          },
        },
        {
          name: "DeleteMonitoringSubscription",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const distId = (ctx as Record<string, unknown>)[
              "_monDistId"
            ] as string;
            if (!distId) return;
            await cloudfront.send(
              new DeleteMonitoringSubscriptionCommand({
                DistributionId: distId,
              }),
            );
          },
        },
      ],
      teardown: async (ctx) => {
        const { cloudfront } = makeClients(ctx);
        const id = (ctx as Record<string, unknown>)["_monDistId"] as string;
        if (!id) return;
        try {
          const get = await cloudfront.send(
            new GetDistributionCommand({ Id: id }),
          );
          const config = get.Distribution?.DistributionConfig;
          const etag = get.ETag;
          if (config && etag) {
            config.Enabled = false;
            const update = await cloudfront.send(
              new UpdateDistributionCommand({
                Id: id,
                IfMatch: etag,
                DistributionConfig: config,
              }),
            );
            if (update.ETag) {
              await cloudfront.send(
                new DeleteDistributionCommand({
                  Id: id,
                  IfMatch: update.ETag,
                }),
              );
            }
          }
        } catch {}
      },
    },

    // ── cloudfront-fle-config ─────────────────────────────────────────────
    {
      suite,
      service: "cloudfront",
      name: "cloudfront-fle-config",
      tests: [
        {
          name: "CreateFieldLevelEncryptionConfig",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const resp = await cloudfront.send(
              new CreateFieldLevelEncryptionConfigCommand({
                FieldLevelEncryptionConfig: {
                  CallerReference: `oc-fle-${ctx.runId}`,
                  Comment: "compat test",
                },
              }),
            );
            assert.ok(
              resp.FieldLevelEncryption?.Id,
              "CreateFieldLevelEncryptionConfig: missing Id",
            );
            (ctx as Record<string, unknown>)["_fleId"] =
              resp.FieldLevelEncryption.Id;
            (ctx as Record<string, unknown>)["_fleEtag"] = resp.ETag;
          },
        },
        {
          name: "GetFieldLevelEncryption",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const id = (ctx as Record<string, unknown>)["_fleId"] as string;
            assert.ok(id, "GetFieldLevelEncryption: no config from Create");
            const resp = await cloudfront.send(
              new GetFieldLevelEncryptionCommand({ Id: id }),
            );
            assert.ok(
              resp.FieldLevelEncryption?.Id,
              "GetFieldLevelEncryption: missing Id",
            );
          },
        },
        {
          name: "GetFieldLevelEncryptionConfig",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const id = (ctx as Record<string, unknown>)["_fleId"] as string;
            assert.ok(
              id,
              "GetFieldLevelEncryptionConfig: no config from Create",
            );
            const resp = await cloudfront.send(
              new GetFieldLevelEncryptionConfigCommand({ Id: id }),
            );
            assert.ok(
              resp.FieldLevelEncryptionConfig,
              "GetFieldLevelEncryptionConfig: missing config",
            );
          },
        },
        {
          name: "UpdateFieldLevelEncryptionConfig",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const id = (ctx as Record<string, unknown>)["_fleId"] as string;
            const etag = (ctx as Record<string, unknown>)["_fleEtag"] as string;
            assert.ok(
              id,
              "UpdateFieldLevelEncryptionConfig: no config from Create",
            );
            const resp = await cloudfront.send(
              new UpdateFieldLevelEncryptionConfigCommand({
                Id: id,
                IfMatch: etag,
                FieldLevelEncryptionConfig: {
                  CallerReference: `oc-fle-${ctx.runId}`,
                  Comment: "compat test updated",
                },
              }),
            );
            assert.ok(
              resp.FieldLevelEncryption?.Id,
              "UpdateFieldLevelEncryptionConfig: missing Id",
            );
            (ctx as Record<string, unknown>)["_fleEtag"] = resp.ETag;
          },
        },
        {
          name: "ListFieldLevelEncryptionConfigs",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const resp = await cloudfront.send(
              new ListFieldLevelEncryptionConfigsCommand({}),
            );
            assert.ok(
              resp.FieldLevelEncryptionList,
              "ListFieldLevelEncryptionConfigs: missing list",
            );
          },
        },
        {
          name: "DeleteFieldLevelEncryption",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const id = (ctx as Record<string, unknown>)["_fleId"] as string;
            const etag = (ctx as Record<string, unknown>)["_fleEtag"] as string;
            if (!id || !etag) return;
            await cloudfront.send(
              new DeleteFieldLevelEncryptionConfigCommand({
                Id: id,
                IfMatch: etag,
              }),
            );
            (ctx as Record<string, unknown>)["_fleId"] = undefined;
          },
        },
      ],
      teardown: async (ctx) => {
        const { cloudfront } = makeClients(ctx);
        const id = (ctx as Record<string, unknown>)["_fleId"] as string;
        if (!id) return;
        try {
          const get = await cloudfront.send(
            new GetFieldLevelEncryptionCommand({ Id: id }),
          );
          if (get.ETag) {
            await cloudfront.send(
              new DeleteFieldLevelEncryptionConfigCommand({
                Id: id,
                IfMatch: get.ETag,
              }),
            );
          }
        } catch {}
      },
    },

    // ── cloudfront-fle-profile ────────────────────────────────────────────
    {
      suite,
      service: "cloudfront",
      name: "cloudfront-fle-profile",
      tests: [
        {
          name: "CreateFieldLevelEncryptionProfile",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const resp = await cloudfront.send(
              new CreateFieldLevelEncryptionProfileCommand({
                FieldLevelEncryptionProfileConfig: {
                  CallerReference: `oc-flep-${ctx.runId}`,
                  Name: `oc-flep-${ctx.runId}`,
                  Comment: "compat test",
                  EncryptionEntities: { Quantity: 0, Items: [] },
                },
              }),
            );
            assert.ok(
              resp.FieldLevelEncryptionProfile?.Id,
              "CreateFieldLevelEncryptionProfile: missing Id",
            );
            (ctx as Record<string, unknown>)["_flepId"] =
              resp.FieldLevelEncryptionProfile.Id;
            (ctx as Record<string, unknown>)["_flepEtag"] = resp.ETag;
          },
        },
        {
          name: "GetFieldLevelEncryptionProfile",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const id = (ctx as Record<string, unknown>)["_flepId"] as string;
            assert.ok(
              id,
              "GetFieldLevelEncryptionProfile: no profile from Create",
            );
            const resp = await cloudfront.send(
              new GetFieldLevelEncryptionProfileCommand({ Id: id }),
            );
            assert.ok(
              resp.FieldLevelEncryptionProfile?.Id,
              "GetFieldLevelEncryptionProfile: missing Id",
            );
          },
        },
        {
          name: "GetFieldLevelEncryptionProfileConfig",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const id = (ctx as Record<string, unknown>)["_flepId"] as string;
            assert.ok(
              id,
              "GetFieldLevelEncryptionProfileConfig: no profile from Create",
            );
            const resp = await cloudfront.send(
              new GetFieldLevelEncryptionProfileConfigCommand({ Id: id }),
            );
            assert.ok(
              resp.FieldLevelEncryptionProfileConfig,
              "GetFieldLevelEncryptionProfileConfig: missing config",
            );
          },
        },
        {
          name: "UpdateFieldLevelEncryptionProfile",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const id = (ctx as Record<string, unknown>)["_flepId"] as string;
            const etag = (ctx as Record<string, unknown>)[
              "_flepEtag"
            ] as string;
            assert.ok(
              id,
              "UpdateFieldLevelEncryptionProfile: no profile from Create",
            );
            const resp = await cloudfront.send(
              new UpdateFieldLevelEncryptionProfileCommand({
                Id: id,
                IfMatch: etag,
                FieldLevelEncryptionProfileConfig: {
                  CallerReference: `oc-flep-${ctx.runId}`,
                  Name: `oc-flep-${ctx.runId}`,
                  Comment: "compat test updated",
                  EncryptionEntities: { Quantity: 0, Items: [] },
                },
              }),
            );
            assert.ok(
              resp.FieldLevelEncryptionProfile?.Id,
              "UpdateFieldLevelEncryptionProfile: missing Id",
            );
            (ctx as Record<string, unknown>)["_flepEtag"] = resp.ETag;
          },
        },
        {
          name: "ListFieldLevelEncryptionProfiles",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const resp = await cloudfront.send(
              new ListFieldLevelEncryptionProfilesCommand({}),
            );
            assert.ok(
              resp.FieldLevelEncryptionProfileList,
              "ListFieldLevelEncryptionProfiles: missing list",
            );
          },
        },
        {
          name: "DeleteFieldLevelEncryptionProfile",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const id = (ctx as Record<string, unknown>)["_flepId"] as string;
            const etag = (ctx as Record<string, unknown>)[
              "_flepEtag"
            ] as string;
            if (!id || !etag) return;
            await cloudfront.send(
              new DeleteFieldLevelEncryptionProfileCommand({
                Id: id,
                IfMatch: etag,
              }),
            );
            (ctx as Record<string, unknown>)["_flepId"] = undefined;
          },
        },
      ],
      teardown: async (ctx) => {
        const { cloudfront } = makeClients(ctx);
        const id = (ctx as Record<string, unknown>)["_flepId"] as string;
        if (!id) return;
        try {
          const get = await cloudfront.send(
            new GetFieldLevelEncryptionProfileCommand({ Id: id }),
          );
          if (get.ETag) {
            await cloudfront.send(
              new DeleteFieldLevelEncryptionProfileCommand({
                Id: id,
                IfMatch: get.ETag,
              }),
            );
          }
        } catch {}
      },
    },

    // ── cloudfront-continuous-deployment ─────────────────────────────────
    {
      suite,
      service: "cloudfront",
      name: "cloudfront-continuous-deployment",
      tests: [
        {
          name: "CreateContinuousDeploymentPolicy",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const resp = await cloudfront.send(
              new CreateContinuousDeploymentPolicyCommand({
                ContinuousDeploymentPolicyConfig: {
                  StagingDistributionDnsNames: {
                    Quantity: 1,
                    Items: ["d1234.cloudfront.net"],
                  },
                  Enabled: true,
                },
              }),
            );
            assert.ok(
              resp.ContinuousDeploymentPolicy?.Id,
              "CreateContinuousDeploymentPolicy: missing Id",
            );
            (ctx as Record<string, unknown>)["_cdpId"] =
              resp.ContinuousDeploymentPolicy.Id;
            (ctx as Record<string, unknown>)["_cdpEtag"] = resp.ETag;
          },
        },
        {
          name: "GetContinuousDeploymentPolicy",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const id = (ctx as Record<string, unknown>)["_cdpId"] as string;
            assert.ok(
              id,
              "GetContinuousDeploymentPolicy: no policy from Create",
            );
            const resp = await cloudfront.send(
              new GetContinuousDeploymentPolicyCommand({ Id: id }),
            );
            assert.ok(
              resp.ContinuousDeploymentPolicy?.Id,
              "GetContinuousDeploymentPolicy: missing Id",
            );
          },
        },
        {
          name: "GetContinuousDeploymentPolicyConfig",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const id = (ctx as Record<string, unknown>)["_cdpId"] as string;
            assert.ok(
              id,
              "GetContinuousDeploymentPolicyConfig: no policy from Create",
            );
            const resp = await cloudfront.send(
              new GetContinuousDeploymentPolicyConfigCommand({ Id: id }),
            );
            assert.ok(
              resp.ContinuousDeploymentPolicyConfig,
              "GetContinuousDeploymentPolicyConfig: missing config",
            );
          },
        },
        {
          name: "UpdateContinuousDeploymentPolicy",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const id = (ctx as Record<string, unknown>)["_cdpId"] as string;
            const etag = (ctx as Record<string, unknown>)["_cdpEtag"] as string;
            assert.ok(
              id,
              "UpdateContinuousDeploymentPolicy: no policy from Create",
            );
            const resp = await cloudfront.send(
              new UpdateContinuousDeploymentPolicyCommand({
                Id: id,
                IfMatch: etag,
                ContinuousDeploymentPolicyConfig: {
                  StagingDistributionDnsNames: {
                    Quantity: 1,
                    Items: ["d5678.cloudfront.net"],
                  },
                  Enabled: false,
                },
              }),
            );
            assert.ok(
              resp.ContinuousDeploymentPolicy?.Id,
              "UpdateContinuousDeploymentPolicy: missing Id",
            );
            (ctx as Record<string, unknown>)["_cdpEtag"] = resp.ETag;
          },
        },
        {
          name: "ListContinuousDeploymentPolicies",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const resp = await cloudfront.send(
              new ListContinuousDeploymentPoliciesCommand({}),
            );
            assert.ok(
              resp.ContinuousDeploymentPolicyList,
              "ListContinuousDeploymentPolicies: missing list",
            );
          },
        },
        {
          name: "DeleteContinuousDeploymentPolicy",
          fn: async (ctx) => {
            const { cloudfront } = makeClients(ctx);
            const id = (ctx as Record<string, unknown>)["_cdpId"] as string;
            const etag = (ctx as Record<string, unknown>)["_cdpEtag"] as string;
            if (!id || !etag) return;
            await cloudfront.send(
              new DeleteContinuousDeploymentPolicyCommand({
                Id: id,
                IfMatch: etag,
              }),
            );
            (ctx as Record<string, unknown>)["_cdpId"] = undefined;
          },
        },
      ],
      teardown: async (ctx) => {
        const { cloudfront } = makeClients(ctx);
        const id = (ctx as Record<string, unknown>)["_cdpId"] as string;
        if (!id) return;
        try {
          const get = await cloudfront.send(
            new GetContinuousDeploymentPolicyCommand({ Id: id }),
          );
          if (get.ETag) {
            await cloudfront.send(
              new DeleteContinuousDeploymentPolicyCommand({
                Id: id,
                IfMatch: get.ETag,
              }),
            );
          }
        } catch {}
      },
    },
  ];
}
