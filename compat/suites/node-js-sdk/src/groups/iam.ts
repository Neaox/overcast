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
 *   iam-simulate — policy simulation (SimulateCustomPolicy / SimulatePrincipalPolicy)
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
  SimulateCustomPolicyCommand,
  SimulatePrincipalPolicyCommand,
  CreateInstanceProfileCommand,
  DeleteInstanceProfileCommand,
  AddRoleToInstanceProfileCommand,
  RemoveRoleFromInstanceProfileCommand,
  GetInstanceProfileCommand,
} from "@aws-sdk/client-iam";
import { makeClients } from "../lib/clients.ts";
import type { TestGroup } from "../lib/harness.ts";
import * as assert from "node:assert/strict";

/**
 * A document AWS refuses with MalformedPolicyDocument: "Statements must include
 * either an Action or NotAction element" (IAM User Guide,
 * reference_policies_elements_action.html). Every writer that takes a document
 * names that error (IAM API Reference, API_CreatePolicy.html and
 * API_CreateRole.html, Errors).
 */
const MALFORMED_POLICY = JSON.stringify({
  Version: "2012-10-17",
  Statement: [{ Effect: "Allow", Resource: "*" }],
});

/**
 * The tag set the role and policy fixtures are created with, and the one
 * GetRole and GetPolicy must hand back on the resource itself.
 */
const RESOURCE_TAGS = [
  { Key: "owner", Value: "compat" },
  { Key: "stage", Value: "dev" },
];

/** Checks the two fixture tags on a resource returned by a Get* call. */
function assertResourceTags(
  op: string,
  tags: { Key?: string; Value?: string }[] | undefined,
): void {
  const got = new Map((tags ?? []).map((t) => [t.Key, t.Value]));
  for (const want of RESOURCE_TAGS) {
    assert.equal(
      got.get(want.Key),
      want.Value,
      `${op}: tag ${want.Key} = ${got.get(want.Key)}, want ${want.Value}`,
    );
  }
}

/**
 * Asserts both halves of the error contract: the code AWS's model names, and
 * the 400 the Query protocol binds it to.
 */
async function assertMalformedPolicyDocument(
  op: string,
  send: Promise<unknown>,
): Promise<void> {
  try {
    await send;
  } catch (err: unknown) {
    const e = err as { name?: string; $metadata?: { httpStatusCode?: number } };
    assert.equal(
      e.name,
      "MalformedPolicyDocumentException",
      `${op}: expected MalformedPolicyDocument, got ${e.name}`,
    );
    assert.equal(
      e.$metadata?.httpStatusCode,
      400,
      `${op}: expected HTTP 400 for MalformedPolicyDocument, got ${e.$metadata?.httpStatusCode}`,
    );
    return;
  }
  throw new Error(`${op}: expected MalformedPolicyDocument, got success`);
}

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
                Tags: RESOURCE_TAGS,
              }),
            );
            assert.ok(resp.Role?.RoleId, "CreateRole: missing RoleId");
            ctx["_roleArn"] = resp.Role.Arn;
          },
        },
        {
          // A trust policy AWS would refuse must be refused here too, rather
          // than stored unparsed.
          name: "CreateRoleMalformedDocument",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            await assertMalformedPolicyDocument(
              "CreateRoleMalformedDocument",
              iam.send(
                new CreateRoleCommand({
                  RoleName: `${ctx.runId}-role-malformed`,
                  AssumeRolePolicyDocument: MALFORMED_POLICY,
                }),
              ),
            );
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
          // Tags given to CreateRole come back on the role (API_Role.html).
          name: "GetRoleReturnsTags",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            const resp = await iam.send(
              new GetRoleCommand({ RoleName: `${ctx.runId}-role` }),
            );
            assertResourceTags("GetRoleReturnsTags", resp.Role?.Tags);
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
            // AWS refuses DeleteRole with DeleteConflict while the role is
            // still in an instance profile, so clear that first — this group
            // put it there in AddRoleToInstanceProfile.
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
        // The malformed-document case must create nothing; delete it anyway so
        // a regression that stored it does not leak a role into the next run.
        try {
          await iam.send(
            new DeleteRoleCommand({ RoleName: `${ctx.runId}-role-malformed` }),
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
                Tags: RESOURCE_TAGS,
              }),
            );
            assert.ok(resp.Policy?.Arn, "CreatePolicy: missing Arn");
            ctx["_policyArn"] = resp.Policy.Arn;
          },
        },
        {
          // The same refusal on the identity-policy writer.
          name: "CreatePolicyMalformedDocument",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            await assertMalformedPolicyDocument(
              "CreatePolicyMalformedDocument",
              iam.send(
                new CreatePolicyCommand({
                  PolicyName: `${ctx.runId}-policy-malformed`,
                  PolicyDocument: MALFORMED_POLICY,
                }),
              ),
            );
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
          // Tags given to CreatePolicy come back on the policy
          // (API_Policy.html).
          name: "GetPolicyReturnsTags",
          fn: async (ctx) => {
            const policyArn = ctx["_policyArn"] as string;
            assert.ok(policyArn, "no policy ARN");
            const { iam } = makeClients(ctx);
            const resp = await iam.send(
              new GetPolicyCommand({ PolicyArn: policyArn }),
            );
            assertResourceTags("GetPolicyReturnsTags", resp.Policy?.Tags);
          },
        },
        {
          // AttachmentCount moves 0 to 1. "The number of entities (users,
          // groups, and roles) that the policy is attached to"
          // (API_Policy.html) is what a cleanup script reads before deleting a
          // policy, so a stuck 0 deletes something in use.
          name: "GetPolicyAttachmentCountAfterAttach",
          fn: async (ctx) => {
            const op = "GetPolicyAttachmentCountAfterAttach";
            const policyArn = ctx["_policyArn"] as string;
            assert.ok(policyArn, "no policy ARN");
            const { iam } = makeClients(ctx);
            const before = await iam.send(
              new GetPolicyCommand({ PolicyArn: policyArn }),
            );
            assert.equal(
              before.Policy?.AttachmentCount,
              0,
              `${op}: AttachmentCount = ${before.Policy?.AttachmentCount} before the attach, want 0`,
            );

            // The group attaches its own customer managed policy to a role of
            // its own, so the counter moves for this policy rather than for the
            // AWS managed one iam-roles attaches.
            const roleName = `${ctx.runId}-policy-role`;
            await iam.send(
              new CreateRoleCommand({
                RoleName: roleName,
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
            ctx["_policyRoleName"] = roleName;
            await iam.send(
              new AttachRolePolicyCommand({
                RoleName: roleName,
                PolicyArn: policyArn,
              }),
            );
            const after = await iam.send(
              new GetPolicyCommand({ PolicyArn: policyArn }),
            );
            assert.equal(
              after.Policy?.AttachmentCount,
              1,
              `${op}: AttachmentCount = ${after.Policy?.AttachmentCount} after attaching to one role, want 1`,
            );
          },
        },
        {
          // The counter moves back to 0, which a never-decremented one fails.
          name: "GetPolicyAttachmentCountAfterDetach",
          fn: async (ctx) => {
            const op = "GetPolicyAttachmentCountAfterDetach";
            const policyArn = ctx["_policyArn"] as string;
            const roleName = ctx["_policyRoleName"] as string;
            assert.ok(policyArn, "no policy ARN");
            assert.ok(roleName, "no role from the attach test");
            const { iam } = makeClients(ctx);
            await iam.send(
              new DetachRolePolicyCommand({
                RoleName: roleName,
                PolicyArn: policyArn,
              }),
            );
            const after = await iam.send(
              new GetPolicyCommand({ PolicyArn: policyArn }),
            );
            assert.equal(
              after.Policy?.AttachmentCount,
              0,
              `${op}: AttachmentCount = ${after.Policy?.AttachmentCount} after the detach, want 0`,
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
        const { iam } = makeClients(ctx);
        const policyArn = ctx["_policyArn"] as string;
        const roleName = ctx["_policyRoleName"] as string;
        if (roleName) {
          if (policyArn) {
            try {
              await iam.send(
                new DetachRolePolicyCommand({
                  RoleName: roleName,
                  PolicyArn: policyArn,
                }),
              );
            } catch {}
          }
          try {
            await iam.send(new DeleteRoleCommand({ RoleName: roleName }));
          } catch {}
        }
        if (policyArn) {
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
    // ── iam-simulate ───────────────────────────────────────────────────────
    {
      suite,
      service: "iam",
      name: "iam-simulate",
      setup: async (ctx) => {
        const { iam } = makeClients(ctx);
        await iam.send(
          new CreateUserCommand({ UserName: `${ctx.runId}-sim-user` }),
        );
        await iam.send(
          new PutUserPolicyCommand({
            UserName: `${ctx.runId}-sim-user`,
            PolicyName: "sim-allow-read",
            PolicyDocument: JSON.stringify({
              Version: "2012-10-17",
              Statement: [
                {
                  Effect: "Allow",
                  Action: "s3:GetObject",
                  Resource: `arn:aws:s3:::${ctx.runId}-sim/*`,
                },
              ],
            }),
          }),
        );
      },
      tests: [
        {
          name: "SimulateCustomPolicyAllowed",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            const resp = await iam.send(
              new SimulateCustomPolicyCommand({
                PolicyInputList: [
                  JSON.stringify({
                    Version: "2012-10-17",
                    Statement: [
                      {
                        Effect: "Allow",
                        Action: "s3:GetObject",
                        Resource: `arn:aws:s3:::${ctx.runId}-sim/*`,
                      },
                    ],
                  }),
                ],
                ActionNames: ["s3:GetObject"],
                ResourceArns: [`arn:aws:s3:::${ctx.runId}-sim/report.csv`],
              }),
            );
            const result = resp.EvaluationResults?.[0];
            assert.ok(result, "SimulateCustomPolicy: missing EvaluationResults");
            assert.strictEqual(
              result.EvalDecision,
              "allowed",
              `SimulateCustomPolicy: expected allowed, got ${result.EvalDecision}`,
            );
            assert.strictEqual(result.EvalActionName, "s3:GetObject");
          },
        },
        {
          name: "SimulateCustomPolicyImplicitDeny",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            const resp = await iam.send(
              new SimulateCustomPolicyCommand({
                PolicyInputList: [
                  JSON.stringify({
                    Version: "2012-10-17",
                    Statement: [
                      {
                        Effect: "Allow",
                        Action: "s3:GetObject",
                        Resource: `arn:aws:s3:::${ctx.runId}-sim/*`,
                      },
                    ],
                  }),
                ],
                ActionNames: ["s3:PutObject"],
                ResourceArns: [`arn:aws:s3:::${ctx.runId}-sim/report.csv`],
              }),
            );
            assert.strictEqual(
              resp.EvaluationResults?.[0]?.EvalDecision,
              "implicitDeny",
              "SimulateCustomPolicy: uncovered action should be implicitDeny",
            );
          },
        },
        {
          name: "SimulateCustomPolicyExplicitDeny",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            const resp = await iam.send(
              new SimulateCustomPolicyCommand({
                PolicyInputList: [
                  JSON.stringify({
                    Version: "2012-10-17",
                    Statement: [
                      { Effect: "Allow", Action: "s3:*", Resource: "*" },
                      {
                        Effect: "Deny",
                        Action: "s3:DeleteObject",
                        Resource: "*",
                      },
                    ],
                  }),
                ],
                ActionNames: ["s3:DeleteObject"],
                ResourceArns: [`arn:aws:s3:::${ctx.runId}-sim/report.csv`],
              }),
            );
            assert.strictEqual(
              resp.EvaluationResults?.[0]?.EvalDecision,
              "explicitDeny",
              "SimulateCustomPolicy: explicit deny should win over allow",
            );
          },
        },
        {
          name: "SimulatePrincipalPolicyAllowed",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            const resp = await iam.send(
              new SimulatePrincipalPolicyCommand({
                PolicySourceArn: `arn:aws:iam::000000000000:user/${ctx.runId}-sim-user`,
                ActionNames: ["s3:GetObject"],
                ResourceArns: [`arn:aws:s3:::${ctx.runId}-sim/report.csv`],
              }),
            );
            const result = resp.EvaluationResults?.[0];
            assert.ok(
              result,
              "SimulatePrincipalPolicy: missing EvaluationResults",
            );
            assert.strictEqual(
              result.EvalDecision,
              "allowed",
              `SimulatePrincipalPolicy: expected allowed, got ${result.EvalDecision}`,
            );
            assert.ok(
              result.MatchedStatements?.some(
                (m) => m.SourcePolicyId === "sim-allow-read",
              ),
              "SimulatePrincipalPolicy: MatchedStatements should name the inline policy",
            );
          },
        },
        {
          name: "SimulatePrincipalPolicyImplicitDeny",
          fn: async (ctx) => {
            const { iam } = makeClients(ctx);
            const resp = await iam.send(
              new SimulatePrincipalPolicyCommand({
                PolicySourceArn: `arn:aws:iam::000000000000:user/${ctx.runId}-sim-user`,
                ActionNames: ["s3:DeleteObject"],
                ResourceArns: [`arn:aws:s3:::${ctx.runId}-sim/report.csv`],
              }),
            );
            assert.strictEqual(
              resp.EvaluationResults?.[0]?.EvalDecision,
              "implicitDeny",
              "SimulatePrincipalPolicy: uncovered action should be implicitDeny",
            );
          },
        },
      ],
      teardown: async (ctx) => {
        const { iam } = makeClients(ctx);
        try {
          await iam.send(
            new DeleteUserPolicyCommand({
              UserName: `${ctx.runId}-sim-user`,
              PolicyName: "sim-allow-read",
            }),
          );
        } catch {}
        try {
          await iam.send(
            new DeleteUserCommand({ UserName: `${ctx.runId}-sim-user` }),
          );
        } catch {}
      },
    },
  ];
}
