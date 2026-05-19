/**
 * groups/elasticache.ts — ElastiCache compatibility test groups for the Node.js suite.
 *
 * Groups:
 *   elasticache-clusters           — Cache cluster lifecycle (create, describe, delete)
 *   elasticache-replication-groups — Replication group lifecycle
 *   elasticache-subnet-groups      — Cache subnet group lifecycle
 *   elasticache-parameter-groups   — Cache parameter group lifecycle
 */

import {
  CreateCacheClusterCommand,
  DescribeCacheClustersCommand,
  DeleteCacheClusterCommand,
  ModifyCacheClusterCommand,
  CreateReplicationGroupCommand,
  DescribeReplicationGroupsCommand,
  DeleteReplicationGroupCommand,
  ModifyReplicationGroupCommand,
  CreateCacheSubnetGroupCommand,
  DescribeCacheSubnetGroupsCommand,
  DeleteCacheSubnetGroupCommand,
  CreateCacheParameterGroupCommand,
  DescribeCacheParameterGroupsCommand,
  DeleteCacheParameterGroupCommand,
} from "@aws-sdk/client-elasticache";
import { makeClients } from "../lib/clients.js";
import type { TestGroup } from "../lib/harness.js";
import * as assert from "node:assert/strict";

export function makeElastiCacheGroups(suite: string): TestGroup[] {
  return [
    {
      name: "elasticache-clusters",
      service: "elasticache",
      suite,
      tests: [
        {
          name: "CreateCacheCluster",
          fn: async (ctx) => {
            const { elasticache } = makeClients(ctx);
            const id = `compat-cluster-${ctx.runId}`;
            (ctx as Record<string, unknown>)["_clusterId"] = id;
            const res = await elasticache.send(
              new CreateCacheClusterCommand({
                CacheClusterId: id,
                Engine: "redis",
                CacheNodeType: "cache.t3.micro",
                NumCacheNodes: 1,
              }),
            );
            assert.ok(res.CacheCluster?.CacheClusterId === id);
            assert.ok(res.CacheCluster?.CacheClusterStatus === "creating" || res.CacheCluster?.CacheClusterStatus === "available");
          },
        },
        {
          name: "DescribeCacheClusters",
          fn: async (ctx) => {
            const { elasticache } = makeClients(ctx);
            const id = `compat-describe-${ctx.runId}`;
            (ctx as Record<string, unknown>)["_describeClusterId"] = id;
            await elasticache.send(
              new CreateCacheClusterCommand({
                CacheClusterId: id,
                Engine: "redis",
                CacheNodeType: "cache.t3.micro",
                NumCacheNodes: 1,
              }),
            );
            const res = await elasticache.send(
              new DescribeCacheClustersCommand({ CacheClusterId: id }),
            );
            const clusters = res.CacheClusters ?? [];
            assert.ok(clusters.length === 1);
            assert.equal(clusters[0].CacheClusterId, id);
          },
        },
        {
          name: "DeleteCacheCluster",
          fn: async (ctx) => {
            const { elasticache } = makeClients(ctx);
            const id = `compat-delete-${ctx.runId}`;
            (ctx as Record<string, unknown>)["_deleteClusterId"] = id;
            await elasticache.send(
              new CreateCacheClusterCommand({
                CacheClusterId: id,
                Engine: "redis",
                CacheNodeType: "cache.t3.micro",
                NumCacheNodes: 1,
              }),
            );
            const res = await elasticache.send(
              new DeleteCacheClusterCommand({ CacheClusterId: id }),
            );
            assert.equal(res.CacheCluster?.CacheClusterStatus, "deleting");
          },
        },
      ],
      teardown: async (ctx) => {
        const { elasticache } = makeClients(ctx);
        for (const key of ["_clusterId", "_describeClusterId", "_deleteClusterId"]) {
          const id = (ctx as Record<string, unknown>)[key] as string | undefined;
          if (id) {
            try { await elasticache.send(new DeleteCacheClusterCommand({ CacheClusterId: id })); } catch {}
          }
        }
      },
    },

    {
      name: "elasticache-modify",
      service: "elasticache",
      suite,
      tests: [
        {
          name: "ModifyCacheCluster",
          fn: async (ctx) => {
            const { elasticache } = makeClients(ctx);
            const id = `compat-modify-cluster-${ctx.runId}`;
            (ctx as Record<string, unknown>)["_modifyClusterId"] = id;
            await elasticache.send(
              new CreateCacheClusterCommand({
                CacheClusterId: id,
                Engine: "redis",
                CacheNodeType: "cache.t3.micro",
                NumCacheNodes: 1,
              }),
            );
            const res = await elasticache.send(
              new ModifyCacheClusterCommand({
                CacheClusterId: id,
                CacheNodeType: "cache.t3.small",
                ApplyImmediately: true,
              }),
            );
            assert.equal(res.CacheCluster?.CacheClusterId, id);
            assert.equal(res.CacheCluster?.CacheClusterStatus, "modifying");
            assert.equal(res.CacheCluster?.CacheNodeType, "cache.t3.small");
          },
        },
        {
          name: "ModifyReplicationGroup",
          fn: async (ctx) => {
            const { elasticache } = makeClients(ctx);
            const id = `compat-modify-rg-${ctx.runId}`;
            (ctx as Record<string, unknown>)["_modifyRgId"] = id;
            await elasticache.send(
              new CreateReplicationGroupCommand({
                ReplicationGroupId: id,
                ReplicationGroupDescription: "original",
                CacheNodeType: "cache.t3.micro",
              }),
            );
            const res = await elasticache.send(
              new ModifyReplicationGroupCommand({
                ReplicationGroupId: id,
                ReplicationGroupDescription: "updated",
                ApplyImmediately: true,
              }),
            );
            assert.equal(res.ReplicationGroup?.ReplicationGroupId, id);
            assert.equal(res.ReplicationGroup?.Status, "modifying");
            assert.equal(res.ReplicationGroup?.Description, "updated");
          },
        },
      ],
      teardown: async (ctx) => {
        const { elasticache } = makeClients(ctx);
        const ctx_ = ctx as Record<string, unknown>;
        if (ctx_["_modifyClusterId"]) {
          try { await elasticache.send(new DeleteCacheClusterCommand({ CacheClusterId: ctx_["_modifyClusterId"] as string })); } catch {}
        }
        if (ctx_["_modifyRgId"]) {
          try { await elasticache.send(new DeleteReplicationGroupCommand({ ReplicationGroupId: ctx_["_modifyRgId"] as string })); } catch {}
        }
      },
    },

    {
      name: "elasticache-replication-groups",
      service: "elasticache",
      suite,
      tests: [
        {
          name: "CreateReplicationGroup",
          fn: async (ctx) => {
            const { elasticache } = makeClients(ctx);
            const id = `compat-rg-${ctx.runId}`;
            (ctx as Record<string, unknown>)["_rgId"] = id;
            const res = await elasticache.send(
              new CreateReplicationGroupCommand({
                ReplicationGroupId: id,
                ReplicationGroupDescription: "compat test group",
                CacheNodeType: "cache.t3.micro",
              }),
            );
            assert.ok(res.ReplicationGroup?.ReplicationGroupId === id);
            assert.ok(
              res.ReplicationGroup?.Status === "creating" ||
                res.ReplicationGroup?.Status === "available",
            );
          },
        },
        {
          name: "DescribeReplicationGroups",
          fn: async (ctx) => {
            const { elasticache } = makeClients(ctx);
            const id = `compat-rg-desc-${ctx.runId}`;
            (ctx as Record<string, unknown>)["_rgDescId"] = id;
            await elasticache.send(
              new CreateReplicationGroupCommand({
                ReplicationGroupId: id,
                ReplicationGroupDescription: "compat describe",
              }),
            );
            const res = await elasticache.send(
              new DescribeReplicationGroupsCommand({ ReplicationGroupId: id }),
            );
            const groups = res.ReplicationGroups ?? [];
            assert.equal(groups.length, 1);
            assert.equal(groups[0].ReplicationGroupId, id);
          },
        },
      ],
      teardown: async (ctx) => {
        const { elasticache } = makeClients(ctx);
        const ctx_ = ctx as Record<string, unknown>;
        for (const key of ["_rgId", "_rgDescId"]) {
          const id = ctx_[key] as string | undefined;
          if (id) {
            try { await elasticache.send(new DeleteReplicationGroupCommand({ ReplicationGroupId: id })); } catch {}
          }
        }
      },
    },

    {
      name: "elasticache-subnet-groups",
      service: "elasticache",
      suite,
      tests: [
        {
          name: "CreateCacheSubnetGroup",
          fn: async (ctx) => {
            const { elasticache } = makeClients(ctx);
            const name = `compat-sngrp-${ctx.runId}`;
            (ctx as Record<string, unknown>)["_sngrpName"] = name;
            const res = await elasticache.send(
              new CreateCacheSubnetGroupCommand({
                CacheSubnetGroupName: name,
                CacheSubnetGroupDescription: "compat test",
                SubnetIds: ["subnet-aabbccdd"],
              }),
            );
            assert.equal(res.CacheSubnetGroup?.CacheSubnetGroupName, name);
          },
        },
        {
          name: "DescribeCacheSubnetGroups",
          fn: async (ctx) => {
            const { elasticache } = makeClients(ctx);
            const name = `compat-sngrp-desc-${ctx.runId}`;
            (ctx as Record<string, unknown>)["_sngrpDescName"] = name;
            await elasticache.send(
              new CreateCacheSubnetGroupCommand({
                CacheSubnetGroupName: name,
                CacheSubnetGroupDescription: "describe test",
                SubnetIds: ["subnet-11223344"],
              }),
            );
            const res = await elasticache.send(
              new DescribeCacheSubnetGroupsCommand({ CacheSubnetGroupName: name }),
            );
            const groups = res.CacheSubnetGroups ?? [];
            assert.equal(groups.length, 1);
            assert.equal(groups[0].CacheSubnetGroupName, name);
          },
        },
      ],
      teardown: async (ctx) => {
        const { elasticache } = makeClients(ctx);
        const ctx_ = ctx as Record<string, unknown>;
        for (const key of ["_sngrpName", "_sngrpDescName"]) {
          const name = ctx_[key] as string | undefined;
          if (name) {
            try { await elasticache.send(new DeleteCacheSubnetGroupCommand({ CacheSubnetGroupName: name })); } catch {}
          }
        }
      },
    },

    {
      name: "elasticache-parameter-groups",
      service: "elasticache",
      suite,
      tests: [
        {
          name: "CreateCacheParameterGroup",
          fn: async (ctx) => {
            const { elasticache } = makeClients(ctx);
            const name = `compat-pg-${ctx.runId}`;
            (ctx as Record<string, unknown>)["_pgName"] = name;
            const res = await elasticache.send(
              new CreateCacheParameterGroupCommand({
                CacheParameterGroupName: name,
                CacheParameterGroupFamily: "redis7",
                Description: "compat test group",
              }),
            );
            assert.equal(res.CacheParameterGroup?.CacheParameterGroupName, name);
            assert.equal(res.CacheParameterGroup?.CacheParameterGroupFamily, "redis7");
            assert.ok(res.CacheParameterGroup?.ARN?.includes(name));
          },
        },
        {
          name: "DescribeCacheParameterGroups",
          fn: async (ctx) => {
            const { elasticache } = makeClients(ctx);
            const name = `compat-pg-desc-${ctx.runId}`;
            (ctx as Record<string, unknown>)["_pgDescName"] = name;
            await elasticache.send(
              new CreateCacheParameterGroupCommand({
                CacheParameterGroupName: name,
                CacheParameterGroupFamily: "redis7",
                Description: "describe test",
              }),
            );
            const res = await elasticache.send(
              new DescribeCacheParameterGroupsCommand({ CacheParameterGroupName: name }),
            );
            const groups = res.CacheParameterGroups ?? [];
            assert.equal(groups.length, 1);
            assert.equal(groups[0].CacheParameterGroupName, name);
            assert.equal(groups[0].CacheParameterGroupFamily, "redis7");
          },
        },
        {
          name: "DeleteCacheParameterGroup",
          fn: async (ctx) => {
            const { elasticache } = makeClients(ctx);
            const name = `compat-pg-del-${ctx.runId}`;
            (ctx as Record<string, unknown>)["_pgDelName"] = name;
            await elasticache.send(
              new CreateCacheParameterGroupCommand({
                CacheParameterGroupName: name,
                CacheParameterGroupFamily: "redis7",
                Description: "delete test",
              }),
            );
            await elasticache.send(new DeleteCacheParameterGroupCommand({ CacheParameterGroupName: name }));
            await assert.rejects(
              elasticache.send(new DescribeCacheParameterGroupsCommand({ CacheParameterGroupName: name })),
              /CacheParameterGroupNotFound/,
            );
          },
        },
      ],
      teardown: async (ctx) => {
        const { elasticache } = makeClients(ctx);
        const ctx_ = ctx as Record<string, unknown>;
        for (const key of ["_pgName", "_pgDescName", "_pgDelName"]) {
          const name = ctx_[key] as string | undefined;
          if (name) {
            try { await elasticache.send(new DeleteCacheParameterGroupCommand({ CacheParameterGroupName: name })); } catch {}
          }
        }
      },
    },
  ];
}
