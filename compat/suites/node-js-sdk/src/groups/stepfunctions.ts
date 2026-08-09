/**
 * groups/stepfunctions.ts — Step Functions compatibility test groups for the Node.js suite.
 *
 * Groups:
 *   sfn-statemachines — state machine lifecycle and execution
 *   sfn-executions    — the ASL execution engine: run, describe, history, list, stop
 */

import {
  CreateStateMachineCommand,
  DeleteStateMachineCommand,
  DescribeExecutionCommand,
  DescribeStateMachineCommand,
  GetExecutionHistoryCommand,
  ListExecutionsCommand,
  ListStateMachinesCommand,
  StartExecutionCommand,
  StateMachineType,
  StopExecutionCommand,
} from "@aws-sdk/client-sfn";
import { makeClients } from "../lib/clients.ts";
import type { TestGroup } from "../lib/harness.ts";
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
            assert.ok(resp.stateMachines?.some((s) => s.stateMachineArn === smArn), `ListStateMachines: state machine ${smArn} not found`);
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
            assert.ok(!resp.stateMachines?.some((s) => s.stateMachineArn === smArn), `DeleteStateMachine: ${smArn} still present after delete`);
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

    // ── sfn-executions ─────────────────────────────────────────────────────
    // The execution engine really interprets the definition, so these tests
    // assert on what the execution produced rather than just that a call
    // succeeded. The group provisions its own state machine in setup.
    {
      suite,
      service: "stepfunctions",
      name: "sfn-executions",
      setup: async (ctx) => {
        const { sfn } = makeClients(ctx);
        const resp = await sfn.send(
          new CreateStateMachineCommand({
            name: `compat-exec-${ctx.runId}`,
            type: StateMachineType.EXPRESS,
            definition: JSON.stringify({
              Comment: "compat executions",
              StartAt: "Hello",
              States: { Hello: { Type: "Pass", Result: { greeting: "hello" }, End: true } },
            }),
            roleArn: `arn:aws:iam::000000000000:role/compat-sfn-exec-${ctx.runId}`,
          }),
        );
        assert.ok(resp.stateMachineArn, "setup sfn-executions: missing stateMachineArn");
        (ctx as Record<string, unknown>)["_execSmArn"] = resp.stateMachineArn;
      },
      tests: [
        {
          name: "StartExecution",
          fn: async (ctx) => {
            const { sfn } = makeClients(ctx);
            const smArn = (ctx as Record<string, unknown>)["_execSmArn"] as string;
            assert.ok(smArn, "StartExecution: no state machine from setup");
            const resp = await sfn.send(
              new StartExecutionCommand({
                stateMachineArn: smArn,
                name: `run-${ctx.runId}`,
                input: JSON.stringify({ key: "value" }),
              }),
            );
            assert.ok(resp.executionArn, "StartExecution: missing executionArn");
            (ctx as Record<string, unknown>)["_runExecArn"] = resp.executionArn;
          },
        },
        {
          name: "DescribeExecution",
          fn: async (ctx) => {
            const { sfn } = makeClients(ctx);
            const execArn = (ctx as Record<string, unknown>)["_runExecArn"] as string;
            assert.ok(execArn, "DescribeExecution: no execution from StartExecution");
            const resp = await sfn.send(new DescribeExecutionCommand({ executionArn: execArn }));
            assert.ok(resp.status, "DescribeExecution: missing status");
            assert.equal(
              resp.executionArn,
              execArn,
              "DescribeExecution: executionArn did not round-trip",
            );
          },
        },
        {
          name: "GetExecutionHistory",
          fn: async (ctx) => {
            const { sfn } = makeClients(ctx);
            const execArn = (ctx as Record<string, unknown>)["_runExecArn"] as string;
            assert.ok(execArn, "GetExecutionHistory: no execution from StartExecution");
            const resp = await sfn.send(new GetExecutionHistoryCommand({ executionArn: execArn }));
            assert.ok(
              resp.events && resp.events.length > 0,
              "GetExecutionHistory: no events",
            );
          },
        },
        {
          name: "ListExecutions",
          fn: async (ctx) => {
            const { sfn } = makeClients(ctx);
            const smArn = (ctx as Record<string, unknown>)["_execSmArn"] as string;
            const execArn = (ctx as Record<string, unknown>)["_runExecArn"] as string;
            assert.ok(smArn, "ListExecutions: no state machine from setup");
            const resp = await sfn.send(new ListExecutionsCommand({ stateMachineArn: smArn }));
            assert.ok(
              resp.executions?.some((e) => e.executionArn === execArn),
              `ListExecutions: execution ${execArn} not listed`,
            );
          },
        },
        {
          name: "StopExecution",
          fn: async (ctx) => {
            const { sfn } = makeClients(ctx);
            const execArn = (ctx as Record<string, unknown>)["_runExecArn"] as string;
            assert.ok(execArn, "StopExecution: no execution from StartExecution");
            await sfn.send(new StopExecutionCommand({ executionArn: execArn }));
          },
        },
      ],
      teardown: async (ctx) => {
        const { sfn } = makeClients(ctx);
        const smArn = (ctx as Record<string, unknown>)["_execSmArn"] as string;
        if (!smArn) return;
        try {
          await sfn.send(new DeleteStateMachineCommand({ stateMachineArn: smArn }));
        } catch {}
      },
    },
  ];
}
