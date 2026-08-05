import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router"
import { describe, expect, it, vi } from "vitest"
import { createTestQueryClient, render, screen } from "@/test/render"
import { routeTree } from "@/routeTree.gen"
import {
  ecsClusterDetailQueryOptions,
  ecsTaskLogsQueryOptions,
  ecsTaskQueryOptions,
} from "@/features/ecs/data"
import type { EcsCluster, EcsTask, EcsTaskLogs } from "@/types"

vi.mock("@/components/application-ownership-banner", () => ({
  ApplicationOwnershipBanner: () => null,
}))

vi.mock("@/components/layout/app-shell", () => ({
  AppShell: ({ children }: { children: React.ReactNode }) => children,
}))

describe("ECS task routing", () => {
  it("renders task detail and selected container logs below the cluster layout", async () => {
    // Given: a stopped task and its retained output are already loaded.
    const cluster: EcsCluster = {
      clusterName: "demo",
      clusterArn: "arn:aws:ecs:us-east-1:000000000000:cluster/demo",
      status: "ACTIVE",
      runningTasksCount: 0,
      pendingTasksCount: 0,
      activeServicesCount: 1,
      registeredContainerInstancesCount: 0,
    }
    const task: EcsTask = {
      taskArn: "arn:aws:ecs:us-east-1:000000000000:task/demo/failed-task",
      taskDefinitionArn: "arn:aws:ecs:us-east-1:000000000000:task-definition/app:1",
      clusterArn: cluster.clusterArn,
      lastStatus: "STOPPED",
      desiredStatus: "STOPPED",
      group: "service:website",
      containers: [{ name: "app", lastStatus: "STOPPED", exitCode: 2 }],
    }
    const logs: EcsTaskLogs = {
      taskArn: task.taskArn,
      containerName: "app",
      logs: "entrypoint syntax error",
    }
    const queryClient = createTestQueryClient()
    queryClient.setQueryData(ecsClusterDetailQueryOptions("demo").queryKey, cluster)
    queryClient.setQueryData(ecsTaskQueryOptions("demo", "failed-task").queryKey, task)
    queryClient.setQueryData(ecsTaskLogsQueryOptions(task.taskArn, "app").queryKey, logs)
    const router = createRouter({
      routeTree,
      history: createMemoryHistory({
        initialEntries: ["/ecs/demo/tasks/failed-task?container=app"],
      }),
    })

    // When: the nested task URL is rendered through the real file-route tree.
    render(<RouterProvider router={router} />, { queryClient })

    // Then: the task child replaces the cluster index and opens the requested logs.
    expect(await screen.findByRole("heading", { name: "failed-task" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Hide logs" })).toBeInTheDocument()
    expect(screen.queryByRole("tab", { name: "Services" })).not.toBeInTheDocument()
  })
})
