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
import { makeClients } from "../lib/clients.ts";
import type { TestGroup } from "../lib/harness.ts";
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
  ];
}
