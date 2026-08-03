import { Fragment, useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { Play, Square, Plus, RefreshCw, Trash2, Pencil } from "lucide-react"
import {
  ecsClusterDetailQueryOptions,
  ecsTaskDefinitionsQueryOptions,
  ecsTasksQueryOptions,
  ecsServicesQueryOptions,
  ecsContainerInstancesQueryOptions,
  ecsKeys,
  ecsTagsQueryOptions,
  ecsTaskDefinitionFamiliesQueryOptions,
  runTaskMutationOptions,
  stopTaskMutationOptions,
  registerTaskDefinitionMutationOptions,
  deregisterTaskDefinitionMutationOptions,
  createServiceMutationOptions,
  updateServiceMutationOptions,
  deleteServiceMutationOptions,
} from "@/features/ecs/data"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { PageHeader, Spinner, EmptyState } from "@/components/ui/primitives"
import { ApplicationOwnershipBanner } from "@/components/application-ownership-banner"
import { Badge } from "@/components/ui/badge"
import { Tabs, TabList, Tab, TabPanel } from "@/components/ui/tabs"
import { Textarea } from "@/components/ui/textarea"
import { Link } from "@tanstack/react-router"
import { AwsvpcFields } from "@/features/ecs/components/awsvpc-fields"
import { emptyAwsvpcNetworking, type AwsvpcNetworking } from "@/features/ecs/awsvpc"
import type { EcsTask, EcsTaskDefinition, EcsService, EcsContainerInstance } from "@/types"
import { fieldLabel } from "@/lib/typography"
import { cn } from "@/lib/utils"

export function ClusterDetail({ clusterName }: { clusterName: string }) {
  const [activeTab, setActiveTab] = useState("tasks")

  const { data: cluster, isLoading } = useQuery(ecsClusterDetailQueryOptions(clusterName))

  if (isLoading) {
    return (
      <div className="flex justify-center py-32">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }

  if (!cluster) return null

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title={cluster.clusterName}
        description={
          <span className="flex items-center gap-2">
            <TaskStatusBadge status={cluster.status} />
            <span className="text-fg-muted">
              {cluster.runningTasksCount} running · {cluster.activeServicesCount} services ·{" "}
              {cluster.pendingTasksCount} pending
            </span>
          </span>
        }
      />

      <ApplicationOwnershipBanner candidates={[cluster.clusterArn, cluster.clusterName]} />

      <Tabs selectedKey={activeTab} onSelectionChange={setActiveTab}>
        <TabList>
          <Tab id="tasks">Tasks</Tab>
          <Tab id="services">Services</Tab>
          <Tab id="container-instances">Container Instances</Tab>
          <Tab id="task-definitions">Task Definitions</Tab>
          <Tab id="tags">Tags</Tab>
        </TabList>

        <TabPanel id="tasks" className="pt-4">
          <TasksPanel clusterName={clusterName} />
        </TabPanel>

        <TabPanel id="services" className="pt-4">
          <ServicesPanel clusterName={clusterName} />
        </TabPanel>

        <TabPanel id="container-instances" className="pt-4">
          <ContainerInstancesPanel clusterName={clusterName} />
        </TabPanel>

        <TabPanel id="task-definitions" className="pt-4">
          <TaskDefinitionsPanel />
        </TabPanel>

        <TabPanel id="tags" className="pt-4">
          <ClusterTagsPanel clusterArn={cluster.clusterArn} />
        </TabPanel>
      </Tabs>
    </div>
  )
}

// ─── Tasks panel ──────────────────────────────────────────────────────────

function TasksPanel({ clusterName }: { clusterName: string }) {
  const [showRunTask, setShowRunTask] = useState(false)
  const [expandedTask, setExpandedTask] = useState<string>()

  const {
    data: tasks = [],
    isLoading,
    isFetching,
    refetch,
  } = useQuery(ecsTasksQueryOptions(clusterName))
  const { data: taskDefs = [] } = useQuery(ecsTaskDefinitionsQueryOptions())

  const runMut = useResourceMutation({
    options: runTaskMutationOptions(),
    invalidateKeys: [ecsKeys.tasks(clusterName), ecsKeys.clusterDetail(clusterName)],
    successTitle: "Task started",
    onSuccess: () => setShowRunTask(false),
  })

  const stopMut = useResourceMutation({
    options: stopTaskMutationOptions(),
    invalidateKeys: [ecsKeys.tasks(clusterName), ecsKeys.clusterDetail(clusterName)],
    successTitle: "Task stopped",
    successVariant: "default",
  })

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <Button size="sm" variant="ghost" onClick={() => refetch()} disabled={isFetching}>
          <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} />
          Refresh
        </Button>
        <Button size="sm" onClick={() => setShowRunTask(true)}>
          <Play className="mr-1.5 h-3.5 w-3.5" />
          Run Task
        </Button>
      </div>

      {isLoading ? (
        <div className="flex justify-center py-12">
          <Spinner className="h-6 w-6" />
        </div>
      ) : tasks.length === 0 ? (
        <EmptyState
          title="No tasks"
          description="Run a task to get started."
          action={
            <Button size="sm" onClick={() => setShowRunTask(true)}>
              <Play className="mr-1.5 h-3.5 w-3.5" />
              Run Task
            </Button>
          }
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Task ID</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Task Definition</TableHead>
              <TableHead>Launch Type</TableHead>
              <TableHead>Created</TableHead>
              <TableHead className="w-10" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {tasks.map((t) => {
              const shortId = t.taskArn.split("/").pop() ?? t.taskArn
              const isExpanded = expandedTask === t.taskArn
              return (
                <Fragment key={t.taskArn}>
                  <TableRow
                    className="cursor-pointer"
                    onClick={() => setExpandedTask(isExpanded ? undefined : t.taskArn)}
                  >
                    <TableCell>
                      <Link
                        to="/ecs/$cluster/tasks/$taskId"
                        params={{ cluster: clusterName, taskId: shortId }}
                        className="text-accent hover:underline"
                        onClick={(e) => e.stopPropagation()}
                      >
                        {shortId.slice(0, 12)}
                      </Link>
                    </TableCell>
                    <TableCell>
                      <TaskStatusBadge status={t.lastStatus} />
                    </TableCell>
                    <TableCell className="max-w-xs truncate text-fg-muted">
                      {shortTaskDef(t.taskDefinitionArn)}
                    </TableCell>
                    <TableCell>{t.launchType ?? "—"}</TableCell>
                    <TableCell className="text-fg-muted">
                      {t.createdAt ? new Date(t.createdAt).toLocaleString() : "—"}
                    </TableCell>
                    <TableCell>
                      {t.lastStatus !== "STOPPED" && (
                        <Button
                          size="icon"
                          variant="ghost"
                          className="text-fg-muted hover:text-danger"
                          onClick={(e) => {
                            e.stopPropagation()
                            stopMut.mutate({ cluster: clusterName, task: t.taskArn })
                          }}
                        >
                          <Square className="h-3.5 w-3.5" />
                        </Button>
                      )}
                    </TableCell>
                  </TableRow>
                  {isExpanded && (
                    <TableRow key={`${t.taskArn}-containers`}>
                      <TableCell colSpan={6} className="bg-bg-muted p-4">
                        <ContainersList containers={t.containers} />
                      </TableCell>
                    </TableRow>
                  )}
                </Fragment>
              )
            })}
          </TableBody>
        </Table>
      )}

      <RunTaskDialog
        open={showRunTask}
        onClose={() => setShowRunTask(false)}
        isPending={runMut.isPending}
        taskDefs={taskDefs}
        onSubmit={(taskDef, count, launchType, networking) =>
          runMut.mutate({
            cluster: clusterName,
            taskDefinition: taskDef,
            count,
            launchType,
            subnets: networking.subnets,
            securityGroups: networking.securityGroups,
            assignPublicIp: networking.assignPublicIp,
          })
        }
      />
    </div>
  )
}

function ContainersList({ containers }: { containers: EcsTask["containers"] }) {
  if (containers.length === 0) {
    return <p className="text-sm text-fg-muted">No containers</p>
  }
  return (
    <div className="space-y-2">
      <p className="font-mono text-xs font-medium text-fg-muted">Containers</p>
      {containers.map((c) => (
        <div
          key={c.name}
          className="flex items-center gap-3 rounded border border-border bg-bg p-2 text-sm"
        >
          <span className="font-medium">{c.name}</span>
          <span className="text-xs text-fg-muted">{c.image}</span>
          <TaskStatusBadge status={c.lastStatus} />
          {c.exitCode != null && (
            <Badge variant={c.exitCode === 0 ? "default" : "danger"}>exit: {c.exitCode}</Badge>
          )}
        </div>
      ))}
    </div>
  )
}

// ─── Task Definitions panel ───────────────────────────────────────────────

function TaskDefinitionsPanel() {
  const [showRegister, setShowRegister] = useState(false)
  const [expandedFamily, setExpandedFamily] = useState<string>()

  const {
    data: taskDefs = [],
    isLoading,
    isFetching,
    refetch,
  } = useQuery(ecsTaskDefinitionsQueryOptions())

  const { data: families = [] } = useQuery(ecsTaskDefinitionFamiliesQueryOptions())

  const registerMut = useResourceMutation({
    options: registerTaskDefinitionMutationOptions(),
    invalidateKeys: [ecsKeys.taskDefinitions()],
    successTitle: "Task definition registered",
    onSuccess: () => setShowRegister(false),
  })

  const deregisterMut = useResourceMutation({
    options: deregisterTaskDefinitionMutationOptions(),
    invalidateKeys: [ecsKeys.taskDefinitions()],
    successTitle: "Task definition deregistered",
    successVariant: "default",
  })

  // Group task definitions by family
  const byFamily = families.map((family) => ({
    family,
    revisions: taskDefs
      .filter((td) => td.family === family)
      .sort((a, b) => b.revision - a.revision),
  }))

  // Include any task defs whose family isn't in the families list
  const knownFamilies = new Set(families)
  const ungrouped = taskDefs.filter((td) => !knownFamilies.has(td.family))

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <Button size="sm" variant="ghost" onClick={() => refetch()} disabled={isFetching}>
          <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} />
          Refresh
        </Button>
        <Button size="sm" onClick={() => setShowRegister(true)}>
          <Plus className="mr-1.5 h-3.5 w-3.5" />
          Register Task Definition
        </Button>
      </div>

      {isLoading ? (
        <div className="flex justify-center py-12">
          <Spinner className="h-6 w-6" />
        </div>
      ) : taskDefs.length === 0 ? (
        <EmptyState
          title="No task definitions"
          description="Register a task definition to define your containers."
          action={
            <Button size="sm" onClick={() => setShowRegister(true)}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Register Task Definition
            </Button>
          }
        />
      ) : (
        <div className="flex flex-col gap-2">
          {byFamily.map(({ family, revisions }) => (
            <div key={family} className="overflow-hidden rounded-md border border-border">
              <button
                className="flex w-full items-center justify-between px-4 py-2.5 text-left text-sm font-medium transition-colors hover:bg-accent-muted"
                onClick={() => setExpandedFamily(expandedFamily === family ? undefined : family)}
              >
                <span>{family}</span>
                <span className="flex items-center gap-2 text-xs text-fg-muted">
                  <Badge variant="default">
                    {revisions.length} revision{revisions.length !== 1 ? "s" : ""}
                  </Badge>
                  <span>{expandedFamily === family ? "▲" : "▼"}</span>
                </span>
              </button>

              {expandedFamily === family && (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Revision</TableHead>
                      <TableHead>Status</TableHead>
                      <TableHead>CPU</TableHead>
                      <TableHead>Memory</TableHead>
                      <TableHead className="w-10" />
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {revisions.map((td) => (
                      <TableRow key={td.taskDefinitionArn}>
                        <TableCell>{td.revision}</TableCell>
                        <TableCell>
                          <TaskStatusBadge status={td.status} />
                        </TableCell>
                        <TableCell className="text-fg-muted">{td.cpu ?? "—"}</TableCell>
                        <TableCell className="text-fg-muted">{td.memory ?? "—"}</TableCell>
                        <TableCell>
                          <Button
                            size="icon"
                            variant="ghost"
                            className="text-fg-muted hover:text-danger"
                            onClick={() => deregisterMut.mutate(td.taskDefinitionArn)}
                          >
                            <Square className="h-3.5 w-3.5" />
                          </Button>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              )}
            </div>
          ))}

          {ungrouped.length > 0 && (
            <div className="overflow-hidden rounded-md border border-border">
              <button
                className="flex w-full items-center justify-between px-4 py-2.5 text-left text-sm font-medium transition-colors hover:bg-accent-muted"
                onClick={() =>
                  setExpandedFamily(
                    expandedFamily === "__ungrouped__" ? undefined : "__ungrouped__",
                  )
                }
              >
                <span className="text-fg-muted">Other</span>
                <span className="flex items-center gap-2 text-xs text-fg-muted">
                  <Badge variant="default">{ungrouped.length}</Badge>
                  <span>{expandedFamily === "__ungrouped__" ? "▲" : "▼"}</span>
                </span>
              </button>
              {expandedFamily === "__ungrouped__" && (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Family</TableHead>
                      <TableHead>Revision</TableHead>
                      <TableHead>Status</TableHead>
                      <TableHead className="w-10" />
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {ungrouped.map((td) => (
                      <TableRow key={td.taskDefinitionArn}>
                        <TableCell className="font-medium">{td.family}</TableCell>
                        <TableCell>{td.revision}</TableCell>
                        <TableCell>
                          <TaskStatusBadge status={td.status} />
                        </TableCell>
                        <TableCell>
                          <Button
                            size="icon"
                            variant="ghost"
                            className="text-fg-muted hover:text-danger"
                            onClick={() => deregisterMut.mutate(td.taskDefinitionArn)}
                          >
                            <Square className="h-3.5 w-3.5" />
                          </Button>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              )}
            </div>
          )}
        </div>
      )}

      <RegisterTaskDefDialog
        open={showRegister}
        onClose={() => setShowRegister(false)}
        isPending={registerMut.isPending}
        onSubmit={(json) => registerMut.mutate(json)}
      />
    </div>
  )
}

// ─── Services panel ───────────────────────────────────────────────────────

function ServicesPanel({ clusterName }: { clusterName: string }) {
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<string>()
  const [updateTarget, setUpdateTarget] = useState<EcsService>()
  const [expandedService, setExpandedService] = useState<string>()

  const {
    data: services = [],
    isLoading,
    isFetching,
    refetch,
  } = useQuery(ecsServicesQueryOptions(clusterName))
  const { data: taskDefs = [] } = useQuery(ecsTaskDefinitionsQueryOptions())

  const createMut = useResourceMutation({
    options: createServiceMutationOptions(),
    invalidateKeys: [ecsKeys.services(clusterName), ecsKeys.clusterDetail(clusterName)],
    successTitle: "Service created",
    onSuccess: () => setShowCreate(false),
  })

  const updateMut = useResourceMutation({
    options: updateServiceMutationOptions(),
    invalidateKeys: [ecsKeys.services(clusterName), ecsKeys.clusterDetail(clusterName)],
    successTitle: "Service updated",
    onSuccess: () => setUpdateTarget(undefined),
  })

  const deleteMut = useResourceMutation({
    options: deleteServiceMutationOptions(),
    invalidateKeys: [ecsKeys.services(clusterName), ecsKeys.clusterDetail(clusterName)],
    successTitle: "Service deleted",
    successVariant: "default",
    onSuccess: () => setDeleteTarget(undefined),
  })

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <Button size="sm" variant="ghost" onClick={() => refetch()} disabled={isFetching}>
          <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} />
          Refresh
        </Button>
        <Button size="sm" onClick={() => setShowCreate(true)}>
          <Plus className="mr-1.5 h-3.5 w-3.5" />
          Create Service
        </Button>
      </div>

      {isLoading ? (
        <div className="flex justify-center py-12">
          <Spinner className="h-6 w-6" />
        </div>
      ) : services.length === 0 ? (
        <EmptyState
          title="No services"
          description="Create a service to maintain running tasks."
          action={
            <Button size="sm" onClick={() => setShowCreate(true)}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Create Service
            </Button>
          }
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Service Name</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Running</TableHead>
              <TableHead>Task Definition</TableHead>
              <TableHead>Launch Type</TableHead>
              <TableHead>Created</TableHead>
              <TableHead className="w-20" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {services.map((svc) => {
              const isExpanded = expandedService === svc.serviceArn
              const primary = svc.deployments.find((d) => d.status === "PRIMARY")
              return (
                <Fragment key={svc.serviceArn}>
                  <TableRow
                    className="cursor-pointer"
                    onClick={() =>
                      setExpandedService(isExpanded ? undefined : svc.serviceArn)
                    }
                  >
                    <TableCell className="font-medium">{svc.serviceName}</TableCell>
                    <TableCell>
                      <div className="flex items-center gap-1.5">
                        <TaskStatusBadge status={svc.status} />
                        {primary?.rolloutState && (
                          <RolloutStateBadge state={primary.rolloutState} />
                        )}
                      </div>
                    </TableCell>
                    <TableCell>
                      <span
                        className={cn(
                          svc.runningCount < svc.desiredCount ? "text-warning" : "text-success",
                        )}
                      >
                        {svc.runningCount}/{svc.desiredCount} running
                      </span>
                      {svc.pendingCount > 0 && (
                        <span className="ml-1 text-xs text-fg-muted">
                          ({svc.pendingCount} pending)
                        </span>
                      )}
                    </TableCell>
                    <TableCell className="max-w-xs truncate text-fg-muted">
                      {shortTaskDef(svc.taskDefinition)}
                    </TableCell>
                    <TableCell>{svc.launchType || "—"}</TableCell>
                    <TableCell className="text-fg-muted">
                      {svc.createdAt ? new Date(svc.createdAt).toLocaleString() : "—"}
                    </TableCell>
                    <TableCell>
                      <div className="flex items-center gap-1">
                        <Button
                          size="icon"
                          variant="ghost"
                          className="h-7 w-7 text-fg-muted"
                          onClick={(e) => {
                            e.stopPropagation()
                            setUpdateTarget(svc)
                          }}
                        >
                          <Pencil className="h-3.5 w-3.5" />
                        </Button>
                        <Button
                          size="icon"
                          variant="ghost"
                          className="h-7 w-7 text-fg-muted hover:text-danger"
                          onClick={(e) => {
                            e.stopPropagation()
                            setDeleteTarget(svc.serviceName)
                          }}
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                  {isExpanded && (
                    <TableRow key={`${svc.serviceArn}-detail`}>
                      <TableCell colSpan={7} className="bg-bg-muted p-4">
                        <ServiceDetailPanel service={svc} />
                      </TableCell>
                    </TableRow>
                  )}
                </Fragment>
              )
            })}
          </TableBody>
        </Table>
      )}

      <CreateServiceDialog
        open={showCreate}
        onClose={() => setShowCreate(false)}
        isPending={createMut.isPending}
        taskDefs={taskDefs}
        onSubmit={(serviceName, taskDefinition, desiredCount, launchType, networking) =>
          createMut.mutate({
            cluster: clusterName,
            serviceName,
            taskDefinition,
            desiredCount,
            launchType,
            subnets: networking.subnets,
            securityGroups: networking.securityGroups,
            assignPublicIp: networking.assignPublicIp,
          })
        }
      />

      <Dialog
        open={!!deleteTarget}
        onOpenChange={(v) => {
          if (!v) setDeleteTarget(undefined)
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete Service</DialogTitle>
          </DialogHeader>
          <DialogBody>
            <p className="text-sm text-fg-muted">
              Are you sure you want to delete service{" "}
              <span className="font-medium text-fg">{deleteTarget}</span>?
            </p>
          </DialogBody>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setDeleteTarget(undefined)}>
              Cancel
            </Button>
            <Button
              variant="danger"
              disabled={deleteMut.isPending}
              onClick={() =>
                deleteTarget && deleteMut.mutate({ cluster: clusterName, service: deleteTarget })
              }
            >
              {deleteMut.isPending && <Spinner className="mr-2" />}
              Delete
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <UpdateServiceDialog
        open={!!updateTarget}
        onClose={() => setUpdateTarget(undefined)}
        isPending={updateMut.isPending}
        service={updateTarget}
        onSubmit={(desiredCount) =>
          updateTarget &&
          updateMut.mutate({
            cluster: clusterName,
            service: updateTarget.serviceName,
            desiredCount,
          })
        }
      />
    </div>
  )
}

// ─── Service detail ───────────────────────────────────────────────────────

/**
 * The deployment and event view ECS surfaces for a service. The event log is
 * where a service that cannot place its tasks explains itself — a service can
 * sit ACTIVE and healthy-looking while every task it tries to start fails, and
 * these messages are the only place that says so.
 */
function ServiceDetailPanel({ service }: { service: EcsService }) {
  const primary = service.deployments.find((d) => d.status === "PRIMARY")

  return (
    <div className="flex flex-col gap-4">
      {primary && (
        <div>
          <h4 className={cn(fieldLabel, "mb-1.5 text-fg")}>Primary deployment</h4>
          <dl className="grid grid-cols-2 gap-x-6 gap-y-1 text-sm sm:grid-cols-4">
            <div>
              <dt className="text-xs text-fg-muted">Rollout</dt>
              <dd>
                {primary.rolloutState ? (
                  <RolloutStateBadge state={primary.rolloutState} />
                ) : (
                  <span className="text-fg-muted">—</span>
                )}
              </dd>
            </div>
            <div>
              <dt className="text-xs text-fg-muted">Running</dt>
              <dd className="text-fg">
                {primary.runningCount}/{primary.desiredCount}
              </dd>
            </div>
            <div>
              <dt className="text-xs text-fg-muted">Pending</dt>
              <dd className="text-fg">{primary.pendingCount}</dd>
            </div>
            <div>
              <dt className="text-xs text-fg-muted">Failed tasks</dt>
              <dd className={cn(primary.failedTasks ? "text-danger" : "text-fg")}>
                {primary.failedTasks ?? 0}
              </dd>
            </div>
          </dl>
          {primary.rolloutStateReason && (
            <p className="mt-1.5 text-xs text-fg-muted">{primary.rolloutStateReason}</p>
          )}
        </div>
      )}

      <div>
        <h4 className={cn(fieldLabel, "mb-1.5 text-fg")}>Events</h4>
        {service.events.length === 0 ? (
          <p className="text-sm text-fg-muted">No events yet.</p>
        ) : (
          <ul className="flex flex-col gap-1">
            {service.events.map((e) => (
              <li key={e.id} className="flex gap-3 text-sm">
                <span className="shrink-0 tabular-nums text-xs text-fg-muted">
                  {e.createdAt ? new Date(e.createdAt).toLocaleTimeString() : "—"}
                </span>
                <span className={cn(isFailureEvent(e.message) ? "text-danger" : "text-fg")}>
                  {e.message}
                </span>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  )
}

/** Highlights the service events that report a task the scheduler could not place. */
function isFailureEvent(message: string) {
  return (
    message.includes("unable to place") ||
    message.includes("unable to consistently start") ||
    message.includes("deployment failed")
  )
}

function RolloutStateBadge({ state }: { state: string }) {
  const variant =
    state === "COMPLETED" ? "success" : state === "FAILED" ? "danger" : "warning"
  return <Badge variant={variant}>{state}</Badge>
}

// ─── Shared helpers ───────────────────────────────────────────────────────

function TaskStatusBadge({ status }: { status: string }) {
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
  // arn:aws:ecs:...:task-definition/family:revision → family:revision
  const parts = arn.split("/")
  return parts.length > 1 ? parts[parts.length - 1] : arn
}

// ─── Run Task dialog ──────────────────────────────────────────────────────

function RunTaskDialog({
  open,
  onClose,
  isPending,
  taskDefs,
  onSubmit,
}: {
  open: boolean
  onClose: () => void
  isPending: boolean
  taskDefs: EcsTaskDefinition[]
  onSubmit: (
    taskDef: string,
    count: number,
    launchType: string,
    networking: AwsvpcNetworking,
  ) => void
}) {
  const [taskDef, setTaskDef] = useState("")
  const [count, setCount] = useState(1)
  const [launchType, setLaunchType] = useState("FARGATE")
  const [networking, setNetworking] = useState<AwsvpcNetworking>(emptyAwsvpcNetworking)

  // A Fargate task definition always uses awsvpc networking, so ECS rejects the
  // call without a subnet. An EC2 launch may still be awsvpc, so the fields
  // stay available rather than being hidden.
  const selected = taskDefs.find((td) => td.taskDefinitionArn === taskDef)
  const requiresNetworking =
    launchType === "FARGATE" || selected?.networkMode === "awsvpc"
  const canRun = !!taskDef && (!requiresNetworking || networking.subnets.length > 0)

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (!v) onClose()
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Run Task</DialogTitle>
        </DialogHeader>
        <DialogBody className="space-y-4">
          <div>
            <label className={cn(fieldLabel, "mb-1 block text-fg")}>Task Definition</label>
            <select
              value={taskDef}
              onChange={(e) => setTaskDef(e.target.value)}
              className="w-full rounded border border-border bg-bg px-3 py-2 text-sm text-fg"
            >
              <option value="">Select a task definition</option>
              {taskDefs.map((td) => (
                <option key={td.taskDefinitionArn} value={td.taskDefinitionArn}>
                  {td.family}:{td.revision}
                </option>
              ))}
            </select>
          </div>
          <div>
            <label className={cn(fieldLabel, "mb-1 block text-fg")}>Count</label>
            <Input
              type="number"
              min={1}
              max={10}
              value={count}
              onChange={(e) => setCount(parseInt(e.target.value) || 1)}
            />
          </div>
          <div>
            <label className={cn(fieldLabel, "mb-1 block text-fg")}>Launch Type</label>
            <select
              value={launchType}
              onChange={(e) => setLaunchType(e.target.value)}
              className="w-full rounded border border-border bg-bg px-3 py-2 text-sm text-fg"
            >
              <option value="FARGATE">FARGATE</option>
              <option value="EC2">EC2</option>
            </select>
          </div>
          <AwsvpcFields
            value={networking}
            onChange={setNetworking}
            required={requiresNetworking}
          />
        </DialogBody>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button
            disabled={isPending || !canRun}
            onClick={() => onSubmit(taskDef, count, launchType, networking)}
          >
            {isPending && <Spinner className="mr-2" />}
            Run
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── Register Task Definition dialog ──────────────────────────────────────

const EXAMPLE_TASK_DEF = JSON.stringify(
  {
    family: "my-task",
    containerDefinitions: [
      {
        name: "app",
        image: "nginx:latest",
        essential: true,
        portMappings: [{ containerPort: 80, hostPort: 80, protocol: "tcp" }],
        memory: 512,
        cpu: 256,
      },
    ],
    cpu: "256",
    memory: "512",
  },
  null,
  2,
)

function RegisterTaskDefDialog({
  open,
  onClose,
  isPending,
  onSubmit,
}: {
  open: boolean
  onClose: () => void
  isPending: boolean
  onSubmit: (json: string) => void
}) {
  const [json, setJson] = useState(EXAMPLE_TASK_DEF)

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (!v) onClose()
      }}
    >
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>Register Task Definition</DialogTitle>
        </DialogHeader>
        <DialogBody>
          <Textarea
            className="min-h-64 font-mono text-xs"
            value={json}
            onChange={(e) => setJson(e.target.value)}
          />
        </DialogBody>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={isPending} onClick={() => onSubmit(json)}>
            {isPending && <Spinner className="mr-2" />}
            Register
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── Create Service dialog ────────────────────────────────────────────────

function CreateServiceDialog({
  open,
  onClose,
  isPending,
  taskDefs,
  onSubmit,
}: {
  open: boolean
  onClose: () => void
  isPending: boolean
  taskDefs: EcsTaskDefinition[]
  onSubmit: (
    serviceName: string,
    taskDefinition: string,
    desiredCount: number,
    launchType: string,
    networking: AwsvpcNetworking,
  ) => void
}) {
  const [serviceName, setServiceName] = useState("")
  const [taskDef, setTaskDef] = useState("")
  const [desiredCount, setDesiredCount] = useState(1)
  const [launchType, setLaunchType] = useState("FARGATE")
  const [networking, setNetworking] = useState<AwsvpcNetworking>(emptyAwsvpcNetworking)

  const selected = taskDefs.find((td) => td.taskDefinitionArn === taskDef)
  const requiresNetworking = launchType === "FARGATE" || selected?.networkMode === "awsvpc"
  const canCreate =
    !!serviceName && !!taskDef && (!requiresNetworking || networking.subnets.length > 0)

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (!v) onClose()
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Create Service</DialogTitle>
        </DialogHeader>
        <DialogBody className="space-y-4">
          <div>
            <label className={cn(fieldLabel, "mb-1 block text-fg")}>Service Name</label>
            <Input
              value={serviceName}
              onChange={(e) => setServiceName(e.target.value)}
              placeholder="my-service"
            />
          </div>
          <div>
            <label className={cn(fieldLabel, "mb-1 block text-fg")}>Task Definition</label>
            <select
              value={taskDef}
              onChange={(e) => setTaskDef(e.target.value)}
              className="w-full rounded border border-border bg-bg px-3 py-2 text-sm text-fg"
            >
              <option value="">Select a task definition</option>
              {taskDefs.map((td) => (
                <option key={td.taskDefinitionArn} value={td.taskDefinitionArn}>
                  {td.family}:{td.revision}
                </option>
              ))}
            </select>
          </div>
          <div>
            <label className={cn(fieldLabel, "mb-1 block text-fg")}>Desired Count</label>
            <Input
              type="number"
              min={0}
              max={100}
              value={desiredCount}
              onChange={(e) => setDesiredCount(parseInt(e.target.value) || 0)}
            />
          </div>
          <div>
            <label className={cn(fieldLabel, "mb-1 block text-fg")}>Launch Type</label>
            <select
              value={launchType}
              onChange={(e) => setLaunchType(e.target.value)}
              className="w-full rounded border border-border bg-bg px-3 py-2 text-sm text-fg"
            >
              <option value="FARGATE">FARGATE</option>
              <option value="EC2">EC2</option>
            </select>
          </div>
          <AwsvpcFields value={networking} onChange={setNetworking} required={requiresNetworking} />
        </DialogBody>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button
            disabled={isPending || !canCreate}
            onClick={() => onSubmit(serviceName, taskDef, desiredCount, launchType, networking)}
          >
            {isPending && <Spinner className="mr-2" />}
            Create
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── Update Service dialog ────────────────────────────────────────────────

function UpdateServiceDialog({
  open,
  onClose,
  isPending,
  service,
  onSubmit,
}: {
  open: boolean
  onClose: () => void
  isPending: boolean
  service?: EcsService
  onSubmit: (desiredCount: number) => void
}) {
  const [desiredCount, setDesiredCount] = useState(service?.desiredCount ?? 1)

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (!v) onClose()
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Update Service — {service?.serviceName}</DialogTitle>
        </DialogHeader>
        <DialogBody className="space-y-4">
          <div>
            <label className={cn(fieldLabel, "mb-1 block text-fg")}>Desired Count</label>
            <Input
              type="number"
              min={0}
              max={100}
              value={desiredCount}
              onChange={(e) => setDesiredCount(parseInt(e.target.value) || 0)}
            />
          </div>
        </DialogBody>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={isPending} onClick={() => onSubmit(desiredCount)}>
            {isPending && <Spinner className="mr-2" />}
            Update
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── Container Instances Panel ────────────────────────────────────────────

function ContainerInstancesPanel({ clusterName }: { clusterName: string }) {
  const { data: instances = [], isLoading } = useQuery(
    ecsContainerInstancesQueryOptions(clusterName),
  )

  if (isLoading) {
    return (
      <div className="flex justify-center py-12">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }

  if (instances.length === 0) {
    return (
      <EmptyState
        title="No container instances"
        description="No EC2 container instances are registered to this cluster."
      />
    )
  }

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Container Instance ARN</TableHead>
          <TableHead>EC2 Instance</TableHead>
          <TableHead>Status</TableHead>
          <TableHead>Agent Connected</TableHead>
          <TableHead>Running</TableHead>
          <TableHead>Pending</TableHead>
          <TableHead>Registered At</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {instances.map((ci) => (
          <ContainerInstanceRow key={ci.containerInstanceArn} ci={ci} />
        ))}
      </TableBody>
    </Table>
  )
}

function ContainerInstanceRow({ ci }: { ci: EcsContainerInstance }) {
  const shortArn = ci.containerInstanceArn.split("/").pop() ?? ci.containerInstanceArn
  return (
    <TableRow>
      <TableCell title={ci.containerInstanceArn}>{shortArn.slice(0, 12)}…</TableCell>
      <TableCell className="text-fg-muted">{ci.ec2InstanceId ?? "—"}</TableCell>
      <TableCell>
        <TaskStatusBadge status={ci.status} />
      </TableCell>
      <TableCell>
        <Badge variant={ci.agentConnected ? "success" : "warning"}>
          {ci.agentConnected ? "Connected" : "Disconnected"}
        </Badge>
      </TableCell>
      <TableCell>{ci.runningTasksCount}</TableCell>
      <TableCell>{ci.pendingTasksCount}</TableCell>
      <TableCell className="text-fg-muted">
        {ci.registeredAt ? new Date(ci.registeredAt).toLocaleString() : "—"}
      </TableCell>
    </TableRow>
  )
}

// ─── Cluster Tags Panel ───────────────────────────────────────────────────

function ClusterTagsPanel({ clusterArn }: { clusterArn: string }) {
  const { data: tags = [], isLoading } = useQuery(ecsTagsQueryOptions(clusterArn))

  if (isLoading) {
    return (
      <div className="flex justify-center py-8">
        <Spinner className="h-5 w-5" />
      </div>
    )
  }

  if (tags.length === 0) {
    return <EmptyState title="No tags" description="This cluster has no tags." />
  }

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Key</TableHead>
          <TableHead>Value</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {tags.map((tag, i) => (
          <TableRow key={i}>
            <TableCell>{tag.key}</TableCell>
            <TableCell className="text-fg-muted">{tag.value}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
