/**
 * groups/cloudformation.ts — CloudFormation compatibility test groups for the Node.js suite.
 *
 * Groups:
 *   cloudformation-stacks — stack lifecycle (create, update, describe, list, delete, validate)
 */

import {
  CreateStackCommand,
  DeleteStackCommand,
  DescribeStacksCommand,
  ListStacksCommand,
  UpdateStackCommand,
  ValidateTemplateCommand,
  StackStatus,
} from "@aws-sdk/client-cloudformation";
import { makeClients } from "../lib/clients.js";
import type { TestGroup } from "../lib/harness.js";
import * as assert from "node:assert/strict";

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
          name: "ListStacks",
          fn: async (ctx) => {
            const { cloudformation } = makeClients(ctx);
            await cloudformation.send(
              new ListStacksCommand({
                StackStatusFilter: [StackStatus.CREATE_COMPLETE],
              }),
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
  ];
}
