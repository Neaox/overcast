/**
 * groups/cloudformation.ts — CloudFormation compatibility test groups for the Node.js suite.
 *
 * Groups:
 *   cloudformation-stacks   — stack lifecycle (create, update, describe, list, delete, validate)
 *   cloudformation-eks-refs — Ref / Fn::GetAtt semantics of the AWS::EKS::* resource types
 */

import {
  CreateStackCommand,
  DeleteStackCommand,
  DescribeStackResourcesCommand,
  DescribeStacksCommand,
  ListStacksCommand,
  UpdateStackCommand,
  ValidateTemplateCommand,
  StackStatus,
  waitUntilStackCreateComplete,
  waitUntilStackDeleteComplete,
} from "@aws-sdk/client-cloudformation";
import { makeClients } from "../lib/clients.ts";
import type { TestContext, TestGroup } from "../lib/harness.ts";
import * as assert from "node:assert/strict";

/**
 * CloudFormation documents Ref and Fn::GetAtt separately for every resource
 * type, and the EKS family disagrees with itself in ways only a deployed stack
 * reveals: Ref on a Cluster is its name and the ARN is a distinct Arn
 * attribute, Ref on a Nodegroup is "<cluster>/<nodegroup>", and Ref on an Addon
 * is "<cluster>|<addon>" — a pipe, not a slash.
 *
 *   https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/aws-resource-eks-cluster.html#aws-resource-eks-cluster-return-values
 *   https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/aws-resource-eks-nodegroup.html#aws-resource-eks-nodegroup-return-values
 *   https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/aws-resource-eks-addon.html#aws-resource-eks-addon-return-values
 *
 * Overcast returned the cluster ARN from Ref (#1690) and separated the add-on
 * with "/" (#1692). Both were wire-observable and neither was caught here, so
 * the group asserts the values through CloudFormation alone — stack outputs and
 * DescribeStackResources — which is what a template author actually sees.
 */

/**
 * vpc-cni is one of the add-ons DescribeAddonVersions publishes. AWS refuses an
 * AddonName it does not know, so the name is not arbitrary.
 */
const EKS_REFS_ADDON_NAME = "vpc-cni";
const EKS_REFS_CLUSTER_ROLE_ARN =
  "arn:aws:iam::000000000000:role/compat-eks-refs-cluster";
const EKS_REFS_NODE_ROLE_ARN =
  "arn:aws:iam::000000000000:role/compat-eks-refs-node";
const EKS_REFS_SUBNET_ID = "subnet-1";

/** Long enough for the stack to settle, short enough to fail rather than hang. */
const EKS_REFS_WAITER = { maxWaitTime: 120, minDelay: 1 };

const eksRefsStackName = (ctx: TestContext) => `compat-eks-refs-${ctx.runId}`;
const eksRefsClusterName = (ctx: TestContext) =>
  `compat-eks-refs-cluster-${ctx.runId}`;
const eksRefsNodegroupName = (ctx: TestContext) =>
  `compat-eks-refs-ng-${ctx.runId}`;

/**
 * The stack under test. The Nodegroup and the Addon are wired to their cluster
 * purely by {"Ref": "Cluster"} — no literal cluster name and no DependsOn. That
 * is the point: CloudFormation must hand CreateNodegroup and CreateAddon the
 * cluster *name*, so a Ref that resolves to the ARN fails the stack outright.
 */
function eksRefsTemplate(clusterName: string, nodegroupName: string): string {
  return JSON.stringify({
    AWSTemplateFormatVersion: "2010-09-09",
    Resources: {
      Cluster: {
        Type: "AWS::EKS::Cluster",
        Properties: {
          Name: clusterName,
          RoleArn: EKS_REFS_CLUSTER_ROLE_ARN,
          ResourcesVpcConfig: { SubnetIds: [EKS_REFS_SUBNET_ID] },
        },
      },
      Nodegroup: {
        Type: "AWS::EKS::Nodegroup",
        Properties: {
          ClusterName: { Ref: "Cluster" },
          NodegroupName: nodegroupName,
          NodeRole: EKS_REFS_NODE_ROLE_ARN,
          Subnets: [EKS_REFS_SUBNET_ID],
        },
      },
      Addon: {
        Type: "AWS::EKS::Addon",
        Properties: {
          ClusterName: { Ref: "Cluster" },
          AddonName: EKS_REFS_ADDON_NAME,
        },
      },
    },
    Outputs: {
      ClusterRef: { Value: { Ref: "Cluster" } },
      ClusterArn: { Value: { "Fn::GetAtt": ["Cluster", "Arn"] } },
      NodegroupRef: { Value: { Ref: "Nodegroup" } },
      AddonRef: { Value: { Ref: "Addon" } },
    },
  });
}

/**
 * The AWS-documented Ref value — and so the physical resource ID — of each
 * resource in the template.
 */
function eksRefsExpectedIds(
  clusterName: string,
  nodegroupName: string,
): Record<string, string> {
  return {
    Cluster: clusterName,
    Nodegroup: `${clusterName}/${nodegroupName}`,
    Addon: `${clusterName}|${EKS_REFS_ADDON_NAME}`,
  };
}

/**
 * The account ID out of a stack ARN
 * ("arn:aws:cloudformation:<region>:<account>:stack/<name>/<uuid>"), so the
 * expected EKS ARN can be built without reaching for a second AWS client.
 */
function accountFromStackId(stackId: string): string {
  const parts = stackId.split(":");
  assert.ok(
    parts.length >= 6 && parts[4],
    `stack ID ${stackId} is not a stack ARN, cannot read the account ID from it`,
  );
  return parts[4] as string;
}

async function describeEksRefsStack(ctx: TestContext, nameOrId: string) {
  const { cloudformation } = makeClients(ctx);
  const resp = await cloudformation.send(
    new DescribeStacksCommand({ StackName: nameOrId }),
  );
  const stack = resp.Stacks?.[0];
  assert.ok(stack, `DescribeStacks: ${nameOrId} returned no stacks`);
  return stack;
}

/**
 * Stack outputs, read afresh for each test rather than stashed, so every
 * assertion is made against a value the server just produced rather than one an
 * earlier test cached.
 */
async function eksRefsOutputs(ctx: TestContext): Promise<Record<string, string>> {
  const stack = await describeEksRefsStack(ctx, eksRefsStackName(ctx));
  const outputs: Record<string, string> = {};
  for (const o of stack.Outputs ?? []) {
    if (o.OutputKey) outputs[o.OutputKey] = o.OutputValue ?? "";
  }
  return outputs;
}

export function makeCloudFormationGroups(suite: string): TestGroup[] {
  return [
    // ── cloudformation-stacks ──────────────────────────────────────────────
    {
      suite,
      service: "cloudformation",
      name: "cloudformation-stacks",
      tests: [
        {
          name: "CreateStack",
          fn: async (ctx) => {
            const { cloudformation } = makeClients(ctx);
            const stackName = `compat-${ctx.runId}`;
            const resp = await cloudformation.send(
              new CreateStackCommand({
                StackName: stackName,
                TemplateBody: JSON.stringify({
                  AWSTemplateFormatVersion: "2010-09-09",
                  Resources: {
                    DummyBucket: {
                      Type: "AWS::S3::Bucket",
                    },
                  },
                }),
              }),
            );
            assert.ok(resp.StackId, "CreateStack: missing StackId");
            (ctx as Record<string, unknown>)["_stackName"] = stackName;
          },
        },
        {
          name: "DescribeStacks",
          fn: async (ctx) => {
            const { cloudformation } = makeClients(ctx);
            const stackName = (ctx as Record<string, unknown>)[
              "_stackName"
            ] as string;
            assert.ok(stackName, "DescribeStacks: no stack from CreateStack");
            const resp = await cloudformation.send(
              new DescribeStacksCommand({ StackName: stackName }),
            );
            assert.ok(
              resp.Stacks?.length,
              "DescribeStacks: no stacks returned",
            );
          },
        },
        {
          // StackStatusFilter is covered in both directions: a filter naming
          // the stack's status must include it, and one naming a status it
          // cannot hold must exclude it. Only the second catches an
          // implementation that accepts the parameter and ignores it.
          //
          // The statuses are the ones a stack created in this group can
          // legitimately be in — the suite does not wait for CREATE_COMPLETE —
          // and DELETE_FAILED is one it cannot reach, since nothing has tried
          // to delete it yet.
          name: "ListStacks",
          fn: async (ctx) => {
            const { cloudformation } = makeClients(ctx);
            const stackName = (ctx as Record<string, unknown>)[
              "_stackName"
            ] as string;
            assert.ok(stackName, "ListStacks: no stack from CreateStack");

            // Lists stack names, checking the invariant a status filter has to
            // hold: every summary returned is in one of the requested statuses.
            const listNames = async (
              StackStatusFilter?: StackStatus[],
            ): Promise<string[]> => {
              const resp = await cloudformation.send(
                new ListStacksCommand({ StackStatusFilter }),
              );
              const summaries = resp.StackSummaries ?? [];
              for (const s of summaries) {
                if (
                  StackStatusFilter &&
                  !StackStatusFilter.includes(s.StackStatus as StackStatus)
                ) {
                  assert.fail(
                    `ListStacks: ${s.StackName} returned with status ${s.StackStatus}, not in filter ${StackStatusFilter.join(", ")}`,
                  );
                }
              }
              return summaries.map((s) => s.StackName ?? "");
            };

            const all = await listNames();
            assert.ok(
              all.includes(stackName),
              `ListStacks: ${stackName} not in unfiltered listing ${all.join(", ")}`,
            );

            const active = await listNames([
              StackStatus.CREATE_COMPLETE,
              StackStatus.CREATE_IN_PROGRESS,
            ]);
            assert.ok(
              active.includes(stackName),
              `ListStacks: ${stackName} not in CREATE_COMPLETE/CREATE_IN_PROGRESS listing ${active.join(", ")}`,
            );

            const deleteFailed = await listNames([StackStatus.DELETE_FAILED]);
            assert.ok(
              !deleteFailed.includes(stackName),
              `ListStacks: ${stackName} returned by a DELETE_FAILED filter`,
            );
          },
        },
        {
          name: "DeleteStack",
          fn: async (ctx) => {
            const { cloudformation } = makeClients(ctx);
            const stackName = (ctx as Record<string, unknown>)[
              "_stackName"
            ] as string;
            assert.ok(stackName, "DeleteStack: no stack from CreateStack");
            await cloudformation.send(
              new DeleteStackCommand({ StackName: stackName }),
            );
          },
        },
        {
          name: "UpdateStack",
          fn: async (ctx) => {
            const { cloudformation } = makeClients(ctx);
            const stackName = (ctx as Record<string, unknown>)[
              "_stackName"
            ] as string;
            assert.ok(stackName, "UpdateStack: no stack from CreateStack");
            const resp = await cloudformation.send(
              new UpdateStackCommand({
                StackName: stackName,
                TemplateBody: JSON.stringify({
                  AWSTemplateFormatVersion: "2010-09-09",
                  Resources: {
                    DummyBucket: {
                      Type: "AWS::S3::Bucket",
                    },
                    DummyBucket2: {
                      Type: "AWS::S3::Bucket",
                    },
                  },
                }),
              }),
            );
            assert.ok(resp.StackId, "UpdateStack: missing StackId");
          },
        },
        {
          name: "ValidateTemplate",
          fn: async (ctx) => {
            const { cloudformation } = makeClients(ctx);
            await cloudformation.send(
              new ValidateTemplateCommand({
                TemplateBody: JSON.stringify({
                  AWSTemplateFormatVersion: "2010-09-09",
                  Resources: {
                    Bucket: { Type: "AWS::S3::Bucket" },
                  },
                }),
              }),
            );
          },
        },
      ],
      teardown: async (ctx) => {
        const { cloudformation } = makeClients(ctx);
        const stackName = (ctx as Record<string, unknown>)[
          "_stackName"
        ] as string;
        if (!stackName) return;
        try {
          await cloudformation.send(
            new DeleteStackCommand({ StackName: stackName }),
          );
        } catch {}
      },
    },

    // ── cloudformation-eks-refs ────────────────────────────────────────────
    {
      suite,
      service: "cloudformation",
      name: "cloudformation-eks-refs",
      tests: [
        {
          name: "CreateStackWithEksRefs",
          fn: async (ctx) => {
            const { cloudformation } = makeClients(ctx);
            const stackName = eksRefsStackName(ctx);
            const resp = await cloudformation.send(
              new CreateStackCommand({
                StackName: stackName,
                TemplateBody: eksRefsTemplate(
                  eksRefsClusterName(ctx),
                  eksRefsNodegroupName(ctx),
                ),
              }),
            );
            assert.ok(resp.StackId, "CreateStack: missing StackId");
            (ctx as Record<string, unknown>)["_eksRefsStackId"] = resp.StackId;

            // Outputs and physical resource IDs only exist once the stack
            // settles.
            await waitUntilStackCreateComplete(
              { client: cloudformation, ...EKS_REFS_WAITER },
              { StackName: stackName },
            );
            const stack = await describeEksRefsStack(ctx, stackName);
            assert.equal(
              stack.StackStatus,
              StackStatus.CREATE_COMPLETE,
              `CreateStack: ${stackName} is ${stack.StackStatus} (${stack.StackStatusReason ?? ""}), want CREATE_COMPLETE`,
            );
          },
        },
        {
          // The physical ID is what Ref resolves to, so this is the same
          // contract the output assertions below check, seen through the API an
          // operator reaches for when a stack misbehaves.
          name: "DescribeStackResourcesPhysicalIds",
          fn: async (ctx) => {
            const { cloudformation } = makeClients(ctx);
            const resp = await cloudformation.send(
              new DescribeStackResourcesCommand({
                StackName: eksRefsStackName(ctx),
              }),
            );
            const physicalIds: Record<string, string> = {};
            for (const r of resp.StackResources ?? []) {
              if (r.LogicalResourceId) {
                physicalIds[r.LogicalResourceId] = r.PhysicalResourceId ?? "";
              }
            }
            const want = eksRefsExpectedIds(
              eksRefsClusterName(ctx),
              eksRefsNodegroupName(ctx),
            );
            for (const logicalId of ["Cluster", "Nodegroup", "Addon"]) {
              assert.equal(
                physicalIds[logicalId],
                want[logicalId],
                `DescribeStackResources: ${logicalId} PhysicalResourceId = ${physicalIds[logicalId]}, want ${want[logicalId]}`,
              );
            }
          },
        },
        {
          // The divergence AWS documents for AWS::EKS::Cluster: Ref is the
          // cluster name, and the ARN is reachable only as the Arn attribute.
          // Asserting both together is what makes the pair meaningful — either
          // value alone looks plausible on its own.
          name: "GetAttClusterArn",
          fn: async (ctx) => {
            const outputs = await eksRefsOutputs(ctx);
            const cluster = eksRefsClusterName(ctx);
            assert.equal(
              outputs["ClusterRef"],
              cluster,
              `Ref on AWS::EKS::Cluster = ${outputs["ClusterRef"]}, want the cluster name ${cluster}`,
            );
            const account = accountFromStackId(
              (ctx as Record<string, unknown>)["_eksRefsStackId"] as string,
            );
            const want = `arn:aws:eks:${ctx.region}:${account}:cluster/${cluster}`;
            assert.equal(
              outputs["ClusterArn"],
              want,
              `Fn::GetAtt [Cluster, Arn] = ${outputs["ClusterArn"]}, want ${want}`,
            );
          },
        },
        {
          name: "RefNodegroupIsClusterSlashName",
          fn: async (ctx) => {
            const outputs = await eksRefsOutputs(ctx);
            const want = eksRefsExpectedIds(
              eksRefsClusterName(ctx),
              eksRefsNodegroupName(ctx),
            )["Nodegroup"];
            assert.equal(
              outputs["NodegroupRef"],
              want,
              `Ref on AWS::EKS::Nodegroup = ${outputs["NodegroupRef"]}, want ${want}`,
            );
          },
        },
        {
          name: "RefAddonIsClusterPipeAddon",
          fn: async (ctx) => {
            const outputs = await eksRefsOutputs(ctx);
            const want = eksRefsExpectedIds(
              eksRefsClusterName(ctx),
              eksRefsNodegroupName(ctx),
            )["Addon"];
            assert.equal(
              outputs["AddonRef"],
              want,
              `Ref on AWS::EKS::Addon = ${outputs["AddonRef"]}, want ${want} (pipe-separated, not a slash)`,
            );
          },
        },
        {
          // A deleted stack is readable by stack ID and gone by name — AWS
          // documents that asymmetry — so the stack ID is what proves the
          // delete finished rather than merely started.
          name: "DeleteStackWithEksRefs",
          fn: async (ctx) => {
            const { cloudformation } = makeClients(ctx);
            const stackName = eksRefsStackName(ctx);
            const stackId = (ctx as Record<string, unknown>)[
              "_eksRefsStackId"
            ] as string;
            assert.ok(
              stackId,
              "DeleteStack: no stack from CreateStackWithEksRefs",
            );
            await cloudformation.send(
              new DeleteStackCommand({ StackName: stackName }),
            );
            await waitUntilStackDeleteComplete(
              { client: cloudformation, ...EKS_REFS_WAITER },
              { StackName: stackName },
            );
            const stack = await describeEksRefsStack(ctx, stackId);
            assert.equal(
              stack.StackStatus,
              StackStatus.DELETE_COMPLETE,
              `DeleteStack: ${stackName} is ${stack.StackStatus} (${stack.StackStatusReason ?? ""}), want DELETE_COMPLETE`,
            );
          },
        },
      ],
      // Deleting the stack cascades to the cluster, the nodegroup and the
      // add-on: CloudFormation owns every resource this group creates, and
      // deleting a stack deletes the resources it owns.
      teardown: async (ctx) => {
        const { cloudformation } = makeClients(ctx);
        try {
          await cloudformation.send(
            new DeleteStackCommand({ StackName: eksRefsStackName(ctx) }),
          );
        } catch {}
      },
    },
  ];
}
