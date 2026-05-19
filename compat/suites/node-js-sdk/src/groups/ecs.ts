/**
 * groups/ecs.ts — ECS compatibility test groups for the Node.js suite.
 *
 * Status: NOT implemented in Overcast. All tests expected to fail with 501.
 * These tests define the coverage target for future ECS implementation.
 *
 * Groups:
 *   ecs-clusters — cluster and task definition lifecycle
 *   ecs-tasks    — task run/describe/list/stop lifecycle
 *   ecs-services — service create/describe/list/update/delete lifecycle
 */

import {
  CreateClusterCommand,
  DeleteClusterCommand,
  DescribeClustersCommand,
  ListClustersCommand,
  RegisterTaskDefinitionCommand,
  DeregisterTaskDefinitionCommand,
  ListTaskDefinitionsCommand,
  RunTaskCommand,
  DescribeTasksCommand,
  ListTasksCommand,
  StopTaskCommand,
  CreateServiceCommand,
  DescribeServicesCommand,
  ListServicesCommand,
  UpdateServiceCommand,
  DeleteServiceCommand,
  NetworkMode,
  LaunchType,
} from "@aws-sdk/client-ecs";
import { makeClients } from "../lib/clients.js";
import type { TestGroup } from "../lib/harness.js";
import * as assert from "node:assert/strict";

export function makeECSGroups(suite: string): TestGroup[] {
  return [
    // ── ecs-clusters ───────────────────────────────────────────────────────
    {
      suite,
      service: "ecs",
      name: "ecs-clusters",
      tests: [
        {
          name: "CreateCluster",
          fn: async (ctx) => {
            const { ecs } = makeClients(ctx);
            const clusterName = `compat-${ctx.runId}`;
            const resp = await ecs.send(
              new CreateClusterCommand({ clusterName }),
            );
            assert.ok(resp.cluster?.clusterArn, "CreateCluster: missing clusterArn");
            (ctx as Record<string, unknown>)["_clusterName"] = clusterName;
          },
        },
        {
          name: "DescribeClusters",
          fn: async (ctx) => {
            const { ecs } = makeClients(ctx);
            const clusterName = (ctx as Record<string, unknown>)[
              "_clusterName"
            ] as string;
            assert.ok(clusterName, "DescribeClusters: no cluster from CreateCluster");
            const resp = await ecs.send(
              new DescribeClustersCommand({ clusters: [clusterName] }),
            );
            assert.ok(resp.clusters?.length, "DescribeClusters: no clusters returned");
          },
        },
        {
          name: "ListClusters",
          fn: async (ctx) => {
            const { ecs } = makeClients(ctx);
            await ecs.send(new ListClustersCommand({}));
          },
        },
        {
          name: "RegisterTaskDefinition",
          fn: async (ctx) => {
            const { ecs } = makeClients(ctx);
            const family = `compat-${ctx.runId}`;
            const resp = await ecs.send(
              new RegisterTaskDefinitionCommand({
                family,
                networkMode: NetworkMode.AWSVPC,
                requiresCompatibilities: [LaunchType.FARGATE],
                cpu: "256",
                memory: "512",
                containerDefinitions: [
                  {
                    name: "app",
                    image: "public.ecr.aws/nginx/nginx:latest",
                    essential: true,
                  },
                ],
              }),
            );
            assert.ok(resp.taskDefinition?.taskDefinitionArn, "RegisterTaskDefinition: missing taskDefinitionArn");
            (ctx as Record<string, unknown>)["_taskDefArn"] =
              resp.taskDefinition.taskDefinitionArn;
          },
        },
        {
          name: "ListTaskDefinitions",
          fn: async (ctx) => {
            const { ecs } = makeClients(ctx);
            await ecs.send(new ListTaskDefinitionsCommand({}));
          },
        },
        {
          name: "DeregisterTaskDefinition",
          fn: async (ctx) => {
            const { ecs } = makeClients(ctx);
            const taskDefArn = (ctx as Record<string, unknown>)[
              "_taskDefArn"
            ] as string;
            if (!taskDefArn) return;
            await ecs.send(
              new DeregisterTaskDefinitionCommand({
                taskDefinition: taskDefArn,
              }),
            );
          },
        },
        {
          name: "DeleteCluster",
          fn: async (ctx) => {
            const { ecs } = makeClients(ctx);
            const clusterName = (ctx as Record<string, unknown>)[
              "_clusterName"
            ] as string;
            if (!clusterName) return;
            await ecs.send(new DeleteClusterCommand({ cluster: clusterName }));
          },
        },
      ],
      teardown: async (ctx) => {
        const { ecs } = makeClients(ctx);
        const taskDefArn = (ctx as Record<string, unknown>)[
          "_taskDefArn"
        ] as string;
        if (taskDefArn) {
          try {
            await ecs.send(
              new DeregisterTaskDefinitionCommand({
                taskDefinition: taskDefArn,
              }),
            );
          } catch {}
        }
        const clusterName = (ctx as Record<string, unknown>)[
          "_clusterName"
        ] as string;
        if (clusterName) {
          try {
            await ecs.send(new DeleteClusterCommand({ cluster: clusterName }));
          } catch {}
        }
      },
    },

    // ── ecs-tasks ──────────────────────────────────────────────────────────
    {
      suite,
      service: "ecs",
      name: "ecs-tasks",
      tests: [
        {
          name: "RunTask",
          fn: async (ctx) => {
            const { ecs } = makeClients(ctx);
            const clusterName = (ctx as Record<string, unknown>)[
              "_taskCluster"
            ] as string;
            const taskDefArn = (ctx as Record<string, unknown>)[
              "_taskTaskDefArn"
            ] as string;
            assert.ok(clusterName, "RunTask: no cluster from setup");
            assert.ok(taskDefArn, "RunTask: no task definition from setup");
            const resp = await ecs.send(
              new RunTaskCommand({
                cluster: clusterName,
                taskDefinition: taskDefArn,
                launchType: LaunchType.FARGATE,
                networkConfiguration: {
                  awsvpcConfiguration: {
                    subnets: ["subnet-00000000"],
                    assignPublicIp: "DISABLED",
                  },
                },
              }),
            );
            assert.ok(resp.tasks?.length, "RunTask: no tasks returned");
            assert.ok(resp.tasks[0].taskArn, "RunTask: missing taskArn");
            (ctx as Record<string, unknown>)["_taskArn"] =
              resp.tasks[0].taskArn;
          },
        },
        {
          name: "DescribeTasks",
          fn: async (ctx) => {
            const { ecs } = makeClients(ctx);
            const clusterName = (ctx as Record<string, unknown>)[
              "_taskCluster"
            ] as string;
            const taskArn = (ctx as Record<string, unknown>)[
              "_taskArn"
            ] as string;
            assert.ok(taskArn, "DescribeTasks: no task from RunTask");
            const resp = await ecs.send(
              new DescribeTasksCommand({
                cluster: clusterName,
                tasks: [taskArn],
              }),
            );
            assert.ok(resp.tasks?.length, "DescribeTasks: no tasks returned");
            assert.ok(
              resp.tasks[0].taskDefinitionArn,
              "DescribeTasks: missing taskDefinitionArn",
            );
          },
        },
        {
          name: "ListTasks",
          fn: async (ctx) => {
            const { ecs } = makeClients(ctx);
            const clusterName = (ctx as Record<string, unknown>)[
              "_taskCluster"
            ] as string;
            const taskArn = (ctx as Record<string, unknown>)[
              "_taskArn"
            ] as string;
            const resp = await ecs.send(
              new ListTasksCommand({ cluster: clusterName }),
            );
            assert.ok(resp.taskArns?.length, "ListTasks: empty taskArns");
            assert.ok(
              resp.taskArns.some((a) => a === taskArn),
              "ListTasks: running task not found in list",
            );
          },
        },
        {
          name: "StopTask",
          fn: async (ctx) => {
            const { ecs } = makeClients(ctx);
            const clusterName = (ctx as Record<string, unknown>)[
              "_taskCluster"
            ] as string;
            const taskArn = (ctx as Record<string, unknown>)[
              "_taskArn"
            ] as string;
            assert.ok(taskArn, "StopTask: no task from RunTask");
            const resp = await ecs.send(
              new StopTaskCommand({
                cluster: clusterName,
                task: taskArn,
                reason: "compat test cleanup",
              }),
            );
            assert.ok(resp.task, "StopTask: missing task in response");
            assert.strictEqual(
              resp.task.desiredStatus,
              "STOPPED",
              `StopTask: expected desiredStatus STOPPED, got ${resp.task.desiredStatus}`,
            );
          },
        },
      ],
      setup: async (ctx) => {
        const { ecs } = makeClients(ctx);
        const clusterName = `compat-task-${ctx.runId}`;
        await ecs.send(new CreateClusterCommand({ clusterName }));
        (ctx as Record<string, unknown>)["_taskCluster"] = clusterName;
        const family = `compat-task-${ctx.runId}`;
        const resp = await ecs.send(
          new RegisterTaskDefinitionCommand({
            family,
            networkMode: NetworkMode.AWSVPC,
            requiresCompatibilities: [LaunchType.FARGATE],
            cpu: "256",
            memory: "512",
            containerDefinitions: [
              {
                name: "app",
                image: "public.ecr.aws/nginx/nginx:latest",
                essential: true,
              },
            ],
          }),
        );
        (ctx as Record<string, unknown>)["_taskTaskDefArn"] =
          resp.taskDefinition!.taskDefinitionArn!;
      },
      teardown: async (ctx) => {
        const { ecs } = makeClients(ctx);
        const clusterName = (ctx as Record<string, unknown>)[
          "_taskCluster"
        ] as string;
        const taskArn = (ctx as Record<string, unknown>)[
          "_taskArn"
        ] as string;
        if (taskArn && clusterName) {
          try {
            await ecs.send(
              new StopTaskCommand({
                cluster: clusterName,
                task: taskArn,
                reason: "compat teardown",
              }),
            );
          } catch {}
        }
        const taskDefArn = (ctx as Record<string, unknown>)[
          "_taskTaskDefArn"
        ] as string;
        if (taskDefArn) {
          try {
            await ecs.send(
              new DeregisterTaskDefinitionCommand({
                taskDefinition: taskDefArn,
              }),
            );
          } catch {}
        }
        if (clusterName) {
          try {
            await ecs.send(
              new DeleteClusterCommand({ cluster: clusterName }),
            );
          } catch {}
        }
      },
    },

    // ── ecs-services ───────────────────────────────────────────────────────
    {
      suite,
      service: "ecs",
      name: "ecs-services",
      tests: [
        {
          name: "CreateService",
          fn: async (ctx) => {
            const { ecs } = makeClients(ctx);
            const clusterName = (ctx as Record<string, unknown>)[
              "_svcCluster"
            ] as string;
            const taskDefArn = (ctx as Record<string, unknown>)[
              "_svcTaskDefArn"
            ] as string;
            assert.ok(clusterName, "CreateService: no cluster from setup");
            assert.ok(taskDefArn, "CreateService: no task definition from setup");
            const serviceName = `compat-svc-${ctx.runId}`;
            const resp = await ecs.send(
              new CreateServiceCommand({
                cluster: clusterName,
                serviceName,
                taskDefinition: taskDefArn,
                desiredCount: 1,
                launchType: LaunchType.FARGATE,
                networkConfiguration: {
                  awsvpcConfiguration: {
                    subnets: ["subnet-00000000"],
                    assignPublicIp: "DISABLED",
                  },
                },
              }),
            );
            assert.ok(
              resp.service?.serviceArn,
              "CreateService: missing serviceArn",
            );
            assert.strictEqual(
              resp.service?.serviceName,
              serviceName,
              `CreateService: expected serviceName ${serviceName}, got ${resp.service?.serviceName}`,
            );
            (ctx as Record<string, unknown>)["_serviceName"] = serviceName;
          },
        },
        {
          name: "DescribeServices",
          fn: async (ctx) => {
            const { ecs } = makeClients(ctx);
            const clusterName = (ctx as Record<string, unknown>)[
              "_svcCluster"
            ] as string;
            const serviceName = (ctx as Record<string, unknown>)[
              "_serviceName"
            ] as string;
            assert.ok(serviceName, "DescribeServices: no service from CreateService");
            const resp = await ecs.send(
              new DescribeServicesCommand({
                cluster: clusterName,
                services: [serviceName],
              }),
            );
            assert.ok(
              resp.services?.length,
              "DescribeServices: no services returned",
            );
            assert.strictEqual(
              resp.services[0].serviceName,
              serviceName,
              `DescribeServices: expected serviceName ${serviceName}`,
            );
            assert.ok(
              resp.services[0].desiredCount !== undefined,
              "DescribeServices: missing desiredCount",
            );
            assert.ok(
              resp.services[0].status,
              "DescribeServices: missing status",
            );
          },
        },
        {
          name: "ListServices",
          fn: async (ctx) => {
            const { ecs } = makeClients(ctx);
            const clusterName = (ctx as Record<string, unknown>)[
              "_svcCluster"
            ] as string;
            const resp = await ecs.send(
              new ListServicesCommand({ cluster: clusterName }),
            );
            assert.ok(
              resp.serviceArns?.length,
              "ListServices: empty serviceArns",
            );
          },
        },
        {
          name: "UpdateService",
          fn: async (ctx) => {
            const { ecs } = makeClients(ctx);
            const clusterName = (ctx as Record<string, unknown>)[
              "_svcCluster"
            ] as string;
            const serviceName = (ctx as Record<string, unknown>)[
              "_serviceName"
            ] as string;
            assert.ok(serviceName, "UpdateService: no service from CreateService");
            const resp = await ecs.send(
              new UpdateServiceCommand({
                cluster: clusterName,
                service: serviceName,
                desiredCount: 2,
              }),
            );
            assert.ok(resp.service, "UpdateService: missing service in response");
            assert.strictEqual(
              resp.service.desiredCount,
              2,
              `UpdateService: expected desiredCount 2, got ${resp.service.desiredCount}`,
            );
          },
        },
        {
          name: "DeleteService",
          fn: async (ctx) => {
            const { ecs } = makeClients(ctx);
            const clusterName = (ctx as Record<string, unknown>)[
              "_svcCluster"
            ] as string;
            const serviceName = (ctx as Record<string, unknown>)[
              "_serviceName"
            ] as string;
            if (!serviceName) return;
            // Must set desiredCount to 0 before deleting
            await ecs.send(
              new UpdateServiceCommand({
                cluster: clusterName,
                service: serviceName,
                desiredCount: 0,
              }),
            );
            await ecs.send(
              new DeleteServiceCommand({
                cluster: clusterName,
                service: serviceName,
              }),
            );
          },
        },
      ],
      setup: async (ctx) => {
        const { ecs } = makeClients(ctx);
        const clusterName = `compat-svc-${ctx.runId}`;
        await ecs.send(new CreateClusterCommand({ clusterName }));
        (ctx as Record<string, unknown>)["_svcCluster"] = clusterName;
        const family = `compat-svc-${ctx.runId}`;
        const resp = await ecs.send(
          new RegisterTaskDefinitionCommand({
            family,
            networkMode: NetworkMode.AWSVPC,
            requiresCompatibilities: [LaunchType.FARGATE],
            cpu: "256",
            memory: "512",
            containerDefinitions: [
              {
                name: "app",
                image: "public.ecr.aws/nginx/nginx:latest",
                essential: true,
              },
            ],
          }),
        );
        (ctx as Record<string, unknown>)["_svcTaskDefArn"] =
          resp.taskDefinition!.taskDefinitionArn!;
      },
      teardown: async (ctx) => {
        const { ecs } = makeClients(ctx);
        const clusterName = (ctx as Record<string, unknown>)[
          "_svcCluster"
        ] as string;
        const serviceName = (ctx as Record<string, unknown>)[
          "_serviceName"
        ] as string;
        if (serviceName && clusterName) {
          try {
            await ecs.send(
              new UpdateServiceCommand({
                cluster: clusterName,
                service: serviceName,
                desiredCount: 0,
              }),
            );
          } catch {}
          try {
            await ecs.send(
              new DeleteServiceCommand({
                cluster: clusterName,
                service: serviceName,
              }),
            );
          } catch {}
        }
        const taskDefArn = (ctx as Record<string, unknown>)[
          "_svcTaskDefArn"
        ] as string;
        if (taskDefArn) {
          try {
            await ecs.send(
              new DeregisterTaskDefinitionCommand({
                taskDefinition: taskDefArn,
              }),
            );
          } catch {}
        }
        if (clusterName) {
          try {
            await ecs.send(
              new DeleteClusterCommand({ cluster: clusterName }),
            );
          } catch {}
        }
      },
    },
  ];
}
