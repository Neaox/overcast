/**
 * groups/iam.ts — IAM compatibility test groups for the Node.js suite.
 *
 * Status: NOT implemented in Overcast. All tests expected to fail with 501.
 * These tests define the coverage target for future IAM implementation.
 *
 * Groups:
 *   iam-users    — user lifecycle
 *   iam-roles    — role lifecycle + assume-role policy
 *   iam-policies — managed and inline policies
 */

import {
  CreateUserCommand,
  DeleteUserCommand,
  GetUserCommand,
  ListUsersCommand,
  UpdateUserCommand,
  ListAccessKeysCommand,
  CreateAccessKeyCommand,
  DeleteAccessKeyCommand,
  CreateRoleCommand,
  DeleteRoleCommand,
  GetRoleCommand,
  ListRolesCommand,
  AttachRolePolicyCommand,
  DetachRolePolicyCommand,
  ListAttachedRolePoliciesCommand,
  PutRolePolicyCommand,
  GetRolePolicyCommand,
  ListRolePoliciesCommand,
  DeleteRolePolicyCommand,
  CreatePolicyCommand,
  DeletePolicyCommand,
  GetPolicyCommand,
  ListPoliciesCommand,
  PutUserPolicyCommand,
  GetUserPolicyCommand,
  DeleteUserPolicyCommand,
  CreateGroupCommand,
  DeleteGroupCommand,
  GetGroupCommand,
  AddUserToGroupCommand,
  RemoveUserFromGroupCommand,
  ListGroupsForUserCommand,
  CreateInstanceProfileCommand,
  DeleteInstanceProfileCommand,
  AddRoleToInstanceProfileCommand,
  RemoveRoleFromInstanceProfileCommand,
  GetInstanceProfileCommand,
} from "@aws-sdk/client-iam";
import { makeClients } from "../lib/clients.js";
import type { TestGroup } from "../lib/harness.js";
import * as assert from "node:assert/strict";

export function makeIAMGroups(suite: string): TestGroup[] {
  return [
    // ── iam-users ──────────────────────────────────────────────────────────
    {
      suite,
      service: "iam",
      name: "iam-users",
      tests: [
        {
          name: "CreateUser",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            const resp = await iam.send(
              new CreateUserCommand({ UserName: `${ctx.runId}-user` }),
            );
            assert.ok(resp.User?.UserId, "CreateUser: missing UserId");
          },
        },
        {
          name: "GetUser",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            const resp = await iam.send(
              new GetUserCommand({ UserName: `${ctx.runId}-user` }),
            );
            assert.strictEqual(
              resp.User?.UserName,
              `${ctx.runId}-user`,
              `GetUser: username mismatch`,
            );
          },
        },
        {
          name: "ListUsers",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            const resp = await iam.send(new ListUsersCommand({}));
            assert.ok(
              resp.Users?.some((u) => u.UserName === `${ctx.runId}-user`),
              "ListUsers: created user not found",
            );
          },
        },
        {
          name: "CreateAccessKey",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            const resp = await iam.send(
              new CreateAccessKeyCommand({ UserName: `${ctx.runId}-user` }),
            );
            assert.ok(
              resp.AccessKey?.AccessKeyId,
              "CreateAccessKey: missing AccessKeyId",
            );
            ctx["_akId"] = resp.AccessKey.AccessKeyId;
          },
        },
        {
          name: "DeleteAccessKey",
          fn: async (ctx) => {
            const akId = ctx["_akId"] as string;
            assert.ok(akId, "no AccessKeyId from previous step");
            const { iam } = makeClients(ctx);
            await iam.send(
              new DeleteAccessKeyCommand({
                UserName: `${ctx.runId}-user`,
                AccessKeyId: akId,
              }),
            );
          },
        },
        {
          name: "PutUserPolicy",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            await iam.send(
              new PutUserPolicyCommand({
                UserName: `${ctx.runId}-user`,
                PolicyName: "inline-policy",
                PolicyDocument: JSON.stringify({
                  Version: "2012-10-17",
                  Statement: [
                    { Effect: "Allow", Action: "s3:GetObject", Resource: "*" },
                  ],
                }),
              }),
            );
          },
        },
        {
          name: "GetUserPolicy",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            const resp = await iam.send(
              new GetUserPolicyCommand({
                UserName: `${ctx.runId}-user`,
                PolicyName: "inline-policy",
              }),
            );
            assert.ok(
              resp.PolicyDocument,
              "GetUserPolicy: missing PolicyDocument",
            );
          },
        },
        {
          name: "DeleteUserPolicy",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            await iam.send(
              new DeleteUserPolicyCommand({
                UserName: `${ctx.runId}-user`,
                PolicyName: "inline-policy",
              }),
            );
          },
        },
        {
          name: "UpdateUser",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            await iam.send(
              new UpdateUserCommand({
                UserName: `${ctx.runId}-user`,
                NewPath: "/newpath/",
              }),
            );
            const resp = await iam.send(
              new GetUserCommand({ UserName: `${ctx.runId}-user` }),
            );
            if (resp.User?.Path !== "/newpath/") {
              throw new Error(
                `UpdateUser: expected Path=/newpath/, got ${resp.User?.Path}`,
              );
            }
          },
        },
        {
          name: "ListAccessKeys",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            // Create a key to list, then clean it up
            const createResp = await iam.send(
              new CreateAccessKeyCommand({ UserName: `${ctx.runId}-user` }),
            );
            const akId = createResp.AccessKey?.AccessKeyId;
            assert.ok(akId, "ListAccessKeys: could not create access key");
            try {
              const resp = await iam.send(
                new ListAccessKeysCommand({ UserName: `${ctx.runId}-user` }),
              );
              assert.ok(
                resp.AccessKeyMetadata?.some((k) => k.AccessKeyId === akId),
                "ListAccessKeys: created key not found",
              );
            } finally {
              await iam.send(
                new DeleteAccessKeyCommand({
                  UserName: `${ctx.runId}-user`,
                  AccessKeyId: akId,
                }),
              );
            }
          },
        },
        {
          name: "DeleteUser",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            await iam.send(
              new DeleteUserCommand({ UserName: `${ctx.runId}-user` }),
            );
          },
        },
      ],
      teardown: async (ctx) => {
        const { iam } = makeClients(ctx);
        try {
          const akId = ctx["_akId"] as string;
          if (akId) {
            await iam.send(
              new DeleteAccessKeyCommand({
                UserName: `${ctx.runId}-user`,
                AccessKeyId: akId,
              }),
            );
          }
        } catch {}
        try {
          await iam.send(
            new DeleteUserPolicyCommand({
              UserName: `${ctx.runId}-user`,
              PolicyName: "inline-policy",
            }),
          );
        } catch {}
        try {
          await iam.send(
            new DeleteUserCommand({ UserName: `${ctx.runId}-user` }),
          );
        } catch {}
      },
    },

    // ── iam-roles ──────────────────────────────────────────────────────────
    {
      suite,
      service: "iam",
      name: "iam-roles",
      tests: [
        {
          name: "CreateRole",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            const resp = await iam.send(
              new CreateRoleCommand({
                RoleName: `${ctx.runId}-role`,
                AssumeRolePolicyDocument: JSON.stringify({
                  Version: "2012-10-17",
                  Statement: [
                    {
                      Effect: "Allow",
                      Principal: { Service: "lambda.amazonaws.com" },
                      Action: "sts:AssumeRole",
                    },
                  ],
                }),
              }),
            );
            assert.ok(resp.Role?.RoleId, "CreateRole: missing RoleId");
            ctx["_roleArn"] = resp.Role.Arn;
          },
        },
        {
          name: "GetRole",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            const resp = await iam.send(
              new GetRoleCommand({ RoleName: `${ctx.runId}-role` }),
            );
            assert.ok(resp.Role?.Arn, "GetRole: missing Arn");
          },
        },
        {
          name: "ListRoles",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            const resp = await iam.send(new ListRolesCommand({}));
            assert.ok(
              resp.Roles?.some((r) => r.RoleName === `${ctx.runId}-role`),
              "ListRoles: role not found",
            );
          },
        },
        {
          name: "AttachRolePolicy",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            await iam.send(
              new AttachRolePolicyCommand({
                RoleName: `${ctx.runId}-role`,
                PolicyArn: "arn:aws:iam::aws:policy/AmazonS3ReadOnlyAccess",
              }),
            );
          },
        },
        {
          name: "ListAttachedRolePolicies",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            const resp = await iam.send(
              new ListAttachedRolePoliciesCommand({
                RoleName: `${ctx.runId}-role`,
              }),
            );
            if (
              !resp.AttachedPolicies?.some(
                (p) =>
                  p.PolicyArn ===
                  "arn:aws:iam::aws:policy/AmazonS3ReadOnlyAccess",
              )
            ) {
              throw new Error("ListAttachedRolePolicies: policy not found");
            }
          },
        },
        {
          name: "DetachRolePolicy",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            await iam.send(
              new DetachRolePolicyCommand({
                RoleName: `${ctx.runId}-role`,
                PolicyArn: "arn:aws:iam::aws:policy/AmazonS3ReadOnlyAccess",
              }),
            );
          },
        },
        {
          name: "CreateInstanceProfile",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            try {
              await iam.send(
                new CreateInstanceProfileCommand({
                  InstanceProfileName: `${ctx.runId}-profile`,
                }),
              );
            } catch (err: unknown) {
              // Idempotent: ignore if the profile already exists from a prior
              // run against the same persistent emulator instance.
              const msg = err instanceof Error ? err.message : String(err);
              if (!msg.includes("EntityAlreadyExists")) throw err;
            }
          },
        },
        {
          name: "AddRoleToInstanceProfile",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            await iam.send(
              new AddRoleToInstanceProfileCommand({
                InstanceProfileName: `${ctx.runId}-profile`,
                RoleName: `${ctx.runId}-role`,
              }),
            );
          },
        },
        {
          name: "GetInstanceProfile",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            const resp = await iam.send(
              new GetInstanceProfileCommand({
                InstanceProfileName: `${ctx.runId}-profile`,
              }),
            );
            if (
              !resp.InstanceProfile?.Roles?.some(
                (r) => r.RoleName === `${ctx.runId}-role`,
              )
            ) {
              throw new Error("GetInstanceProfile: role not attached");
            }
          },
        },
        {
          name: "PutRolePolicy",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            await iam.send(
              new PutRolePolicyCommand({
                RoleName: `${ctx.runId}-role`,
                PolicyName: "inline-role-policy",
                PolicyDocument: JSON.stringify({
                  Version: "2012-10-17",
                  Statement: [
                    { Effect: "Allow", Action: "logs:*", Resource: "*" },
                  ],
                }),
              }),
            );
          },
        },
        {
          name: "GetRolePolicy",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            const resp = await iam.send(
              new GetRolePolicyCommand({
                RoleName: `${ctx.runId}-role`,
                PolicyName: "inline-role-policy",
              }),
            );
            assert.ok(
              resp.PolicyDocument,
              "GetRolePolicy: missing PolicyDocument",
            );
          },
        },
        {
          name: "ListRolePolicies",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            const resp = await iam.send(
              new ListRolePoliciesCommand({
                RoleName: `${ctx.runId}-role`,
              }),
            );
            assert.ok(
              resp.PolicyNames?.includes("inline-role-policy"),
              "ListRolePolicies: policy not found",
            );
          },
        },
        {
          name: "DeleteRolePolicy",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            await iam.send(
              new DeleteRolePolicyCommand({
                RoleName: `${ctx.runId}-role`,
                PolicyName: "inline-role-policy",
              }),
            );
          },
        },
        {
          name: "DeleteRole",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            await iam.send(
              new DeleteRoleCommand({ RoleName: `${ctx.runId}-role` }),
            );
          },
        },
      ],
      teardown: async (ctx) => {
        const { iam } = makeClients(ctx);
        try {
          await iam.send(
            new DetachRolePolicyCommand({
              RoleName: `${ctx.runId}-role`,
              PolicyArn: "arn:aws:iam::aws:policy/AmazonS3ReadOnlyAccess",
            }),
          );
        } catch {}
        try {
          await iam.send(
            new DeleteRolePolicyCommand({
              RoleName: `${ctx.runId}-role`,
              PolicyName: "inline-role-policy",
            }),
          );
        } catch {}
        try {
          await iam.send(
            new RemoveRoleFromInstanceProfileCommand({
              InstanceProfileName: `${ctx.runId}-profile`,
              RoleName: `${ctx.runId}-role`,
            }),
          );
        } catch {}
        try {
          await iam.send(
            new DeleteInstanceProfileCommand({
              InstanceProfileName: `${ctx.runId}-profile`,
            }),
          );
        } catch {}
        try {
          await iam.send(
            new DeleteRoleCommand({ RoleName: `${ctx.runId}-role` }),
          );
        } catch {}
      },
    },

    // ── iam-policies ───────────────────────────────────────────────────────
    {
      suite,
      service: "iam",
      name: "iam-policies",
      tests: [
        {
          name: "CreatePolicy",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            const resp = await iam.send(
              new CreatePolicyCommand({
                PolicyName: `${ctx.runId}-policy`,
                PolicyDocument: JSON.stringify({
                  Version: "2012-10-17",
                  Statement: [{ Effect: "Deny", Action: "*", Resource: "*" }],
                }),
              }),
            );
            assert.ok(resp.Policy?.Arn, "CreatePolicy: missing Arn");
            ctx["_policyArn"] = resp.Policy.Arn;
          },
        },
        {
          name: "GetPolicy",
          fn: async (ctx) => {
            const policyArn = ctx["_policyArn"] as string;
            assert.ok(policyArn, "no policy ARN");
            const { iam } = makeClients(ctx);
            const resp = await iam.send(
              new GetPolicyCommand({ PolicyArn: policyArn }),
            );
            assert.ok(resp.Policy?.PolicyName, "GetPolicy: missing PolicyName");
          },
        },
        {
          name: "ListPolicies",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            const resp = await iam.send(
              new ListPoliciesCommand({ Scope: "Local" }),
            );
            const name = `${ctx.runId}-policy`;
            assert.ok(
              resp.Policies?.some((p) => p.PolicyName === name),
              `ListPolicies: ${name} not found`,
            );
          },
        },
        {
          name: "DeletePolicy",
          fn: async (ctx) => {
            const policyArn = ctx["_policyArn"] as string;
            assert.ok(policyArn, "no policy ARN");
            const { iam } = makeClients(ctx);
            await iam.send(new DeletePolicyCommand({ PolicyArn: policyArn }));
          },
        },
      ],
      teardown: async (ctx) => {
        const policyArn = ctx["_policyArn"] as string;
        if (policyArn) {
          const { iam } = makeClients(ctx);
          try {
            await iam.send(new DeletePolicyCommand({ PolicyArn: policyArn }));
          } catch {}
        }
      },
    },

    // ── iam-groups ─────────────────────────────────────────────────────────
    {
      suite,
      service: "iam",
      name: "iam-groups",
      tests: [
        {
          name: "CreateGroup",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            await iam.send(
              new CreateGroupCommand({ GroupName: `${ctx.runId}-grp` }),
            );
          },
        },
        {
          name: "AddUserToGroup",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            // Create a temp user for this test
            await iam.send(
              new CreateUserCommand({ UserName: `${ctx.runId}-grp-user` }),
            );
            await iam.send(
              new AddUserToGroupCommand({
                GroupName: `${ctx.runId}-grp`,
                UserName: `${ctx.runId}-grp-user`,
              }),
            );
          },
        },
        {
          name: "ListGroupsForUser",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            const resp = await iam.send(
              new ListGroupsForUserCommand({
                UserName: `${ctx.runId}-grp-user`,
              }),
            );
            assert.ok(
              resp.Groups?.some((g) => g.GroupName === `${ctx.runId}-grp`),
              "ListGroupsForUser: group not found",
            );
          },
        },
        {
          name: "RemoveUserFromGroup",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            await iam.send(
              new RemoveUserFromGroupCommand({
                GroupName: `${ctx.runId}-grp`,
                UserName: `${ctx.runId}-grp-user`,
              }),
            );
          },
        },
        {
          name: "GetGroup",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            const resp = await iam.send(
              new GetGroupCommand({ GroupName: `${ctx.runId}-grp` }),
            );
            assert.ok(resp.Group, "GetGroup: missing Group");
            assert.strictEqual(
              resp.Group.GroupName,
              `${ctx.runId}-grp`,
              `GetGroup: expected group name ${ctx.runId}-grp`,
            );
          },
        },
        {
          name: "DeleteGroup",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            await iam.send(
              new DeleteGroupCommand({ GroupName: `${ctx.runId}-grp` }),
            );
          },
        },
      ],
      teardown: async (ctx) => {
        const { iam } = makeClients(ctx);
        try {
          await iam.send(
            new RemoveUserFromGroupCommand({
              GroupName: `${ctx.runId}-grp`,
              UserName: `${ctx.runId}-grp-user`,
            }),
          );
        } catch {}
        try {
          await iam.send(
            new DeleteUserCommand({ UserName: `${ctx.runId}-grp-user` }),
          );
        } catch {}
        try {
          await iam.send(
            new DeleteGroupCommand({ GroupName: `${ctx.runId}-grp` }),
          );
        } catch {}
      },
    },
  ];
}
