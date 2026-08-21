/**
 * groups/rds.ts — RDS and Aurora compatibility test groups for the Node.js suite.
 *
 * Groups:
 *   rds-instances        — DB instance lifecycle (create, describe, stop, start, modify, delete)
 *   rds-clusters         — Aurora DB cluster lifecycle (create, describe, modify, stop, start, delete)
 *   rds-cluster-members  — an Aurora cluster with an instance in it: DBClusterMembers and cluster-owned settings
 *   rds-subnet-groups    — DB subnet group lifecycle
 *   rds-parameter-groups — DB parameter group lifecycle
 */

import {
  CreateDBInstanceCommand,
  DeleteDBInstanceCommand,
  DescribeDBInstancesCommand,
  DescribeDBEngineVersionsCommand,
  StopDBInstanceCommand,
  StartDBInstanceCommand,
  ModifyDBInstanceCommand,
  RDSClient,
  CreateDBClusterCommand,
  DescribeDBClustersCommand,
  ModifyDBClusterCommand,
  StopDBClusterCommand,
  StartDBClusterCommand,
  DeleteDBClusterCommand,
  CreateDBSubnetGroupCommand,
  DescribeDBSubnetGroupsCommand,
  DeleteDBSubnetGroupCommand,
  CreateDBParameterGroupCommand,
  DescribeDBParameterGroupsCommand,
  DeleteDBParameterGroupCommand,
} from "@aws-sdk/client-rds";
import { makeClients } from "../lib/clients.ts";
import type { TestGroup } from "../lib/harness.ts";
import * as assert from "node:assert/strict";

// How long a DB instance is given to reach a target status, and how often it is
// asked. Shared with the go, python, java and cli suites, which wait for the
// same thing against the same emulator.
//
// A wall-clock budget rather than a count of attempts, because those two stop
// meaning the same thing the moment the machine is busy: "60 attempts, 500ms
// apart" is 30 seconds only while every DescribeDBInstances returns instantly.
// What is being waited for is a real database container's first boot, data
// directory initialisation included, and 30 seconds does not cover that on a
// loaded machine — this suite, go and java all failed here during a release run
// with five suites in flight, and passed run alone. The CLI suite already
// carried 120 seconds for exactly this reason; this is that number, made common.
const RDS_WAIT_BUDGET_MS = 120_000;
const RDS_POLL_INTERVAL_MS = 1_000;

/** Poll until the DB instance reaches `targetStatus`, or throw once the budget is spent. */
async function waitForDBStatus(
  rds: RDSClient,
  dbId: string,
  targetStatus: string,
  budgetMs = RDS_WAIT_BUDGET_MS,
): Promise<void> {
  const deadline = Date.now() + budgetMs;
  let last = "(none reported)";
  for (;;) {
    const resp = await rds.send(
      new DescribeDBInstancesCommand({ DBInstanceIdentifier: dbId }),
    );
    const status = resp.DBInstances?.[0]?.DBInstanceStatus;
    if (status) {
      last = status;
      if (status === targetStatus) return;
      // A failed instance will never reach any target, so spending the rest of
      // the budget on it only delays the report.
      if (status === "failed") {
        throw new Error(
          `DB instance ${dbId} went to "failed" while waiting for "${targetStatus}"`,
        );
      }
    }
    if (Date.now() >= deadline) {
      throw new Error(
        `DB instance ${dbId} did not reach "${targetStatus}" within ${budgetMs}ms (last status "${last}")`,
      );
    }
    await new Promise<void>((r) =>
      globalThis.setTimeout(r, RDS_POLL_INTERVAL_MS),
    );
  }
}

// ── rds-clusters helpers ─────────────────────────────────────────────────────
//
// Aurora DB clusters. A cluster is a logical record in Overcast — member
// instances are what start engine containers — so the waits below are for the
// emulator's own status transitions rather than for a database to boot. They
// still have to be waited for: StopDBCluster refuses a cluster that is not
// "available" and StartDBCluster refuses one that is not "stopped", here and on
// AWS, so going straight from create to stop asks for InvalidDBClusterStateFault.
//
// The poll interval is tighter than the instance one for the same reason: there
// is no container boot to wait on, only a scheduled transition, so a coarser
// interval would spend seconds observing something that had already happened.
const RDS_CLUSTER_POLL_INTERVAL_MS = 500;

const RDS_CLUSTER_ENGINE = "aurora-mysql";
const RDS_CLUSTER_MASTER_USERNAME = "admin";
const RDS_CLUSTER_MASTER_PASSWORD = "Password1!";
/**
 * The retention period ModifyDBCluster sets. Non-zero so it is distinguishable
 * from the create-time default, and echoed back by DescribeDBClusters so the
 * change can be observed rather than assumed.
 */
const RDS_CLUSTER_BACKUP_RETENTION = 7;

/**
 * Describe this group's cluster, failing with a message that names the cluster
 * when nothing comes back. Every assertion reads through it, so an empty
 * describe reports as a missing cluster rather than a property access on
 * undefined.
 */
async function describeCluster(rds: RDSClient, clusterId: string) {
  const resp = await rds.send(
    new DescribeDBClustersCommand({ DBClusterIdentifier: clusterId }),
  );
  const cluster = resp.DBClusters?.[0];
  assert.ok(cluster, `DescribeDBClusters: no cluster returned for ${clusterId}`);
  return cluster;
}

/** Poll until the DB cluster reaches `targetStatus`, or throw once the budget is spent. */
async function waitForClusterStatus(
  rds: RDSClient,
  clusterId: string,
  targetStatus: string,
  budgetMs = RDS_WAIT_BUDGET_MS,
): Promise<void> {
  const deadline = Date.now() + budgetMs;
  let last = "(none reported)";
  for (;;) {
    const resp = await rds.send(
      new DescribeDBClustersCommand({ DBClusterIdentifier: clusterId }),
    );
    const status = resp.DBClusters?.[0]?.Status;
    if (status) {
      last = status;
      if (status === targetStatus) return;
    }
    if (Date.now() >= deadline) {
      throw new Error(
        `DB cluster ${clusterId} did not reach "${targetStatus}" within ${budgetMs}ms (last status "${last}")`,
      );
    }
    await new Promise<void>((r) =>
      globalThis.setTimeout(r, RDS_CLUSTER_POLL_INTERVAL_MS),
    );
  }
}

/**
 * Poll the unfiltered DescribeDBClusters until this run's cluster is no longer
 * listed. Deletion is asynchronous — the call marks the cluster "deleting" and
 * the record goes shortly after — so absence is what has to be waited for. The
 * unfiltered form rather than a lookup by identifier, so the check does not
 * depend on which not-found shape the SDK surfaces.
 */
async function waitForClusterGone(
  rds: RDSClient,
  clusterId: string,
  budgetMs = RDS_WAIT_BUDGET_MS,
): Promise<void> {
  const deadline = Date.now() + budgetMs;
  for (;;) {
    const resp = await rds.send(new DescribeDBClustersCommand({}));
    const found = (resp.DBClusters ?? []).some(
      (c) => c.DBClusterIdentifier === clusterId,
    );
    if (!found) return;
    if (Date.now() >= deadline) {
      throw new Error(
        `DB cluster ${clusterId} still listed ${budgetMs}ms after DeleteDBCluster`,
      );
    }
    await new Promise<void>((r) =>
      globalThis.setTimeout(r, RDS_CLUSTER_POLL_INTERVAL_MS),
    );
  }
}

// ── rds-cluster-members helpers ──────────────────────────────────────────────
//
// An Aurora cluster with an instance in it. rds-clusters deliberately has none,
// which leaves two things nothing outside the emulator's own Go tests looks at:
// DBClusterMembers, and the settings a member is supposed to take from its
// cluster rather than from its own request.
//
// Kept apart from rds-clusters rather than bolted onto it: a member changes what
// the cluster endpoints mean — with a writer on record they can collapse onto
// one loopback address for a host caller, which is deliberate and would break
// that group's "the two endpoints differ" assertion — and a second group
// declaring CreateDBCluster would make the bare impl key for it ambiguous in
// every suite, which aborts the run.
const RDS_MEMBER_ENGINE = "aurora-mysql";
const RDS_MEMBER_USERNAME = "clusteradmin";
const RDS_MEMBER_PASSWORD = "Password1!";
const RDS_MEMBER_CLASS = "db.t3.micro";

/** The DBInstanceIdentifier of every member listed on a cluster. */
function clusterMemberIds(cluster: { DBClusterMembers?: { DBInstanceIdentifier?: string }[] }) {
  return (cluster.DBClusterMembers ?? []).map((m) => m.DBInstanceIdentifier);
}

export function makeRDSGroups(suite: string): TestGroup[] {
  return [
    // ── rds-instances ──────────────────────────────────────────────────────
    {
      suite,
      service: "rds",
      name: "rds-instances",
      tests: [
        {
          name: "DescribeDBEngineVersions",
          fn: async (ctx) => {
            const { rds } = makeClients(ctx);
            await rds.send(new DescribeDBEngineVersionsCommand({}));
          },
        },
        {
          name: "CreateDBInstance",
          fn: async (ctx) => {
            const { rds } = makeClients(ctx);
            const dbId = `compat-${ctx.runId}`;
            const resp = await rds.send(
              new CreateDBInstanceCommand({
                DBInstanceIdentifier: dbId,
                DBInstanceClass: "db.t3.micro",
                Engine: "mysql",
                MasterUsername: "admin",
                MasterUserPassword: "Password1!",
                AllocatedStorage: 20,
              }),
            );
            assert.ok(
              resp.DBInstance?.DBInstanceIdentifier,
              "CreateDBInstance: missing DBInstanceIdentifier",
            );
            (ctx as Record<string, unknown>)["_dbId"] = dbId;
          },
        },
        {
          name: "DescribeDBInstances",
          fn: async (ctx) => {
            const { rds } = makeClients(ctx);
            const resp = await rds.send(new DescribeDBInstancesCommand({}));
            assert.notStrictEqual(
              resp.DBInstances,
              undefined,
              "DescribeDBInstances: missing DBInstances",
            );
          },
        },
        {
          name: "StopDBInstance",
          fn: async (ctx) => {
            const { rds } = makeClients(ctx);
            const dbId = (ctx as Record<string, unknown>)["_dbId"] as string;
            assert.ok(dbId, "StopDBInstance: no DB from CreateDBInstance");
            await waitForDBStatus(rds, dbId, "available");
            const resp = await rds.send(
              new StopDBInstanceCommand({
                DBInstanceIdentifier: dbId,
              }),
            );
            assert.ok(
              resp.DBInstance?.DBInstanceStatus,
              "StopDBInstance: missing DBInstanceStatus",
            );
          },
        },
        {
          name: "StartDBInstance",
          fn: async (ctx) => {
            const { rds } = makeClients(ctx);
            const dbId = (ctx as Record<string, unknown>)["_dbId"] as string;
            assert.ok(dbId, "StartDBInstance: no DB from CreateDBInstance");
            await waitForDBStatus(rds, dbId, "stopped");
            const resp = await rds.send(
              new StartDBInstanceCommand({
                DBInstanceIdentifier: dbId,
              }),
            );
            assert.ok(
              resp.DBInstance?.DBInstanceStatus,
              "StartDBInstance: missing DBInstanceStatus",
            );
          },
        },
        {
          name: "ModifyDBInstance",
          fn: async (ctx) => {
            const { rds } = makeClients(ctx);
            const dbId = (ctx as Record<string, unknown>)["_dbId"] as string;
            assert.ok(dbId, "ModifyDBInstance: no DB from CreateDBInstance");
            const resp = await rds.send(
              new ModifyDBInstanceCommand({
                DBInstanceIdentifier: dbId,
                AllocatedStorage: 30,
              }),
            );
            assert.ok(
              resp.DBInstance?.DBInstanceIdentifier,
              "ModifyDBInstance: missing DBInstanceIdentifier",
            );
          },
        },
        {
          name: "DeleteDBInstance",
          fn: async (ctx) => {
            const { rds } = makeClients(ctx);
            const dbId = (ctx as Record<string, unknown>)["_dbId"] as string;
            if (!dbId) return;
            await rds.send(
              new DeleteDBInstanceCommand({
                DBInstanceIdentifier: dbId,
                SkipFinalSnapshot: true,
              }),
            );
          },
        },
      ],
      teardown: async (ctx) => {
        const { rds } = makeClients(ctx);
        const dbId = (ctx as Record<string, unknown>)["_dbId"] as string;
        if (!dbId) return;
        try {
          await rds.send(
            new DeleteDBInstanceCommand({
              DBInstanceIdentifier: dbId,
              SkipFinalSnapshot: true,
            }),
          );
        } catch {}
      },
    },

    // ── rds-clusters ───────────────────────────────────────────────────────
    {
      suite,
      service: "rds",
      name: "rds-clusters",
      tests: [
        {
          name: "CreateDBCluster",
          fn: async (ctx) => {
            const { rds } = makeClients(ctx);
            // Named apart from the instance group's resource so the two never
            // collide when both run against the same emulator.
            const clusterId = `compat-cluster-${ctx.runId}`;
            // Recorded before the call, not after it: a create that fails
            // partway still leaves something for teardown to remove.
            (ctx as Record<string, unknown>)["_clusterId"] = clusterId;
            const resp = await rds.send(
              new CreateDBClusterCommand({
                DBClusterIdentifier: clusterId,
                Engine: RDS_CLUSTER_ENGINE,
                MasterUsername: RDS_CLUSTER_MASTER_USERNAME,
                MasterUserPassword: RDS_CLUSTER_MASTER_PASSWORD,
              }),
            );
            assert.strictEqual(
              resp.DBCluster?.DBClusterIdentifier,
              clusterId,
              "CreateDBCluster: DBClusterIdentifier mismatch",
            );
            assert.ok(
              resp.DBCluster.DBClusterArn,
              "CreateDBCluster: missing DBClusterArn",
            );
          },
        },
        {
          name: "DescribeDBClusters",
          fn: async (ctx) => {
            const { rds } = makeClients(ctx);
            const clusterId = (ctx as Record<string, unknown>)[
              "_clusterId"
            ] as string;
            assert.ok(
              clusterId,
              "DescribeDBClusters: no cluster from CreateDBCluster",
            );
            const cluster = await describeCluster(rds, clusterId);
            assert.strictEqual(
              cluster.DBClusterIdentifier,
              clusterId,
              "DescribeDBClusters: DBClusterIdentifier mismatch",
            );
            assert.strictEqual(
              cluster.Engine,
              RDS_CLUSTER_ENGINE,
              "DescribeDBClusters: Engine mismatch",
            );
            assert.ok(cluster.Status, "DescribeDBClusters: missing Status");
            // The writer and reader endpoints are what makes a cluster a
            // cluster, and nothing outside the emulator's own Go tests looked
            // at them until this group existed. Both must be present and they
            // must differ — a reader endpoint collapsed onto the writer reads
            // as success everywhere else.
            // Both stay distinct only while the cluster has no writer member:
            // since #1076 a host caller that cannot resolve the names is given
            // the loopback address for each, deliberately. This group creates
            // no members, so it never reaches that case — one that adds an
            // instance would.
            assert.ok(cluster.Endpoint, "DescribeDBClusters: missing Endpoint");
            assert.ok(
              cluster.ReaderEndpoint,
              "DescribeDBClusters: missing ReaderEndpoint",
            );
            assert.notStrictEqual(
              cluster.Endpoint,
              cluster.ReaderEndpoint,
              "DescribeDBClusters: Endpoint and ReaderEndpoint are the same name",
            );
          },
        },
        {
          name: "ModifyDBCluster",
          fn: async (ctx) => {
            const { rds } = makeClients(ctx);
            const clusterId = (ctx as Record<string, unknown>)[
              "_clusterId"
            ] as string;
            assert.ok(
              clusterId,
              "ModifyDBCluster: no cluster from CreateDBCluster",
            );
            const resp = await rds.send(
              new ModifyDBClusterCommand({
                DBClusterIdentifier: clusterId,
                BackupRetentionPeriod: RDS_CLUSTER_BACKUP_RETENTION,
              }),
            );
            assert.strictEqual(
              resp.DBCluster?.BackupRetentionPeriod,
              RDS_CLUSTER_BACKUP_RETENTION,
              "ModifyDBCluster: BackupRetentionPeriod not applied in the response",
            );
            // Read it back rather than trusting the modify response to have
            // echoed a value it never stored.
            const after = await describeCluster(rds, clusterId);
            assert.strictEqual(
              after.BackupRetentionPeriod,
              RDS_CLUSTER_BACKUP_RETENTION,
              "ModifyDBCluster: BackupRetentionPeriod not persisted",
            );
          },
        },
        {
          name: "StopDBCluster",
          fn: async (ctx) => {
            const { rds } = makeClients(ctx);
            const clusterId = (ctx as Record<string, unknown>)[
              "_clusterId"
            ] as string;
            assert.ok(
              clusterId,
              "StopDBCluster: no cluster from CreateDBCluster",
            );
            // StopDBCluster requires an available cluster, here and on AWS.
            await waitForClusterStatus(rds, clusterId, "available");
            const resp = await rds.send(
              new StopDBClusterCommand({ DBClusterIdentifier: clusterId }),
            );
            assert.ok(resp.DBCluster?.Status, "StopDBCluster: missing Status");
            // The stop is this test's mutation, so this test is where it is
            // confirmed to have landed — not StartDBCluster's precondition wait.
            await waitForClusterStatus(rds, clusterId, "stopped");
          },
        },
        {
          name: "StartDBCluster",
          fn: async (ctx) => {
            const { rds } = makeClients(ctx);
            const clusterId = (ctx as Record<string, unknown>)[
              "_clusterId"
            ] as string;
            assert.ok(
              clusterId,
              "StartDBCluster: no cluster from CreateDBCluster",
            );
            await waitForClusterStatus(rds, clusterId, "stopped");
            const resp = await rds.send(
              new StartDBClusterCommand({ DBClusterIdentifier: clusterId }),
            );
            assert.ok(resp.DBCluster?.Status, "StartDBCluster: missing Status");
            await waitForClusterStatus(rds, clusterId, "available");
          },
        },
        {
          name: "DeleteDBCluster",
          fn: async (ctx) => {
            const { rds } = makeClients(ctx);
            const clusterId = (ctx as Record<string, unknown>)[
              "_clusterId"
            ] as string;
            assert.ok(
              clusterId,
              "DeleteDBCluster: no cluster from CreateDBCluster",
            );
            await rds.send(
              new DeleteDBClusterCommand({
                DBClusterIdentifier: clusterId,
                SkipFinalSnapshot: true,
              }),
            );
            await waitForClusterGone(rds, clusterId);
            (ctx as Record<string, unknown>)["_clusterId"] = undefined;
          },
        },
      ],
      teardown: async (ctx) => {
        const { rds } = makeClients(ctx);
        const clusterId = (ctx as Record<string, unknown>)[
          "_clusterId"
        ] as string;
        if (!clusterId) return;
        try {
          await rds.send(
            new DeleteDBClusterCommand({
              DBClusterIdentifier: clusterId,
              SkipFinalSnapshot: true,
            }),
          );
        } catch {}
      },
    },

    // ── rds-cluster-members ────────────────────────────────────────────────
    {
      suite,
      service: "rds",
      name: "rds-cluster-members",
      tests: [
        {
          name: "CreateClusterForMembers",
          fn: async (ctx) => {
            const { rds } = makeClients(ctx);
            const clusterId = `compat-members-${ctx.runId}`;
            (ctx as Record<string, unknown>)["_memberCluster"] = clusterId;
            const resp = await rds.send(
              new CreateDBClusterCommand({
                DBClusterIdentifier: clusterId,
                Engine: RDS_MEMBER_ENGINE,
                MasterUsername: RDS_MEMBER_USERNAME,
                MasterUserPassword: RDS_MEMBER_PASSWORD,
              }),
            );
            assert.ok(
              resp.DBCluster?.DBClusterArn,
              "CreateClusterForMembers: missing DBClusterArn",
            );
            // The engine version the cluster settled on is what the member must
            // inherit, so it is recorded here rather than assumed.
            (ctx as Record<string, unknown>)["_memberClusterEngineVersion"] =
              resp.DBCluster.EngineVersion;
          },
        },
        {
          name: "AddClusterMember",
          fn: async (ctx) => {
            const { rds } = makeClients(ctx);
            const clusterId = (ctx as Record<string, unknown>)[
              "_memberCluster"
            ] as string;
            assert.ok(
              clusterId,
              "AddClusterMember: no cluster from CreateClusterForMembers",
            );
            const instanceId = `compat-member-${ctx.runId}`;
            (ctx as Record<string, unknown>)["_memberInstance"] = instanceId;
            // No MasterUsername or MasterUserPassword: an Aurora member takes
            // both from its cluster, and AWS documents them as not applying here.
            const resp = await rds.send(
              new CreateDBInstanceCommand({
                DBInstanceIdentifier: instanceId,
                DBClusterIdentifier: clusterId,
                Engine: RDS_MEMBER_ENGINE,
                DBInstanceClass: RDS_MEMBER_CLASS,
              }),
            );
            assert.strictEqual(
              resp.DBInstance?.DBInstanceIdentifier,
              instanceId,
              "AddClusterMember: DBInstanceIdentifier mismatch",
            );
            assert.strictEqual(
              resp.DBInstance.DBClusterIdentifier,
              clusterId,
              "AddClusterMember: DBClusterIdentifier mismatch",
            );
          },
        },
        {
          name: "DescribeClusterMembers",
          fn: async (ctx) => {
            const { rds } = makeClients(ctx);
            const clusterId = (ctx as Record<string, unknown>)[
              "_memberCluster"
            ] as string;
            const instanceId = (ctx as Record<string, unknown>)[
              "_memberInstance"
            ] as string;
            assert.ok(
              clusterId && instanceId,
              "DescribeClusterMembers: no cluster or member from the earlier tests",
            );
            const cluster = await describeCluster(rds, clusterId);
            const members = cluster.DBClusterMembers ?? [];
            assert.ok(
              members.length > 0,
              `DescribeClusterMembers: DBClusterMembers is empty after adding ${instanceId}`,
            );
            const entry = members.find(
              (m) => m.DBInstanceIdentifier === instanceId,
            );
            assert.ok(
              entry,
              `DescribeClusterMembers: ${instanceId} is not among the listed members ${JSON.stringify(clusterMemberIds(cluster))}`,
            );
            // The first member of a cluster is its writer. A membership recorded
            // with no writer is the shape that leaves the cluster endpoints
            // pointing at nothing.
            assert.ok(
              entry.IsClusterWriter,
              `DescribeClusterMembers: ${instanceId} is listed but IsClusterWriter is false, and it is the only member`,
            );
          },
        },
        {
          name: "DescribeMemberInheritedSettings",
          fn: async (ctx) => {
            const { rds } = makeClients(ctx);
            const clusterId = (ctx as Record<string, unknown>)[
              "_memberCluster"
            ] as string;
            const instanceId = (ctx as Record<string, unknown>)[
              "_memberInstance"
            ] as string;
            assert.ok(
              instanceId,
              "DescribeMemberInheritedSettings: no member from AddClusterMember",
            );
            const resp = await rds.send(
              new DescribeDBInstancesCommand({
                DBInstanceIdentifier: instanceId,
              }),
            );
            const member = resp.DBInstances?.[0];
            assert.ok(
              member,
              `DescribeMemberInheritedSettings: no instance returned for ${instanceId}`,
            );
            // The member never sent a master username; it must be the cluster's.
            assert.strictEqual(
              member.MasterUsername,
              RDS_MEMBER_USERNAME,
              "DescribeMemberInheritedSettings: MasterUsername was not inherited from the cluster",
            );
            assert.strictEqual(
              member.DBClusterIdentifier,
              clusterId,
              "DescribeMemberInheritedSettings: DBClusterIdentifier mismatch",
            );
            const wantVersion = (ctx as Record<string, unknown>)[
              "_memberClusterEngineVersion"
            ] as string | undefined;
            if (wantVersion) {
              assert.strictEqual(
                member.EngineVersion,
                wantVersion,
                "DescribeMemberInheritedSettings: EngineVersion was not inherited from the cluster",
              );
            }
            // Port is deliberately not asserted: Overcast answers with a port
            // the caller can actually dial, which differs between a host and a
            // sibling container, so any fixed expectation would be wrong for one.
          },
        },
        {
          name: "RemoveClusterMember",
          fn: async (ctx) => {
            const { rds } = makeClients(ctx);
            const clusterId = (ctx as Record<string, unknown>)[
              "_memberCluster"
            ] as string;
            const instanceId = (ctx as Record<string, unknown>)[
              "_memberInstance"
            ] as string;
            assert.ok(
              clusterId && instanceId,
              "RemoveClusterMember: no cluster or member from the earlier tests",
            );
            await rds.send(
              new DeleteDBInstanceCommand({
                DBInstanceIdentifier: instanceId,
                SkipFinalSnapshot: true,
              }),
            );
            // Deleting the instance must take it out of the cluster too. It did
            // not until recently: the record went and the membership entry
            // stayed, so DescribeDBClusters kept reporting a member that no
            // longer existed.
            const deadline = Date.now() + RDS_WAIT_BUDGET_MS;
            for (;;) {
              const cluster = await describeCluster(rds, clusterId);
              if (!clusterMemberIds(cluster).includes(instanceId)) {
                (ctx as Record<string, unknown>)["_memberInstance"] = undefined;
                return;
              }
              if (Date.now() >= deadline) {
                throw new Error(
                  `DB instance ${instanceId} was still listed in ${clusterId}'s DBClusterMembers ${RDS_WAIT_BUDGET_MS}ms after DeleteDBInstance`,
                );
              }
              await new Promise<void>((r) =>
                globalThis.setTimeout(r, RDS_CLUSTER_POLL_INTERVAL_MS),
              );
            }
          },
        },
        {
          name: "DeleteClusterAfterMembers",
          fn: async (ctx) => {
            const { rds } = makeClients(ctx);
            const clusterId = (ctx as Record<string, unknown>)[
              "_memberCluster"
            ] as string;
            assert.ok(
              clusterId,
              "DeleteClusterAfterMembers: no cluster from CreateClusterForMembers",
            );
            await rds.send(
              new DeleteDBClusterCommand({
                DBClusterIdentifier: clusterId,
                SkipFinalSnapshot: true,
              }),
            );
            await waitForClusterGone(rds, clusterId);
            (ctx as Record<string, unknown>)["_memberCluster"] = undefined;
          },
        },
      ],
      teardown: async (ctx) => {
        const { rds } = makeClients(ctx);
        // Reverse creation order: the member owns a container, the cluster owns
        // the member.
        const instanceId = (ctx as Record<string, unknown>)[
          "_memberInstance"
        ] as string;
        if (instanceId) {
          try {
            await rds.send(
              new DeleteDBInstanceCommand({
                DBInstanceIdentifier: instanceId,
                SkipFinalSnapshot: true,
              }),
            );
          } catch {}
        }
        const clusterId = (ctx as Record<string, unknown>)[
          "_memberCluster"
        ] as string;
        if (clusterId) {
          try {
            await rds.send(
              new DeleteDBClusterCommand({
                DBClusterIdentifier: clusterId,
                SkipFinalSnapshot: true,
              }),
            );
          } catch {}
        }
      },
    },

    // ── rds-subnet-groups ──────────────────────────────────────────────────
    {
      suite,
      service: "rds",
      name: "rds-subnet-groups",
      tests: [
        {
          name: "CreateDBSubnetGroup",
          fn: async (ctx) => {
            const { rds } = makeClients(ctx);
            const groupName = `compat-${ctx.runId}`;
            const resp = await rds.send(
              new CreateDBSubnetGroupCommand({
                DBSubnetGroupName: groupName,
                DBSubnetGroupDescription: "compat test subnet group",
                SubnetIds: ["subnet-00000000", "subnet-00000001"],
              }),
            );
            assert.ok(
              resp.DBSubnetGroup?.DBSubnetGroupName,
              "CreateDBSubnetGroup: missing DBSubnetGroupName",
            );
            (ctx as Record<string, unknown>)["_subnetGroupName"] = groupName;
          },
        },
        {
          name: "DescribeDBSubnetGroups",
          fn: async (ctx) => {
            const { rds } = makeClients(ctx);
            const groupName = (ctx as Record<string, unknown>)[
              "_subnetGroupName"
            ] as string;
            assert.ok(
              groupName,
              "DescribeDBSubnetGroups: no group from CreateDBSubnetGroup",
            );
            const resp = await rds.send(
              new DescribeDBSubnetGroupsCommand({
                DBSubnetGroupName: groupName,
              }),
            );
            assert.ok(
              resp.DBSubnetGroups?.length,
              "DescribeDBSubnetGroups: no groups returned",
            );
            assert.strictEqual(
              resp.DBSubnetGroups[0].DBSubnetGroupName,
              groupName,
              `DescribeDBSubnetGroups: expected name ${groupName}`,
            );
          },
        },
        {
          name: "DeleteDBSubnetGroup",
          fn: async (ctx) => {
            const { rds } = makeClients(ctx);
            const groupName = (ctx as Record<string, unknown>)[
              "_subnetGroupName"
            ] as string;
            if (!groupName) return;
            await rds.send(
              new DeleteDBSubnetGroupCommand({
                DBSubnetGroupName: groupName,
              }),
            );
          },
        },
      ],
      teardown: async (ctx) => {
        const { rds } = makeClients(ctx);
        const groupName = (ctx as Record<string, unknown>)[
          "_subnetGroupName"
        ] as string;
        if (groupName) {
          try {
            await rds.send(
              new DeleteDBSubnetGroupCommand({
                DBSubnetGroupName: groupName,
              }),
            );
          } catch {}
        }
      },
    },

    // ── rds-parameter-groups ───────────────────────────────────────────────
    {
      suite,
      service: "rds",
      name: "rds-parameter-groups",
      tests: [
        {
          name: "CreateDBParameterGroup",
          fn: async (ctx) => {
            const { rds } = makeClients(ctx);
            const groupName = `compat-pg-${ctx.runId}`;
            const resp = await rds.send(
              new CreateDBParameterGroupCommand({
                DBParameterGroupName: groupName,
                DBParameterGroupFamily: "aurora-mysql8.0",
                Description: "compat test parameter group",
              }),
            );
            assert.ok(
              resp.DBParameterGroup?.DBParameterGroupName,
              "CreateDBParameterGroup: missing DBParameterGroupName",
            );
            assert.equal(
              resp.DBParameterGroup.DBParameterGroupFamily,
              "aurora-mysql8.0",
              "CreateDBParameterGroup: family mismatch",
            );
            (ctx as Record<string, unknown>)["_paramGroupName"] = groupName;
          },
        },
        {
          name: "DescribeDBParameterGroups",
          fn: async (ctx) => {
            const { rds } = makeClients(ctx);
            const groupName = (ctx as Record<string, unknown>)[
              "_paramGroupName"
            ] as string;
            assert.ok(
              groupName,
              "DescribeDBParameterGroups: no group from CreateDBParameterGroup",
            );
            const resp = await rds.send(
              new DescribeDBParameterGroupsCommand({
                DBParameterGroupName: groupName,
              }),
            );
            assert.ok(
              resp.DBParameterGroups?.length,
              "DescribeDBParameterGroups: no groups returned",
            );
            assert.strictEqual(
              resp.DBParameterGroups[0].DBParameterGroupName,
              groupName,
              `DescribeDBParameterGroups: expected name ${groupName}`,
            );
          },
        },
        {
          name: "DeleteDBParameterGroup",
          fn: async (ctx) => {
            const { rds } = makeClients(ctx);
            const groupName = (ctx as Record<string, unknown>)[
              "_paramGroupName"
            ] as string;
            if (!groupName) return;
            await rds.send(
              new DeleteDBParameterGroupCommand({
                DBParameterGroupName: groupName,
              }),
            );
          },
        },
      ],
      teardown: async (ctx) => {
        const { rds } = makeClients(ctx);
        const groupName = (ctx as Record<string, unknown>)[
          "_paramGroupName"
        ] as string;
        if (groupName) {
          try {
            await rds.send(
              new DeleteDBParameterGroupCommand({
                DBParameterGroupName: groupName,
              }),
            );
          } catch {}
        }
      },
    },
  ];
}
