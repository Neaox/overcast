import { renderWithData, screen } from "@/test/render"
import {
  ecsClusterDetailQueryOptions,
  ecsTaskDefinitionFamiliesQueryOptions,
  ecsTaskDefinitionsQueryOptions,
  ecsTasksQueryOptions,
} from "@/features/ecs/data"
import type { EcsCluster, EcsTask } from "@/types"
import { ClusterDetail } from "./cluster-detail"

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children }: { children: React.ReactNode }) => <a>{children}</a>,
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

    await user.click(screen.getByRole("button", { name: "Stopped" }))

    expect(screen.getByText("stopped-task")).toBeInTheDocument()
    expect(screen.queryByText("running-task")).not.toBeInTheDocument()
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
