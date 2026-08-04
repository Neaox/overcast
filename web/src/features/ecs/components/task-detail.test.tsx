import { renderWithData, screen } from "@/test/render"
import { ecsTaskQueryOptions } from "@/features/ecs/data"
import type { EcsTask } from "@/types"
import { TaskDetail } from "./task-detail"

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
})
