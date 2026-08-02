/**
 * groups/efs.ts — Elastic File System compatibility test groups for the Node.js suite.
 *
 * Groups:
 *   efs-mount-targets — mount-target lifecycle and security groups
 */

import {
  CreateFileSystemCommand,
  CreateMountTargetCommand,
  DeleteFileSystemCommand,
  DeleteMountTargetCommand,
  DescribeMountTargetSecurityGroupsCommand,
  DescribeMountTargetsCommand,
  ModifyMountTargetSecurityGroupsCommand,
} from "@aws-sdk/client-efs";
import { makeClients } from "../lib/clients.js";
import type { TestContext, TestGroup } from "../lib/harness.js";
import * as assert from "node:assert/strict";

/** Bag keys shared between the tests in this group. */
const FS_ID = "_efsFileSystemId";
const MT_ID = "_efsMountTargetId";

const bag = (ctx: TestContext) => ctx as unknown as Record<string, unknown>;

/**
 * An AWS-shaped subnet ID that is still unique per run.
 *
 * A file system accepts one mount target per availability zone, and the
 * emulator derives the zone from the subnet ID — so it must vary per run. The
 * shape matters too: the SDKs validate SubnetId's length before the request
 * leaves the client, and `subnet-${runId}` is short enough to be rejected.
 * Real long-form IDs are "subnet-" plus 17 hex characters, so keep the run
 * ID's own hex digits and pad the rest.
 */
const subnetId = (ctx: TestContext) => {
  const hex = [...ctx.runId].filter((c) => /[0-9a-f]/.test(c)).join("");
  return `subnet-${(hex + "0".repeat(17)).slice(0, 17)}`;
};

export function makeEFSGroups(suite: string): TestGroup[] {
  return [
    // ── efs-mount-targets ──────────────────────────────────────────────────
    {
      suite,
      service: "efs",
      name: "efs-mount-targets",
      // Mount targets have no meaning without a file system, so creating one
      // is setup rather than a test.
      setup: async (ctx) => {
        const { efs } = makeClients(ctx);
        const resp = await efs.send(
          new CreateFileSystemCommand({
            CreationToken: `oc-${ctx.runId}-efs-mount-targets`,
          }),
        );
        assert.ok(
          resp.FileSystemId,
          "setup: CreateFileSystem returned no FileSystemId",
        );
        bag(ctx)[FS_ID] = resp.FileSystemId;
      },
      tests: [
        {
          name: "CreateMountTarget",
          fn: async (ctx) => {
            const { efs } = makeClients(ctx);
            const fsId = bag(ctx)[FS_ID] as string;
            assert.ok(fsId, "CreateMountTarget: no file system from setup");
            const resp = await efs.send(
              new CreateMountTargetCommand({
                FileSystemId: fsId,
                SubnetId: subnetId(ctx),
                SecurityGroups: ["sg-compat-a"],
              }),
            );
            assert.ok(
              resp.MountTargetId,
              "CreateMountTarget: missing MountTargetId",
            );
            assert.strictEqual(
              resp.FileSystemId,
              fsId,
              `CreateMountTarget: expected FileSystemId ${fsId}`,
            );
            assert.ok(
              resp.LifeCycleState,
              "CreateMountTarget: missing LifeCycleState",
            );
            bag(ctx)[MT_ID] = resp.MountTargetId;
          },
        },
        {
          name: "DescribeMountTargets",
          fn: async (ctx) => {
            const { efs } = makeClients(ctx);
            const fsId = bag(ctx)[FS_ID] as string;
            const mtId = bag(ctx)[MT_ID] as string;
            assert.ok(
              mtId,
              "DescribeMountTargets: no mount target from CreateMountTarget",
            );
            const resp = await efs.send(
              new DescribeMountTargetsCommand({ FileSystemId: fsId }),
            );
            const targets = resp.MountTargets ?? [];
            assert.ok(
              targets.length > 0,
              "DescribeMountTargets: expected at least 1 mount target, got 0",
            );
            const match = targets.find((t) => t.MountTargetId === mtId);
            assert.ok(
              match,
              `DescribeMountTargets: mount target ${mtId} not in the response`,
            );
            assert.ok(
              match.IpAddress,
              `DescribeMountTargets: mount target ${mtId} has no IpAddress`,
            );
          },
        },
        {
          name: "DescribeMountTargetSecurityGroups",
          fn: async (ctx) => {
            const { efs } = makeClients(ctx);
            const mtId = bag(ctx)[MT_ID] as string;
            assert.ok(
              mtId,
              "DescribeMountTargetSecurityGroups: no mount target from CreateMountTarget",
            );
            const resp = await efs.send(
              new DescribeMountTargetSecurityGroupsCommand({
                MountTargetId: mtId,
              }),
            );
            assert.deepStrictEqual(
              resp.SecurityGroups,
              ["sg-compat-a"],
              "DescribeMountTargetSecurityGroups: expected the group set at create",
            );
          },
        },
        {
          name: "ModifyMountTargetSecurityGroups",
          fn: async (ctx) => {
            const { efs } = makeClients(ctx);
            const mtId = bag(ctx)[MT_ID] as string;
            assert.ok(
              mtId,
              "ModifyMountTargetSecurityGroups: no mount target from CreateMountTarget",
            );
            await efs.send(
              new ModifyMountTargetSecurityGroupsCommand({
                MountTargetId: mtId,
                SecurityGroups: ["sg-compat-b", "sg-compat-c"],
              }),
            );
            // The mutation is what this test is about, so assert it here
            // rather than leaving it for another test to notice.
            const resp = await efs.send(
              new DescribeMountTargetSecurityGroupsCommand({
                MountTargetId: mtId,
              }),
            );
            assert.deepStrictEqual(
              resp.SecurityGroups,
              ["sg-compat-b", "sg-compat-c"],
              "ModifyMountTargetSecurityGroups: security groups were not updated",
            );
          },
        },
        {
          name: "DeleteMountTarget",
          fn: async (ctx) => {
            const { efs } = makeClients(ctx);
            const fsId = bag(ctx)[FS_ID] as string;
            const mtId = bag(ctx)[MT_ID] as string;
            assert.ok(
              mtId,
              "DeleteMountTarget: no mount target from CreateMountTarget",
            );
            await efs.send(
              new DeleteMountTargetCommand({ MountTargetId: mtId }),
            );
            bag(ctx)[MT_ID] = "";

            const resp = await efs.send(
              new DescribeMountTargetsCommand({ FileSystemId: fsId }),
            );
            const remaining = (resp.MountTargets ?? []).map(
              (t) => t.MountTargetId,
            );
            assert.ok(
              !remaining.includes(mtId),
              `DeleteMountTarget: mount target ${mtId} still listed after delete`,
            );
          },
        },
      ],
      // Reverse creation order: DeleteFileSystem answers FileSystemInUse
      // while any mount target remains.
      teardown: async (ctx) => {
        const { efs } = makeClients(ctx);
        const mtId = bag(ctx)[MT_ID] as string;
        if (mtId) {
          try {
            await efs.send(
              new DeleteMountTargetCommand({ MountTargetId: mtId }),
            );
          } catch {
            /* teardown must not throw */
          }
        }
        const fsId = bag(ctx)[FS_ID] as string;
        if (fsId) {
          try {
            await efs.send(new DeleteFileSystemCommand({ FileSystemId: fsId }));
          } catch {
            /* teardown must not throw */
          }
        }
      },
    },
  ];
}
