import { describe, expect, it, vi } from "vitest"
import { renderWithData, screen } from "@/test/render"
import { ecsTaskLogsQueryOptions, ecsTaskQueryOptions } from "@/features/ecs/data"
import type { EcsTask, EcsTaskLogs } from "@/types"
import { TaskDetail } from "./task-detail"

vi.mock("@tanstack/react-router", () => ({
  Link: ({
    children,
    to,
    params = {},
    search = {},
  }: {
    children: React.ReactNode
    to: string
    params?: Record<string, string>
    search?: Record<string, string>
  }) => {
    const path = Object.entries(params).reduce(
      (result, [key, value]) => result.replace(`$${key}`, value),
      to,
    )
    const query = new URLSearchParams(search).toString()
    return <a href={query ? `${path}?${query}` : path}>{children}</a>
  },
}))

vi.mock("@/components/application-ownership-banner", () => ({
  ApplicationOwnershipBanner: () => null,
}))

describe("TaskDetail", () => {
  it("renders a stopped task fetched directly by task ID", () => {
    const task: EcsTask = {
      taskArn: "arn:aws:ecs:us-east-1:000000000000:task/demo/stopped-task",
      taskDefinitionArn: "arn:aws:ecs:us-east-1:000000000000:task-definition/web:2",
      clusterArn: "arn:aws:ecs:us-east-1:000000000000:cluster/demo",
      desiredStatus: "STOPPED",
      lastStatus: "STOPPED",
      launchType: "FARGATE",
      stopCode: "ServiceSchedulerInitiated",
      stoppedReason: "Task stopped by a newer service deployment",
      containers: [],
    }

    renderWithData(<TaskDetail clusterName="demo" taskId="stopped-task" />, [
      [ecsTaskQueryOptions("demo", "stopped-task").queryKey, task],
    ])

    expect(screen.getAllByText("STOPPED").length).toBeGreaterThan(0)
    expect(screen.getByText("ServiceSchedulerInitiated")).toBeInTheDocument()
    expect(screen.getByText("Task stopped by a newer service deployment")).toBeInTheDocument()
  })

  it("opens retained output automatically for a container with a non-zero exit", () => {
    const failed = failedTask()
    const logs: EcsTaskLogs = {
      taskArn: failed.taskArn,
      containerName: "app",
      logs: "entrypoint.sh: line 18: syntax error near unexpected token `fi'",
    }

    renderWithData(<TaskDetail clusterName="demo" taskId="failed-task" />, [
      [ecsTaskQueryOptions("demo", "failed-task").queryKey, failed],
      [ecsTaskLogsQueryOptions(failed.taskArn, "app").queryKey, logs],
    ])

    expect(screen.getByText("exit: 2")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Hide logs" })).toBeInTheDocument()
    expect(screen.getByText("Container output")).toBeInTheDocument()
  })

  it("opens the container selected by a logs deep link", () => {
    const running: EcsTask = {
      ...failedTask(),
      taskArn: "arn:aws:ecs:us-east-1:123456789012:task/demo/running-task",
      lastStatus: "RUNNING",
      desiredStatus: "RUNNING",
      stopCode: undefined,
      stoppedReason: undefined,
      stoppedAt: undefined,
      group: "service:website",
      containers: [{ name: "sidecar", lastStatus: "RUNNING" }],
    }
    const logs: EcsTaskLogs = {
      taskArn: running.taskArn,
      containerName: "sidecar",
      logs: "sidecar is ready",
    }

    renderWithData(
      <TaskDetail clusterName="demo" taskId="running-task" initialContainerName="sidecar" />,
      [
        [ecsTaskQueryOptions("demo", "running-task").queryKey, running],
        [ecsTaskLogsQueryOptions(running.taskArn, "sidecar").queryKey, logs],
      ],
    )

    expect(screen.getByRole("button", { name: "Hide logs" })).toBeInTheDocument()
    expect(screen.getByText("Container output")).toBeInTheDocument()
    expect(screen.getByRole("link", { name: "website" })).toHaveAttribute(
      "href",
      "/ecs/demo?tab=services&service=website",
    )
  })
})

function failedTask(): EcsTask {
  return {
    taskArn: "arn:aws:ecs:us-east-1:123456789012:task/demo/failed-task",
    taskDefinitionArn: "arn:aws:ecs:us-east-1:123456789012:task-definition/app:2",
    clusterArn: "arn:aws:ecs:us-east-1:123456789012:cluster/demo",
    lastStatus: "STOPPED",
    desiredStatus: "STOPPED",
    stopCode: "EssentialContainerExited",
    stoppedReason: "Essential container in task exited",
    stoppedAt: "2026-08-05T06:00:00.000Z",
    containers: [{ name: "app", lastStatus: "STOPPED", exitCode: 2 }],
  }
}
