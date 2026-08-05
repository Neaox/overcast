import { useQuery } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import { TriangleAlert } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { ecsTaskLogsQueryOptions } from "@/features/ecs/data"
import {
  diagnosticLogExcerpt,
  failedContainer,
  findRecentServiceFailures,
  isServiceFailureEvent,
} from "@/features/ecs/diagnostics"
import { fieldLabel } from "@/lib/typography"
import { cn } from "@/lib/utils"
import type { EcsService, EcsTask } from "@/types"

export function ServiceFailureAlert({
  clusterName,
  service,
  tasks,
  onViewStoppedTasks,
}: {
  clusterName: string
  service: EcsService
  tasks: EcsTask[]
  onViewStoppedTasks: () => void
}) {
  const failures = findRecentServiceFailures(service.serviceName, tasks)
  const latestTask = failures.at(0)
  const container = latestTask ? failedContainer(latestTask) : undefined
  const logsQuery = useQuery(
    ecsTaskLogsQueryOptions(latestTask?.taskArn ?? "", container?.name ?? "", Boolean(latestTask)),
  )
  if (!latestTask || !container) return null

  const excerpt = diagnosticLogExcerpt(logsQuery.data?.logs ?? "")
  const taskId = latestTask.taskArn.split("/").at(-1) ?? latestTask.taskArn
  const schedulerContext = service.events.find((event) => isServiceFailureEvent(event.message))
  const fallbackCause =
    container.reason ??
    (container.exitCode != null
      ? `${container.name} exited with code ${container.exitCode}`
      : undefined) ??
    latestTask.stoppedReason ??
    schedulerContext?.message

  return (
    <Card role="alert" className="border-danger/40 bg-danger-muted shadow-none">
      <CardContent className="space-y-3">
        <div className="flex items-start gap-3">
          <TriangleAlert className="mt-0.5 h-4 w-4 shrink-0 text-danger" aria-hidden="true" />
          <div className="min-w-0 flex-1 space-y-1">
            <p className="text-sm font-semibold text-fg">
              {failures.length === 1
                ? "A task failed in the last 15 minutes"
                : `${failures.length} tasks failed in the last 15 minutes`}
            </p>
            <p className="text-xs text-fg-muted">
              Latest: {container.name}
              {container.exitCode != null ? ` exited ${container.exitCode}` : " stopped"}
            </p>
          </div>
        </div>

        <div className="rounded border border-danger/30 bg-bg px-3 py-2">
          <p className={cn(fieldLabel, "mb-1 text-danger")}>Likely cause</p>
          {excerpt ? (
            <pre className="overflow-x-auto font-mono text-xs whitespace-pre-wrap text-fg">
              {excerpt}
            </pre>
          ) : (
            <p className="text-sm text-fg">
              {fallbackCause ?? "No container reason was recorded."}
            </p>
          )}
          {logsQuery.isLoading && !excerpt && (
            <p className="mt-1 text-xs text-fg-muted">Checking final container output...</p>
          )}
        </div>

        {schedulerContext && schedulerContext.message !== fallbackCause && (
          <details className="text-xs text-fg-muted">
            <summary className="cursor-pointer">Scheduler context</summary>
            <p className="mt-1 break-words">{schedulerContext.message}</p>
          </details>
        )}

        <div className="flex flex-wrap gap-2">
          <Button asChild size="sm" variant="secondary">
            <Link to="/ecs/$cluster/tasks/$taskId" params={{ cluster: clusterName, taskId }}>
              Open task and logs
            </Link>
          </Button>
          <Button size="sm" variant="ghost" onClick={onViewStoppedTasks}>
            View failed tasks
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}
