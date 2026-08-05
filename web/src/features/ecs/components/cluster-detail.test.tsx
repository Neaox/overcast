import { describe, expect, it, vi } from "vitest"
import { renderWithData, screen, within } from "@/test/render"
import {
  ecsClusterDetailQueryOptions,
  ecsTaskDefinitionFamiliesQueryOptions,
  ecsTaskDefinitionsQueryOptions,
  ecsTaskLogsQueryOptions,
  ecsTasksQueryOptions,
} from "@/features/ecs/data"
import type { EcsCluster, EcsService, EcsTask, EcsTaskLogs } from "@/types"
import { ClusterDetail } from "./cluster-detail"
import { ServiceFailureAlert } from "./service-failure-alert"

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children }: { children: React.ReactNode }) => <a href="#">{children}</a>,
}))

vi.mock("@/components/application-ownership-banner", () => ({
  ApplicationOwnershipBanner: () => null,
}))

describe("ClusterDetail tasks", () => {
  it("switches from current tasks to explicitly requested stopped tasks", async () => {
    const cluster: EcsCluster = {
      clusterName: "demo",
      clusterArn: "arn:aws:ecs:us-east-1:000000000000:cluster/demo",
      status: "ACTIVE",
      runningTasksCount: 1,
      pendingTasksCount: 0,
      activeServicesCount: 1,
      registeredContainerInstancesCount: 0,
    }
    const running = task("running-task", "RUNNING")
    const stopped = task("stopped-task", "STOPPED")

    const { user } = renderWithData(<ClusterDetail clusterName="demo" />, [
      [ecsClusterDetailQueryOptions("demo").queryKey, cluster],
      [ecsTasksQueryOptions("demo", "RUNNING").queryKey, [running]],
      [ecsTasksQueryOptions("demo", "STOPPED").queryKey, [stopped]],
      [ecsTaskDefinitionsQueryOptions().queryKey, []],
      [ecsTaskDefinitionFamiliesQueryOptions().queryKey, []],
    ])

    expect(screen.getByText("running-task")).toBeInTheDocument()
    expect(screen.queryByText("stopped-task")).not.toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: /^Stopped/ }))

    expect(screen.getByText("stopped-task")).toBeInTheDocument()
    expect(screen.queryByText("running-task")).not.toBeInTheDocument()
  })
})

describe("ServiceFailureAlert", () => {
  it("surfaces retained container output ahead of a follow-on scheduler error", async () => {
    const failed = failedTask()
    const service = failedService()
    const logs: EcsTaskLogs = {
      taskArn: failed.taskArn,
      containerName: "app",
      logs: [
        "loading configuration",
        "entrypoint.sh: line 18: syntax error near unexpected token `fi'",
      ].join("\n"),
    }

    renderWithData(
      <ServiceFailureAlert
        clusterName="demo"
        service={service}
        tasks={[failed]}
        onViewStoppedTasks={() => undefined}
      />,
      [[ecsTaskLogsQueryOptions(failed.taskArn, "app").queryKey, logs]],
    )

    const alert = await screen.findByRole("alert")
    expect(within(alert).getByText("Likely cause")).toBeInTheDocument()
    expect(within(alert).getByText(/syntax error near unexpected token/)).toBeInTheDocument()
    expect(within(alert).getByText("Scheduler context")).toBeInTheDocument()
    expect(within(alert).getByRole("link", { name: "Open task and logs" })).toBeInTheDocument()
  })
})

function task(id: string, status: string): EcsTask {
  return {
    taskArn: `arn:aws:ecs:us-east-1:000000000000:task/demo/${id}`,
    taskDefinitionArn: "arn:aws:ecs:us-east-1:000000000000:task-definition/web:1",
    clusterArn: "arn:aws:ecs:us-east-1:000000000000:cluster/demo",
    desiredStatus: status,
    lastStatus: status,
    launchType: "FARGATE",
    containers: [],
  }
}

function failedTask(): EcsTask {
  return {
    ...task("failed-task", "STOPPED"),
    group: "service:website",
    stoppedAt: new Date(Date.now() - 60_000).toISOString(),
    stoppedReason: "Essential container in task exited",
    stopCode: "EssentialContainerExited",
    containers: [{ name: "app", lastStatus: "STOPPED", exitCode: 2, reason: "exit 2" }],
  }
}

function failedService(): EcsService {
  return {
    serviceName: "website",
    serviceArn: "arn:aws:ecs:us-east-1:123456789012:service/demo/website",
    clusterArn: "arn:aws:ecs:us-east-1:123456789012:cluster/demo",
    taskDefinition: "app:2",
    desiredCount: 1,
    runningCount: 0,
    pendingCount: 0,
    status: "ACTIVE",
    launchType: "FARGATE",
    deployments: [],
    events: [
      {
        id: "event-1",
        createdAt: new Date().toISOString(),
        message:
          "(service website) was unable to place a task: container is marked for removal and cannot be connected to the network",
      },
    ],
    createdAt: new Date().toISOString(),
  }
}
