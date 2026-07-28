import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { ChevronDown, ChevronRight } from "lucide-react"
import { ecsTasksQueryOptions } from "@/features/ecs/data"
import { PageHeader, Spinner } from "@/components/ui/primitives"
import { ApplicationOwnershipBanner } from "@/components/application-ownership-banner"
import { Badge } from "@/components/ui/badge"
import { CopyButton } from "@/components/ui/copy-button"
import { Definition, DefinitionList } from "@/components/ui/definition-card"
import type { EcsContainer } from "@/types"

export function TaskDetail({ clusterName, taskId }: { clusterName: string; taskId: string }) {
  const { data: tasks = [], isLoading } = useQuery(ecsTasksQueryOptions(clusterName))

  const task = tasks.find((t) => {
    const id = t.taskArn.split("/").pop() ?? ""
    return id === taskId
  })

  if (isLoading) {
    return (
      <div className="flex justify-center py-32">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }

  if (!task) {
    return (
      <div className="flex justify-center py-32">
        <p className="text-sm text-fg-muted">Task not found</p>
      </div>
    )
  }

  const shortId = taskId.slice(0, 12)

  return (
    <div className="flex w-full flex-col gap-6">
      <PageHeader
        title={shortId}
        description={
          <span className="flex items-center gap-2">
            <StatusBadge status={task.lastStatus} />
            <span className="text-fg-muted">{shortTaskDef(task.taskDefinitionArn)}</span>
          </span>
        }
      />

      <ApplicationOwnershipBanner candidates={[task.taskArn, task.taskDefinitionArn]} />

      {/* Overview */}
      <section className="space-y-3">
        <h2 className="font-mono text-sm font-semibold text-fg">Overview</h2>
        <DefinitionList>
          <Definition label="Task ID" value={taskId} />
          <Definition label="Status" value={task.lastStatus} />
          <Definition label="Desired Status" value={task.desiredStatus} />
          <Definition label="Task Definition" value={shortTaskDef(task.taskDefinitionArn)} />
          <Definition label="Launch Type" value={task.launchType} />
          <Definition
            label="Started At"
            value={task.startedAt ? new Date(task.startedAt).toLocaleString() : null}
          />
          {task.stoppedAt && (
            <Definition label="Stopped At" value={new Date(task.stoppedAt).toLocaleString()} />
          )}
          {task.stoppedReason && (
            <Definition label="Stop Reason" value={task.stoppedReason} variant="prose" />
          )}
        </DefinitionList>
      </section>

      {/* Containers */}
      <section className="space-y-3">
        <h2 className="font-mono text-sm font-semibold text-fg">Containers</h2>
        {task.containers.length === 0 ? (
          <p className="text-sm text-fg-muted">No containers</p>
        ) : (
          <div className="space-y-3">
            {task.containers.map((c) => (
              <ContainerCard key={c.name} container={c} />
            ))}
          </div>
        )}
      </section>
    </div>
  )
}

function ContainerCard({ container }: { container: EcsContainer }) {
  const [envExpanded, setEnvExpanded] = useState(false)

  return (
    <div className="space-y-3 rounded border border-border bg-bg p-4">
      <div className="flex items-center gap-3">
        <span className="text-sm font-medium">{container.name}</span>
        <span className="text-xs text-fg-muted">{container.image ?? "—"}</span>
        <StatusBadge status={container.lastStatus} />
        {container.exitCode != null && (
          <Badge variant={container.exitCode === 0 ? "default" : "danger"}>
            exit: {container.exitCode}
          </Badge>
        )}
      </div>

      {/* Port mappings */}
      {container.networkBindings && container.networkBindings.length > 0 && (
        <div className="space-y-1">
          <p className="font-mono text-xs font-medium text-fg-muted">Port Mappings</p>
          <div className="flex flex-wrap gap-2">
            {container.networkBindings.map((b, i) => {
              const label = `${b.hostPort ?? "—"}:${b.containerPort ?? "—"}`
              return (
                <div
                  key={i}
                  className="flex items-center gap-1 rounded bg-bg-muted px-2 py-1 font-mono text-xs"
                >
                  <span>{label}</span>
                  {b.hostPort != null && (
                    <CopyButton value={String(b.hostPort)} noun="host port" tone="inline" />
                  )}
                </div>
              )
            })}
          </div>
        </div>
      )}

      {/* Environment variables — collapsible placeholder */}
      {/* ECS DescribeTasks doesn't return env vars directly, but we show the section if available */}
      <button
        className="flex items-center gap-1 text-xs text-fg-muted hover:text-fg"
        onClick={() => setEnvExpanded(!envExpanded)}
      >
        {envExpanded ? <ChevronDown className="h-3 w-3" /> : <ChevronRight className="h-3 w-3" />}
        Environment
      </button>
      {envExpanded && (
        <p className="pl-4 text-xs text-fg-muted">
          Environment variables are defined in the task definition.
        </p>
      )}
    </div>
  )
}

function StatusBadge({ status }: { status: string }) {
  const variant =
    status === "RUNNING" || status === "ACTIVE"
      ? "success"
      : status === "PROVISIONING" || status === "PENDING"
        ? "warning"
        : status === "STOPPED" || status === "INACTIVE" || status === "DEPROVISIONING"
          ? "danger"
          : "default"
  return <Badge variant={variant}>{status}</Badge>
}

function shortTaskDef(arn: string) {
  const parts = arn.split("/")
  return parts.length > 1 ? parts[parts.length - 1] : arn
}
