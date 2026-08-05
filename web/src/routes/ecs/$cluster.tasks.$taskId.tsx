/**
 * Route for /ecs/$cluster/tasks/$taskId.
 * Displays detailed information about a single ECS task.
 */
import { createFileRoute } from "@tanstack/react-router"
import { TaskDetail } from "@/features/ecs/components/task-detail"

type EcsTaskSearch = {
  container?: string
}

export const Route = createFileRoute("/ecs/$cluster/tasks/$taskId")({
  head: ({ params }) => ({
    meta: [{ title: `Task ${params.taskId.slice(0, 12)} — ECS — Overcast` }],
  }),
  validateSearch: (search: Record<string, unknown>): EcsTaskSearch => ({
    container: typeof search.container === "string" ? search.container : undefined,
  }),
  component: TaskDetailRoute,
})

function TaskDetailRoute() {
  const { cluster, taskId } = Route.useParams()
  const { container } = Route.useSearch()
  return <TaskDetail clusterName={cluster} taskId={taskId} initialContainerName={container} />
}
