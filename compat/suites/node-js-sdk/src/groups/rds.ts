/**
 * groups/rds.ts — RDS and Aurora compatibility test groups for the Node.js suite.
 *
 * Status: NOT implemented in Overcast. All tests expected to fail with 501.
 * These tests define the coverage target for future RDS/Aurora implementation.
 *
 * Groups:
 *   rds-instances        — DB instance lifecycle (create, describe, stop, start, modify, delete)
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
