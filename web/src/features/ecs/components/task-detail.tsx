import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { Copy, ChevronDown, ChevronRight } from "lucide-react"
import { useNavigate } from "@tanstack/react-router"
import { ecsTasksQueryOptions } from "@/features/ecs/data"
import { PageHeader, Spinner, Breadcrumb } from "@/components/ui/primitives"
import { ApplicationOwnershipBanner } from "@/components/application-ownership-banner"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { useToast } from "@/components/ui/toast"
import type { EcsContainer } from "@/types"

export function TaskDetail({ clusterName, taskId }: { clusterName: string; taskId: string }) {
  const navigate = useNavigate()
  const { toast } = useToast()

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

  const copyToClipboard = (text: string) => {
    void navigator.clipboard.writeText(text)
    toast({ title: "Copied!", variant: "success" })
  }

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
        breadcrumb={
          <Breadcrumb
            items={[
              { label: "ECS Clusters", onClick: () => navigate({ to: "/ecs" }) },
              {
                label: clusterName,
                onClick: () => navigate({ to: "/ecs/$cluster", params: { cluster: clusterName } }),
              },
              { label: "Tasks" },
              { label: shortId },
            ]}
          />
        }
      />

      <ApplicationOwnershipBanner candidates={[task.taskArn, task.taskDefinitionArn]} />

      {/* Overview */}
      <section className="space-y-3">
        <h2 className="text-sm font-semibold text-fg">Overview</h2>
        <div className="grid grid-cols-2 gap-x-8 gap-y-3 sm:grid-cols-3">
          <InfoRow label="Task ID" value={taskId} />
          <InfoRow label="Status" value={task.lastStatus} />
          <InfoRow label="Desired Status" value={task.desiredStatus} />
          <InfoRow label="Task Definition" value={shortTaskDef(task.taskDefinitionArn)} />
          <InfoRow label="Launch Type" value={task.launchType ?? "—"} />
          <InfoRow label="Started At" value={task.startedAt ? new Date(task.startedAt).toLocaleString() : "—"} />
          {task.stoppedAt && (
            <InfoRow label="Stopped At" value={new Date(task.stoppedAt).toLocaleString()} />
          )}
          {task.stoppedReason && <InfoRow label="Stop Reason" value={task.stoppedReason} />}
        </div>
      </section>

      {/* Containers */}
      <section className="space-y-3">
        <h2 className="text-sm font-semibold text-fg">Containers</h2>
        {task.containers.length === 0 ? (
          <p className="text-sm text-fg-muted">No containers</p>
        ) : (
          <div className="space-y-3">
            {task.containers.map((c) => (
              <ContainerCard key={c.name} container={c} onCopy={copyToClipboard} />
            ))}
          </div>
        )}
      </section>
    </div>
  )
}

function ContainerCard({
  container,
  onCopy,
}: {
  container: EcsContainer
  onCopy: (text: string) => void
}) {
  const [envExpanded, setEnvExpanded] = useState(false)

  return (
    <div className="rounded border border-border bg-bg p-4 space-y-3">
      <div className="flex items-center gap-3">
        <span className="font-medium text-sm">{container.name}</span>
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
          <p className="text-xs font-medium text-fg-muted">Port Mappings</p>
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
                    <Button
                      size="icon"
                      variant="ghost"
                      className="h-5 w-5"
                      onClick={() => onCopy(String(b.hostPort))}
                    >
                      <Copy className="h-3 w-3" />
                    </Button>
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
        {envExpanded ? (
          <ChevronDown className="h-3 w-3" />
        ) : (
          <ChevronRight className="h-3 w-3" />
        )}
        Environment
      </button>
      {envExpanded && (
        <p className="text-xs text-fg-muted pl-4">
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

function InfoRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-xs text-fg-muted">{label}</span>
      <span className="text-sm text-fg">{value}</span>
    </div>
  )
}

function shortTaskDef(arn: string) {
  const parts = arn.split("/")
  return parts.length > 1 ? parts[parts.length - 1] : arn
}
