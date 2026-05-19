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
import { makeClients } from "../lib/clients.js";
import type { TestGroup } from "../lib/harness.js";
import * as assert from "node:assert/strict";

/** Poll until the DB instance reaches `targetStatus` or throw after maxAttempts. */
async function waitForDBStatus(
  rds: RDSClient,
  dbId: string,
  targetStatus: string,
  maxAttempts = 60,
): Promise<void> {
  for (let i = 0; i < maxAttempts; i++) {
    const resp = await rds.send(
      new DescribeDBInstancesCommand({ DBInstanceIdentifier: dbId }),
    );
    const status = resp.DBInstances?.[0]?.DBInstanceStatus;
    if (status === targetStatus) return;
    await new Promise<void>((r) => globalThis.setTimeout(r, 500));
  }
  throw new Error(
    `DB instance ${dbId} did not reach "${targetStatus}" after ${maxAttempts} attempts`,
  );
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
                DBParameterGroupFamily: "mysql8.0",
                Description: "compat test parameter group",
              }),
            );
            assert.ok(
              resp.DBParameterGroup?.DBParameterGroupName,
              "CreateDBParameterGroup: missing DBParameterGroupName",
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
