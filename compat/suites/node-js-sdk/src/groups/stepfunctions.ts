/**
 * groups/stepfunctions.ts — Step Functions compatibility test groups for the Node.js suite.
 *
 * Status: NOT implemented in Overcast. All tests expected to fail with 501.
 * These tests define the coverage target for future Step Functions implementation.
 *
 * Groups:
 *   sfn-statemachines — state machine lifecycle and execution
 */

import {
  CreateStateMachineCommand,
  DeleteStateMachineCommand,
  DescribeStateMachineCommand,
  ListStateMachinesCommand,
  StartExecutionCommand,
  StateMachineType,
} from "@aws-sdk/client-sfn";
import { makeClients } from "../lib/clients.js";
import type { TestGroup } from "../lib/harness.js";
import * as assert from "node:assert/strict";

export function makeStepFunctionsGroups(suite: string): TestGroup[] {
  return [
    // ── sfn-statemachines ──────────────────────────────────────────────────
    {
      suite,
      service: "stepfunctions",
      name: "sfn-statemachines",
      tests: [
        {
          name: "CreateStateMachine",
          fn: async (ctx) => {
            const { sfn } = makeClients(ctx);
            const name = `compat-${ctx.runId}`;
            const resp = await sfn.send(
              new CreateStateMachineCommand({
                name,
                type: StateMachineType.EXPRESS,
                definition: JSON.stringify({
                  Comment: "compat test",
                  StartAt: "Pass",
                  States: {
                    Pass: { Type: "Pass", End: true },
                  },
                }),
                roleArn: `arn:aws:iam::000000000000:role/compat-sfn-${ctx.runId}`,
              }),
            );
            assert.ok(resp.stateMachineArn, "CreateStateMachine: missing stateMachineArn");
            (ctx as Record<string, unknown>)["_smArn"] = resp.stateMachineArn;
          },
        },
        {
          name: "DescribeStateMachine",
          fn: async (ctx) => {
            const { sfn } = makeClients(ctx);
            const smArn = (ctx as Record<string, unknown>)["_smArn"] as string;
            assert.ok(smArn, "DescribeStateMachine: no state machine from CreateStateMachine");
            const resp = await sfn.send(
              new DescribeStateMachineCommand({ stateMachineArn: smArn }),
            );
            assert.ok(resp.stateMachineArn, "DescribeStateMachine: missing stateMachineArn");
          },
        },
        {
          name: "ListStateMachines",
          fn: async (ctx) => {
            const { sfn } = makeClients(ctx);
            const smArn = (ctx as Record<string, unknown>)["_smArn"] as string;
            const resp = await sfn.send(new ListStateMachinesCommand({}));
            assert.notStrictEqual(smArn && !resp.stateMachines?.some((s) => s.stateMachineArn, smArn), `ListStateMachines: state machine ${smArn} not found`);
          },
        },
        {
          name: "StartExecution",
          fn: async (ctx) => {
            const { sfn } = makeClients(ctx);
            const smArn = (ctx as Record<string, unknown>)["_smArn"] as string;
            assert.ok(smArn, "StartExecution: no state machine from CreateStateMachine");
            const resp = await sfn.send(
              new StartExecutionCommand({
                stateMachineArn: smArn,
                input: JSON.stringify({ key: "value" }),
              }),
            );
            assert.ok(resp.executionArn, "StartExecution: missing executionArn");
            (ctx as Record<string, unknown>)["_execArn"] = resp.executionArn;
          },
        },
        {
          name: "DeleteStateMachine",
          fn: async (ctx) => {
            const { sfn } = makeClients(ctx);
            const smArn = (ctx as Record<string, unknown>)["_smArn"] as string;
            if (!smArn) return;
            await sfn.send(
              new DeleteStateMachineCommand({ stateMachineArn: smArn }),
            );
            const resp = await sfn.send(new ListStateMachinesCommand({}));
            assert.notStrictEqual(resp.stateMachines?.some((s) => s.stateMachineArn, smArn), `DeleteStateMachine: ${smArn} still present after delete`);
          },
        },
      ],
      teardown: async (ctx) => {
        const { sfn } = makeClients(ctx);
        const smArn = (ctx as Record<string, unknown>)["_smArn"] as string;
        if (!smArn) return;
        try {
          await sfn.send(
            new DeleteStateMachineCommand({ stateMachineArn: smArn }),
          );
        } catch {}
      },
    },
  ];
}
